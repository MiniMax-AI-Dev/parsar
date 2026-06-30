package blob

import (
	"errors"
	"io"
	"net/http"
	"strings"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// ProxyPathPrefix is the route prefix the handler is mounted under. The
// ref is the remainder of the path (pg:<uuid> contains no slash). Exported
// so main.go can mount the route at ProxyPathPrefix+"*" — single source of
// truth for the path.
const ProxyPathPrefix = "/internal/blobs/"

// ProxyHandler serves authenticated PUT/GET for PG-backed blobs. It is
// the presigned-URL equivalent for the PG backend: every request must
// carry a token minted by the PGStore for that exact ref + method.
type ProxyHandler struct {
	store  *PGStore
	signer *ProxySigner
}

// NewProxyHandler wires the handler to a PG store + signer.
func NewProxyHandler(store *PGStore, signer *ProxySigner) *ProxyHandler {
	return &ProxyHandler{store: store, signer: signer}
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimPrefix(r.URL.Path, ProxyPathPrefix)
	if ref == "" || strings.Contains(ref, "/") {
		http.Error(w, "invalid blob ref", http.StatusBadRequest)
		return
	}
	claims, err := h.signer.Verify(r.URL.Query().Get("token"))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if claims.Ref != ref || claims.Method != r.Method {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodPut:
		h.put(w, r, claims)
	case http.MethodGet:
		h.get(w, r, claims)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *ProxyHandler) put(w http.ResponseWriter, r *http.Request, claims ProxyClaims) {
	// Cap the read at MaxBlobBytes; MaxBytesReader makes the body error
	// once the limit is exceeded so a hostile client can't stream forever.
	r.Body = http.MaxBytesReader(w, r.Body, MaxBlobBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "blob exceeds max size", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if err := h.store.PutBytes(r.Context(), claims.Ref, claims.WorkspaceID, data); err != nil {
		if errors.Is(err, ErrTooLarge) {
			http.Error(w, "blob exceeds max size", http.StatusRequestEntityTooLarge)
			return
		}
		obslog.Bg().Error("blob proxy put failed", "ref", claims.Ref, "error", err.Error())
		http.Error(w, "store failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ProxyHandler) get(w http.ResponseWriter, r *http.Request, claims ProxyClaims) {
	data, err := h.store.Download(r.Context(), claims.Ref)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		obslog.Bg().Error("blob proxy get failed", "ref", claims.Ref, "error", err.Error())
		http.Error(w, "store failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
