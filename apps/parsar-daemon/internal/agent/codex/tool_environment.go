package codex

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const toolEnvironmentHookSource = "/etc/codex/runtime-hooks"
const toolEnvironmentHookCommand = "/usr/bin/python3 -I -S /etc/codex/tool-env.py"

func verifyToolEnvironmentHook(ctx context.Context, rpc *JSONRPCClient, cwd string) error {
	operation, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := rpc.Request(operation, "hooks/list", map[string]any{"cwds": []string{cwd}})
	if err != nil {
		return errors.New("codex: initialized tool hook unavailable")
	}
	var response struct {
		Data []struct {
			CWD   string `json:"cwd"`
			Hooks []struct {
				EventName   string `json:"eventName"`
				Command     string `json:"command"`
				Matcher     string `json:"matcher"`
				Enabled     bool   `json:"enabled"`
				IsManaged   bool   `json:"isManaged"`
				Async       bool   `json:"async"`
				SourcePath  string `json:"sourcePath"`
				TrustStatus string `json:"trustStatus"`
			} `json:"hooks"`
			Errors []json.RawMessage `json:"errors"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Data) != 1 || response.Data[0].CWD != cwd || len(response.Data[0].Errors) != 0 {
		return errors.New("codex: initialized tool hook configuration unavailable")
	}
	for _, hook := range response.Data[0].Hooks {
		if hook.EventName == "preToolUse" && hook.Command == toolEnvironmentHookCommand && hook.Matcher == "^Bash$" && hook.Enabled && hook.IsManaged && !hook.Async && hook.SourcePath == toolEnvironmentHookSource && hook.TrustStatus == "managed" {
			return nil
		}
	}
	return errors.New("codex: required initialized tool hook missing")
}

func (s *Session) onToolEnvironmentHook(raw json.RawMessage) {
	if !s.toolEnvironment {
		return
	}
	var notification struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Run      struct {
			SourcePath string `json:"sourcePath"`
			Status     string `json:"status"`
		} `json:"run"`
	}
	if json.Unmarshal(raw, &notification) != nil || notification.Run.SourcePath != toolEnvironmentHookSource || !s.isRootTurn(notification.ThreadID, notification.TurnID) {
		return
	}
	if notification.Run.Status == "failed" {
		// Native hook process failures can run the original command before this
		// notification. Stop subsequent work and report failure, never success.
		s.emitTerminal("codex: initialized tool configuration failed; prior command effects may exist", true)
		s.cancelFn()
	}
}
