// Package feishu — outbound card rendering (PR #3a.1).
//
// RenderProgress / RenderTerminal turn the neutral channel.ProgressState /
// channel.TerminalResult the driver computes into a Feishu interactive-card
// payload. They are pure functions: they delegate to the existing
// gateway.Build*Card builders so output is byte-for-byte identical to the
// in-place feishuoutbound rendering path (buildMidRunCardContent /
// buildFinalCardForRun). The inflight driver switches to calling these in
// PR #3b; until then the legacy worker stays on the production path and these
// methods are exercised only by golden tests.
package feishu

import (
	"context"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// feishuCardMIME tags a rendered Feishu interactive-card payload. The driver
// treats Card.Payload opaquely; the MIME lets a future multi-platform send
// path assert it is handing the right card shape to the right channel.
const feishuCardMIME = "feishu/interactive"

// errCardFallbackMessage mirrors the driver's failed-run fallback copy
// (buildFinalCardForRun) so a TerminalResult with an empty ErrorMessage
// renders the same error card the legacy path produced.
const errCardFallbackMessage = "Agent run failed. Please retry later."

// RenderProgress renders the in-flight ("executing") card. Mirrors
// buildMidRunCardContent: BuildRunningCard(title, steps, streamingText,
// elapsed, now) → MarshalCard. now defaults to time.Now().UTC() when the
// caller leaves ProgressState.Now zero.
func (c *Channel) RenderProgress(_ context.Context, _ channel.ReplyTarget, state channel.ProgressState) (channel.Card, error) {
	now := state.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	content, err := gateway.MarshalCard(gateway.BuildRunningCard(
		state.Title,
		state.Steps,
		state.StreamingText,
		state.Elapsed,
		now,
	))
	if err != nil {
		return channel.Card{}, err
	}
	return channel.Card{MIME: feishuCardMIME, Payload: []byte(content)}, nil
}

// RenderTerminal renders the terminal Done / Error card. Success selects the
// DoneCard path (mirrors buildFinalCardForRun's success branch); otherwise it
// renders the error card with the same empty-message fallback the driver
// applies.
func (c *Channel) RenderTerminal(_ context.Context, _ channel.ReplyTarget, result channel.TerminalResult) (channel.Card, error) {
	var (
		content string
		err     error
	)
	if result.Success {
		content, err = gateway.MarshalCard(gateway.BuildDoneCard(
			result.Title,
			strings.TrimSpace(result.StreamingText),
			result.Steps,
			result.Thinking,
			result.Elapsed,
			result.Usage,
		))
	} else {
		msg := strings.TrimSpace(result.ErrorMessage)
		if msg == "" {
			msg = errCardFallbackMessage
		}
		content, err = gateway.BuildFeishuErrorCardContent(
			result.Title,
			msg,
			result.RawError,
			result.RunDetailURL,
			result.GuestHint,
		)
	}
	if err != nil {
		return channel.Card{}, err
	}
	return channel.Card{MIME: feishuCardMIME, Payload: []byte(content)}, nil
}

// RenderPermission renders the Allow/Deny card. It delegates to the existing
// gateway.BuildFeishuPermissionCardContent so the bytes are identical to the
// driver's maybeSendPermissionCard path. The Feishu hot path never calls this
// (it still builds the card inline); it exists for interface parity.
func (c *Channel) RenderPermission(_ context.Context, _ channel.ReplyTarget, req channel.PermissionRequest) (channel.Card, error) {
	content, err := gateway.BuildFeishuPermissionCardContent(
		req.Title,
		req.ToolName,
		req.ToolInput,
		req.RequestID,
	)
	if err != nil {
		return channel.Card{}, err
	}
	return channel.Card{MIME: feishuCardMIME, Payload: []byte(content)}, nil
}

// RenderChoiceForm renders the prompt_for_user_choice card. It maps the neutral
// channel.ChoiceQuestion list onto the gateway card-question shape and delegates
// to gateway.BuildFeishuPromptForUserChoiceCardContent, keeping byte-parity with
// the driver's maybeSendPromptForUserChoiceCard path.
func (c *Channel) RenderChoiceForm(_ context.Context, _ channel.ReplyTarget, form channel.ChoiceForm) (channel.Card, error) {
	questions := make([]gateway.PromptForUserChoiceCardQuestion, 0, len(form.Questions))
	for _, q := range form.Questions {
		opts := make([]gateway.PromptForUserChoiceCardOption, 0, len(q.Options))
		for _, label := range q.Options {
			opts = append(opts, gateway.PromptForUserChoiceCardOption{Label: label})
		}
		questions = append(questions, gateway.PromptForUserChoiceCardQuestion{
			Header:      q.Header,
			Question:    q.Question,
			MultiSelect: q.MultiSelect,
			Options:     opts,
		})
	}
	content, err := gateway.BuildFeishuPromptForUserChoiceCardContent(form.Title, questions, form.RequestID)
	if err != nil {
		return channel.Card{}, err
	}
	return channel.Card{MIME: feishuCardMIME, Payload: []byte(content)}, nil
}

// RenderCredentialForm renders the missing-credential input form. It delegates
// to gateway.BuildCredentialFormCard (which returns a card map) and marshals it,
// matching the driver's tryBuildCredentialFormCard path.
func (c *Channel) RenderCredentialForm(_ context.Context, _ channel.ReplyTarget, form channel.CredentialForm) (channel.Card, error) {
	content, err := gateway.MarshalCard(gateway.BuildCredentialFormCard(form.Title, form.Fields, form.Qkey))
	if err != nil {
		return channel.Card{}, err
	}
	return channel.Card{MIME: feishuCardMIME, Payload: []byte(content)}, nil
}
