package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type fakeResolver struct {
	identity      store.RuntimeIdentity
	found         bool
	err           error
	gotPlaintexts []string
}

func (f *fakeResolver) ResolveRuntimeIdentity(ctx context.Context, plaintext string) (store.RuntimeIdentity, bool, error) {
	f.gotPlaintexts = append(f.gotPlaintexts, plaintext)
	if f.err != nil {
		return store.RuntimeIdentity{}, false, f.err
	}
	return f.identity, f.found, nil
}

func runWith(t *testing.T, m func(http.Handler) http.Handler, req *http.Request) (int, string, store.RuntimeIdentity, bool) {
	t.Helper()
	var seen store.RuntimeIdentity
	var sawHandler bool
	h := m(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, sawHandler = RuntimeIdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code, strings.TrimSpace(rr.Body.String()), seen, sawHandler
}

func TestRunnerCredentialHappyPath(t *testing.T) {
	id := store.RuntimeIdentity{
		RuntimeID:   "11111111-1111-1111-1111-111111111111",
		WorkspaceID: "22222222-2222-2222-2222-222222222222",
		RuntimeType: "agent_daemon",
	}
	res := &fakeResolver{identity: id, found: true}
	m := RunnerCredential(res, RunnerCredentialOptions{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runtime/x", nil)
	req.Header.Set("Authorization", "Bearer rtc_validlooking")

	code, body, seen, sawHandler := runWith(t, m, req)
	if code != http.StatusOK {
		t.Fatalf("status = %d body = %q, want 200", code, body)
	}
	if !sawHandler {
		t.Fatal("handler never saw the identity")
	}
	if seen.RuntimeID != id.RuntimeID || seen.WorkspaceID != id.WorkspaceID {
		t.Errorf("identity in ctx = %+v, want %+v", seen, id)
	}
	if len(res.gotPlaintexts) != 1 || res.gotPlaintexts[0] != "rtc_validlooking" {
		t.Errorf("resolver saw plaintexts %v, want [rtc_validlooking]", res.gotPlaintexts)
	}
}

func TestRunnerCredentialMissingHeader(t *testing.T) {
	res := &fakeResolver{}
	m := RunnerCredential(res, RunnerCredentialOptions{})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	code, _, _, sawHandler := runWith(t, m, req)
	if code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", code)
	}
	if sawHandler {
		t.Error("handler ran on missing-header request")
	}
	if len(res.gotPlaintexts) != 0 {
		t.Errorf("resolver was called %d times, want 0", len(res.gotPlaintexts))
	}
}

// TestRunnerCredentialWrongScheme: HTTP scheme matching is case-
// insensitive per RFC, but we deliberately require canonical "Bearer ".
func TestRunnerCredentialWrongScheme(t *testing.T) {
	cases := []string{
		"Basic dXNlcjpwYXNz",
		"Token rtc_xxx",
		"rtc_xxx",
		"bearer rtc_xxx",
		"Bearer\trtc_xxx",
		"BearerNoSpace_rtc_",
	}
	for _, raw := range cases {
		res := &fakeResolver{}
		m := RunnerCredential(res, RunnerCredentialOptions{})

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", raw)
		code, _, _, sawHandler := runWith(t, m, req)
		if code != http.StatusUnauthorized {
			t.Errorf("Authorization=%q: status = %d, want 401", raw, code)
		}
		if sawHandler {
			t.Errorf("Authorization=%q: handler ran", raw)
		}
		if len(res.gotPlaintexts) != 0 {
			t.Errorf("Authorization=%q: resolver called", raw)
		}
	}
}

func TestRunnerCredentialEmptyBearer(t *testing.T) {
	cases := []string{"Bearer ", "Bearer    ", "Bearer \t\n"}
	for _, raw := range cases {
		res := &fakeResolver{}
		m := RunnerCredential(res, RunnerCredentialOptions{})

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", raw)
		code, _, _, sawHandler := runWith(t, m, req)
		if code != http.StatusUnauthorized {
			t.Errorf("%q: status = %d, want 401", raw, code)
		}
		if sawHandler {
			t.Errorf("%q: handler ran on empty token", raw)
		}
		if len(res.gotPlaintexts) != 0 {
			t.Errorf("%q: resolver called", raw)
		}
	}
}

func TestRunnerCredentialResolverMiss(t *testing.T) {
	res := &fakeResolver{found: false}
	m := RunnerCredential(res, RunnerCredentialOptions{})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer rtc_unknown")
	code, body, _, sawHandler := runWith(t, m, req)
	if code != http.StatusUnauthorized {
		t.Errorf("status = %d body = %q, want 401", code, body)
	}
	if sawHandler {
		t.Error("handler ran on resolver miss")
	}
	if len(res.gotPlaintexts) != 1 || res.gotPlaintexts[0] != "rtc_unknown" {
		t.Errorf("resolver saw %v, want [rtc_unknown]", res.gotPlaintexts)
	}
}

// TestRunnerCredentialResolverError: resolver returns an error → 500,
// distinct from a 401 miss.
func TestRunnerCredentialResolverError(t *testing.T) {
	res := &fakeResolver{err: errors.New("db down")}
	m := RunnerCredential(res, RunnerCredentialOptions{})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer rtc_anything")
	code, _, _, sawHandler := runWith(t, m, req)
	if code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", code)
	}
	if sawHandler {
		t.Error("handler ran despite resolver error")
	}
}

func TestRunnerCredentialPanicsOnNilResolver(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil resolver")
		}
	}()
	_ = RunnerCredential(nil, RunnerCredentialOptions{})
}

