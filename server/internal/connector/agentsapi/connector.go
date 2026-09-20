package agentsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	agentsclient "github.com/MiniMax-AI-Dev/parsar/packages/agents-client/v1"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/coreaccess"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const ConnectorType = "agents_api"

type Store interface {
	GetConversation(context.Context, string) (store.ConversationRead, error)
	ClaimCoreRun(context.Context, string) (context.Context, func(), error)
	GetCoreSession(context.Context, string) (store.CoreSessionBinding, error)
	EnsureCoreSession(context.Context, string, json.RawMessage) (store.CoreSessionBinding, error)
	BindCoreSession(context.Context, string, string) error
	EnsureCoreRun(context.Context, string, string, json.RawMessage) error
	GetCoreRun(context.Context, string) (store.CoreRunBinding, error)
	SetCoreRunBaseline(context.Context, string, string) error
	MarkCoreRunSubmitted(context.Context, string) error
	MarkCoreRunAttempted(context.Context, string) error
	BindCoreTurn(context.Context, string, string) error
	SettleCoreRun(context.Context, string) error
	RecordCoreUsage(context.Context, string, store.UsageInput) error
	GetCoreExecutionStatus(context.Context, string) (string, error)
	CancelAgentRun(context.Context, string, string) (bool, error)
	ListCoreExecutionEvents(context.Context, string, int64) ([]store.AgentRunEventRead, error)
	GetEnabledCapabilitiesForAgent(context.Context, string) ([]store.EnabledCapabilityRead, error)
}

type Connector struct {
	Blobs    blob.Store
	Resolve  coreaccess.Resolver
	sessions openai.BetaAgentSessionService
	store    Store
	poll     time.Duration
	slots    chan struct{}
}

