package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// fakeRepo is an in-memory bootstrap.Repo for tests. ownerCount is
// atomic so a single instance can be reused across sequential
// invocations (Create flips count from 0 to 1).
type fakeRepo struct {
	ownerCount   atomic.Int64
	countErr     error
	provisionErr error
	provisioned  store.ProvisionFirstOwnerResult

	gotInput store.ProvisionFirstOwnerInput
}

func (f *fakeRepo) ActiveWorkspaceOwnerCount(_ context.Context) (int64, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.ownerCount.Load(), nil
}

func (f *fakeRepo) ProvisionFirstOwner(_ context.Context, in store.ProvisionFirstOwnerInput) (store.ProvisionFirstOwnerResult, error) {
	f.gotInput = in
	if f.provisionErr != nil {
		return store.ProvisionFirstOwnerResult{}, f.provisionErr
	}
	f.ownerCount.Add(1)
	return f.provisioned, nil
}

func fixedClock() time.Time { return time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC) }

func TestStatusEmptyDB(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, WithClock(fixedClock))
	st, err := svc.Status(context.Background(), false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Needed {
		t.Fatalf("Needed should be true when no owners; got %+v", st)
	}
	if st.HasOwners || st.OwnerCount != 0 {
		t.Fatalf("HasOwners=%v OwnerCount=%d", st.HasOwners, st.OwnerCount)
	}
	if st.DevAuthEnabled {
		t.Fatalf("DevAuthEnabled should reflect arg")
	}
}

func TestStatusReflectsExistingOwners(t *testing.T) {
	repo := &fakeRepo{}
	repo.ownerCount.Store(3)
	svc := NewService(repo)
	st, err := svc.Status(context.Background(), true)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Needed {
		t.Fatalf("Needed should be false when owners exist")
	}
	if !st.HasOwners || st.OwnerCount != 3 {
		t.Fatalf("HasOwners=%v OwnerCount=%d", st.HasOwners, st.OwnerCount)
	}
	if !st.DevAuthEnabled {
		t.Fatalf("DevAuthEnabled should mirror arg (true)")
	}
}

