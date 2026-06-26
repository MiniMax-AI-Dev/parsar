package otlp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// DefaultAddr is the OTLP/HTTP default bind. Operators override via
// Config.Addr.
const DefaultAddr = ":4318"

const (
	PathTraces = "/v1/traces"
	PathLogs   = "/v1/logs"
)

const (
	contentTypeProto = "application/x-protobuf"
	contentTypeJSON  = "application/json"
)

// MaxBodyBytes caps each OTLP request body. 16 MiB matches the
// default OTel collector ingest limit.
const MaxBodyBytes = 16 << 20

// DefaultReadTimeout bounds how long the receiver spends draining a
// single request body. OTLP batches are small in practice.
const DefaultReadTimeout = 30 * time.Second

// Config holds receiver wiring.
type Config struct {
	// Addr is the TCP bind address. Defaults to DefaultAddr.
	Addr string
	// Ingester is the audit pipeline this receiver feeds. Required.
	Ingester *audit.Ingester
	// Signer enforces bearer-token auth on every request. Required:
	// the receiver refuses to start without one so there is no
	// "open by default" path.
	Signer *TokenSigner
	// Logger receives validation warnings and lifecycle messages.
	// Defaults to obslog.Bg().
	Logger *slog.Logger
	// ReadTimeout overrides DefaultReadTimeout.
	ReadTimeout time.Duration
}

// Receiver is a minimal OTLP/HTTP receiver scoped to Parsar's
// audit-emit needs. See package doc.go for the rationale.
type Receiver struct {
	addr         string
	ingester     *audit.Ingester
	signer       *TokenSigner
	logger       *slog.Logger
	readTimeout  time.Duration
	server       *http.Server
	listenerAddr string // resolved after Start; useful for tests using :0
}

// NewReceiver validates the config and returns an unstarted Receiver.
// Both Ingester and Signer are required — there is no unauthenticated
// mode.
func NewReceiver(cfg Config) (*Receiver, error) {
	if cfg.Ingester == nil {
		return nil, errors.New("otlp: receiver requires a non-nil audit ingester")
	}
	if cfg.Signer == nil {
		return nil, errors.New("otlp: receiver requires a non-nil token signer; configure audit.otlp.signing_key")
	}
	addr := cfg.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	logger := cfg.Logger
	if logger == nil {
		logger = obslog.Bg()
	}
	timeout := cfg.ReadTimeout
	if timeout <= 0 {
		timeout = DefaultReadTimeout
	}
	return &Receiver{
		addr:        addr,
		ingester:    cfg.Ingester,
		signer:      cfg.Signer,
		logger:      logger,
		readTimeout: timeout,
	}, nil
}

// Start binds the listener and serves OTLP requests asynchronously.
// Returns once the TCP listener is up; the HTTP server runs in a
// background goroutine until Shutdown.
//
// The supplied ctx is propagated to every request handler via
// BaseContext, so a parent shutdown signal reaches in-flight requests
// even before Shutdown is called.
func (r *Receiver) Start(ctx context.Context) error {
	if r.server != nil {
		return errors.New("otlp: receiver already started")
	}

	ln, err := net.Listen("tcp", r.addr)
	if err != nil {
		return fmt.Errorf("otlp: bind %s: %w", r.addr, err)
	}
	r.listenerAddr = ln.Addr().String()

	mux := chi.NewMux()
	// OTLP runs on its own listener, so register obslog HTTP
	// middleware here as well to get trace_id on every inbound
	// request.
	mux.Use(obslog.HTTPMiddleware)
	mux.Use(authMiddleware(r.signer, r.logger))
	mux.Post(PathTraces, r.handleTraces)
	mux.Post(PathLogs, r.handleLogs)

	r.server = &http.Server{
		Handler:     mux,
		ReadTimeout: r.readTimeout,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	srv := r.server
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.logger.Error("otlp receiver crashed", "addr", r.listenerAddr, "error", err)
		}
	}()
	r.logger.Info("otlp receiver listening", "addr", r.listenerAddr)
	return nil
}

// Addr returns the resolved listener address (useful for tests that
// bind to ":0").
func (r *Receiver) Addr() string { return r.listenerAddr }

// Shutdown stops the receiver gracefully. Safe to call before Start
// (no-op) or multiple times.
func (r *Receiver) Shutdown(ctx context.Context) error {
	if r.server == nil {
		return nil
	}
	err := r.server.Shutdown(ctx)
	r.server = nil
	return err
}

