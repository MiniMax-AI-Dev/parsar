package runtime

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagedEnrollRequiresLoopback(t *testing.T) {
	h := &handler{}
	req := httptest.NewRequest(http.MethodPost, "/internal/managed-daemon/enroll", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	rec := httptest.NewRecorder()

	h.enrollManagedRuntime(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestLoopbackRequest(t *testing.T) {
	for _, remoteAddr := range []string{"127.0.0.1:1234", "[::1]:1234"} {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = remoteAddr
		if !isLoopbackRequest(req) {
			t.Fatalf("expected %s to be loopback", remoteAddr)
		}
	}
}
