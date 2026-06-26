package api

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
)

// fakePinger drives the happy / failure / slow-cancellation paths
// without standing up Postgres.
type fakePinger struct {
	err        error
	delay      time.Duration
	callCount  int
	lastCtxErr error
}

func (f *fakePinger) Ping(ctx context.Context) error {
	f.callCount++
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			f.lastCtxErr = ctx.Err()
			return ctx.Err()
		}
	}
	return f.err
}

func serveOnce(t *testing.T, deps HealthDeps, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	RegisterHealthRoutes(r, deps)
	req := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

// TestLivenessAlwaysOK: liveness must succeed even when the database
// is broken — failures restart the pod and a thrashing pod makes a
// stressed database worse.
func TestLivenessAlwaysOK(t *testing.T) {
	cases := []struct {
		name string
		deps HealthDeps
	}{
		{"nil_db", HealthDeps{DB: nil}},
		{"healthy_db", HealthDeps{DB: &fakePinger{}}},
		{"broken_db", HealthDeps{DB: &fakePinger{err: errors.New("connection refused")}}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rr := serveOnce(t, tc.deps, http.MethodGet, "/healthz")
			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr.Code)
			}
			var body HealthResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v (raw=%q)", err, rr.Body.String())
			}
			if body.Status != "ok" || body.Name != "parsar" {
				t.Fatalf("body shape regressed: %+v", body)
			}
		})
	}
}

// TestLivenessNeverPingsDB: liveness must not depend on any external
// system. Even when DB is wired, /healthz must not invoke Ping.
func TestLivenessNeverPingsDB(t *testing.T) {
	db := &fakePinger{}
	rr := serveOnce(t, HealthDeps{DB: db}, http.MethodGet, "/healthz")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if db.callCount != 0 {
		t.Fatalf("liveness must not call DB.Ping; got %d calls", db.callCount)
	}
}

func TestReadinessAllOK(t *testing.T) {
	db := &fakePinger{}
	rr := serveOnce(t, HealthDeps{DB: db}, http.MethodGet, "/readyz")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	var body ReadyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (raw=%q)", err, rr.Body.String())
	}
	if body.Status != "ok" || body.Name != "parsar" {
		t.Fatalf("body envelope regressed: %+v", body)
	}
	if len(body.Checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(body.Checks))
	}
	c := body.Checks[0]
	if c.Name != "database" || c.Status != CheckStatusOK || c.Error != "" {
		t.Fatalf("database check row wrong: %+v", c)
	}
	if db.callCount != 1 {
		t.Fatalf("expected exactly 1 DB.Ping, got %d", db.callCount)
	}
}

// TestReadinessDBMissing: process is up but cannot serve real traffic
// without DATABASE_URL — /readyz must report 503 with a not_configured
// row pointing at the missing env var.
func TestReadinessDBMissing(t *testing.T) {
	rr := serveOnce(t, HealthDeps{DB: nil}, http.MethodGet, "/readyz")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	var body ReadyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "degraded" {
		t.Fatalf("expected status=degraded, got %q", body.Status)
	}
	if len(body.Checks) != 1 || body.Checks[0].Status != CheckStatusNotConfigured {
		t.Fatalf("expected one not_configured check, got %+v", body.Checks)
	}
	if !strings.Contains(body.Checks[0].Error, "DATABASE_URL") {
		t.Fatalf("expected error hint to mention DATABASE_URL, got %q", body.Checks[0].Error)
	}
}

// TestReadinessDBFail: error message must be surfaced verbatim so an
// operator scraping the JSON sees the same string the server saw.
func TestReadinessDBFail(t *testing.T) {
	dbErr := errors.New("FATAL: password authentication failed")
	db := &fakePinger{err: dbErr}
	rr := serveOnce(t, HealthDeps{DB: db}, http.MethodGet, "/readyz")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
	var body ReadyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "degraded" {
		t.Fatalf("expected status=degraded, got %q", body.Status)
	}
	if len(body.Checks) != 1 || body.Checks[0].Status != CheckStatusFail {
		t.Fatalf("expected one fail check, got %+v", body.Checks)
	}
	if body.Checks[0].Error != dbErr.Error() {
		t.Fatalf("error message not propagated: got %q want %q", body.Checks[0].Error, dbErr.Error())
	}
}

// TestReadinessDBTimeout: a database that never responds must trip
// readyCheckTimeout and surface as fail rather than block the handler.
func TestReadinessDBTimeout(t *testing.T) {
	origTimeout := readyCheckTimeout
	readyCheckTimeout = 20 * time.Millisecond
	t.Cleanup(func() { readyCheckTimeout = origTimeout })

	db := &fakePinger{delay: 500 * time.Millisecond}
	start := time.Now()
	rr := serveOnce(t, HealthDeps{DB: db}, http.MethodGet, "/readyz")
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Fatalf("/readyz blocked %s, expected fast deadline (~%s)", elapsed, readyCheckTimeout)
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
	var body ReadyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Checks) != 1 || body.Checks[0].Status != CheckStatusFail {
		t.Fatalf("expected fail check on timeout, got %+v", body.Checks)
	}
	if !errors.Is(db.lastCtxErr, context.DeadlineExceeded) {
		t.Fatalf("expected ctx.DeadlineExceeded inside pinger, got %v", db.lastCtxErr)
	}
}

// TestLegacyV1HealthBackcompat freezes the wire shape of /api/v1/health.
// dev-server-up.sh and smoke.sh curl this path.
func TestLegacyV1HealthBackcompat(t *testing.T) {
	rr := serveOnce(t, HealthDeps{DB: nil}, http.MethodGet, "/api/v1/health")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected JSON content-type, got %q", ct)
	}
	var body HealthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (raw=%q)", err, rr.Body.String())
	}
	if body.Status != "ok" || body.Name != "parsar" {
		t.Fatalf("legacy /api/v1/health shape regressed: %+v", body)
	}
}

// TestReadinessTypedNilPinger: guards the typed-nil interface pitfall.
// openPool() returning a nil *pgxpool.Pool wrapped into HealthDeps.DB
// must surface as not_configured, not crash on Ping. Must FAIL if the
// reflect-based nil check in isNilPinger is removed.
func TestReadinessTypedNilPinger(t *testing.T) {
	var typedNil *fakePinger
	deps := HealthDeps{DB: typedNil}

	rr := serveOnce(t, deps, http.MethodGet, "/readyz")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	var body ReadyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Checks) != 1 || body.Checks[0].Status != CheckStatusNotConfigured {
		t.Fatalf("typed-nil pinger must be treated as not_configured, got %+v", body.Checks)
	}
}
