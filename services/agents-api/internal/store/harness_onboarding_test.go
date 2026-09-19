package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestThirdHarnessPublicOnboarding(t *testing.T) {
	h := newDispatchHarness(t)
	profile := engine.Profile{Placements: []string{"none"}, ValidateConfiguration: func(a v1.Agent, _ *v1.Environment, _ bool) error {
		if a.Text.Verbosity != "medium" || a.Text.Format.Type != "text" || a.MultiAgent.Enabled || a.Reasoning.Effort != nil || a.Reasoning.Summary != nil || a.ServiceTier != "auto" {
			return engine.ErrInvalidInput
		}
		return nil
	}, ValidateTools: func(_ *v1.Environment, _ bool, f []proto.FunctionTool, m []proto.MCPHTTPServer) error {
		if len(f)+len(m) > 0 {
			return engine.ErrInvalidInput
		}
		return nil
	}}
	policy := execution.Policy{Engines: engine.NewCatalog(map[string]engine.Profile{"fixture_harness": profile})}
	h.d.Policy = policy
	// The fixture only supplies an adapter and registration to the real daemon router.
	// Core sees its ordinary authenticated gateway connection and neutral frames.
	started, write := startOnboardingPeer(t, h)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	worker, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(15 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	token := uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{{OrganizationID: "test-org", ProjectID: h.tenant, SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential(token), TenantID: h.tenant}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(h.s, auth, "fixture_harness", api.WithExecution(worker), api.WithExecutionPolicy(policy))
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("OpenAI-Beta", "agents=v1")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, res.Code, res.Body)
		}
		return res
	}
	for _, fields := range []string{`,"text":{"verbosity":"high"}`, `,"tools":[{"type":"function","name":"f","parameters":{"type":"object"}}]`} {
		request("POST", "/v1/agents/sessions", `{"agent":{"model":"fixture"`+fields+`},"environment":{"type":"none"}}`, 400)
	}
	res := request("POST", "/v1/agents/sessions", `{"agent":{"model":"fixture"},"environment":{"type":"none"},"input":"hold"}`, 200)
	var created struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(res.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatal(res.Body, err)
	}
	h.session, err = h.s.GetSession(ctx, h.tenant, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := awaitOnboardingPrompt(t, started)
	if first.AgentKind != "fixture_harness" || first.AgentSessionID != "" {
		t.Fatal(first)
	}
	request("POST", "/v1/agents/sessions/"+created.ID+"/events", `{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"finish"}]}]}]}`, 204)
	waitTurn(t, h, first.RunID, store.TurnCompleted)
	turn, err := h.s.GetTurn(ctx, h.tenant, created.ID, first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var result execution.Result
	inputs, inputErr := h.s.ListTurnInputs(ctx, h.tenant, created.ID, first.RunID, 0, 100)
	if inputErr != nil || len(inputs) != 2 {
		t.Fatal(inputs, inputErr)
	}
	if err = json.Unmarshal(turn.Outcome, &result); err != nil || result.AppliedThrough != inputs[1].Sequence || result.Done.Content != "readyfinish" {
		t.Fatal(string(turn.Outcome), err)
	}
	bound, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, created.ID)
	if err != nil || bound.NativeSessionID == "" {
		t.Fatal(bound, err)
	}
	request("POST", "/v1/agents/sessions/"+created.ID+"/events", `{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"hold"}]}]}]}`, 204)
	next := awaitOnboardingPrompt(t, started)
	if next.RunID == first.RunID || next.AgentSessionID != bound.NativeSessionID || !next.StrictResume {
		t.Fatal(next)
	}
	request("POST", "/v1/agents/sessions/"+created.ID+"/events", `{"events":[{"type":"agent.session.input.cancel"}]}`, 204)
	waitTurn(t, h, next.RunID, store.TurnCancelled)
	// A missing mandatory receipt capability must prevent claiming queued work.
	peer, _ := h.registry.LookupDevice(h.device.ID)
	info, _, _ := peer.AgentKindStatus("fixture_harness")
	info.Capabilities.DurableInputReceipts = false
	// A separate unbound Session is used, without changing public handler behavior.
	wire, _ := json.Marshal(info)
	var changed proto.SupportedAgentKind
	if err := json.Unmarshal(wire, &changed); err != nil {
		t.Fatal(err)
	}
	update, _ := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{changed}})
	if err := write(update); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); ; {
		current, _, _ := peer.AgentKindStatus("fixture_harness")
		if !current.Capabilities.DurableInputReceipts {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capability update missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	res = request("POST", "/v1/agents/sessions", `{"agent":{"model":"fixture"},"environment":{"type":"none"},"input":"hold"}`, 200)
	if err = json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-started:
		t.Fatalf("incapable runtime received work: %s", p.RunID)
	case <-time.After(700 * time.Millisecond):
	}
	queued, err := h.s.GetSession(ctx, h.tenant, created.ID)
	if err != nil || queued.LastTurn == nil || queued.LastTurn.Status != store.TurnQueued {
		t.Fatal(queued, err)
	}
}

func awaitOnboardingPrompt(t *testing.T, c <-chan proto.PromptRequestPayload) proto.PromptRequestPayload {
	t.Helper()
	select {
	case p := <-c:
		return p
	case <-time.After(10 * time.Second):
		t.Fatal("fixture was not dispatched")
		return proto.PromptRequestPayload{}
	}
}

func startOnboardingPeer(t *testing.T, h *dispatchHarness) (<-chan proto.PromptRequestPayload, func(proto.Envelope) error) {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "onboarding")
	build := exec.Command(filepath.Join(goruntime.GOROOT(), "bin", "go"), "build", "-o", binary, "./apps/parsar-daemon/testdata/onboarding")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, out)
	}
	child := exec.Command(binary)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	in, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	started := make(chan proto.PromptRequestPayload, 4)
	up := make(chan error, 1)
	down := make(chan error, 1)
	var writeMu sync.Mutex
	write := func(e proto.Envelope) error { writeMu.Lock(); defer writeMu.Unlock(); return h.conn.WriteJSON(e) }
	go func() {
		dec := json.NewDecoder(out)
		for {
			var e proto.Envelope
			if err := dec.Decode(&e); err != nil {
				up <- err
				return
			}
			if err := write(e); err != nil {
				up <- err
				return
			}
		}
	}()
	go func() {
		enc := json.NewEncoder(in)
		for {
			var e proto.Envelope
			if err := h.conn.ReadJSON(&e); err != nil {
				down <- err
				return
			}
			if e.Type == proto.TypePromptRequest {
				var p proto.PromptRequestPayload
				if err := e.DecodePayload(&p); err != nil {
					down <- err
					return
				}
				started <- p
			}
			if err := enc.Encode(e); err != nil {
				down <- err
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = in.Close()
		done := make(chan error, 1)
		go func() { done <- child.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("fixture: %v %s", err, stderr.String())
			}
		case <-time.After(5 * time.Second):
			_ = child.Process.Kill()
			<-done
			t.Error("fixture failed to release")
		}
		_ = h.conn.Close()
		<-down
		if err := <-up; err != nil && err != io.EOF {
			t.Log(fmt.Sprint("fixture transport closed: ", err))
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		peer, err := h.registry.LookupDevice(h.device.ID)
		if err == nil {
			_, found, known := peer.AgentKindStatus("fixture_harness")
			if found && known {
				return started, write
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture registration missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