func TestRuntimeIdentityRoundTripsThroughContext(t *testing.T) {
	id := store.RuntimeIdentity{
		RuntimeID:   "abc",
		WorkspaceID: "wks",
		RuntimeType: "agent_daemon",
	}
	ctx := WithRuntimeIdentity(context.Background(), id)
	got, ok := RuntimeIdentityFromContext(ctx)
	if !ok {
		t.Fatal("identity not in ctx after WithRuntimeIdentity")
	}
	if got != id {
		t.Errorf("got %+v, want %+v", got, id)
	}
}

func TestRuntimeIdentityFromContextEmpty(t *testing.T) {
	got, ok := RuntimeIdentityFromContext(context.Background())
	if ok {
		t.Errorf("ok = true on empty ctx, want false")
	}
	if (got != store.RuntimeIdentity{}) {
		t.Errorf("got = %+v, want zero", got)
	}
}

func TestBearerTokenExtraction(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
		ok     bool
	}{
		{"perfect", "Bearer rtc_abc", "rtc_abc", true},
		{"trailing space trimmed", "Bearer rtc_abc   ", "rtc_abc", true},
		{"empty header", "", "", false},
		{"no prefix", "rtc_abc", "", false},
		{"empty token", "Bearer ", "", false},
		{"whitespace token", "Bearer    \t", "", false},
		{"lowercase scheme rejected", "bearer rtc_abc", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			got, ok := bearerToken(r)
			if ok != tc.ok {
				t.Errorf("ok = %v, want %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Errorf("token = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunnerCredentialIgnoresQueryAndBody pins the security model:
// the middleware MUST only read Authorization, never URL or body.
func TestRunnerCredentialIgnoresQueryAndBody(t *testing.T) {
	res := &fakeResolver{found: false}
	m := RunnerCredential(res, RunnerCredentialOptions{})

	req := httptest.NewRequest(http.MethodPost, "/x?token=rtc_inquery", strings.NewReader(`{"token":"rtc_inbody"}`))
	req.Header.Set("Content-Type", "application/json")

	code, _, _, _ := runWith(t, m, req)
	if code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (must ignore query/body token sources)", code)
	}
	if len(res.gotPlaintexts) != 0 {
		t.Errorf("resolver was called with %v — query/body token leaked into resolver", res.gotPlaintexts)
	}
}
