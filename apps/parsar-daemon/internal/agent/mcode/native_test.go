package mcode

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Opt in with the installed native CLI; the default test gate uses protocol fixtures.
func TestNativeMCodeACP(t *testing.T) {
	binary := os.Getenv("PARSAR_MCODE_INTEGRATION_BIN")
	if binary == "" {
		t.Skip("set PARSAR_MCODE_INTEGRATION_BIN to run native ACP smoke test")
	}
	req := testRequest(t)
	var mu sync.Mutex
	var requests []string
	mcpCalls := 0
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&msg) != nil {
			w.WriteHeader(400)
			return
		}
		if len(msg.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch msg.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "fixture", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "get_fixture", "description": "Returns the fixture marker", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		case "tools/call":
			mu.Lock()
			mcpCalls++
			mu.Unlock()
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": "MCP-READY"}}}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	}))
	defer mcp.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		raw, _ := json.Marshal(body)
		mu.Lock()
		requests = append(requests, string(raw))
		mu.Unlock()
		tool := ""
		if !strings.Contains(string(raw), "MCP-READY") && strings.Contains(string(raw), "QA-CALL-MCP") {
			tools, _ := body["tools"].([]any)
			for _, entry := range tools {
				value, _ := entry.(map[string]any)
				name, _ := value["name"].(string)
				if strings.Contains(name, "get_fixture") {
					tool = name
					break
				}
			}
		}
		writeNativeResponse(w, tool)
	}))
	defer model.Close()
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	f, _ := zw.Create("SKILL.md")
	_, _ = f.Write([]byte("---\nname: qa-mcode-skill\ndescription: Test skill marker SKILL-MCODE-451\n---\nReturn SKILL-MCODE-451.\n"))
	_ = zw.Close()
	digest := sha256.Sum256(archive.Bytes())
	skill := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive.Bytes()) }))
	defer skill.Close()
	req.AgentOptions["mcode_provider"] = map[string]any{"name": "Parsar", "kind": "custom", "enabled": true, "npm": "@ai-sdk/anthropic", "options": map[string]any{"apiKey": "fixture-only", "baseURL": model.URL}, "models": map[string]any{"fixture": map[string]any{"name": "Fixture", "tool_call": true, "limit": map[string]int{"context": 64000, "output": 4096}}}}
	req.AgentOptions["skills"] = []any{map[string]any{"name": "qa-mcode-skill", "version": "1", "download_url": skill.URL, "sha256": hex.EncodeToString(digest[:])}}
	req.AgentOptions["mcp_servers"] = map[string]any{"qa": map[string]any{"type": "http", "url": mcp.URL}}
	req.AgentOptions["system_prompt"] = "SP-MCODE-672: use the available tools when requested."
	req.Prompt = "QA-CALL-MCP: call get_fixture, then reply PARSAR-MCODE-OK."
	run := func() proto.DonePayload {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		t.Cleanup(cancel)
		out := make(chan proto.Envelope, 64)
		session, err := newSession(ctx, req, out, binary)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = session.Cancel(context.Background())
			select {
			case <-session.exited:
			case <-time.After(5 * time.Second):
				t.Error("native CLI did not stop")
			}
		})
		var done proto.DonePayload
		for event := range out {
			if event.Type == proto.TypeError {
				t.Fatalf("native ACP failure: %s", event.Payload)
			}
			if event.Type == proto.TypeDone {
				_ = json.Unmarshal(event.Payload, &done)
			}
		}
		if done.Content != "PARSAR-MCODE-OK" {
			t.Fatalf("native output=%q", done.Content)
		}
		if _, ok := done.Metadata[proto.DoneMetaAgentSessionID].(string); !ok {
			t.Fatal("native session ID missing")
		}
		return done
	}
	done := run()
	mu.Lock()
	firstRequests := strings.Join(requests, "\n")
	calls := mcpCalls
	requests = nil
	mu.Unlock()
	if !strings.Contains(firstRequests, "SP-MCODE-672") || !strings.Contains(firstRequests, "qa-mcode-skill") {
		t.Fatalf("native context missing: prompt=%t skill=%t", strings.Contains(firstRequests, "SP-MCODE-672"), strings.Contains(firstRequests, "qa-mcode-skill"))
	}
	if calls == 0 {
		t.Fatal("native MCP was not called")
	}
	req.RunID = "run-2"
	req.AgentSessionID = done.Metadata[proto.DoneMetaAgentSessionID].(string)
	req.AgentOptions["system_prompt"] = "SP-MCODE-NEW: reply concisely."
	req.AgentOptions["skills"] = []any{}
	provider := req.AgentOptions["mcode_provider"].(map[string]any)
	models := provider["models"].(map[string]any)
	models["fixture-new"] = models["fixture"]
	delete(models, "fixture")
	req.AgentOptions["model"] = "fixture-new"
	req.Prompt = "Now reply PARSAR-MCODE-OK."
	run()
	mu.Lock()
	resumed := strings.Join(requests, "\n")
	mu.Unlock()
	if !strings.Contains(resumed, "SP-MCODE-NEW") {
		t.Fatal("resume retained stale instructions")
	}
	if !strings.Contains(resumed, `"model":"fixture-new"`) {
		t.Fatal("resume did not use the updated model")
	}
	t.Logf("native new/resume, model, instructions, Skill discovery and MCP verified (%d tool calls)", calls)
}

func writeNativeResponse(w http.ResponseWriter, tool string) {
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(kind string, payload any) {
		data, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
	}
	send("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_qa", "type": "message", "role": "assistant", "model": "fixture", "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 20, "output_tokens": 0}}})
	stop := "end_turn"
	if tool != "" {
		send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "call_fixture", "name": tool, "input": map[string]any{}}})
		send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "input_json_delta", "partial_json": "{}"}})
		stop = "tool_use"
	} else {
		send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]string{"type": "text", "text": ""}})
		send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": "PARSAR-MCODE-OK"}})
	}
	send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 8}})
	send("message_stop", map[string]string{"type": "message_stop"})
}