func (r *Receiver) handleTraces(w http.ResponseWriter, req *http.Request) {
	claims, ok := ClaimsFromContext(req.Context())
	if !ok {
		// Defense in depth — authMiddleware should have rejected
		// the request already. Never dispatch unattributed events.
		r.logger.Error("otlp traces handler reached without claims; refusing")
		writeError(w, http.StatusInternalServerError, "authentication context missing")
		return
	}

	body, err := readBody(w, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	msg := &coltracepb.ExportTraceServiceRequest{}
	if err := unmarshalOTLP(req.Header.Get("Content-Type"), body, msg); err != nil {
		writeError(w, http.StatusBadRequest, "decode OTLP request: "+err.Error())
		return
	}

	events, schemaErrs := convertTraces(msg)
	applyClaims(events, claims)
	bufferDropped := r.dispatch(events)

	totalRejected := len(schemaErrs) + bufferDropped
	if totalRejected > 0 {
		r.logger.Warn("otlp traces partial reject",
			"agent_run_id", claims.AgentRunID,
			"workspace_id", claims.WorkspaceID,
			"schema_rejected", len(schemaErrs),
			"buffer_dropped", bufferDropped,
			"sample_error", firstError(schemaErrs))
	}

	writePartialSuccess(w, totalRejected, totalRejected > 0)
}

func (r *Receiver) handleLogs(w http.ResponseWriter, req *http.Request) {
	if _, ok := ClaimsFromContext(req.Context()); !ok {
		r.logger.Error("otlp logs handler reached without claims; refusing")
		writeError(w, http.StatusInternalServerError, "authentication context missing")
		return
	}
	// Logs path is a stub: payload is parsed so malformed senders
	// see a 4xx, but no events are dispatched.
	body, err := readBody(w, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	msg := &collogspb.ExportLogsServiceRequest{}
	if err := unmarshalOTLP(req.Header.Get("Content-Type"), body, msg); err != nil {
		writeError(w, http.StatusBadRequest, "decode OTLP request: "+err.Error())
		return
	}
	dropped := 0
	for _, rl := range msg.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			dropped += len(sl.GetLogRecords())
		}
	}
	if dropped > 0 {
		r.logger.Warn("otlp logs path is a stub; events not dispatched",
			"dropped", dropped)
	}
	writePartialSuccess(w, dropped, dropped > 0)
}

// applyClaims overrides security-sensitive identifier fields on every
// event with the values pinned in the token's claims. Anti-forgery:
// even if a tool puts the wrong workspace_id / agent_run_id in its
// OTLP attributes, the receiver rewrites them before the event reaches
// the ingester. SandboxID lands in payload (not promoted to a column)
// so admin queries can pivot without changing audit_records schema.
func applyClaims(events []audit.Event, claims TokenClaims) {
	for i := range events {
		events[i].WorkspaceID = claims.WorkspaceID
		if events[i].Payload == nil {
			events[i].Payload = map[string]any{}
		}
		events[i].Payload["agent_run_id"] = claims.AgentRunID
		if claims.SandboxID != "" {
			events[i].Payload["sandbox_id"] = claims.SandboxID
		}
	}
}

// dispatch forwards converted events to the ingester. Returns the
// number of events the ingester refused.
func (r *Receiver) dispatch(events []audit.Event) int {
	rejected := 0
	for _, ev := range events {
		if err := r.ingester.Emit(ev); err != nil {
			rejected++
			r.logger.Warn("otlp ingester emit failed",
				"event_type", ev.EventType, "error", err)
		}
	}
	return rejected
}

func readBody(w http.ResponseWriter, req *http.Request) ([]byte, error) {
	req.Body = http.MaxBytesReader(w, req.Body, MaxBodyBytes)
	return io.ReadAll(req.Body)
}

// unmarshalOTLP decodes an OTLP/HTTP body. OTLP requires protobuf;
// JSON is optional but enabled because operators routinely smoke-test
// with curl. Empty Content-Type defaults to JSON to match curl.
func unmarshalOTLP(contentType string, body []byte, msg proto.Message) error {
	switch contentType {
	case contentTypeProto:
		return proto.Unmarshal(body, msg)
	case contentTypeJSON, "":
		return protojson.Unmarshal(body, msg)
	default:
		return fmt.Errorf("unsupported Content-Type %q (want %s or %s)",
			contentType, contentTypeProto, contentTypeJSON)
	}
}

// writePartialSuccess emits the OTLP partial-success envelope. OTLP
// collectors return 200 OK for transport success even when records
// were dropped server-side; rejection count + message live in the
// body. We follow the same convention so off-the-shelf SDKs treat us
// correctly.
func writePartialSuccess(w http.ResponseWriter, rejected int, hadError bool) {
	w.Header().Set("Content-Type", "application/json")
	msg := ""
	if hadError {
		msg = "some events failed Parsar schema validation; see receiver logs"
	}
	body := fmt.Sprintf(
		`{"partialSuccess":{"rejectedLogRecords":%d,"errorMessage":%q}}`,
		rejected, msg,
	)
	_, _ = w.Write([]byte(body))
}

func writeError(w http.ResponseWriter, status int, msg string) {
	http.Error(w, msg, status)
}

func firstError(errs []error) string {
	if len(errs) == 0 {
		return ""
	}
	return errs[0].Error()
}
