package mcode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type Session struct {
	ctx            context.Context
	req            proto.PromptRequestPayload
	opts           launchOptions
	process        *clirunner.Process
	out            chan<- proto.Envelope
	frames         chan rpcFrame
	exited         chan struct{}
	finished       chan struct{}
	writeMu        sync.Mutex
	mu             sync.Mutex
	sessionID      string
	permissions    map[string]pendingPermission
	questions      map[string]pendingQuestion
	exitErr        error
	nextID         int
	sequence       uint64
	active         bool
	content        strings.Builder
	tools          map[string]toolUpdate
	completedTools map[string]bool
}

var _ agent.Session = (*Session)(nil)

func Factory(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
	return newSession(ctx, req, out, defaultBinary())
}

func newSession(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope, binary string) (*Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if out == nil {
		return nil, fmt.Errorf("mcode: output channel is required")
	}
	opts, err := prepareOptions(ctx, req)
	if err != nil {
		return nil, err
	}
	process, err := clirunner.Start(clirunner.StartOptions{Parent: ctx, Binary: binary, Args: []string{"acp"}, Dir: opts.Dir, Env: opts.Env, NeedStdin: true})
	if err != nil {
		return nil, err
	}
	s := &Session{ctx: ctx, req: req, opts: opts, process: process, out: out, frames: make(chan rpcFrame, 32), exited: make(chan struct{}), finished: make(chan struct{}), permissions: map[string]pendingPermission{}, questions: map[string]pendingQuestion{}, tools: map[string]toolUpdate{}, completedTools: map[string]bool{}}
	go func() { _, _ = io.Copy(io.Discard, process.Stderr) }()
	go s.read()
	go s.run()
	return s, nil
}

func (s *Session) read() {
	defer close(s.exited)
	defer close(s.frames)
	scanner := bufio.NewScanner(s.process.Stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var readErr error
	for scanner.Scan() {
		var frame rpcFrame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			readErr = fmt.Errorf("mcode: malformed ACP response")
			s.process.Cancel()
			break
		}
		select {
		case s.frames <- frame:
		case <-s.finished:
		case <-s.process.Context().Done():
		}
	}
	if scanner.Err() != nil {
		readErr = fmt.Errorf("mcode: ACP stream read failed")
		s.process.Cancel()
	}
	waitErr := s.process.Wait()
	if readErr != nil {
		s.exitErr = readErr
	} else {
		s.exitErr = waitErr
	}
}

func (s *Session) run() {
	defer close(s.out)
	defer close(s.finished)
	err := s.execute()
	if err != nil {
		s.process.Cancel()
		s.emit(proto.TypeError, proto.ErrorPayload{Error: err.Error()})
	}
	s.mu.Lock()
	sessionID := s.sessionID
	s.permissions = map[string]pendingPermission{}
	s.questions = map[string]pendingQuestion{}
	s.mu.Unlock()
	metadata := map[string]any{proto.DoneMetaAgentSessionType: "mcode"}
	if sessionID != "" {
		metadata[proto.DoneMetaAgentSessionID] = sessionID
	}
	// ACP context usage is not per-turn token usage; do not record it as spend.
	s.emit(proto.TypeDone, proto.DonePayload{Content: s.content.String(), Metadata: metadata})
}

