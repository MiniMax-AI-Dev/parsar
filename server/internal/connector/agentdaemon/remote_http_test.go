package agentdaemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// TestInternalSubmitPermission_AppliesLocallyOnOwnerPod is the
// receiving end of the multi-pod flow: the webhook pod POSTs to
// /internal/agent-daemon/submit-permission and the owner pod must
// translate the body into a permission_decision envelope on the
// device's WS.
func TestInternalSubmitPermission_AppliesLocallyOnOwnerPod(t *testing.T) {
	c, _, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")

	// Attach a perm so SubmitPermissionLocal can find a session to send to.
	c.registry.AttachPermission("perm-x", sess)

	router := chi.NewRouter()
	RegisterInternalRoutes(router, c, "tok")
	srv := httptest.NewServer(router)
	defer srv.Close()

	body, _ := json.Marshal(remoteSubmitPermissionRequest{
		Generation: 0, // 0 skips stale-owner check (ownerResolver is nil here)
		Decision: connector.PermissionDecision{
			RequestID: "perm-x",
			Approved:  true,
			Note:      "lgtm",
		},
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+internalSubmitPermissionPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(buf))
	}
	if !waitForWrite(t, conn, proto.TypePermissionDecision, 2*time.Second) {
		t.Fatal("permission_decision envelope never written to device")
	}
}

func TestInternalSubmitPermission_RejectsBadToken(t *testing.T) {
	c, _, sess, _, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")

	router := chi.NewRouter()
	RegisterInternalRoutes(router, c, "tok")
	srv := httptest.NewServer(router)
	defer srv.Close()

	body, _ := json.Marshal(remoteSubmitPermissionRequest{
		Generation: 0,
		Decision:   connector.PermissionDecision{RequestID: "perm-x"},
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+internalSubmitPermissionPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}

func TestInternalSubmitPermission_RejectsBadJSON(t *testing.T) {
	c, _, sess, _, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")

	router := chi.NewRouter()
	RegisterInternalRoutes(router, c, "tok")
	srv := httptest.NewServer(router)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+internalSubmitPermissionPath, strings.NewReader("{"))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
}

// TestInternalSubmitPromptForUserChoice_AppliesLocallyOnOwnerPod
// mirrors the permission endpoint for the ask-user-question path.
func TestInternalSubmitPromptForUserChoice_AppliesLocallyOnOwnerPod(t *testing.T) {
	c, _, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")
	c.registry.AttachPromptForUserChoice("ask-y", sess)

	router := chi.NewRouter()
	RegisterInternalRoutes(router, c, "tok")
	srv := httptest.NewServer(router)
	defer srv.Close()

	body, _ := json.Marshal(remoteSubmitPromptForUserChoiceRequest{
		Generation: 0,
		Decision: connector.PromptForUserChoiceDecision{
			RequestID: "ask-y",
			Answers:   []string{"yes"},
		},
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+internalSubmitPromptForUserChoicePath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(buf))
	}
	if !waitForWrite(t, conn, proto.TypePromptForUserChoiceDecision, 2*time.Second) {
		t.Fatal("prompt_for_user_choice_decision envelope never written to device")
	}
}

// TestInternalSubmitPermission_StaleGenerationConflict guards against
// the case where the owner pod has restarted (new generation) between
// when the webhook pod read the owner row and when the POST arrived.
// We want a 409 so the webhook pod surfaces "Please retry later" rather than
// silently sending the decision to a stale-state pod.
func TestInternalSubmitPermission_StaleGenerationConflict(t *testing.T) {
	c, _, sess, _, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")

	// Wire ownerResolver + submitSlots so assertLocalOwnerForSubmit runs.
	c.ownerResolver = fakeOwnerResolver{
		owner: store.AgentDaemonDeviceOwnerRead{
			DeviceID:       "dev-1",
			OwnerPodID:     "pod-a",
			Generation:     11,
			Status:         store.AgentDaemonOwnerStatusConnected,
			LeaseExpiresAt: time.Now().Add(time.Minute),
		},
		ok: true,
	}
	c.ownerPodID = "pod-a"
	c.submitSlots = fakeSubmitSlots{perms: map[string]string{"perm-x": "dev-1"}}
	c.registry.AttachPermission("perm-x", sess)

	router := chi.NewRouter()
	RegisterInternalRoutes(router, c, "tok")
	srv := httptest.NewServer(router)
	defer srv.Close()

	// Body carries generation=10, owner row has 11 → stale.
	body, _ := json.Marshal(remoteSubmitPermissionRequest{
		Generation: 10,
		Decision:   connector.PermissionDecision{RequestID: "perm-x", Approved: true},
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+internalSubmitPermissionPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d, want 409", resp.StatusCode)
	}
}
