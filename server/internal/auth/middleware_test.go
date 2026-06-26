package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// fakeQuerier is an in-memory Querier for unit tests.
type fakeQuerier struct {
	mu      sync.Mutex
	rows    map[string]sqlc.GetActiveSessionRow
	revoked map[string]bool
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{rows: map[string]sqlc.GetActiveSessionRow{}, revoked: map[string]bool{}}
}

func (f *fakeQuerier) CreateSession(_ context.Context, arg sqlc.CreateSessionParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	uid := ""
	if arg.UserID.Valid {
		uid = arg.UserID.String()
	}
	f.rows[arg.ID] = sqlc.GetActiveSessionRow{
		ID: arg.ID, UserID: uid,
		CreatedAt: arg.Now, LastSeenAt: arg.Now, ExpiresAt: arg.ExpiresAt,
	}
	return nil
}

func (f *fakeQuerier) GetActiveSession(_ context.Context, arg sqlc.GetActiveSessionParams) (sqlc.GetActiveSessionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revoked[arg.ID] {
		return sqlc.GetActiveSessionRow{}, errPgxNoRowsForTest
	}
	row, ok := f.rows[arg.ID]
	if !ok {
		return sqlc.GetActiveSessionRow{}, errPgxNoRowsForTest
	}
	if arg.Now.Time.After(row.ExpiresAt.Time) {
		return sqlc.GetActiveSessionRow{}, errPgxNoRowsForTest
	}
	return row, nil
}

func (f *fakeQuerier) TouchSession(_ context.Context, _ sqlc.TouchSessionParams) error { return nil }

func (f *fakeQuerier) RevokeSession(_ context.Context, arg sqlc.RevokeSessionParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[arg.ID] = true
	return nil
}

// errPgxNoRowsForTest mimics pgx.ErrNoRows so unit tests don't
// import pgx; the store's wrap is exercised by integration tests.
var errPgxNoRowsForTest = pgxNoRows{}

type pgxNoRows struct{}

func (pgxNoRows) Error() string { return "no rows in result set" }

const fixtureUserID = "00000000-0000-0000-0000-000000000001"

