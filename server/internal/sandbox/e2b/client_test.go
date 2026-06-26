package e2b

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCreateRunCommandAndKill(t *testing.T) {
	var killed bool
	envd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/process.Process/Start" {
			t.Fatalf("unexpected envd path %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Access-Token"); got != "envd-token" {
			t.Fatalf("missing access token: %q", got)
		}
		var reqPayload struct {
			Process struct {
				Cmd  string            `json:"cmd"`
				Args []string          `json:"args"`
				Envs map[string]string `json:"envs"`
			} `json:"process"`
		}
		if err := readConnectEnvelope(r.Body, &reqPayload); err != nil {
			t.Fatalf("read request envelope: %v", err)
		}
		if reqPayload.Process.Cmd != "/bin/bash" {
			t.Fatalf("unexpected command wrapper %q", reqPayload.Process.Cmd)
		}
		if len(reqPayload.Process.Args) != 3 || reqPayload.Process.Args[2] != "echo hi" {
			t.Fatalf("unexpected args %#v", reqPayload.Process.Args)
		}
		stdout := base64.StdEncoding.EncodeToString([]byte("hi\n"))
		writeConnectEnvelope(t, w, map[string]any{"event": map[string]any{"start": map[string]any{"pid": 123}}})
		writeConnectEnvelope(t, w, map[string]any{"event": map[string]any{"data": map[string]any{"stdout": stdout}}})
		writeConnectEnvelope(t, w, map[string]any{"event": map[string]any{"end": map[string]any{"exited": true, "status": "exit status 0"}}})
	}))
	defer envd.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "test-key" {
			t.Fatalf("missing api key: %q", got)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			var req struct {
				TemplateID string            `json:"templateID"`
				Timeout    int               `json:"timeout"`
				EnvVars    map[string]string `json:"envVars"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if req.TemplateID != "base" || req.Timeout != 60 || req.EnvVars["HELLO"] != "world" {
				t.Fatalf("unexpected create request %#v", req)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(Sandbox{TemplateID: "base", SandboxID: "sbx_123", EnvdVersion: "0.2.0", EnvdAccessToken: "envd-token"})
		case r.Method == http.MethodDelete && r.URL.Path == "/sandboxes/sbx_123":
			killed = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected api request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer api.Close()

	client := &Client{HTTPClient: api.Client(), APIBaseURL: api.URL, SandboxBaseURL: envd.URL, APIKey: "test-key"}
	sbx, err := client.Create(context.Background(), CreateInput{TemplateID: "base", TimeoutSeconds: 60, Env: map[string]string{"HELLO": "world"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	result, err := client.RunCommand(context.Background(), RunCommandInput{Sandbox: sbx, Command: "echo hi", Timeout: time.Second})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if result.PID != 123 || result.Stdout != "hi\n" || !result.Exited || result.Status != "0" {
		t.Fatalf("unexpected result %#v", result)
	}
	if err := client.Kill(context.Background(), sbx.SandboxID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if !killed {
		t.Fatalf("expected sandbox kill")
	}
}

func TestRedactsAPIKeyFromAPIErrorMessage(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "invalid key sk_live_SECRET_123"})
	}))
	defer api.Close()

	client := &Client{HTTPClient: api.Client(), APIBaseURL: api.URL, APIKey: "sk_live_SECRET_123"}
	_, err := client.Create(context.Background(), CreateInput{})
	if err == nil {
		t.Fatalf("expected create error")
	}
	msg := err.Error()
	if msg == "" || msg == "sk_live_SECRET_123" {
		t.Fatalf("unexpected error %q", msg)
	}
	if contains := strings.Contains(msg, "sk_live_SECRET_123"); contains {
		t.Fatalf("error leaked api key: %s", msg)
	}
	if !strings.Contains(msg, "[REDACTED]") {
		t.Fatalf("expected redacted marker, got %s", msg)
	}
}

func readConnectEnvelope(r io.Reader, out any) error {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header[1:])
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}
	return json.Unmarshal(payload, out)
}

func writeConnectEnvelope(t *testing.T, w http.ResponseWriter, msg any) {
	t.Helper()
	payload, err := marshalConnectEnvelope(msg)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	_, _ = w.Write(payload)
}

func TestSandboxPortURL(t *testing.T) {
	c := &Client{}
	sb := Sandbox{SandboxID: "sbx_phase_b1", Domain: "example.test"}

	t.Run("envd default", func(t *testing.T) {
		got := c.SandboxPortURL(sb, 0)
		want := "https://49983-sbx_phase_b1.example.test"
		if got != want {
			t.Fatalf("port=0 should fall back to DefaultEnvdPort: got %q want %q", got, want)
		}
	})

	t.Run("opencode port", func(t *testing.T) {
		got := c.SandboxPortURL(sb, 4096)
		want := "https://4096-sbx_phase_b1.example.test"
		if got != want {
			t.Fatalf("port=4096 mismatch: got %q want %q", got, want)
		}
	})

	t.Run("override base url wins", func(t *testing.T) {
		c2 := &Client{SandboxBaseURL: "http://127.0.0.1:9999"}
		got := c2.SandboxPortURL(sb, 4096)
		want := "http://127.0.0.1:9999"
		if got != want {
			t.Fatalf("SandboxBaseURL must override: got %q want %q", got, want)
		}
	})

	t.Run("client SandboxHost fallback", func(t *testing.T) {
		c3 := &Client{SandboxHost: "tenant.e2b-compat.test"}
		bare := Sandbox{SandboxID: "sbx_no_domain"}
		got := c3.SandboxPortURL(bare, 4096)
		want := "https://4096-sbx_no_domain.tenant.e2b-compat.test"
		if got != want {
			t.Fatalf("SandboxHost fallback mismatch: got %q want %q", got, want)
		}
	})
}
