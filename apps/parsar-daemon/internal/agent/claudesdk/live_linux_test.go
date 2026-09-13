//go:build linux

package claudesdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func TestLiveClaudeSDKTextResume(t *testing.T) {
	entrypoint := os.Getenv("PARSAR_CLAUDE_SDK_ENTRYPOINT")
	keyFile := os.Getenv("PARSAR_CLAUDE_SDK_MINIMAX_KEY_FILE")
	if entrypoint == "" || keyFile == "" {
		t.Skip("real SDK/provider acceptance requires explicit entrypoint and private key file")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	proofRoot := os.Getenv("PARSAR_CLAUDE_SDK_PROOF_DIR")
	if !filepath.IsAbs(proofRoot) {
		t.Fatal("PARSAR_CLAUDE_SDK_PROOF_DIR must be an absolute managed proof directory")
	}
	if err := os.MkdirAll(proofRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(proofRoot, "claude-adapter-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARSAR_HOME", root)
	target, _ := url.Parse("https://api.minimax.cn/anthropic")
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(req *http.Request) { director(req); req.Host = target.Host }
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "provider transport failed", http.StatusBadGateway)
	}
	var mu sync.Mutex
	type providerRequest struct {
		Model string `json:"model"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	var requests []providerRequest
	forwarder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/messages") {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				http.Error(w, "request read failed", 400)
				return
			}
			_ = req.Body.Close()
			req.Body = io.NopCloser(bytes.NewReader(body))
			var value providerRequest
			_ = json.Unmarshal(body, &value)
			mu.Lock()
			requests = append(requests, value)
			mu.Unlock()
		}
		proxy.ServeHTTP(w, req)
	}))
	defer forwarder.Close()
	config := Config{Entrypoint: entrypoint, StateDir: filepath.Join(root, "state"), Env: []string{
		"ANTHROPIC_BASE_URL=" + forwarder.URL, "ANTHROPIC_AUTH_TOKEN=" + strings.TrimSpace(string(key)),
		"ANTHROPIC_API_KEY=", "CLAUDE_CODE_OAUTH_TOKEN=", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=MiniMax-M3", "ANTHROPIC_DEFAULT_OPUS_MODEL=MiniMax-M3", "ANTHROPIC_DEFAULT_HAIKU_MODEL=MiniMax-M3",
	}}

	// Ambient project configuration must not add a model tool or start a server.
	work := filepath.Join(config.StateDir, "work")
	if err := os.MkdirAll(filepath.Join(work, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(root, "ambient-mcp-started")
	ambient, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"ambient": map[string]any{
		"command": "node", "args": []string{"-e", "require('node:fs').writeFileSync(process.argv[1], 'unexpected')", canary},
	}}})
	if err := os.WriteFile(filepath.Join(work, ".mcp.json"), ambient, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".claude", "settings.json"), []byte(`{"enableAllProjectMcpServers":true,"permissions":{"allow":["Bash","Read","Agent","WebSearch"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	type evidence struct {
		ExecutionControls    *proto.ExecutionControls `json:"execution_controls"`
		SteeringText         string                   `json:"steering_text,omitempty"`
		SteeringConfirmed    bool                     `json:"steering_confirmed,omitempty"`
		SteeringMilliseconds int64                    `json:"steering_milliseconds,omitempty"`
		SessionID            string                   `json:"session_id"`
		NodePID              int                      `json:"node_pid"`
		NativePIDs           []int                    `json:"native_pids"`
		ChildPIDs            []int                    `json:"child_pids"`
		Text                 string                   `json:"text"`
		Failure              string                   `json:"failure,omitempty"`
		Events               []proto.Envelope         `json:"events"`
		FunctionCalls        int                      `json:"function_calls"`
		AppliedResults       int                      `json:"applied_results"`
		ProviderRequests     []providerRequest        `json:"provider_requests"`
	}
	functionNonce := "function-" + uuid.NewString()
	run := func(prompt, resume string, success *bool, steering ...string) evidence {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		mu.Lock()
		requestStart := len(requests)
		mu.Unlock()
		out := make(chan proto.Envelope, 64)
		request := proto.PromptRequestPayload{RunID: uuid.NewString(), Prompt: prompt, AgentSessionID: resume, StrictResume: true, ReleaseOnCompletion: true, ObserveMessages: true, DisableExecutionEnvironment: true, DisableSubagents: true, ExecutionControls: &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"}, AgentOptions: map[string]any{"model": "MiniMax-M3", "system_prompt": "Answer briefly and preserve the exact verification value in the conversation. Use no tools."}}
		if success != nil {
			request.ObserveToolObservations = true
			request.AgentOptions["system_prompt"] = "Call lookup exactly once as requested, then report both result parts and any prior verification value. Never retry a failed tool."
			request.FunctionTools = []proto.FunctionTool{{Name: "lookup", Description: "Return a synthetic verification value.", Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`)}}
		}
		running, err := NewFactory(config)(ctx, request, out)
		if err != nil {
			t.Fatal(err)
		}
		s := running.(*session)
		defer running.Cancel(context.Background())
		proof := evidence{NodePID: s.process.Cmd.Process.Pid, ExecutionControls: request.ExecutionControls}
		if len(steering) > 0 {
			proof.SteeringText = steering[0]
		}
		type steeringResult struct {
			err     error
			elapsed int64
		}
		steeringReply := make(chan steeringResult, 1)
		var steeringAt time.Time
		beginSteering := func() {
			if proof.SteeringText == "" || !steeringAt.IsZero() {
				return
			}
			steeringAt = time.Now()
			go func() {
				err := s.Steer(ctx, proto.PromptSteerPayload{InputID: uuid.NewString(), Text: proof.SteeringText})
				steeringReply <- steeringResult{err: err, elapsed: time.Since(steeringAt).Milliseconds()}
			}()
		}
		type children struct{ all, native []int }
		observed := make(chan children, 1)
		go func() {
			pids := map[int]bool{}
			native := map[int]bool{}
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				raw, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", proof.NodePID, proof.NodePID))
				for _, value := range strings.Fields(string(raw)) {
					if pid, err := strconv.Atoi(value); err == nil {
						pids[pid] = true
						// SDK history lookup may also spawn Git helpers; identify the execution transport.
						args, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
						if bytes.Contains(args, []byte("\x00--input-format\x00stream-json\x00")) &&
							bytes.Contains(args, []byte("\x00--output-format\x00stream-json\x00")) {
							native[pid] = true
						}
					}
				}
				select {
				case <-s.process.Done():
					var result children
					for pid := range pids {
						result.all = append(result.all, pid)
					}
					for pid := range native {
						result.native = append(result.native, pid)
					}
					observed <- result
					return
				case <-ticker.C:
				}
			}
		}()
		done := false
		for event := range out {
			proof.Events = append(proof.Events, event)
			switch event.Type {
			case proto.TypeDelta:
				if success == nil {
					beginSteering()
				}
			case proto.TypeToolCall:
				var tool proto.ToolCallPayload
				if err := event.DecodePayload(&tool); err != nil {
					t.Fatal(err)
				}
				if tool.Observation == nil || tool.Observation.Kind != "function" || tool.NativeItem != nil || (tool.Stage != "before" && tool.Stage != "after") {
					t.Fatal("invalid live neutral function observation")
				}
				if tool.Stage == "after" {
					expectedStatus := "completed"
					if success != nil && !*success {
						expectedStatus = "failed"
					}
					if tool.Observation.Status != expectedStatus || tool.Observation.Content == nil || len(*tool.Observation.Content) != 2 {
						t.Fatal("live result observation lost status or content")
					}
				}
			case proto.TypeFunctionCall:
				var call proto.FunctionCallPayload
				if err := event.DecodePayload(&call); err != nil {
					t.Fatal(err)
				}
				proof.FunctionCalls++
				beginSteering()
				if success == nil || proof.FunctionCalls != 1 || call.Name != "lookup" {
					t.Fatal("unexpected live function call")
				}
				first, second := functionNonce, "ordered-second-part"
				if !*success {
					first, second = "synthetic-current-failure", "do-not-retry"
				}
				value := proto.FunctionResultPayload{CallID: call.CallID, DeliveryID: uuid.NewString(), Success: *success, Content: []proto.FunctionResultContent{{Type: "input_text", Text: &first}, {Type: "input_text", Text: &second}}}
				if err := s.SubmitFunctionResult(ctx, value); err != nil {
					t.Fatalf("live native result receipt failed: %v; proof root %s", err, root)
				}
				proof.AppliedResults++
			case proto.TypeError:
				var payload proto.ErrorPayload
				_ = json.Unmarshal(event.Payload, &payload)
				proof.Failure = payload.Error
			case proto.TypeDone:
				done = true
				var payload proto.DonePayload
				_ = json.Unmarshal(event.Payload, &payload)
				proof.Text = payload.Content
				proof.SessionID, _ = payload.Metadata[proto.DoneMetaAgentSessionID].(string)
				select {
				case <-s.process.Done():
				default:
					t.Fatal("daemon Done preceded process release")
				}
			}
		}
		if !steeringAt.IsZero() {
			receipt := <-steeringReply
			if receipt.err != nil {
				proof.Failure = "steering receipt: " + receipt.err.Error()
			} else {
				proof.SteeringConfirmed = true
			}
			proof.SteeringMilliseconds = receipt.elapsed
		}
		released := <-observed
		proof.NativePIDs, proof.ChildPIDs = released.native, released.all
		if !done {
			t.Fatal("no daemon completion before timeout")
		}
		for _, pid := range proof.ChildPIDs {
			if value, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
				fields := strings.Fields(string(value)[strings.LastIndex(string(value), ")")+1:])
				if len(fields) == 0 || fields[0] != "Z" {
					t.Fatalf("SDK child %d remains alive after Done", pid)
				}
			}
		}

		mu.Lock()
		proof.ProviderRequests = append([]providerRequest{}, requests[requestStart:]...)
		mu.Unlock()
		for _, sent := range proof.ProviderRequests {
			if sent.Model != "MiniMax-M3" {
				t.Fatal("unexpected provider model", sent.Model)
			}
			want := 0
			if success != nil {
				want = 1
			}
			if len(sent.Tools) != want {
				t.Fatal("native tool inventory widened", sent.Tools)
			}
			for _, tool := range sent.Tools {
				if tool.Name != "mcp__functions__lookup" {
					t.Fatal("undeclared native tool", tool.Name)
				}
			}
		}
		if _, err := os.Stat(canary); !os.IsNotExist(err) {
			t.Fatal("ambient MCP configuration was not excluded", err)
		}
		data, err := json.MarshalIndent(proof, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("execution-%d.json", proof.NodePID)), data, 0o600); err != nil {
			t.Fatal(err)
		}
		return proof
	}
	nonce := "sdk-adapter-" + uuid.NewString()
	first := run("Remember this exact verification value and reply with it: "+nonce, "", nil)
	if first.Failure != "" || first.SessionID == "" || !strings.Contains(first.Text, nonce) || len(first.NativePIDs) == 0 {
		t.Fatalf("first execution failed: %+v; evidence root %s", first, root)
	}
	verifyMessageEvents(t, first.Events, first.Text)
	second := run("Return only the exact verification value from the previous user message.", first.SessionID, nil)
	if second.Failure != "" || second.SessionID != first.SessionID || !strings.Contains(second.Text, nonce) || first.NodePID == second.NodePID || len(second.NativePIDs) == 0 {
		t.Fatalf("cold resume failed: %+v; evidence root %s", second, root)
	}
	verifyMessageEvents(t, second.Events, second.Text)
	for _, a := range first.NativePIDs {
		for _, b := range second.NativePIDs {
			if a == b {
				t.Fatal("native process was reused")
			}
		}
	}
	accepted, rejected := true, false
	functionFirst := run("Call lookup once with id 42 as a string. Report both returned parts verbatim.", "", &accepted)
	if functionFirst.Failure != "" || functionFirst.FunctionCalls != 1 || functionFirst.AppliedResults != 1 || !strings.Contains(functionFirst.Text, functionNonce) || !strings.Contains(functionFirst.Text, "ordered-second-part") {
		t.Fatalf("live function failed: %+v", functionFirst)
	}
	functionSecond := run("Call lookup once with id 42 as a string. Report the prior verification value and both current result parts. Do not retry.", functionFirst.SessionID, &rejected)
	if functionSecond.Failure != "" || functionSecond.FunctionCalls != 1 || functionSecond.AppliedResults != 1 || functionSecond.SessionID != functionFirst.SessionID || !strings.Contains(functionSecond.Text, functionNonce) || !strings.Contains(functionSecond.Text, "synthetic-current-failure") || !strings.Contains(functionSecond.Text, "do-not-retry") || functionSecond.NodePID == functionFirst.NodePID {
		t.Fatalf("live function resume failed: %+v", functionSecond)
	}
	steeringNonce := "live-steering-" + uuid.NewString()
	steered := run("Write twelve short numbered observations about trees. Use no tools.", "", nil, "Remember this additional verification value and return it verbatim: "+steeringNonce)
	if steered.Failure != "" || !steered.SteeringConfirmed || !strings.Contains(steered.Text, steeringNonce) {
		t.Fatalf("live steering failed: %+v", steered)
	}
	steeredResume := run("Return only the exact live-steering verification value from the previous conversation.", steered.SessionID, nil)
	if steeredResume.Failure != "" || steeredResume.SessionID != steered.SessionID || !strings.Contains(steeredResume.Text, steeringNonce) || steeredResume.NodePID == steered.NodePID {
		t.Fatalf("steered cold continuation failed: %+v", steeredResume)
	}
	functionSteered := run("Call lookup once with id 42 as a string. Report both returned parts verbatim.", "", &accepted, "Also remember and report this value: "+steeringNonce)
	if functionSteered.Failure != "" || !functionSteered.SteeringConfirmed || functionSteered.FunctionCalls != 1 || functionSteered.AppliedResults != 1 || !strings.Contains(functionSteered.Text, steeringNonce) || !strings.Contains(functionSteered.Text, functionNonce) {
		t.Fatalf("live function steering failed: %+v", functionSteered)
	}
	for _, completed := range []evidence{first, second, functionFirst, functionSecond, steered, steeredResume, functionSteered} {
		verifyLiveUsageEvents(t, completed.Events)
	}
	mu.Lock()
	before := len(requests)
	mu.Unlock()
	missing := run("Say hello.", uuid.NewString(), nil)
	mu.Lock()
	measured := append([]providerRequest{}, requests...)
	mu.Unlock()
	if !strings.Contains(missing.Failure, "history_unavailable") || missing.SessionID != "" || len(missing.NativePIDs) != 0 || len(measured) != before {
		t.Fatalf("missing history did not fail before native/model start: %+v", missing)
	}
	if len(measured) < 2 {
		t.Fatal("expected real model requests")
	}
	for _, request := range measured {
		if request.Model != "MiniMax-M3" {
			t.Fatalf("unexpected requested model %q", request.Model)
		}
	}
	data, _ := json.MarshalIndent(map[string]any{"scope": "private Go factory -> official SDK -> real MiniMax with default typed execution controls, active input, native continuation and disabled environment/subagent tools; public API not enabled; no filesystem isolation claim", "turns": []evidence{first, second, functionFirst, functionSecond, steered, steeredResume, functionSteered}, "missing_history": missing, "model_requests": measured}, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "proof.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("real adapter proof: %s", filepath.Join(root, "proof.json"))
}
