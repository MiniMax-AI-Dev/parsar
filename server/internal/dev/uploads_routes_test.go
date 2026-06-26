package dev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/oss"
)

// uploadsTestWorkspaceID + uploadsTestUserID match
// store.DefaultDevFixtureIDs so requestContextForRBAC's dev fallback
// (when no session is wired) resolves to a known user that
// roleStubStore can recognize.
const (
	uploadsTestWorkspaceID = "00000000-0000-0000-0000-000000000002"
	uploadsTestUserID      = "00000000-0000-0000-0000-000000000001"
)

// fakeOSSClient is a deterministic OSSClient stand-in for the
// uploads_routes tests. It records the most recent call and returns
// canned URLs / expirations without touching the network.
//
// Set returnErr to simulate a signer failure; the handler should
// surface a 500 without leaking the inner message.
type fakeOSSClient struct {
	lastPutKey       string
	lastPutTTL       time.Duration
	lastGetKey       string
	lastGetTTL       time.Duration
	urlFromKey       func(key string) string
	expiresAt        time.Time
	returnErr        error
	returnErrOnPut   bool
	returnErrOnGet   bool
	returnInvalidGet bool // surface oss.ErrInvalidKey on Get
	// download lets a test simulate a successful Download by returning
	// the bytes-for-key it expects to see in the import handler. If
	// nil, Download returns the legacy "not stubbed" error so existing
	// tests don't accidentally start receiving bytes they didn't ask
	// for. Used by the skill / plugin zip import integration tests.
	download func(key string) ([]byte, error)
}

func (f *fakeOSSClient) PresignPut(_ context.Context, key string, ttl time.Duration) (string, time.Time, error) {
	f.lastPutKey = key
	f.lastPutTTL = ttl
	if f.returnErrOnPut {
		return "", time.Time{}, f.returnErr
	}
	return f.urlFromKey(key) + "?put=1", f.expiresAt, nil
}

func (f *fakeOSSClient) PresignGet(_ context.Context, key string, ttl time.Duration) (string, time.Time, error) {
	f.lastGetKey = key
	f.lastGetTTL = ttl
	if f.returnInvalidGet {
		return "", time.Time{}, oss.ErrInvalidKey
	}
	if f.returnErrOnGet {
		return "", time.Time{}, f.returnErr
	}
	return f.urlFromKey(key) + "?get=1", f.expiresAt, nil
}

func (f *fakeOSSClient) Download(_ context.Context, key string) ([]byte, error) {
	if f.download != nil {
		return f.download(key)
	}
	return nil, errors.New("fakeOSSClient: Download not stubbed in uploads_routes_test")
}

func newFakeOSS() *fakeOSSClient {
	return &fakeOSSClient{
		urlFromKey: func(key string) string { return "https://fake.example/" + key },
		expiresAt:  time.Now().Add(time.Hour).UTC().Truncate(time.Second),
	}
}

// uploadsTestRuntime drives requireWorkspaceCapabilityAdmin against a
// known user/role pair. Reuses the package's existing roleStubStore so
// we don't have to satisfy the (large) RuntimeStore interface ourselves.
type uploadsTestRuntime struct {
	roleStubStore
	workspaceID string
	userID      string
}

func newUploadsTestRouter(t *testing.T, client OSSClient) (http.Handler, *uploadsTestRuntime) {
	t.Helper()
	rt := &uploadsTestRuntime{
		roleStubStore: newRoleStubStore(map[string]string{uploadsTestUserID: "admin"}),
		workspaceID:   uploadsTestWorkspaceID,
		userID:        uploadsTestUserID,
	}
	r := chi.NewRouter()
	r.Post("/api/v1/workspaces/{workspaceID}/uploads/presign-upload", presignUpload(rt, client))
	r.Post("/api/v1/workspaces/{workspaceID}/uploads/presign-download", presignDownload(rt, client))
	return r, rt
}

// callRouter posts body to path on the supplied chi router and returns
// status + decoded JSON body. Replaces the old callHandler that bypassed
// routing — uploads handlers now require URL params + middleware.
func callRouter(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response: %v; raw=%q", err, rec.Body.String())
		}
	}
	return rec.Code, out
}