func New(cfg agentsclient.Config, st Store) (*Connector, error) {
	if st == nil {
		return nil, errors.New("Core execution store is required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 6 * time.Minute}
	}
	sessions, err := agentsclient.New(cfg)
	if err != nil {
		return nil, err
	}
	return &Connector{sessions: sessions, store: st, poll: 500 * time.Millisecond, slots: make(chan struct{}, 8)}, nil
}

func NewForWorkspaces(resolve coreaccess.Resolver, st Store) *Connector {
	return &Connector{Resolve: resolve, store: st, poll: 500 * time.Millisecond, slots: make(chan struct{}, 8)}
}

func (*Connector) Type() string { return ConnectorType }
func (*Connector) Capabilities() connector.Capabilities {
	return connector.Capabilities{Sync: true, Streaming: true, Cancellation: true, Usage: true, Audit: true}
}

func (c *Connector) StreamPrompt(ctx context.Context, in connector.PromptInput) (<-chan connector.PromptEvent, error) {
	if c.Resolve != nil {
		client, err := c.Resolve(in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		scoped := *c
		scoped.Resolve = nil
		scoped.sessions = client.Sessions
		return scoped.StreamPrompt(ctx, in)
	}
	select {
	case c.slots <- struct{}{}:
	default:
		return nil, store.ErrCoreExecutionClaimed
	}
	leaseCtx, release, err := c.store.ClaimCoreRun(ctx, in.RunID)
	if err != nil {
		<-c.slots
		return nil, fmt.Errorf("%w: %v", store.ErrCoreExecutionClaimed, err)
	}
	ctx = leaseCtx
	var request openai.BetaAgentSessionNewParams
	frozen, err := c.store.GetCoreSession(ctx, in.RunID)
	if err == nil {
		err = json.Unmarshal(frozen.Request, &request)
	} else if errors.Is(err, store.ErrUnknownAgentRun) {
		request, err = c.sessionRequest(ctx, in)
	} else {
		err = errPersistence
	}
	if err != nil {
		release()
		<-c.slots
		return nil, err
	}
	ch := make(chan connector.PromptEvent)
	go func() {
		defer close(ch)
		defer release()
		defer func() { <-c.slots }()
		var err error
		for {
			err = c.execute(ctx, in, request, ch)
			if !retryable(err) || ctx.Err() != nil {
				break
			}
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(time.Second):
			}
			if ctx.Err() != nil {
				break
			}
		}
		if err != nil && ctx.Err() == nil && !errors.Is(err, errPersistence) {
			// Recovery of an already terminal product run still fences uncertain
			// Core work, but must not append another product failure each scan.
			current, readErr := c.store.GetCoreExecutionStatus(ctx, in.RunID)
			if readErr != nil || current == "failed" || current == "cancelled" || current == "completed" {
				return
			}
			message := safeError(err)
			emit(ctx, ch, connector.PromptEvent{Type: connector.EventError, Error: message})
			emit(ctx, ch, connector.PromptEvent{Type: connector.EventDone, Final: &connector.PromptOutput{Metadata: map[string]any{"error": message}}})
		}
	}()
	return ch, nil
}

func (c *Connector) Prompt(ctx context.Context, in connector.PromptInput) (connector.PromptOutput, error) {
	events, err := c.StreamPrompt(ctx, in)
	if err != nil {
		return connector.PromptOutput{}, err
	}
	var final connector.PromptOutput
	completed := false
	for event := range events {
		if event.Type == connector.EventDone {
			completed = true
		}
		if event.Persisted != nil {
			event.Persisted <- nil
		}
		if event.Type == connector.EventError {
			err = errors.New(event.Error)
		}
		if event.Final != nil {
			final = *event.Final
		}
	}
	if ctx.Err() != nil {
		return final, ctx.Err()
	}
	if !completed && err == nil {
		err = errPersistence
	}
	return final, err
}

func (c *Connector) execute(ctx context.Context, in connector.PromptInput, request openai.BetaAgentSessionNewParams, ch chan<- connector.PromptEvent) error {
	if len(in.TriggerAttachments) > 0 {
		return errors.New("Core execution does not support message attachments yet")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	binding, err := c.ensureModelSession(ctx, in, raw)
	if err != nil {
		if errors.Is(err, store.ErrCatalogNotFound) || errors.Is(err, store.ErrInvalidInput) || errors.Is(err, store.ErrCatalogKeyUnavailable) {
			return fmt.Errorf("Core model configuration unavailable: %w", err)
		}
		return errPersistence
	}
	if err := json.Unmarshal(binding.Request, &request); err != nil {
		return err
	}
	var frozen struct {
		Agent map[string]any `json:"agent"`
	}
	if err := json.Unmarshal(binding.Request, &frozen); err != nil {
		return err
	}
	request.Agent.SetExtraFields(frozen.Agent)
	in.AgentConfig = map[string]any{"model": request.Agent.Model.Value}
	if binding.SessionID == "" {
		options, err := c.modelSessionOptions(binding)
		if err != nil {
			return err
		}
		options = append(options, option.WithHeader("Idempotency-Key", "parsar-session-"+binding.ID))
		session, err := c.sessions.New(ctx, request, options...)
		if err != nil {
			return err
		}
		if err := c.store.BindCoreSession(ctx, binding.ID, session.ID); err != nil {
			return errPersistence
		}
		binding.SessionID = session.ID
	}
	input := openai.BetaAgentSessionEventNewParams{Events: []openai.AgentSessionInputParamUnion{{
		OfParamAgentSessionInputMessage: &openai.AgentSessionInputParamAgentSessionInputMessage{Input: []openai.AgentSessionInputMessageParam{{
			Content: []openai.InputContentParamUnion{{OfParamInputText: &openai.InputContentParamInputText{Text: in.TriggerMessageContent}}},
		}}},
	}}}
	raw, err = json.Marshal(input)
	if err != nil {
		return err
	}
	if err := c.store.EnsureCoreRun(ctx, in.RunID, binding.ID, raw); err != nil {
		return errPersistence
	}
	run, err := c.store.GetCoreRun(ctx, in.RunID)
	if err != nil {
		return errPersistence
	}
	if !run.BaselineSet {
		turns, err := c.sessions.Turns.List(ctx, binding.SessionID, openai.BetaAgentSessionTurnListParams{Order: "desc", Limit: openai.Int(1)})
		if err != nil {
			return err
		}
		previous := ""
		if len(turns.Data) > 0 {
			if !terminal(string(turns.Data[0].Status)) {
				return errors.New("the previous Core turn is still active")
			}
			previous = turns.Data[0].ID
		}
		if err := c.store.SetCoreRunBaseline(ctx, in.RunID, previous); err != nil {
			return errPersistence
		}
		run.PreviousTurnID, run.BaselineSet = previous, true
	}
	productRun, err := c.store.GetCoreExecutionStatus(ctx, in.RunID)
	if err != nil {
		return errPersistence
	}
	if (productRun == "cancelled" || productRun == "failed") && !run.Attempted {
		if err := c.store.SettleCoreRun(ctx, in.RunID); err != nil {
			return errPersistence
		}
		return nil
	}
	if !run.Submitted {
		if err := c.store.MarkCoreRunAttempted(ctx, in.RunID); err != nil {
			return errPersistence
		}
		if err := json.Unmarshal(run.Input, &input); err != nil {
			return err
		}
		input.IdempotencyKey = openai.String("parsar-input-" + in.RunID)
		if err := c.sessions.Events.New(ctx, binding.SessionID, input); err != nil {
			if inputRejected(err) {
				if settleErr := c.store.SettleCoreRun(ctx, in.RunID); settleErr != nil {
					return errPersistence
				}
			}
			return err
		}
		if err := c.store.MarkCoreRunSubmitted(ctx, in.RunID); err != nil {
			return errPersistence
		}
	}
	return c.observe(ctx, in, run, ch)
}

func (c *Connector) Abort(ctx context.Context, in connector.AbortInput) error {
	_, err := c.store.CancelAgentRun(ctx, in.RunID, "user_requested_cancel")
	return err
}

func (c *Connector) submitCancel(ctx context.Context, in connector.AbortInput) error {
	run, err := c.store.GetCoreRun(ctx, in.RunID)
	if err != nil {
		return errPersistence
	}
	if run.SessionID == "" || run.Settled {
		return nil
	}
	return c.sessions.Events.New(ctx, run.SessionID, openai.BetaAgentSessionEventNewParams{
		IdempotencyKey: openai.String("parsar-cancel-" + in.RunID),
		Events:         []openai.AgentSessionInputParamUnion{{OfParamAgentSessionInputCancel: &openai.AgentSessionInputParamAgentSessionInputCancel{}}},
	})
}

func (*Connector) Cancel(context.Context, string) error { return connector.ErrNotSupported }
func (*Connector) Close(context.Context, string) error  { return connector.ErrNotSupported }
func (*Connector) SubmitPermission(context.Context, connector.PermissionDecision) error {
	return connector.ErrNotSupported
}
func (*Connector) SubmitPromptForUserChoice(context.Context, connector.PromptForUserChoiceDecision) error {
	return connector.ErrNotSupported
}

func terminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}

var errPersistence = connector.ErrObservationInterrupted

func emit(ctx context.Context, ch chan<- connector.PromptEvent, event connector.PromptEvent) error {
	event.Persisted = make(chan error, 1)
	select {
	case ch <- event:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-event.Persisted:
		if err != nil {
			return errPersistence
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func safeError(err error) string {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == 409 && inputRejected(err) {
			return "Core input rejected: " + apiErr.Code
		}
		return fmt.Sprintf("Core request failed (HTTP %d)", apiErr.StatusCode)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Core request timed out; its recorded execution can be recovered"
	}
	if strings.Contains(err.Error(), "Core") {
		return err.Error()
	}
	return "Core execution is unavailable"
}

func retryable(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if inputRejected(err) {
			return false
		}
		return (apiErr.StatusCode >= 500 && apiErr.StatusCode != 501) || apiErr.StatusCode == 429 || apiErr.StatusCode == 408 || apiErr.StatusCode == 409
	}
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded)
}

// inputRejected distinguishes final admission rejection from an uncertain receipt.
// Other conflicts retain their lane fence: they do not prove absence of execution.
func inputRejected(err error) bool {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case 400, 422, 501:
		return true
	case 409:
		switch apiErr.Code {
		case "environment_unavailable", "environment_input_expired", "environment_input_cancelled":
			return true
		}
	}
	return false
}
