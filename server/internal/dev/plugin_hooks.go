package dev

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/pluginhook"
)

// PluginHookInvoker is the interface used by the run stream dispatcher
// to consult plugin hooks before forwarding permission requests.
type PluginHookInvoker interface {
	// InvokeBeforePermissionForward calls registered before_permission_forward
	// handlers with the permission request payload. Returns a decision.
	//
	// On timeout or error, implementations must return a fallback decision
	// (ask_human) rather than blocking the permission flow.
	InvokeBeforePermissionForward(ctx context.Context, payload pluginhook.PermissionPayload) (pluginhook.Decision, error)
}

// interceptPermissionWithHook checks whether a plugin hook can auto-decide
// a permission request. Returns (true, modifiedEvent) if the hook made a
// decisive deny/allow, meaning the caller should NOT persist the original
// permission.asked event but instead persist the auto-decided event.
//
// Returns (false, original) if no hook is configured, no decisive answer
// was given, or an error occurred — meaning the caller should proceed with
// normal approval flow.
func interceptPermissionWithHook(
	ctx context.Context,
	invoker PluginHookInvoker,
	ev connector.PromptEvent,
) (intercepted bool, replacement connector.PromptEvent) {
	if invoker == nil {
		return false, ev
	}
	if ev.Type != connector.EventPermissionRequest || ev.Permission == nil {
		return false, ev
	}

	perm := ev.Permission
	payload := pluginhook.PermissionPayload{
		RequestID: perm.ID,
		Tool:      perm.Tool,
		Title:     perm.Title,
		Detail:    perm.Detail,
		Payload:   perm.Payload,
	}
	// Extract args from payload if present (common for bash commands).
	if perm.Payload != nil {
		if args, ok := perm.Payload["command"].(string); ok {
			payload.Args = args
		}
	}

	decision, err := invoker.InvokeBeforePermissionForward(ctx, payload)
	if err != nil {
		log.Bg().Warn("plugin_hooks: before_permission_forward hook error, falling through to ask_human",
			"request_id", perm.ID, "tool", perm.Tool, "err", err)
		return false, ev
	}

	switch decision.Result {
	case "deny":
		log.Bg().Info("plugin_hooks: before_permission_forward hook auto-denied",
			"request_id", perm.ID, "tool", perm.Tool,
			"reason", decision.Reason, "plugin", decision.Plugin)
		return true, connector.PromptEvent{
			Type: connector.EventPermissionRequest,
			Permission: &connector.PermissionRequest{
				ID:       perm.ID,
				DeviceID: perm.DeviceID,
				Tool:     perm.Tool,
				Title:    perm.Title,
				Detail:   perm.Detail,
				Payload:  mergeHookDecisionPayload(perm.Payload, decision),
			},
			HookDecision: &connector.HookDecisionMeta{
				Result: decision.Result,
				Reason: decision.Reason,
				Plugin: decision.Plugin,
			},
		}

	case "allow":
		log.Bg().Info("plugin_hooks: before_permission_forward hook auto-allowed",
			"request_id", perm.ID, "tool", perm.Tool,
			"reason", decision.Reason, "plugin", decision.Plugin)
		return true, connector.PromptEvent{
			Type: connector.EventPermissionRequest,
			Permission: &connector.PermissionRequest{
				ID:       perm.ID,
				DeviceID: perm.DeviceID,
				Tool:     perm.Tool,
				Title:    perm.Title,
				Detail:   perm.Detail,
				Payload:  mergeHookDecisionPayload(perm.Payload, decision),
			},
			HookDecision: &connector.HookDecisionMeta{
				Result: decision.Result,
				Reason: decision.Reason,
				Plugin: decision.Plugin,
			},
		}

	default:
		// "ask_human", "no_handler", or anything else → normal flow.
		return false, ev
	}
}

// mergeHookDecisionPayload adds hook decision metadata into the permission
// payload so it can be persisted and displayed by the UI.
func mergeHookDecisionPayload(original map[string]any, decision pluginhook.Decision) map[string]any {
	merged := make(map[string]any, len(original)+3)
	for k, v := range original {
		merged[k] = v
	}
	merged["__hook_decision"] = decision.Result
	merged["__hook_reason"] = decision.Reason
	merged["__hook_plugin"] = decision.Plugin
	return merged
}

// autoSubmitHookDecision submits an auto-deny or auto-allow decision back
// to the connector so the daemon unblocks the agent without waiting for
// a human. This is fire-and-forget — errors are logged but do not block
// the event stream.
func autoSubmitHookDecision(ctx context.Context, target connector.AgentConnector, ev connector.PromptEvent) {
	if ev.Permission == nil || ev.HookDecision == nil {
		return
	}
	approved := ev.HookDecision.Result == "allow"
	note := ev.HookDecision.Reason
	if note == "" {
		if approved {
			note = "auto-allowed by plugin hook"
		} else {
			note = "auto-denied by plugin hook"
		}
	}

	decision := connector.PermissionDecision{
		RequestID: ev.Permission.ID,
		DeviceID:  ev.Permission.DeviceID,
		Approved:  approved,
		Note:      note,
		By:        "plugin:" + ev.HookDecision.Plugin,
	}

	submitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := target.SubmitPermission(submitCtx, decision); err != nil {
		log.Bg().Warn("plugin_hooks: auto-submit permission decision failed",
			"request_id", ev.Permission.ID,
			"decision", ev.HookDecision.Result,
			"plugin", ev.HookDecision.Plugin,
			"err", err)
	} else {
		log.Bg().Info("plugin_hooks: auto-submitted permission decision",
			"request_id", ev.Permission.ID,
			"decision", ev.HookDecision.Result,
			"plugin", ev.HookDecision.Plugin)
	}
}
