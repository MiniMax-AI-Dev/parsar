package password

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// fakeQuerier stubs the two sqlc methods the handler needs.
type fakeQuerier struct {
	row      sqlc.GetPasswordHashByEmailRow
	getErr   error
	touchErr error

	touchCalls int
}

func (f *fakeQuerier) GetPasswordHashByEmail(_ context.Context, _ string) (sqlc.GetPasswordHashByEmailRow, error) {
	return f.row, f.getErr
}

func (f *fakeQuerier) TouchEmailIdentityLastUsed(_ context.Context, _ sqlc.TouchEmailIdentityLastUsedParams) error {
	f.touchCalls++
	return f.touchErr
}

// fakeSessions stubs auth.SessionStore.
type fakeSessions struct {
	createID  string
	createErr error
	revoked   string
}

func (s *fakeSessions) Create(_ context.Context, in auth.CreateSessionInput) (string, error) {
	if s.createErr != nil {
		return "", s.createErr
	}
	if s.createID == "" {
		return "sess-" + in.UserID, nil
	}
	return s.createID, nil
}

func (s *fakeSessions) Resolve(_ context.Context, id string, _ time.Time) (auth.SessionInfo, error) {
	return auth.SessionInfo{ID: id}, nil
}

func (s *fakeSessions) Revoke(_ context.Context, id string, _ time.Time) error {
	s.revoked = id
	return nil
}

// helper: pre-hash a password to feed into the fake row.
func mustHash(t *testing.T, p string) string {
	t.Helper()
	h, err := Hash(p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	return h
}

func TestLoginSuccessSetsCookie(t *testing.T) {
	h := mustHash(t, "correct horse battery staple")
	q := &fakeQuerier{
		row: sqlc.GetPasswordHashByEmailRow{
			UserID: "user-1", Email: "admin@example.com", Name: "Admin",
			Status: "active", PasswordHash: h,
		},
	}
	sess := &fakeSessions{}
	lh := NewLoginHandler(q, sess, false, nil)

	body := strings.NewReader(`{"email":"admin@example.com","password":"correct horse battery staple"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rec := httptest.NewRecorder()
	lh.Login(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.UserID != "user-1" || got.Email != "admin@example.com" {
		t.Fatalf("unexpected payload: %+v", got)
	}
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == auth.CookieName {
			if c.Value == "" {
				t.Fatalf("cookie value empty")
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no %s cookie set", auth.CookieName)
	}
	if q.touchCalls != 1 {
		t.Fatalf("TouchEmailIdentityLastUsed calls = %d", q.touchCalls)
	}
}

func TestLoginWrongPasswordReturns401(t *testing.T) {
	h := mustHash(t, "correct horse battery staple")
	q := &fakeQuerier{
		row: sqlc.GetPasswordHashByEmailRow{
			UserID: "user-1", Email: "admin@example.com", Status: "active",
			PasswordHash: h,
		},
	}
	lh := NewLoginHandler(q, &fakeSessions{}, false, nil)

	body := strings.NewReader(`{"email":"admin@example.com","password":"wrong password nope"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rec := httptest.NewRecorder()
	lh.Login(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	if q.touchCalls != 0 {
		t.Fatalf("TouchEmailIdentityLastUsed should not run on failed login")
	}
}

func TestLoginUnknownEmailReturns401NoTiming(t *testing.T) {
	// pgx.ErrNoRows would mean the user does not exist. The handler
	// should still call Compare("", ...) to burn dummy bcrypt time,
	// then return the same 401 shape as a wrong password.
	q := &fakeQuerier{
		row:    sqlc.GetPasswordHashByEmailRow{}, // all zero
		getErr: nil,                              // sqlc returns zero row with no error on left-joined miss
	}
	lh := NewLoginHandler(q, &fakeSessions{}, false, nil)

	body := strings.NewReader(`{"email":"ghost@example.com","password":"correct horse battery staple"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rec := httptest.NewRecorder()

	start := time.Now()
	lh.Login(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	// A short-circuit (returning 401 without hitting bcrypt) would
	// finish well under 5ms.
	if elapsed < 5*time.Millisecond {
		t.Fatalf("login for unknown email too fast: %s -- dummy bcrypt did not run", elapsed)
	}
}

func TestLoginInactiveUserReturns401(t *testing.T) {
	h := mustHash(t, "correct horse battery staple")
	q := &fakeQuerier{
		row: sqlc.GetPasswordHashByEmailRow{
			UserID: "user-1", Email: "admin@example.com", Status: "disabled",
			PasswordHash: h,
		},
	}
	lh := NewLoginHandler(q, &fakeSessions{}, false, nil)

	body := strings.NewReader(`{"email":"admin@example.com","password":"correct horse battery staple"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rec := httptest.NewRecorder()
	lh.Login(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestLoginEmptyBodyReturns401(t *testing.T) {
	q := &fakeQuerier{}
	lh := NewLoginHandler(q, &fakeSessions{}, false, nil)

	body := strings.NewReader(`{"email":"","password":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rec := httptest.NewRecorder()
	lh.Login(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestLoginBadJSONReturns400(t *testing.T) {
	lh := NewLoginHandler(&fakeQuerier{}, &fakeSessions{}, false, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`nope`))
	rec := httptest.NewRecorder()
	lh.Login(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestLoginSessionCreateFailure500(t *testing.T) {
	h := mustHash(t, "correct horse battery staple")
	q := &fakeQuerier{
		row: sqlc.GetPasswordHashByEmailRow{
			UserID: "user-1", Email: "admin@example.com", Status: "active",
			PasswordHash: h,
		},
	}
	sess := &fakeSessions{createErr: errors.New("boom")}
	lh := NewLoginHandler(q, sess, false, nil)

	body := strings.NewReader(`{"email":"admin@example.com","password":"correct horse battery staple"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rec := httptest.NewRecorder()
	lh.Login(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestLogoutRevokesAndClears(t *testing.T) {
	sess := &fakeSessions{}
	lh := NewLoginHandler(&fakeQuerier{}, sess, false, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "sid-xyz"})
	rec := httptest.NewRecorder()
	lh.Logout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if sess.revoked != "sid-xyz" {
		t.Fatalf("revoked = %q, want sid-xyz", sess.revoked)
	}
	// Cookie cleared: MaxAge < 0.
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName && c.MaxAge >= 0 {
			t.Fatalf("cookie not cleared: %+v", c)
		}
	}
}

func TestLogoutNoCookieStillClears(t *testing.T) {
	sess := &fakeSessions{}
	lh := NewLoginHandler(&fakeQuerier{}, sess, false, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rec := httptest.NewRecorder()
	lh.Logout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if sess.revoked != "" {
		t.Fatalf("no cookie should skip Revoke")
	}
}