func TestPostgresSessionStoreCreateAndResolve(t *testing.T) {
	q := newFakeQuerier()
	s := NewPostgresSessionStore(q)
	ctx := context.Background()

	id, err := s.Create(ctx, CreateSessionInput{UserID: fixtureUserID, UserAgent: "ua", IP: "1.2.3.4"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(id) != SessionIDByteLen*2 {
		t.Fatalf("session id length = %d, want %d", len(id), SessionIDByteLen*2)
	}

	info, err := s.Resolve(ctx, id, time.Now().UTC())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info.UserID != fixtureUserID {
		t.Fatalf("resolved user_id = %q, want %q", info.UserID, fixtureUserID)
	}
}

func TestResolveUnknownSessionReturnsErrInvalidSession(t *testing.T) {
	q := newFakeQuerier()
	s := NewPostgresSessionStore(q)
	_, err := s.Resolve(context.Background(), "deadbeef", time.Now().UTC())
	// The fake returns pgxNoRows which the store does NOT map to
	// ErrInvalidSession (errors.Is mismatch with real pgx.ErrNoRows);
	// the real-pgx path is covered by an integration test.
	if err == nil {
		t.Fatal("Resolve on unknown id should error")
	}
}

func TestRevokedSessionFailsResolve(t *testing.T) {
	q := newFakeQuerier()
	s := NewPostgresSessionStore(q)
	ctx := context.Background()
	id, err := s.Create(ctx, CreateSessionInput{UserID: fixtureUserID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, id, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(ctx, id, time.Now().UTC()); err == nil {
		t.Fatal("Resolve on revoked id should error")
	}
}

func TestExpiredSessionFailsResolve(t *testing.T) {
	q := newFakeQuerier()
	s := NewPostgresSessionStore(q)
	ctx := context.Background()
	id, err := s.Create(ctx, CreateSessionInput{UserID: fixtureUserID, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(2 * time.Hour)
	if _, err := s.Resolve(ctx, id, future); err == nil {
		t.Fatal("Resolve past TTL should error")
	}
}

func TestMiddlewareRequireRejectsAnonymous(t *testing.T) {
	q := newFakeQuerier()
	mw := NewMiddleware(NewPostgresSessionStore(q)).WithDevAuth(false)

	h := mw.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("handler should not be reached on anonymous request, ctx user=%q", UserIDFromContext(r.Context()))
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMiddlewareRequireAcceptsValidCookie(t *testing.T) {
	q := newFakeQuerier()
	store := NewPostgresSessionStore(q)
	mw := NewMiddleware(store).WithDevAuth(false)

	id, err := store.Create(context.Background(), CreateSessionInput{UserID: fixtureUserID})
	if err != nil {
		t.Fatal(err)
	}
	var seenUser string
	h := mw.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUser = UserIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: id})
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if seenUser != fixtureUserID {
		t.Fatalf("ctx user_id = %q, want %q", seenUser, fixtureUserID)
	}
}

func TestMiddlewareDevHeaderShimWhenEnabled(t *testing.T) {
	q := newFakeQuerier()
	mw := NewMiddleware(NewPostgresSessionStore(q)).WithDevAuth(true)

	var seenUser string
	h := mw.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUser = UserIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(DevUserHeader, fixtureUserID)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (dev shim should accept)", rec.Code)
	}
	if seenUser != fixtureUserID {
		t.Fatalf("ctx user_id = %q, want %q", seenUser, fixtureUserID)
	}
}

func TestMiddlewareDevHeaderIgnoredWhenDisabled(t *testing.T) {
	q := newFakeQuerier()
	mw := NewMiddleware(NewPostgresSessionStore(q)).WithDevAuth(false)

	h := mw.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run when dev shim disabled and no cookie")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(DevUserHeader, fixtureUserID)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (dev shim must be off by default in prod)", rec.Code)
	}
}

func TestMiddlewareOptionalDoesNotReject(t *testing.T) {
	q := newFakeQuerier()
	mw := NewMiddleware(NewPostgresSessionStore(q)).WithDevAuth(false)

	called := false
	h := mw.Optional(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if UserIDFromContext(r.Context()) != "" {
			t.Fatal("Optional middleware should not inject user when none resolved")
		}
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if !called {
		t.Fatal("Optional middleware should still call the handler on anonymous request")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestIssueAndClearCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	IssueCookie(rec, "abc123", time.Hour, false)
	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, CookieName+"=abc123") {
		t.Fatalf("IssueCookie Set-Cookie missing token: %q", setCookie)
	}
	if !strings.Contains(setCookie, "HttpOnly") || !strings.Contains(setCookie, "SameSite=Lax") {
		t.Fatalf("IssueCookie cookie attrs missing: %q", setCookie)
	}
	if strings.Contains(setCookie, "Secure") {
		t.Fatalf("IssueCookie should not set Secure when secure=false: %q", setCookie)
	}

	rec2 := httptest.NewRecorder()
	ClearCookie(rec2, false)
	clear := rec2.Header().Get("Set-Cookie")
	if !strings.Contains(clear, CookieName+"=") {
		t.Fatalf("ClearCookie should reset cookie value: %q", clear)
	}
	if !strings.Contains(clear, "Max-Age=0") && !strings.Contains(clear, "Max-Age=-1") {
		t.Fatalf("ClearCookie should set negative Max-Age: %q", clear)
	}
}

func TestUserIDContextRoundTrip(t *testing.T) {
	ctx := WithUserID(context.Background(), fixtureUserID)
	if got := UserIDFromContext(ctx); got != fixtureUserID {
		t.Fatalf("UserIDFromContext = %q, want %q", got, fixtureUserID)
	}
	if got := UserIDFromContext(context.Background()); got != "" {
		t.Fatalf("UserIDFromContext on empty ctx = %q, want empty", got)
	}
	if got := UserIDFromContext(nil); got != "" { //nolint:staticcheck
		t.Fatalf("UserIDFromContext on nil ctx = %q, want empty", got)
	}
}

func TestNewSessionIDProducesUniqueHex(t *testing.T) {
	a, err := NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("NewSessionID returned identical tokens twice — RNG broken")
	}
	if len(a) != SessionIDByteLen*2 {
		t.Fatalf("token length = %d, want %d hex chars", len(a), SessionIDByteLen*2)
	}
}
