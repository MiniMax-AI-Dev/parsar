package gateway

import (
	"fmt"
	"strings"
)

// Visibility encodes who can invoke an Agent — see docs/feishu-routing.md §3.
type Visibility string

const (
	VisibilityWorkspace Visibility = "workspace"
	VisibilityTenant    Visibility = "tenant"
	VisibilityPublic    Visibility = "public"
)

func (v Visibility) IsValid() bool {
	switch v {
	case VisibilityWorkspace, VisibilityTenant, VisibilityPublic:
		return true
	default:
		return false
	}
}

// SenderState captures Parsar-side knowledge about the inbound sender.
// WorkspaceMember implies Registered; the gate does not cross-check.
type SenderState struct {
	Registered      bool
	WorkspaceMember bool
}

// Decision is the outcome of a VisibilityGate evaluation.
// ReplyHint carries rejection text when Allowed=false. GuestReplyHint
// is a one-shot register suffix used only on VisibilityPublic when the
// sender is not Registered.
type Decision struct {
	Allowed        bool
	Reason         string
	ReplyHint      string
	GuestReplyHint string
}

const (
	ReasonAllowedWorkspaceMember = "allowed_workspace_member"
	ReasonAllowedTenantUser      = "allowed_tenant_user"
	ReasonAllowedPublicUser      = "allowed_public_user"
	ReasonAllowedPublicGuest     = "allowed_public_guest"
	ReasonDeniedNotWorkspace     = "denied_not_workspace_member"
	ReasonDeniedNotRegistered    = "denied_not_registered"
	ReasonInvalidVisibility      = "invalid_visibility"
)

// WorkspaceInfo carries human-readable context for rejection-text
// rendering. Empty OwnerNames omits the "管理员" line. Empty JoinURL
// (private workspace) falls back to "请联系上述管理员加入"; callers
// pre-compute OwnerNames / JoinURL only when they know the gate denies.
type WorkspaceInfo struct {
	Name       string
	OwnerNames []string
	JoinURL    string
}

// GateConfig carries runtime hints for rejection-text rendering.
// JoinURLBuilder nil signals "PublicURL not configured, don't render a link".
type GateConfig struct {
	RegisterURL    string
	JoinURLBuilder func(workspaceID string) string
}

// VisibilityGate is the central authorisation decision for inbound
// Feishu IM events. Spec: docs/feishu-routing.md §4.1. Pure: no DB, no IO.
func VisibilityGate(v Visibility, sender SenderState, ws WorkspaceInfo, cfg GateConfig) Decision {
	if !v.IsValid() {
		return Decision{
			Allowed:   false,
			Reason:    ReasonInvalidVisibility,
			ReplyHint: "Agent 配置异常 (visibility 未知)，请联系管理员。",
		}
	}

	switch v {
	case VisibilityWorkspace:
		if sender.WorkspaceMember {
			return Decision{Allowed: true, Reason: ReasonAllowedWorkspaceMember}
		}
		return Decision{
			Allowed:   false,
			Reason:    ReasonDeniedNotWorkspace,
			ReplyHint: workspaceOnlyReply(ws),
		}

	case VisibilityTenant:
		if sender.Registered {
			if sender.WorkspaceMember {
				return Decision{Allowed: true, Reason: ReasonAllowedWorkspaceMember}
			}
			return Decision{Allowed: true, Reason: ReasonAllowedTenantUser}
		}
		return Decision{
			Allowed:   false,
			Reason:    ReasonDeniedNotRegistered,
			ReplyHint: registerHint(cfg.RegisterURL),
		}

	case VisibilityPublic:
		if sender.Registered {
			return Decision{Allowed: true, Reason: ReasonAllowedPublicUser}
		}
		return Decision{
			Allowed:        true,
			Reason:         ReasonAllowedPublicGuest,
			GuestReplyHint: guestRegisterHint(cfg.RegisterURL),
		}
	}

	// Unreachable; the switch above covers all valid Visibility values.
	return Decision{Allowed: false, Reason: ReasonInvalidVisibility}
}

// workspaceOnlyReply renders the rejection text shown to a non-member
// of the Agent's workspace. Output is markdown — the inbound handler
// wraps it in a notice card body that is already rendered as markdown.
func workspaceOnlyReply(ws WorkspaceInfo) string {
	name := strings.TrimSpace(ws.Name)
	var lines []string
	if name == "" {
		lines = append(lines, "此 Agent 仅其所在 workspace 成员可用。")
	} else {
		lines = append(lines, fmt.Sprintf("此 Agent 仅 %s workspace 成员可用。", name))
	}

	if line := ownerLine(ws.OwnerNames); line != "" {
		lines = append(lines, line)
	}

	switch {
	case strings.TrimSpace(ws.JoinURL) != "":
		lines = append(lines, fmt.Sprintf("你可以 [申请加入 workspace](%s)。", strings.TrimSpace(ws.JoinURL)))
	case len(ws.OwnerNames) > 0:
		lines = append(lines, "请联系上述管理员加入。")
	default:
		lines = append(lines, "请联系管理员开通。")
	}
	return strings.Join(lines, "\n")
}

// ownerLine formats "管理员: A、B 等 N 人". Returns "" on empty input.
// For >2 names, lists first 2 + count; full list is one click away on web.
func ownerLine(names []string) string {
	cleaned := make([]string, 0, len(names))
	for _, n := range names {
		if t := strings.TrimSpace(n); t != "" {
			cleaned = append(cleaned, t)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	if len(cleaned) <= 2 {
		return "管理员: " + strings.Join(cleaned, "、")
	}
	return fmt.Sprintf("管理员: %s 等 %d 人", strings.Join(cleaned[:2], "、"), len(cleaned))
}

func registerHint(registerURL string) string {
	if registerURL == "" {
		return "您还未绑定账号，请联系管理员获取 Parsar 网页端地址，通过「飞书登录」完成绑定后再使用机器人。"
	}
	return fmt.Sprintf("您还未绑定账号，请前往 Parsar 网页端（%s），通过「飞书登录」完成绑定后再使用机器人。", registerURL)
}

func guestRegisterHint(registerURL string) string {
	if registerURL == "" {
		return "您还未绑定账号，请联系管理员获取 Parsar 网页端地址，通过「飞书登录」完成绑定后再使用机器人。"
	}
	return fmt.Sprintf("您还未绑定账号，请前往 Parsar 网页端（%s），通过「飞书登录」完成绑定后再使用机器人。", registerURL)
}
