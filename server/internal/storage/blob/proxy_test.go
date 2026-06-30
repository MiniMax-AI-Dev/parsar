package blob

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newProxyTestRig() (*ProxyHandler, *PGStore, *ProxySigner) {
	signer := NewProxySigner("k")
	store := NewPGStore(newFakePGQ(), signer, "https://api.test")
	return NewProxyHandler(store, signer), store, signer
}

func TestProxyPutThenGet(t *testing.T) {
	h, _, signer := newProxyTestRig()
	ref := "pg:abc"
	putTok, _ := signer.Sign(ProxyClaims{Ref: ref, WorkspaceID: "ws-1", Method: "PUT"}, time.Minute)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/internal/blobs/"+ref+"?token="+putTok, bytes.NewReader([]byte("payload")))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT: want 204, got %d (%s)", rec.Code, rec.Body)
	}

	getTok, _ := signer.Sign(ProxyClaims{Ref: ref, WorkspaceID: "ws-1", Method: "GET"}, time.Minute)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/internal/blobs/"+ref+"?token="+getTok, nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: want 200, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "payload" {
		t.Fatalf("GET body: got %q", body)
	}
}

func TestProxyRejectsMissingToken(t *testing.T) {
	h, _, _ := newProxyTestRig()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/blobs/pg:abc", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestProxyRejectsMethodMismatch(t *testing.T) {
	h, _, signer := newProxyTestRig()
	getTok, _ := signer.Sign(ProxyClaims{Ref: "pg:abc", WorkspaceID: "ws-1", Method: "GET"}, time.Minute)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/internal/blobs/pg:abc?token="+getTok, bytes.NewReader([]byte("x")))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 on method mismatch, got %d", rec.Code)
	}
}

func TestProxyRejectsRefMismatch(t *testing.T) {
	h, _, signer := newProxyTestRig()
	tok, _ := signer.Sign(ProxyClaims{Ref: "pg:abc", WorkspaceID: "ws-1", Method: "GET"}, time.Minute)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/internal/blobs/pg:other?token="+tok, nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 on ref mismatch, got %d", rec.Code)
	}
}

func TestProxyGetMissingIs404(t *testing.T) {
	h, _, signer := newProxyTestRig()
	tok, _ := signer.Sign(ProxyClaims{Ref: "pg:missing", WorkspaceID: "ws-1", Method: "GET"}, time.Minute)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/blobs/pg:missing?token="+tok, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestProxyPutTooLargeIs413(t *testing.T) {
	h, _, signer := newProxyTestRig()
	tok, _ := signer.Sign(ProxyClaims{Ref: "pg:big", WorkspaceID: "ws-1", Method: "PUT"}, time.Minute)
	body := bytes.NewReader(make([]byte, MaxBlobBytes+1))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/internal/blobs/pg:big?token="+tok, body)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", rec.Code)
	}
}

func TestProxyDownloadAfterPutViaStore(t *testing.T) {
	h, store, signer := newProxyTestRig()
	putTok, _ := signer.Sign(ProxyClaims{Ref: "pg:rt", WorkspaceID: "ws-1", Method: "PUT"}, time.Minute)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/internal/blobs/pg:rt?token="+putTok, bytes.NewReader([]byte("abc"))))
	got, err := store.Download(context.Background(), "pg:rt")
	if err != nil || string(got) != "abc" {
		t.Fatalf("store.Download after proxy PUT: got %q err %v", got, err)
	}
}
