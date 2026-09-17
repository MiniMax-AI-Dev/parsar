package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const workspaceReadSuccess = `{"read":{"data_base64":"AAEC/w==","truncated":false,"close_acknowledged":true}}` + "\n"

func workspaceReadFixture(t *testing.T) (*Session, net.Listener) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, ".parsar")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "wr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	listener, err := net.Listen("unix", filepath.Join(root, "files.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	owner, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	session := &Session{
		harness: &privateHarness{root: root, environment: "frozen-environment"},
		rpc:     &JSONRPCClient{alive: true}, cancelCtx: owner, cancelFn: cancel,
	}
	return session, listener
}

func workspaceReadConnection(t *testing.T, listener net.Listener) (net.Conn, []byte) {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		t.Error(err)
		return nil, nil
	}
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Error(err)
	}
	frame, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Error(err)
	}
	return conn, frame
}

func TestWorkspaceReadValidatesAcknowledgedResult(t *testing.T) {
	for _, test := range []struct {
		name, response string
		limit          int
		want           error
		truncated      bool
	}{
		{name: "binary", response: workspaceReadSuccess, limit: 4},
		{name: "truncated", response: strings.Replace(workspaceReadSuccess, "false", "true", 1), limit: 4, truncated: true},
		{name: "empty", response: `{"read":{"data_base64":"","truncated":false,"close_acknowledged":true}}`, limit: 4},
		{name: "not found", response: `{"error":"not_found"}`, limit: 4, want: fs.ErrNotExist},
		{name: "permission", response: `{"error":"permission_denied"}`, limit: 4, want: fs.ErrPermission},
		{name: "native error", response: `{"error":"native_error"}`, limit: 4, want: agent.ErrWorkspaceReadUnavailable},
		{name: "unknown error", response: `{"error":"secret native detail"}`, limit: 4, want: agent.ErrWorkspaceReadUncertain},
		{name: "no close", response: strings.Replace(workspaceReadSuccess, `,"close_acknowledged":true`, "", 1), limit: 4, want: agent.ErrWorkspaceReadUncertain},
		{name: "false close", response: strings.Replace(workspaceReadSuccess, `"close_acknowledged":true`, `"close_acknowledged":false`, 1), limit: 4, want: agent.ErrWorkspaceReadUncertain},
		{name: "no truncation", response: strings.Replace(workspaceReadSuccess, `,"truncated":false`, "", 1), limit: 4, want: agent.ErrWorkspaceReadUncertain},
		{name: "oversize", response: workspaceReadSuccess, limit: 3, want: agent.ErrWorkspaceReadUncertain},
		{name: "short truncation", response: strings.Replace(workspaceReadSuccess, "false", "true", 1), limit: 5, want: agent.ErrWorkspaceReadUncertain},
		{name: "bad base64", response: strings.Replace(workspaceReadSuccess, "AAEC/w==", "%%%", 1), limit: 4, want: agent.ErrWorkspaceReadUncertain},
		{name: "trailing frame", response: workspaceReadSuccess + `{}`, limit: 4, want: agent.ErrWorkspaceReadUncertain},
		{name: "unknown field", response: strings.Replace(workspaceReadSuccess, `"read":`, `"extra":true,"read":`, 1), limit: 4, want: agent.ErrWorkspaceReadUncertain},
		{name: "ambiguous", response: strings.Replace(workspaceReadSuccess, `"read":`, `"error":"not_found","read":`, 1), limit: 4, want: agent.ErrWorkspaceReadUncertain},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := decodeWorkspaceRead([]byte(test.response), test.limit)
			if !errors.Is(err, test.want) || result.Truncated != test.truncated {
				t.Fatalf("unexpected result: %+v, %v", result, err)
			}
			if err != nil && len(result.Data) != 0 {
				t.Fatal("failed read returned partial bytes")
			}
			if err == nil && test.name != "empty" && string(result.Data) != string([]byte{0, 1, 2, 255}) {
				t.Fatal("binary bytes changed")
			}
		})
	}
}

func TestWorkspaceReadFrozenBindingAndAdmission(t *testing.T) {
	session, listener := workspaceReadFixture(t)
	for _, path := range []string{"", "/absolute", "../escape", "a/../b", "a\\b", "a\n", strings.Repeat("x", 8192)} {
		if _, err := session.ReadWorkspaceFile(t.Context(), path, 4); !errors.Is(err, agent.ErrWorkspaceReadInvalid) {
			t.Fatalf("path %q admitted: %v", path, err)
		}
	}
	for _, limit := range []int{0, -1, workspaceReadMaxBytes + 1} {
		if _, err := session.ReadWorkspaceFile(t.Context(), "file", limit); !errors.Is(err, agent.ErrWorkspaceReadInvalid) {
			t.Fatalf("limit %d admitted: %v", limit, err)
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, frame := workspaceReadConnection(t, listener)
		if conn == nil {
			return
		}
		defer conn.Close()
		var request map[string]any
		if err := json.Unmarshal(frame, &request); err != nil || len(request) != 4 || request["environment_id"] != "frozen-environment" || request["operation"] != "read" || request["path"] != "dir/file" || request["max_bytes"] != float64(4) {
			t.Errorf("binding changed: %s", frame)
		}
		_, _ = conn.Write([]byte(workspaceReadSuccess))
	}()
	if result, err := session.ReadWorkspaceFile(t.Context(), "dir/file", 4); err != nil || len(result.Data) != 4 {
		t.Fatalf("read: %+v %v", result, err)
	}
	<-done
	if _, err := (&Session{}).ReadWorkspaceFile(t.Context(), "file", 4); !errors.Is(err, agent.ErrWorkspaceReadUnsupported) {
		t.Fatal("stock resource admitted read", err)
	}
	session.cancelFn()
	if _, err := session.ReadWorkspaceFile(t.Context(), "file", 4); !errors.Is(err, agent.ErrWorkspaceReadUnavailable) {
		t.Fatal("cancelled owner admitted read", err)
	}
}

func TestWorkspaceReadRetainsCancelledObservationAndBusySlot(t *testing.T) {
	session, listener := workspaceReadFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := session.ReadWorkspaceFile(ctx, "file", 4)
		result <- err
	}()
	conn, _ := workspaceReadConnection(t, listener)
	if conn == nil {
		return
	}
	defer conn.Close()
	cancel()
	if _, err := session.ReadWorkspaceFile(t.Context(), "file", 4); !errors.Is(err, agent.ErrWorkspaceReadBusy) {
		t.Fatal("cancelled observer freed read slot", err)
	}
	select {
	case err := <-result:
		t.Fatal("cancelled observation discarded native wait", err)
	default:
	}
	_, _ = conn.Write([]byte(workspaceReadSuccess))
	if err := <-result; err != nil {
		t.Fatal("acknowledged result lost after observation cancellation", err)
	}
}

