package deepseekharness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/enginehost"
	"github.com/google/uuid"
)

// The dsh /api gateway is a single-envelope RPC surface: every method is
// a POST to /api/<method> carrying a client-request envelope, and every
// answer is a server-response envelope whose result is an explicit
// ok/error union rather than an HTTP status. A 200 with ok=false is the
// normal way a method reports a rejected request, so the union has to be
// unwrapped before anything else.
const (
	clientRequestType  = "client-request"
	serverResponseType = "server-response"

	methodSessionCreate = "session.create"
	methodSessionPrompt = "session.prompt"
	methodSessionList   = "session.list"
	methodSessionCancel = "session.cancel"

	// promptModeQueue appends the turn behind anything already running.
	// The alternative, "steer", interrupts the current turn — wrong for a
	// daemon that submits one turn and waits for it.
	promptModeQueue = "queue"
)

type clientRequest struct {
	Type    string `json:"type"`
	RPCID   string `json:"rpcId"`
	Method  string `json:"method"`
	Payload any    `json:"payload"`
}

type serverResponse struct {
	Type   string `json:"type"`
	RPCID  string `json:"rpcId"`
	Result struct {
		OK    bool            `json:"ok"`
		Value json.RawMessage `json:"value"`
		Error *rpcError       `json:"error"`
	} `json:"result"`
}

// rpcError is the gateway's failure shape. Code is a stable machine
// string ("bad-request", "not-found", ...); Details carries the zod issue
// list for a schema rejection, which is the difference between a
// debuggable error and a shrug.
type rpcError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return "deepseekharness: unknown api error"
	}
	msg := fmt.Sprintf("deepseekharness: dsh api %s: %s", e.Code, e.Message)
	if len(e.Details) > 0 {
		msg += ": " + truncate(string(e.Details), 400)
	}
	return msg
}

// apiClient speaks the dsh gateway envelope over an enginehost transport.
type apiClient struct {
	transport *enginehost.Client
}

func newAPIClient(transport *enginehost.Client) *apiClient {
	return &apiClient{transport: transport}
}

// call sends one method and decodes the ok branch into out.
func (c *apiClient) call(ctx context.Context, method string, payload, out any) error {
	req := clientRequest{
		Type:    clientRequestType,
		RPCID:   uuid.NewString(),
		Method:  method,
		Payload: payload,
	}
	var resp serverResponse
	if err := c.transport.PostJSON(ctx, apiPathPrefix+"/"+method, req, &resp); err != nil {
		return err
	}
	if resp.Type != serverResponseType {
		return fmt.Errorf("deepseekharness: unexpected api envelope %q for %s", resp.Type, method)
	}
	if !resp.Result.OK {
		if resp.Result.Error != nil {
			return fmt.Errorf("%s: %w", method, resp.Result.Error)
		}
		return fmt.Errorf("deepseekharness: %s failed without an error body", method)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Result.Value, out); err != nil {
		return fmt.Errorf("deepseekharness: decode %s value: %w", method, err)
	}
	return nil
}

type sessionCreateValue struct {
	SessionID string `json:"sessionId"`
}

// CreateSession opens a fresh dsh session rooted at cwd.
func (c *apiClient) CreateSession(ctx context.Context, cwd string) (string, error) {
	payload := map[string]any{}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		payload["cwd"] = cwd
	}
	var value sessionCreateValue
	if err := c.call(ctx, methodSessionCreate, payload, &value); err != nil {
		return "", err
	}
	if value.SessionID == "" {
		return "", fmt.Errorf("deepseekharness: %s returned no session id", methodSessionCreate)
	}
	return value.SessionID, nil
}

type promptContentPart struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	Data      string `json:"data,omitempty"`
	Name      string `json:"name,omitempty"`
}

// Prompt submits one turn. It returns as soon as the gateway accepts the
// turn — the turn's own events arrive on the downlink, not here.
//
// There is no separate resume method: prompting a session id the server
// does not hold in memory makes it load that session's log from disk and
// resume the agent. Warm continuation and cold resume are the same call.
func (c *apiClient) Prompt(ctx context.Context, sessionID string, content []promptContentPart) error {
	payload := map[string]any{
		"sessionId": sessionID,
		"mode":      promptModeQueue,
		"content":   content,
	}
	return c.call(ctx, methodSessionPrompt, payload, nil)
}

type sessionListItem struct {
	SessionID string `json:"sessionId"`
	UpdatedAt int64  `json:"updatedAt"`
	Running   bool   `json:"running"`
	Blank     bool   `json:"blank"`
	CWD       string `json:"cwd"`
}

type sessionListValue struct {
	Items []sessionListItem `json:"items"`
}

// ListSessions enumerates the sessions the server can serve. Used as the
// readiness probe: it is the cheapest method that proves the gateway, the
// carrier and the session store are all composed, which a bare TCP
// connect does not.
func (c *apiClient) ListSessions(ctx context.Context) ([]sessionListItem, error) {
	var value sessionListValue
	if err := c.call(ctx, methodSessionList, map[string]any{}, &value); err != nil {
		return nil, err
	}
	return value.Items, nil
}

// Cancel aborts the session's running turn. It targets the session, not
// the process: the resident server keeps serving other conversations, so
// cancelling a run must not take the engine down with it.
func (c *apiClient) Cancel(ctx context.Context, sessionID string) error {
	return c.call(ctx, methodSessionCancel, map[string]any{"sessionId": sessionID}, nil)
}
