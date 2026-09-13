package codex

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"time"
)

const maxHarnessGrants = 32

type harnessGrant struct {
	credential    *harnessCredential
	publicKey     PublicKey
	authorization [32]byte
	expires       time.Time
	executor      *connection
	harness       *connection
	validated     bool
}

// @Summary Authorize an execution-owned harness key without disrupting an existing connection
// @Tags Native executor registry
// @Accept json
// @Produce json
// @Param environment path string true "Environment ID"
// @Param request body ConnectRequest true "Native harness public key"
// @Success 200 {object} ConnectResponse
// @Failure 400,401,404,429,503 {object} RegistryError
// @Router /cloud/environment/{environment}/connect [post]
func (r *Registry) connect(w http.ResponseWriter, req *http.Request) {
	environment := req.PathValue("environment")
	credential, ok := r.harnessCredential(req, environment)
	if !ok {
		writeError(w, http.StatusUnauthorized)
		return
	}
	if !r.check(w, req, credential.key) {
		return
	}
	var body ConnectRequest
	if !decodeRequest(w, req, &body) {
		return
	}
	if !body.HarnessPublicKey.valid() {
		writeError(w, http.StatusBadRequest)
		return
	}
	ticket, err := capability()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable)
		return
	}
	authorization, err := capability()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable)
		return
	}
	r.mu.Lock()
	if !r.harnessCredentialActiveLocked(credential) {
		r.mu.Unlock()
		writeError(w, http.StatusUnauthorized)
		return
	}
	reg := r.registrations[environment]
	if r.closed || reg == nil || reg.socket == nil {
		r.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable)
		return
	}
	now := time.Now()
	for hash, grant := range reg.grants {
		if grant.executor != reg.socket || (grant.harness == nil && !now.Before(grant.expires)) {
			delete(reg.grants, hash)
		}
	}
	if len(reg.grants) >= maxHarnessGrants {
		r.mu.Unlock()
		writeError(w, http.StatusTooManyRequests)
		return
	}
	reg.grants[sha256.Sum256([]byte(ticket))] = &harnessGrant{credential: credential, publicKey: body.HarnessPublicKey, authorization: sha256.Sum256([]byte(authorization)), expires: now.Add(ticketLifetime), executor: reg.socket}
	response := ConnectResponse{
		RegistrationResponse: RegistrationResponse{EnvironmentID: environment, ExecutorRegistrationID: reg.id, SecurityProfile: securityProfile, URL: r.publicWS + "/cloud/environment/" + environment + "/harness/" + reg.id + "?ticket=" + ticket},
		ExecutorPublicKey:    reg.publicKey, HarnessKeyAuthorization: authorization,
	}
	r.mu.Unlock()
	writeJSON(w, http.StatusOK, response)
}

// @Summary Validate a connected harness key proved by the native Noise handshake
// @Tags Native executor registry
// @Accept json
// @Produce json
// @Param environment path string true "Environment ID"
// @Param request body ValidationRequest true "Native harness key authorization"
// @Success 200 {object} ValidationResponse
// @Failure 400,401,404,503 {object} RegistryError
// @Router /cloud/environment/{environment}/validate [post]
func (r *Registry) validate(w http.ResponseWriter, req *http.Request) {
	environment := req.PathValue("environment")
	key, ok := r.executorCredential(w, req, environment)
	if !ok {
		return
	}
	if !r.check(w, req, key) {
		return
	}
	var body ValidationRequest
	if !decodeRequest(w, req, &body) {
		return
	}
	if !body.HarnessPublicKey.valid() {
		writeError(w, http.StatusBadRequest)
		return
	}
	authorization := sha256.Sum256([]byte(body.HarnessKeyAuthorization))
	valid := false
	r.mu.Lock()
	reg := r.registrations[environment]
	if !r.closed && reg != nil && reg.id == body.ExecutorRegistrationID && reg.key.TokenSHA256 == key.TokenSHA256 && reg.socket != nil {
		for _, grant := range reg.grants {
			if r.harnessCredentialActiveLocked(grant.credential) && grant.executor == reg.socket && grant.harness != nil && grant.harness == reg.socket.peer && !grant.validated && time.Now().Before(grant.expires) && grant.publicKey == body.HarnessPublicKey && subtle.ConstantTimeCompare(authorization[:], grant.authorization[:]) == 1 {
				grant.validated = true
				valid = true
				break
			}
		}
	}
	r.mu.Unlock()
	writeJSON(w, http.StatusOK, ValidationResponse{Valid: valid})
}

// @Summary Attach one harness using a short-lived connection capability
// @Tags Native executor registry
// @Param environment path string true "Environment ID"
// @Param registration path string true "Registration ID"
// @Param ticket query string true "Private connection capability"
// @Success 101 {string} string "WebSocket upgrade"
// @Failure 401,404,409,503 {object} RegistryError
// @Router /cloud/environment/{environment}/harness/{registration} [get]
func (r *Registry) connectHarness(w http.ResponseWriter, req *http.Request) {
	environment, id := req.PathValue("environment"), req.PathValue("registration")
	ticket := sha256.Sum256([]byte(req.URL.Query().Get("ticket")))
	r.mu.Lock()
	reg := r.registrations[environment]
	var grant *harnessGrant
	if reg != nil {
		grant = reg.grants[ticket]
	}
	valid := r.harnessAttachable(reg, grant, id)
	r.mu.Unlock()
	if !valid {
		writeError(w, http.StatusUnauthorized)
		return
	}
	if !r.check(w, req, grant.credential.key) || !r.checkCurrentExecutor(w, req, reg.key) {
		return
	}
	r.mu.Lock()
	if r.registrations[environment] != reg || !r.harnessAttachable(reg, grant, id) {
		r.mu.Unlock()
		writeError(w, http.StatusUnauthorized)
		return
	}
	if reg.socket.peer != nil {
		r.mu.Unlock()
		writeError(w, http.StatusConflict)
		return
	}
	socket, err := nativeUpgrader.Upgrade(w, req, nil)
	if err != nil {
		r.mu.Unlock()
		return
	}
	c := &connection{socket: socket, peer: reg.socket}
	reg.socket.peer = c
	grant.harness = c
	r.mu.Unlock()
	r.serveConnection(environment, reg, c)
}

func (r *Registry) harnessAttachable(reg *registration, grant *harnessGrant, id string) bool {
	return !r.closed && reg != nil && reg.id == id && grant != nil && r.harnessCredentialActiveLocked(grant.credential) && grant.executor == reg.socket && grant.harness == nil && time.Now().Before(grant.expires)
}