func (s *Session) execute() error {
	var initialized struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if err := s.call("initialize", map[string]any{"protocolVersion": 1, "clientInfo": map[string]string{"name": "parsar", "version": "1"}, "clientCapabilities": map[string]any{"elicitation": map[string]any{"form": map[string]any{}}}}, &initialized, false); err != nil {
		return err
	}
	if initialized.ProtocolVersion != 1 {
		return fmt.Errorf("mcode: unsupported ACP protocol version %d", initialized.ProtocolVersion)
	}
	params := map[string]any{"cwd": s.opts.Dir, "mcpServers": s.opts.MCP}
	method := "session/new"
	if s.req.AgentSessionID != "" {
		method = "session/load"
		params["sessionId"] = s.req.AgentSessionID
	}
	var session sessionResult
	if err := s.call(method, params, &session, false); err != nil {
		return err
	}
	if s.req.AgentSessionID != "" {
		session.SessionID = s.req.AgentSessionID
	}
	if session.SessionID == "" {
		return fmt.Errorf("mcode: ACP returned an empty session id")
	}
	s.mu.Lock()
	s.sessionID = session.SessionID
	s.mu.Unlock()
	model, err := advertisedModel(session.ConfigOptions, s.opts.Model)
	if err != nil && s.req.AgentSessionID != "" && !slices.ContainsFunc(session.ConfigOptions, func(option configOption) bool { return option.ID == "model" }) {
		// Native load omits the selector when its persisted model was removed; selection still validates against the current catalog.
		model = "m:custom_provider%3Aparsar:" + strings.ReplaceAll(url.QueryEscape(s.opts.Model), "+", "%20") + ":v:"
		err = nil
	}
	if err != nil {
		return err
	}
	for _, config := range []struct{ key, value string }{{"model", model}, {"permissionMode", s.opts.Mode}} {
		if err := s.call("session/set_config_option", map[string]any{"sessionId": session.SessionID, "configId": config.key, "value": config.value}, nil, false); err != nil {
			return err
		}
	}
	s.active = true
	var result struct {
		StopReason string `json:"stopReason"`
	}
	err = s.call("session/prompt", map[string]any{"sessionId": session.SessionID, "prompt": []map[string]string{{"type": "text", "text": s.req.Prompt}}}, &result, true)
	s.active = false
	if err != nil {
		return err
	}
	if result.StopReason != "end_turn" {
		return fmt.Errorf("mcode: prompt stopped (%s)", result.StopReason)
	}
	return nil
}

// Select the advertised custom model, rather than using mcode's native default.
func advertisedModel(options []configOption, model string) (string, error) {
	for _, option := range options {
		if option.ID != "model" {
			continue
		}
		for _, candidate := range option.Options {
			parts := strings.Split(candidate.Value, ":")
			if len(parts) < 4 || parts[0] != "m" {
				continue
			}
			provider, err := decodeComponent(parts[1])
			if err != nil {
				continue
			}
			id, err := decodeComponent(parts[2])
			if err != nil {
				continue
			}
			if provider == "custom_provider:parsar" && id == model && (parts[3] == "u" || (len(parts) == 5 && parts[3] == "v" && parts[4] == "")) {
				return candidate.Value, nil
			}
		}
	}
	return "", fmt.Errorf("mcode: configured model is not advertised by the CLI")
}

func (s *Session) call(method string, params any, result any, prompt bool) error {
	s.nextID++
	id := json.RawMessage(strconv.Itoa(s.nextID))
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := s.write(rpcFrame{JSONRPC: "2.0", ID: id, Method: method, Params: raw}); err != nil {
		return err
	}
	ctx := s.process.Context()
	if !prompt {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("mcode: %s: %w", method, ctx.Err())
		case frame, ok := <-s.frames:
			if !ok {
				<-s.exited
				return fmt.Errorf("mcode: ACP process exited: %v", s.exitErr)
			}
			if frame.Method != "" {
				if err := s.handle(frame); err != nil {
					return err
				}
				continue
			}
			if !bytes.Equal(frame.ID, id) {
				continue
			}
			if frame.Error != nil {
				return fmt.Errorf("mcode: %s: %s", method, frame.Error.Message)
			}
			if result != nil {
				if err := json.Unmarshal(frame.Result, result); err != nil {
					return fmt.Errorf("mcode: invalid %s result", method)
				}
			}
			return nil
		}
	}
}

func (s *Session) write(frame rpcFrame) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return json.NewEncoder(s.process.Stdin).Encode(frame)
}

func (s *Session) emit(kind string, payload any) {
	env, err := proto.NewEnvelope(kind, s.req.RunID, payload)
	if err != nil {
		return
	}
	select {
	case s.out <- env:
	case <-s.ctx.Done():
	}
}

func (s *Session) Cancel(context.Context) error {
	s.process.Cancel()
	return nil
}

func (s *Session) SubmitPermission(_ context.Context, id string, decision proto.PermissionDecisionPayload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.permissions[id]
	if !ok {
		return agent.ErrUnknownPermission
	}
	choice := pending.Deny
	if decision.Approved {
		choice = pending.Allow
	}
	if choice == "" {
		return errors.New("mcode: ACP permission option is unavailable")
	}
	if len(decision.UpdatedInput) > 0 {
		return errors.New("mcode: edited permission input is not supported")
	}
	raw, _ := json.Marshal(map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": choice}})
	if err := s.write(rpcFrame{JSONRPC: "2.0", ID: pending.RPCID, Result: raw}); err != nil {
		return err
	}
	delete(s.permissions, id)
	return nil
}
