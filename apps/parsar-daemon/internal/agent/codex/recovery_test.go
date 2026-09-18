package codex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

func TestNativeSessionRecoveryRequiresPinnedNative(t *testing.T) {
	for _, version := range []string{"", "codex-cli 0.153.3", "codex-cli 0.153.4", "codex-cli 0.154.0"} {
		if SupportsNativeSessionRecovery(version) != (version == "codex-cli 0.153.4") {
			t.Fatalf("unexpected recovery capability for %q", version)
		}
	}
}

func TestRequiredHistoryResolution(t *testing.T) {
	for _, scenario := range []string{"recover", "fresh", "known", "missing", "ambiguous", "paged", "omitted-cursor", "missing-parent", "child", "fork", "ephemeral", "wrong-cwd", "wrong-home", "archived", "lookup-error", "resume-error", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			home := t.TempDir()
			plan := SessionPlan{Cwd: "/workspace", Env: []string{"CODEX_HOME=" + home}}
			row := map[string]any{"id": "original", "parentThreadId": nil, "forkedFromId": nil, "ephemeral": false, "source": "vscode", "cwd": plan.Cwd, "path": filepath.Join(home, "sessions", "rollout.jsonl")}
			switch scenario {
			case "missing-parent":
				delete(row, "parentThreadId")
			case "child":
				row["parentThreadId"] = "parent"
			case "fork":
				row["forkedFromId"] = "old"
			case "ephemeral":
				row["ephemeral"] = true
			case "wrong-cwd":
				row["cwd"] = "/another"
			case "wrong-home":
				row["path"] = "/another/sessions/rollout.jsonl"
			}
			req := proto.PromptRequestPayload{StrictResume: true, RequireExistingNativeSession: true}
			if scenario == "fresh" {
				req.RequireExistingNativeSession = false
			}
			if scenario == "known" {
				req.AgentSessionID = "original"
			}
			session := &Session{rpc: client.JSONRPCClient, cancelCtx: ctx, cfg: sessionConfig{logger: log.With("component", "recovery-test")}}
			methods := make(chan []string, 1)
			go func() {
				decoder, encoder := json.NewDecoder(server.FromClient), json.NewEncoder(server.ToClient)
				var seen []string
				defer func() { methods <- seen }()
				for {
					var frame struct {
						ID     string         `json:"id"`
						Method string         `json:"method"`
						Params map[string]any `json:"params"`
					}
					if decoder.Decode(&frame) != nil {
						return
					}
					seen = append(seen, frame.Method)
					response := map[string]any{"id": frame.ID}
					switch frame.Method {
					case "thread/list":
						if frame.Params["limit"] != float64(2) || frame.Params["useStateDbOnly"] != false {
							return
						}
						data := []any{row}
						if frame.Params["archived"] == true || scenario == "missing" {
							data = []any{}
						}
						if scenario == "archived" && frame.Params["archived"] == true {
							data = []any{row}
						}
						if scenario == "ambiguous" {
							data = append(data, row)
						}
						page := map[string]any{"data": data, "nextCursor": nil}
						if scenario == "paged" {
							page["nextCursor"] = "next"
						}
						if scenario == "omitted-cursor" {
							delete(page, "nextCursor")
						}
						response["result"] = page
						if scenario == "malformed" {
							response["result"] = "invalid"
						}
						if scenario == "lookup-error" {
							delete(response, "result")
							response["error"] = map[string]any{"code": -1, "message": "failed"}
						}
					case "thread/resume":
						if frame.Params["threadId"] != "original" {
							return
						}
						response["result"] = map[string]any{"thread": map[string]string{"id": "original"}}
						if scenario == "resume-error" {
							delete(response, "result")
							response["error"] = map[string]any{"code": -1, "message": "failed"}
						}
					case "thread/start":
						response["result"] = map[string]any{"thread": map[string]string{"id": "fresh"}}
					default:
						return
					}
					if encoder.Encode(response) != nil {
						return
					}
				}
			}()
			err := session.resolveThread(req, plan)
			wantSuccess := scenario == "recover" || scenario == "fresh" || scenario == "known"
			if (err == nil) != wantSuccess {
				t.Fatalf("resolution error=%v", err)
			}
			cleanup()
			seen := <-methods
			for _, method := range seen {
				if method == "thread/start" && scenario != "fresh" {
					t.Fatal("silently created fresh history", seen)
				}
				if method == "thread/list" && scenario == "known" {
					t.Fatal("searched instead of using authoritative ID", seen)
				}
			}
			if scenario == "recover" && (len(seen) != 3 || seen[2] != "thread/resume" || session.currentThreadID() != "original") {
				t.Fatal("did not resume exact root", seen)
			}
		})
	}
}

func TestPreparedRecoveryCannotStartWithoutExistingHistory(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	req.RequireExistingNativeSession = true
	p, err := newPreparation(t.Context(), req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	assertPreparationOnly(t, root)
	out := make(chan proto.Envelope, 16)
	session, err := p.Start(t.Context(), "recovery-run", "continue", out)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Cancel(context.Background())
	select {
	case <-p.session.waitDone:
	case <-time.After(4 * time.Second):
		t.Fatal("recovery did not terminate")
	}
	found := false
	for _, frame := range preparationFrames(t, root) {
		if frame.Method == "thread/list" {
			found = true
		}
		if frame.Method == "thread/start" || frame.Method == "turn/start" {
			t.Fatal("missing history started work", frame.Method)
		}
	}
	if !found {
		t.Fatal("prepared start lost recovery requirement")
	}
}

func TestRecoveryRequiresStrictPrivateExecution(t *testing.T) {
	for _, mode := range []string{"non-strict", "no-state", "read-only"} {
		t.Run(mode, func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			req.RequireExistingNativeSession = true
			switch mode {
			case "non-strict":
				req.StrictResume = false
			case "no-state":
				req.AgentStateKey = ""
			case "read-only":
				req.WorkspaceReadOnly = true
			}
			if p, err := newPreparation(t.Context(), req, cfg); err == nil {
				p.Close()
				t.Fatal("invalid recovery admitted")
			}
			if len(preparationFrames(t, root)) != 0 {
				t.Fatal("invalid recovery launched native process")
			}
		})
	}
}
