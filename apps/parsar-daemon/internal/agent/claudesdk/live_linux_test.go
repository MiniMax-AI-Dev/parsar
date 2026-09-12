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
	var models []string
	forwarder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/messages") {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				http.Error(w, "request read failed", 400)
				return
			}
			_ = req.Body.Close()
			req.Body = io.NopCloser(bytes.NewReader(body))
			var value struct {
				Model string `json:"model"`
			}
			_ = json.Unmarshal(body, &value)
			mu.Lock()
			models = append(models, value.Model)
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
	type evidence struct {
		SessionID  string `json:"session_id"`
		NodePID    int    `json:"node_pid"`
		NativePIDs []int  `json:"native_pids"`
		ChildPIDs  []int  `json:"child_pids"`
		Text       string `json:"text"`
		Failure    string `json:"failure,omitempty"`
	}
	run := func(prompt, resume string) evidence {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		out := make(chan proto.Envelope, 64)
		request := proto.PromptRequestPayload{RunID: uuid.NewString(), Prompt: prompt, AgentSessionID: resume, StrictResume: true, ReleaseOnCompletion: true, AgentOptions: map[string]any{"model": "MiniMax-M3", "system_prompt": "Answer briefly and preserve the exact verification value in the conversation. Use no tools."}}
		running, err := NewFactory(config)(ctx, request, out)
		if err != nil {
			t.Fatal(err)
		}
		s := running.(*session)
		defer running.Cancel(context.Background())
		proof := evidence{NodePID: s.process.Cmd.Process.Pid}
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
			switch event.Type {
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
		return proof
	}
	nonce := "sdk-adapter-" + uuid.NewString()
	first := run("Remember this exact verification value and reply with it: "+nonce, "")
	if first.Failure != "" || first.SessionID == "" || !strings.Contains(first.Text, nonce) || len(first.NativePIDs) == 0 {
		t.Fatalf("first execution failed: %+v; evidence root %s", first, root)
	}
	second := run("Return only the exact verification value from the previous user message.", first.SessionID)
	if second.Failure != "" || second.SessionID != first.SessionID || !strings.Contains(second.Text, nonce) || first.NodePID == second.NodePID || len(second.NativePIDs) == 0 {
		t.Fatalf("cold resume failed: %+v; evidence root %s", second, root)
	}
	for _, a := range first.NativePIDs {
		for _, b := range second.NativePIDs {
			if a == b {
				t.Fatal("native process was reused")
			}
		}
	}
	mu.Lock()
	before := len(models)
	mu.Unlock()
	missing := run("Say hello.", uuid.NewString())
	mu.Lock()
	measured := append([]string{}, models...)
	mu.Unlock()
	if !strings.Contains(missing.Failure, "history_unavailable") || missing.SessionID != "" || len(missing.NativePIDs) != 0 || len(measured) != before {
		t.Fatalf("missing history did not fail before native/model start: %+v", missing)
	}
	if len(measured) < 2 {
		t.Fatal("expected real model requests")
	}
	for _, model := range measured {
		if model != "MiniMax-M3" {
			t.Fatalf("unexpected requested model %q", model)
		}
	}
	data, _ := json.MarshalIndent(map[string]any{"scope": "Go daemon factory -> official SDK -> real MiniMax API; public API not enabled", "turns": []evidence{first, second}, "missing_history": missing, "model_requests": measured}, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "proof.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("real adapter proof: %s", filepath.Join(root, "proof.json"))
}