func TestCreateRejectsEmptyEmail(t *testing.T) {
	svc := NewService(&fakeRepo{})
	_, err := svc.Create(context.Background(), store.ProvisionFirstOwnerInput{
		Email: "  ", WorkspaceName: "Demo",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

func TestCreateRejectsMalformedEmail(t *testing.T) {
	svc := NewService(&fakeRepo{})
	_, err := svc.Create(context.Background(), store.ProvisionFirstOwnerInput{
		Email: "no-at-sign", WorkspaceName: "Demo",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

func TestCreateRejectsEmptyWorkspaceName(t *testing.T) {
	svc := NewService(&fakeRepo{})
	_, err := svc.Create(context.Background(), store.ProvisionFirstOwnerInput{
		Email: "admin@example.com", WorkspaceName: "",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

func TestCreateSuccessFillsClockAndForwards(t *testing.T) {
	repo := &fakeRepo{
		provisioned: store.ProvisionFirstOwnerResult{
			UserID: "user-1", UserCreated: true,
			WorkspaceID: "ws-1", WorkspaceSlug: "workspace-deadbeef", WorkspaceName: "Demo",
			MemberID: "mem-1",
		},
	}
	svc := NewService(repo, WithClock(fixedClock))
	out, err := svc.Create(context.Background(), store.ProvisionFirstOwnerInput{
		Email: "admin@example.com", Name: "", WorkspaceName: "Demo",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if out.WorkspaceID != "ws-1" {
		t.Fatalf("output not forwarded, got %+v", out)
	}
	if !repo.gotInput.Now.Equal(fixedClock()) {
		t.Fatalf("clock not applied, got %v", repo.gotInput.Now)
	}
}

func TestCreateUsesProvidedNow(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, WithClock(fixedClock))
	supplied := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	_, _ = svc.Create(context.Background(), store.ProvisionFirstOwnerInput{
		Email: "admin@example.com", WorkspaceName: "Demo", Now: supplied,
	})
	if !repo.gotInput.Now.Equal(supplied) {
		t.Fatalf("provided Now should be preserved, got %v", repo.gotInput.Now)
	}
}

func TestCreatePassesThroughStoreBootstrapClosed(t *testing.T) {
	repo := &fakeRepo{
		provisionErr: store.ErrBootstrapClosed,
	}
	svc := NewService(repo, WithClock(fixedClock))
	_, err := svc.Create(context.Background(), store.ProvisionFirstOwnerInput{
		Email: "admin@example.com", WorkspaceName: "Demo",
	})
	if !errors.Is(err, store.ErrBootstrapClosed) {
		t.Fatalf("expected store.ErrBootstrapClosed, got %v", err)
	}
}

// --- HTTP handler tests ---

func TestStatusHandler200JSON(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, WithClock(fixedClock))
	r := chi.NewRouter()
	RegisterRoutes(r, svc, func() bool { return true }, nil, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got StatusResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Needed || got.HasOwners || !got.DevAuthEnabled {
		t.Fatalf("unexpected status payload: %+v", got)
	}
}

func TestCreateHandler201NoPasswordNoCookie(t *testing.T) {
	repo := &fakeRepo{
		provisioned: store.ProvisionFirstOwnerResult{
			UserID: "user-1", UserCreated: true,
			WorkspaceID: "ws-1", WorkspaceSlug: "workspace-deadbeef",
			WorkspaceName: "Demo Workspace", MemberID: "mem-1",
		},
	}
	svc := NewService(repo, WithClock(fixedClock))
	r := chi.NewRouter()
	RegisterRoutes(r, svc, func() bool { return false }, nil, false)

	body := strings.NewReader(`{"email":"admin@example.com","name":"Admin","workspace_name":"Demo Workspace"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got CreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.WorkspaceSlug != "workspace-deadbeef" || !got.SetupComplete {
		t.Fatalf("unexpected response: %+v", got)
	}
	if got.UserID != "user-1" {
		t.Fatalf("UserID = %q", got.UserID)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatalf("no password supplied, expected no cookie; got %d", len(rec.Result().Cookies()))
	}
	if repo.gotInput.PasswordHash != "" {
		t.Fatalf("PasswordHash should be empty when password omitted; got %q", repo.gotInput.PasswordHash)
	}
}

func TestCreateHandler400BadJSON(t *testing.T) {
	svc := NewService(&fakeRepo{})
	r := chi.NewRouter()
	RegisterRoutes(r, svc, nil, nil, false)

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	var got ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Code != "bootstrap_invalid_input" {
		t.Fatalf("code = %q", got.Code)
	}
}

func TestCreateHandler400WeakPassword(t *testing.T) {
	svc := NewService(&fakeRepo{})
	r := chi.NewRouter()
	RegisterRoutes(r, svc, nil, nil, false)

	body := strings.NewReader(`{"email":"a@b.com","workspace_name":"x","password":"password"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	var got ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Code != "bootstrap_weak_password" {
		t.Fatalf("code = %q", got.Code)
	}
}

func TestCreateHandler409Conflict(t *testing.T) {
	repo := &fakeRepo{
		provisionErr: store.ErrBootstrapClosed,
	}
	svc := NewService(repo)
	r := chi.NewRouter()
	RegisterRoutes(r, svc, nil, nil, false)

	body := strings.NewReader(`{"email":"a@b.com","workspace_name":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
	var got ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Code != "bootstrap_closed" {
		t.Fatalf("code = %q", got.Code)
	}
}

func TestRegisterRoutesNilSvcGracefullyFails(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutes(r, nil, nil, nil, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestStatusIncludesPublicURL(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, WithPublicURL("https://parsar.example.com/"))
	st, err := svc.Status(context.Background(), false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.PublicURL != "https://parsar.example.com/" {
		t.Fatalf("PublicURL = %q, want %q", st.PublicURL, "https://parsar.example.com/")
	}
}

func TestStatusPublicURLEmptyByDefault(t *testing.T) {
	svc := NewService(&fakeRepo{})
	st, err := svc.Status(context.Background(), false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.PublicURL != "" {
		t.Fatalf("PublicURL = %q, want empty", st.PublicURL)
	}
}
