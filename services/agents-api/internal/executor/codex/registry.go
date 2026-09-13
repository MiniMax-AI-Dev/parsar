package codex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const ticketLifetime = 5 * time.Minute

// Registry owns replaceable connections, never durable public readiness.
type Registry struct {
	source         EnvironmentStore
	checkOwnership func(context.Context) error
	publicWS       string
	keys           map[[32]byte]ExecutorKey
	mu             sync.Mutex
	closed         bool
	registrations  map[string]*registration
}

type registration struct {
	id        string
	key       ExecutorKey
	publicKey PublicKey
	ticket    [32]byte
	expires   time.Time
	socket    *websocket.Conn
}

func New(c Config) (*Registry, error) {
	url, keys, err := validateConfig(c)
	if err != nil {
		return nil, err
	}
	return &Registry{source: c.Store, checkOwnership: c.CheckOwnership, publicWS: url, keys: keys, registrations: make(map[string]*registration)}, nil
}

func (r *Registry) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /cloud/environment/{environment}/register", r.register)
	mux.HandleFunc("GET /cloud/environment/{environment}/executor/{registration}", r.connectExecutor)
	return mux
}

func (r *Registry) authorized(ctx context.Context, key ExecutorKey) error {
	// Client disconnects must not cancel a query on the shared execution lease.
	ownerCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := r.checkOwnership(ownerCtx); err != nil {
		r.Close()
		return err
	}
	_, err := r.source.GetEnvironment(ctx, key.TenantID, key.EnvironmentID)
	return err
}

func (r *Registry) check(w http.ResponseWriter, req *http.Request, key ExecutorKey) bool {
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
	key, ok := r.credential(req, environment)
	if !ok {
		writeError(w, http.StatusUnauthorized)
		return
	}
	if !r.check(w, req, key) {
		return
	}
	var body RegistrationRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.SecurityProfile != securityProfile || !body.ExecutorPublicKey.valid() {
		writeError(w, http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest)
		return
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		writeError(w, http.StatusServiceUnavailable)
		return
	}
	ticket := base64.RawURLEncoding.EncodeToString(secret)
	next := &registration{id: uuid.NewString(), key: key, publicKey: body.ExecutorPublicKey, ticket: sha256.Sum256([]byte(ticket)), expires: time.Now().Add(ticketLifetime)}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable)
		return
	}
	previous := r.registrations[environment]
	r.registrations[environment] = next
	var oldSocket *websocket.Conn
	if previous != nil {
		oldSocket = previous.socket
	}
	r.mu.Unlock()
	if oldSocket != nil {
		_ = oldSocket.Close()
	}
	writeJSON(w, http.StatusOK, RegistrationResponse{EnvironmentID: environment, ExecutorRegistrationID: next.id, SecurityProfile: securityProfile, URL: r.publicWS + "/cloud/environment/" + environment + "/executor/" + next.id + "?ticket=" + ticket})
}

// Connected reports a current authenticated socket, not harness readiness or filesystem isolation.
func (r *Registry) Connected(ctx context.Context, tenant, environment string) (bool, error) {
	if err := r.authorized(ctx, ExecutorKey{TenantID: tenant, EnvironmentID: environment}); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	reg := r.registrations[environment]
	return !r.closed && reg != nil && reg.socket != nil, nil
}

func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for environment, reg := range r.registrations {
		if reg.socket != nil {
			_ = reg.socket.Close()
		}
		delete(r.registrations, environment)
	}
}