func uploadsPath(suffix string) string {
	return "/api/v1/workspaces/" + uploadsTestWorkspaceID + "/uploads/" + suffix
}

func TestPresignUpload_NilClient503(t *testing.T) {
	t.Parallel()
	r, _ := newUploadsTestRouter(t, nil)
	status, body := callRouter(t, r, "POST", uploadsPath("presign-upload"), `{"filename":"x.zip","prefix":"plugin"}`)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if got, _ := body["code"].(string); got != "OSS_NOT_CONFIGURED" {
		t.Fatalf("body.code = %v, want OSS_NOT_CONFIGURED", body["code"])
	}
}

func TestPresignUpload_RejectsBadJSON(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	r, _ := newUploadsTestRouter(t, f)
	status, _ := callRouter(t, r, "POST", uploadsPath("presign-upload"), `{not json`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if f.lastPutKey != "" {
		t.Fatalf("client should not be called on bad json; lastPutKey=%q", f.lastPutKey)
	}
}

func TestPresignUpload_RequiresFilename(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	r, _ := newUploadsTestRouter(t, f)
	status, body := callRouter(t, r, "POST", uploadsPath("presign-upload"), `{"filename":"","prefix":"plugin"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "filename is required") {
		t.Fatalf("body.error = %q, missing 'filename is required'", msg)
	}
}

func TestPresignUpload_RejectsUnknownPrefix(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	r, _ := newUploadsTestRouter(t, f)
	status, body := callRouter(t, r, "POST", uploadsPath("presign-upload"), `{"filename":"x.zip","prefix":"evil"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "prefix must be one of") {
		t.Fatalf("body.error = %q, missing prefix allowlist hint", msg)
	}
}

func TestPresignUpload_PluginPrefixMintsWorkspaceScopedKey(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	r, _ := newUploadsTestRouter(t, f)
	status, body := callRouter(t, r, "POST", uploadsPath("presign-upload"), `{"filename":"my-plugin.zip","prefix":"plugin"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", status, body)
	}
	key, _ := body["ossKey"].(string)
	wantPrefix := oss.PluginObjectPrefix + "/" + uploadsTestWorkspaceID + "/"
	if !strings.HasPrefix(key, wantPrefix) {
		t.Fatalf("ossKey = %q, missing workspace-scoped prefix %q", key, wantPrefix)
	}
	if !strings.HasSuffix(key, "/my-plugin.zip") {
		t.Fatalf("ossKey = %q, missing filename suffix", key)
	}
}

func TestPresignUpload_DefaultTTLDelegatedToClient(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	r, _ := newUploadsTestRouter(t, f)
	status, _ := callRouter(t, r, "POST", uploadsPath("presign-upload"), `{"filename":"x.zip","prefix":"plugin"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if f.lastPutTTL != 0 {
		t.Fatalf("expected handler to delegate TTL default to client; lastPutTTL=%v", f.lastPutTTL)
	}
}

func TestPresignUpload_ExplicitExpiresSecondsHonoured(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	r, _ := newUploadsTestRouter(t, f)
	status, _ := callRouter(t, r, "POST", uploadsPath("presign-upload"), `{"filename":"x.zip","prefix":"plugin","expiresSeconds":600}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if f.lastPutTTL != 10*time.Minute {
		t.Fatalf("lastPutTTL = %v, want 10m", f.lastPutTTL)
	}
}

func TestPresignUpload_SignerFailure500(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	f.returnErr = errors.New("signer exploded")
	f.returnErrOnPut = true
	r, _ := newUploadsTestRouter(t, f)
	status, body := callRouter(t, r, "POST", uploadsPath("presign-upload"), `{"filename":"x.zip","prefix":"plugin"}`)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if msg, _ := body["error"].(string); strings.Contains(msg, "signer exploded") {
		t.Fatalf("body.error leaked inner error: %q", msg)
	}
}

func TestPresignUpload_NonAdminRejected(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	r, rt := newUploadsTestRouter(t, f)
	// Flip the runtime so the default fixture user is no longer admin —
	// roleStubStore returns ErrNotMember for missing entries.
	rt.roleStubStore = newRoleStubStore(map[string]string{})
	status, _ := callRouter(t, r, "POST", uploadsPath("presign-upload"), `{"filename":"x.zip","prefix":"plugin"}`)
	if status == http.StatusOK {
		t.Fatalf("status = 200, expected admin gate to block non-admin user")
	}
}

func TestPresignDownload_NilClient503(t *testing.T) {
	t.Parallel()
	r, _ := newUploadsTestRouter(t, nil)
	status, body := callRouter(t, r, "POST", uploadsPath("presign-download"), `{"ossKey":"capabilities/plugins/`+uploadsTestWorkspaceID+`/u/x.zip"}`)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if got, _ := body["code"].(string); got != "OSS_NOT_CONFIGURED" {
		t.Fatalf("body.code = %v, want OSS_NOT_CONFIGURED", body["code"])
	}
}

func TestPresignDownload_RequiresOssKey(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	r, _ := newUploadsTestRouter(t, f)
	status, body := callRouter(t, r, "POST", uploadsPath("presign-download"), `{"ossKey":""}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "ossKey is required") {
		t.Fatalf("body.error = %q", msg)
	}
}

func TestPresignDownload_ReturnsSignedURLForOwnKey(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	r, _ := newUploadsTestRouter(t, f)
	ownKey := "capabilities/plugins/" + uploadsTestWorkspaceID + "/u/x.zip"
	status, body := callRouter(t, r, "POST", uploadsPath("presign-download"), `{"ossKey":"`+ownKey+`"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", status, body)
	}
	downloadURL, _ := body["downloadUrl"].(string)
	if !strings.Contains(downloadURL, "?get=1") {
		t.Fatalf("downloadUrl = %q, missing GET marker", downloadURL)
	}
	if f.lastGetKey != ownKey {
		t.Fatalf("lastGetKey = %q", f.lastGetKey)
	}
}

func TestPresignDownload_CrossWorkspaceKey403(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	r, _ := newUploadsTestRouter(t, f)
	// Key belongs to a different workspace; KeyBelongsToWorkspace must
	// catch it before any signing happens. 403 (not 404) — the key
	// shape capabilities/<kind>/<wid>/... structurally encodes the
	// workspace mismatch, so existence-leak concerns are moot and
	// the explicit "wrong workspace" makes operator triage cheap.
	foreignKey := "capabilities/plugins/00000000-0000-0000-0000-000000000099/u/x.zip"
	status, _ := callRouter(t, r, "POST", uploadsPath("presign-download"), `{"ossKey":"`+foreignKey+`"}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for cross-workspace key", status)
	}
	if f.lastGetKey != "" {
		t.Fatalf("client must NOT be called for foreign key; lastGetKey=%q", f.lastGetKey)
	}
}

func TestPresignDownload_PathTraversalKeyRejected(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	r, _ := newUploadsTestRouter(t, f)
	// Try to escape the workspace prefix with ../. KeyBelongsToWorkspace
	// rejects traversal segments before normalising; surfaces as 403
	// (same as cross-workspace) because both fail the workspace-ownership
	// gate for the same reason.
	traversal := "capabilities/plugins/" + uploadsTestWorkspaceID + "/../00000000-0000-0000-0000-000000000099/u/x.zip"
	status, _ := callRouter(t, r, "POST", uploadsPath("presign-download"), `{"ossKey":"`+traversal+`"}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for traversal key", status)
	}
	if f.lastGetKey != "" {
		t.Fatalf("client must NOT be called for traversal key")
	}
}

func TestPresignDownload_SignerFailure500(t *testing.T) {
	t.Parallel()
	f := newFakeOSS()
	f.returnErr = errors.New("signer exploded")
	f.returnErrOnGet = true
	r, _ := newUploadsTestRouter(t, f)
	ownKey := "capabilities/plugins/" + uploadsTestWorkspaceID + "/u/x.zip"
	status, body := callRouter(t, r, "POST", uploadsPath("presign-download"), `{"ossKey":"`+ownKey+`"}`)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if msg, _ := body["error"].(string); strings.Contains(msg, "signer exploded") {
		t.Fatalf("body.error leaked: %q", msg)
	}
}
