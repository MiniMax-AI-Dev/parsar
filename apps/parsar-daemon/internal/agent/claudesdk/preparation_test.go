//go:build unix

package claudesdk

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestPreparationWaitsForReceiptAndRetainsConfiguration(t *testing.T) {
	config := preparationFixture(t, "delayed-ready")
	req := preparationRequest()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := make(chan agent.Prepared, 1)
	failed := make(chan error, 1)
	go func() {
		p, err := NewPreparationFactory(config)(ctx, req)
		if err != nil {
			failed <- err
			return
		}
		result <- p
	}()
	raw := waitPreparationFile(t, filepath.Join(config.StateDir, "prepare.json"))
	select {
	case <-result:
		t.Fatal("preparation returned before its native receipt")
	case err := <-failed:
		t.Fatal(err)
	default:
	}
	if err := os.WriteFile(filepath.Join(config.StateDir, "ready"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var preparedResource agent.Prepared
	select {
	case preparedResource = <-result:
	case err := <-failed:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	p := preparedResource.(*prepared)
	defer p.Cancel(context.Background())
	pid := p.session.process.Cmd.Process.Pid
	if _, err := os.Stat(filepath.Join(config.StateDir, "start.json")); !os.IsNotExist(err) {
		t.Fatal("preparation submitted input")
	}
	var frozen startRequest
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.Model != "fixture" || frozen.Resume != "native-session" || frozen.Workspace == nil || frozen.Prompt != "" {
		t.Fatal("configuration-only request was not retained")
	}
	config.Env[0] = "ANTHROPIC_AUTH_TOKEN=changed"
	config.Workspace.Directory = "/changed"
	config.Workspace.ProtectedDirs[0] = "/changed"
	req.AgentOptions["model"] = "changed"
	req.AgentSessionID = "changed"
	out := make(chan proto.Envelope, 16)
	operation, stopOperation := context.WithCancel(ctx)
	s, err := p.Start(operation, "actual-run", "hello", out)
	stopOperation()
	if err != nil {
		t.Fatal(err)
	}
	if s != p.session || s.(*session).process.Cmd.Process.Pid != pid {
		t.Fatal("Start replaced the prepared native process")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Start(ctx, "duplicate", "hello", make(chan proto.Envelope, 8)); err == nil {
		t.Fatal("duplicate Start was accepted")
	}
	var done proto.DonePayload
	for event := range out {
		if event.ID != "actual-run" || event.Type == proto.TypeError {
			t.Fatal("lost execution identity or owner lifetime", event.Type)
		}
		if event.Type == proto.TypeDone {
			if err := event.DecodePayload(&done); err != nil {
				t.Fatal(err)
			}
		}
	}
	if done.Content != "completed" || done.Metadata[proto.DoneMetaAgentSessionID] != "native-session" || done.Usage.Raw["claude_sdk_result"] == nil {
		t.Fatal("prepared execution lost ordinary output or frozen resume", done)
	}
	if _, err := os.Stat(filepath.Join(config.StateDir, "released")); err != nil {
		t.Fatal("completion preceded process release", err)
	}
}

func TestPreparationRejectsInputAndUnavailableProfilesBeforeLaunch(t *testing.T) {
	for _, name := range []string{"run", "prompt", "conversation", "attachments", "authoring", "subagents", "workspace-missing", "none", "functions", "mcp", "tools", "controls", "old-runtime"} {
		t.Run(name, func(t *testing.T) {
			config := preparationFixture(t, name)
			req := preparationRequest()
			switch name {
			case "run":
				req.RunID = "unexpected"
			case "prompt":
				req.Prompt = "unexpected"
			case "conversation":
				req.ConversationID = "product"
			case "attachments":
				req.Attachments = []proto.PromptAttachment{{Kind: "image"}}
			case "authoring":
				req.WorkspaceAuthoring = true
			case "subagents":
				req.ObserveSubagentIdentities = true
			case "workspace-missing":
				config.Workspace = nil
			case "none":
				req.DisableExecutionEnvironment = true
			case "functions":
				req.FunctionTools = []proto.FunctionTool{{Name: "hello", Parameters: json.RawMessage(`{"type":"object"}`)}}
			case "mcp":
				req.MCPHTTPServers = &[]proto.MCPHTTPServer{}
			case "tools":
				req.ObserveTools = true
			case "controls":
				req.ExecutionControls = &proto.ExecutionControls{WebSearch: "enabled", TextVerbosity: "medium"}
			}
			if _, err := NewPreparationFactory(config)(t.Context(), req); err == nil {
				t.Fatal("invalid preparation was accepted")
			}
			if _, err := os.Stat(filepath.Join(config.StateDir, "launched")); !os.IsNotExist(err) {
				t.Fatal("rejection launched native preparation")
			}
		})
	}
}

func TestPreparationFailureAndUnusedRelease(t *testing.T) {
	for _, mode := range []string{"history-missing", "invalid-receipt", "close", "owner-cancel", "native-exit", "invalid-start", "cancelled-start"} {
		t.Run(mode, func(t *testing.T) {
			config := preparationFixture(t, mode)
			owner, stop := context.WithCancel(t.Context())
			defer stop()
			resource, err := NewPreparationFactory(config)(owner, preparationRequest())
			if mode == "history-missing" || mode == "invalid-receipt" {
				if err == nil || mode == "history-missing" && !strings.Contains(err.Error(), "history_unavailable") {
					t.Fatal("preparation failure was lost", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p := resource.(*prepared)
			defer p.Cancel(context.Background())
			switch mode {
			case "close":
				err = p.Close()
			case "owner-cancel":
				stop()
			case "native-exit":
				err = p.session.process.Cmd.Process.Signal(syscall.SIGKILL)
			case "invalid-start", "cancelled-start":
				operation, cancel := context.WithCancel(t.Context())
				prompt := ""
				if mode == "cancelled-start" {
					prompt = "hello"
					cancel()
				}
				_, startErr := p.Start(operation, "run", prompt, make(chan proto.Envelope, 8))
				cancel()
				if startErr == nil {
					t.Fatal("invalid or cancelled Start succeeded")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-p.session.settled:
			case <-time.After(5 * time.Second):
				t.Fatal("unused process was not settled")
			}
			if _, err := p.Start(t.Context(), "late", "hello", make(chan proto.Envelope, 8)); err == nil {
				t.Fatal("released preparation was reusable")
			}
			if got := p.CancellationOutcome(); !reflect.DeepEqual(got, proto.DonePayload{}) {
				t.Fatal("unstarted process fabricated an execution outcome", got)
			}
		})
	}
}

func TestPreparedCancellationKeepsOwnershipUntilOutputDrain(t *testing.T) {
	config := preparationFixture(t, "cancellation")
	owner, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	resource, err := NewPreparationFactory(config)(owner, preparationRequest())
	if err != nil {
		t.Fatal(err)
	}
	p := resource.(*prepared)
	defer p.Cancel(context.Background())
	out := make(chan proto.Envelope)
	operation, stopOperation := context.WithCancel(owner)
	if _, err := p.Start(operation, "run", "hello", out); err != nil {
		t.Fatal(err)
	}
	stopOperation()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if event := <-out; event.Type != proto.TypeDelta {
		t.Fatal("operation cancellation or Close cancelled the Session")
	}
	short, cancel := context.WithTimeout(owner, 30*time.Millisecond)
	err = p.Cancel(short)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(p.CancellationOutcome(), proto.DonePayload{}) {
		t.Fatal("pending output drain reported settled ownership", err)
	}
	if err := os.WriteFile(filepath.Join(config.StateDir, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Cancel(owner); err != nil {
		t.Fatal(err)
	}
	got := p.CancellationOutcome()
	if got.Content != "partialtaildrained" || got.Metadata[proto.DoneMetaAgentSessionID] != "native-session" || got.Usage.Raw["claude_sdk_result"] == nil {
		t.Fatal("cancellation across transfer lost observed output", got)
	}
	for range out {
	}
}

func TestPreparedStartRacesCloseAndCancellation(t *testing.T) {
	for _, cancelResource := range []bool{false, true} {
		for range 6 {
			config := preparationFixture(t, "success")
			resource, err := NewPreparationFactory(config)(t.Context(), preparationRequest())
			if err != nil {
				t.Fatal(err)
			}
			p := resource.(*prepared)
			out := make(chan proto.Envelope, 16)
			var running agent.Session
			var startErr error
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); running, startErr = p.Start(t.Context(), "run", "hello", out) }()
			go func() {
				defer wg.Done()
				if cancelResource {
					_ = p.Cancel(t.Context())
				} else {
					_ = p.Close()
				}
			}()
			wg.Wait()
			if startErr == nil {
				if running == nil {
					t.Fatal("successful transfer lost its Session")
				}
				for range out {
				}
			}
			if err := p.Cancel(t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-p.session.process.Done():
			default:
				t.Fatal("race left the owned process alive")
			}
		}
	}
}

func TestPreparedConcurrentStartTransfersOnlyOnce(t *testing.T) {
	config := preparationFixture(t, "success")
	resource, err := NewPreparationFactory(config)(t.Context(), preparationRequest())
	if err != nil {
		t.Fatal(err)
	}
	p := resource.(*prepared)
	defer p.Cancel(context.Background())
	results := make(chan bool, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := make(chan proto.Envelope, 16)
			_, err := p.Start(t.Context(), "run", "hello", out)
			results <- err == nil
			if err == nil {
				for event := range out {
					if event.Type == proto.TypeError {
						t.Error("losing Start cancelled the transferred Session")
					}
				}
			}
		}()
	}
	wg.Wait()
	if first, second := <-results, <-results; first == second {
		t.Fatal("concurrent Start did not produce exactly one owner")
	}
}
