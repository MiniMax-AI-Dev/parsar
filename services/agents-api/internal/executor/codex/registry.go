package codex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

const ticketLifetime = 5 * time.Minute

// Registry owns replaceable connections, never durable public readiness.
type Registry struct {
	source            EnvironmentStore
	checkOwnership    func(context.Context) error
	replaceConnection func(context.Context, string, string, string) error
	observeConnection func(context.Context, string, string, string, int64, bool) error
	observations      sync.WaitGroup
	lifecycleErr      error
	publicWS          string
	harnessKeys       map[[32]byte]*harnessCredential
	registrationMu    sync.Mutex
	mu                sync.Mutex
	closed            bool
	registrations     map[string]*registration
}

type registration struct {
	id        string
	key       ScopedKey
	publicKey PublicKey
	ticket    [32]byte
	expires   time.Time
	socket    *connection
	grants    map[[32]byte]*harnessGrant
	revision  int64
}

func New(c Config) (*Registry, error) {
	url, err := validateConfig(c)
	if err != nil {
		return nil, err
	}
	return &Registry{harnessKeys: make(map[[32]byte]*harnessCredential), source: c.Store, checkOwnership: c.CheckOwnership,
		replaceConnection: c.ReplaceConnection, observeConnection: c.ObserveConnection,
		publicWS: url, registrations: make(map[string]*registration)}, nil
}

func (r *Registry) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /cloud/environment/{environment}/register", r.register)
	mux.HandleFunc("GET /cloud/environment/{environment}/executor/{registration}", r.connectExecutor)
	mux.HandleFunc("POST /cloud/environment/{environment}/connect", r.connect)
	mux.HandleFunc("POST /cloud/environment/{environment}/validate", r.validate)
	mux.HandleFunc("GET /cloud/environment/{environment}/harness/{registration}", r.connectHarness)
	return mux
}

func (r *Registry) authorized(ctx context.Context, key ScopedKey) error {
	// Client disconnects must not cancel a query on the shared execution lease.
	ownerCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := r.checkOwnership(ownerCtx); err != nil {
		r.closeConnections()
		return err
	}
	_, err := r.source.GetEnvironment(ctx, key.TenantID, key.EnvironmentID)
	return err
}

func (r *Registry) check(w http.ResponseWriter, req *http.Request, key ScopedKey) bool {
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	err := r.authorized(ctx, key)
	if err == nil {
		return true
	}
	status := http.StatusServiceUnavailable
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidInput) {
		status = http.StatusNotFound
	}
	writeError(w, status)
	return false
}

// @Summary Register a native executor (internal transport, not public Environment readiness)
// @Tags Native executor registry
// @Accept json
// @Produce json
// @Param environment path string true "Environment ID"
// @Param request body RegistrationRequest true "Native executor key"
// @Success 200 {object} RegistrationResponse
// @Failure 400,401,404,503 {object} RegistryError
// @Router /cloud/environment/{environment}/register [post]
func (r *Registry) register(w http.ResponseWriter, req *http.Request) {
	environment := req.PathValue("environment")
	key, ok := r.executorCredential(w, req, environment)
	if !ok {
		return
	}
	if !r.check(w, req, key) {
		return
	}
	var body RegistrationRequest
	if !decodeRequest(w, req, &body) {
		return
	}
	if body.SecurityProfile != securityProfile || !body.ExecutorPublicKey.valid() {
		writeError(w, http.StatusBadRequest)
		return
	}
	ticket, err := capability()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable)
		return
	}
	next := &registration{id: uuid.NewString(), key: key, publicKey: body.ExecutorPublicKey, ticket: sha256.Sum256([]byte(ticket)), expires: time.Now().Add(ticketLifetime), grants: make(map[[32]byte]*harnessGrant)}
	// Order credential observation and replacement without blocking relay heartbeats on a database read.
	r.registrationMu.Lock()
	if !r.checkCurrentExecutor(w, req, key) {
		r.registrationMu.Unlock()
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		r.registrationMu.Unlock()
		writeError(w, http.StatusServiceUnavailable)
		return
	}
	r.observations.Add(1)
	r.mu.Unlock()
	if err := r.replaceGeneration(next); err != nil {
		r.registrationMu.Unlock()
		writeError(w, http.StatusServiceUnavailable)
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		r.registrationMu.Unlock()
		writeError(w, http.StatusServiceUnavailable)
		return
	}
	previous := r.registrations[environment]
	r.registrations[environment] = next
	if previous != nil {
		r.closeConnectionLocked(previous, previous.socket)
	}
	r.mu.Unlock()
	r.registrationMu.Unlock()
	writeJSON(w, http.StatusOK, RegistrationResponse{EnvironmentID: environment, ExecutorRegistrationID: next.id, SecurityProfile: securityProfile, URL: r.publicWS + "/cloud/environment/" + environment + "/executor/" + next.id + "?ticket=" + ticket})
}

// Connected reports a current authenticated socket, not harness readiness or filesystem isolation.
func (r *Registry) Connected(ctx context.Context, tenant, environment string) (bool, error) {
	if err := r.authorized(ctx, ScopedKey{TenantID: tenant, EnvironmentID: environment}); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	reg := r.registrations[environment]
	return !r.closed && reg != nil && reg.socket != nil, nil
}

func capability() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(secret), nil
}