func TestWorkspaceReadUncertaintyStopsLaterReads(t *testing.T) {
	for _, response := range []string{"", "{broken}\n", strings.Repeat("x", 2048) + "\n"} {
		t.Run(response[:min(len(response), 8)], func(t *testing.T) {
			session, listener := workspaceReadFixture(t)
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, _ := workspaceReadConnection(t, listener)
				if conn != nil {
					_, _ = conn.Write([]byte(response))
					_ = conn.Close()
				}
			}()
			if result, err := session.ReadWorkspaceFile(t.Context(), "file", 4); !errors.Is(err, agent.ErrWorkspaceReadUncertain) || len(result.Data) != 0 {
				t.Fatal("ambiguous read succeeded", result, err)
			}
			<-done
			if _, err := session.ReadWorkspaceFile(t.Context(), "file", 4); !errors.Is(err, agent.ErrWorkspaceReadUncertain) {
				t.Fatal("uncertain owner admitted another read", err)
			}
		})
	}
}

func TestWorkspaceReadDeadlineRetainsUncertainty(t *testing.T) {
	session, listener := workspaceReadFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := session.ReadWorkspaceFile(ctx, "file", 4)
		result <- err
	}()
	conn, _ := workspaceReadConnection(t, listener)
	if conn == nil {
		return
	}
	defer conn.Close()
	if err := <-result; !errors.Is(err, agent.ErrWorkspaceReadUncertain) {
		t.Fatal("deadline did not preserve uncertainty", err)
	}
	if _, err := session.ReadWorkspaceFile(t.Context(), "file", 4); !errors.Is(err, agent.ErrWorkspaceReadUncertain) {
		t.Fatal("deadline admitted a replacement read", err)
	}
}

func TestWorkspaceReadOwnerExitDoesNotEstablishSettlement(t *testing.T) {
	session, listener := workspaceReadFixture(t)
	result := make(chan error, 1)
	go func() {
		_, err := session.ReadWorkspaceFile(t.Context(), "file", 4)
		result <- err
	}()
	conn, _ := workspaceReadConnection(t, listener)
	if conn == nil {
		return
	}
	session.cancelFn()
	_ = conn.Close()
	if err := <-result; !errors.Is(err, agent.ErrWorkspaceReadUncertain) {
		t.Fatal("owner exit reported read settlement", err)
	}
	if _, err := session.ReadWorkspaceFile(t.Context(), "file", 4); !errors.Is(err, agent.ErrWorkspaceReadUnavailable) {
		t.Fatal("exited owner admitted read", err)
	}
}

func TestWorkspaceReadFollowsPreparedTransfer(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	p, err := newPreparation(t.Context(), req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	fixture, listener := workspaceReadFixture(t)
	p.session.harness = fixture.harness
	result := make(chan error, 1)
	go func() {
		_, err := p.ReadWorkspaceFile(t.Context(), "file", 4)
		result <- err
	}()
	conn, _ := workspaceReadConnection(t, listener)
	if conn == nil {
		return
	}
	defer conn.Close()
	started, err := p.Start(t.Context(), "actual-run", "actual prompt", make(chan proto.Envelope, 16))
	if err != nil {
		t.Fatal(err)
	}
	session := started.(*Session)
	defer session.Cancel(context.Background())
	if _, err := p.ReadWorkspaceFile(t.Context(), "file", 4); !errors.Is(err, agent.ErrWorkspaceReadUnavailable) {
		t.Fatal("transferred preparation admitted read", err)
	}
	if _, err := session.ReadWorkspaceFile(t.Context(), "file", 4); !errors.Is(err, agent.ErrWorkspaceReadBusy) {
		t.Fatal("transfer lost admitted read", err)
	}
	_, _ = conn.Write([]byte(workspaceReadSuccess))
	if err := <-result; err != nil {
		t.Fatal("read failed across Start", err)
	}
	waitPreparationMethod(t, root, "turn/start")
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, _ := workspaceReadConnection(t, listener)
		if conn != nil {
			_, _ = conn.Write([]byte(workspaceReadSuccess))
			_ = conn.Close()
		}
	}()
	if _, err := session.ReadWorkspaceFile(t.Context(), "file", 4); err != nil {
		t.Fatal("transferred Session cannot read", err)
	}
	<-done
}
