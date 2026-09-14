package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/google/uuid"
)

func callerBinding() APIKey {
	return APIKey{TokenSHA256: device.HashCredential("caller"), TenantID: uuid.NewString(),
		OrganizationID: "org-one", ProjectID: "project-one", SubjectKind: "user", SubjectID: "user-one"}
}

func TestCallerPrincipalConfiguration(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*APIKey)
	}{
		{"missing organization", func(k *APIKey) { k.OrganizationID = "" }},
		{"missing project", func(k *APIKey) { k.ProjectID = "" }},
		{"missing subject", func(k *APIKey) { k.SubjectID = "" }},
		{"unknown subject kind", func(k *APIKey) { k.SubjectKind = "workspace" }},
		{"untyped subject", func(k *APIKey) { k.SubjectKind = "" }},
		{"blank subject", func(k *APIKey) { k.SubjectID = " " }},
		{"invalid tenant", func(k *APIKey) { k.TenantID = "product-workspace" }},
		{"zero tenant", func(k *APIKey) { k.TenantID = uuid.Nil.String() }},
		{"invalid digest", func(k *APIKey) { k.TokenSHA256 = "plaintext" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			key := callerBinding()
			test.edit(&key)
			if _, err := NewAuthenticator([]APIKey{key}); err == nil {
				t.Fatal("invalid caller configuration accepted")
			}
		})
	}
	key := callerBinding()
	for _, test := range []struct {
		name string
		edit func(*APIKey)
	}{
		{"tenant remap", func(k *APIKey) { k.ProjectID = "project-two" }},
		{"project remap", func(k *APIKey) { k.TenantID = uuid.NewString() }},
		{"duplicate key", func(k *APIKey) { k.TokenSHA256 = key.TokenSHA256 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			other := key
			other.TokenSHA256 = device.HashCredential("other")
			test.edit(&other)
			if _, err := NewAuthenticator([]APIKey{key, other}); err == nil {
				t.Fatal("ambiguous caller configuration accepted")
			}
		})
	}
}

func TestCallerPrincipalHeadersAndKeyRotation(t *testing.T) {
	key := callerBinding()
	rotated, peer := key, key
	rotated.TokenSHA256 = device.HashCredential("rotated")
	peer.TokenSHA256, peer.SubjectKind, peer.SubjectID = device.HashCredential("peer"), "service_account", "service-one"
	auth, err := NewAuthenticator([]APIKey{key, rotated, peer})
	if err != nil {
		t.Fatal(err)
	}
	scopes := auth.ProjectScopes()
	if len(scopes) != 1 || scopes[0].TenantID != key.TenantID {
		t.Fatalf("project scopes = %+v", scopes)
	}
	scopes[0].ProjectID = "mutated"
	if auth.ProjectScopes()[0].ProjectID != key.ProjectID {
		t.Fatal("returned scopes mutate authenticated configuration")
	}
	for _, test := range []struct {
		name, token, subject string
		headers              http.Header
		status               int
	}{
		{"absent scopes", "caller", key.SubjectID, nil, 200},
		{"matching scopes", "caller", key.SubjectID, http.Header{"Openai-Organization": {key.OrganizationID}, "Openai-Project": {key.ProjectID}}, 200},
		{"rotated key", "rotated", key.SubjectID, nil, 200},
		{"same project peer", "peer", peer.SubjectID, nil, 200},
		{"forged identity", "caller", key.SubjectID, http.Header{"X-Tenant-Id": {uuid.NewString()}, "X-User-Id": {"other"}, "X-Forwarded-User": {"other"}}, 200},
		{"wrong organization", "caller", "", http.Header{"Openai-Organization": {"org-two"}}, 401},
		{"wrong project", "caller", "", http.Header{"Openai-Project": {"project-two"}}, 401},
		{"empty project", "caller", "", http.Header{"Openai-Project": {""}}, 401},
		{"duplicate project", "caller", "", http.Header{"Openai-Project": {key.ProjectID, key.ProjectID}}, 401},
		{"duplicate authorization", "caller", "", http.Header{"Authorization": {"Bearer caller", "Bearer peer"}}, 401},
		{"merged projects", "caller", "", http.Header{"Openai-Project": {key.ProjectID + ", other"}}, 401},
		{"wrong key", "unknown", "", nil, 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1/agents/sessions", nil)
			r.Header.Set("Authorization", "Bearer "+test.token)
			r.Header.Set("OpenAI-Beta", "agents=v1")
			for name, values := range test.headers {
				r.Header[name] = values
			}
			called := false
			h := (&Handler{auth: auth}).authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				principal := r.Context().Value(principalContextKey{}).(identity.Principal)
				if principal.SubjectID != test.subject || tenantID(r) != key.TenantID || principal.ProjectID != key.ProjectID {
					t.Fatalf("authenticated principal = %+v", principal)
				}
				w.WriteHeader(http.StatusOK)
			}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.status || called != (test.status == 200) {
				t.Fatalf("response = %d, called = %v", w.Code, called)
			}
			if test.status == 401 && (!strings.Contains(w.Body.String(), "invalid_api_key") || w.Header().Get("WWW-Authenticate") != "Bearer") {
				t.Fatalf("authentication response = %s", w.Body)
			}
		})
	}
}
