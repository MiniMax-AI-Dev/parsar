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
// rendering. Empty OwnerNames omits the "Admins" line. Empty JoinURL
// (private workspace) falls back to "Contact one of the admins above to join"; callers
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
			ReplyHint: "Agent misconfigured (unknown visibility); please contact an admin.",
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
		lines = append(lines, "This Agent is only available to members of its workspace.")
	} else {
		lines = append(lines, fmt.Sprintf("This Agent is only available to members of the %s workspace.", name))
	}

	if line := ownerLine(ws.OwnerNames); line != "" {
		lines = append(lines, line)
	}

	switch {
	case strings.TrimSpace(ws.JoinURL) != "":
		lines = append(lines, fmt.Sprintf("You can [Request to join workspace](%s).", strings.TrimSpace(ws.JoinURL)))
	case len(ws.OwnerNames) > 0:
		lines = append(lines, "Contact one of the admins above to join.")
	default:
		lines = append(lines, "Contact an admin to request access.")
	}
	return strings.Join(lines, "\n")
}

// ownerLine formats "Admins: A, B and N others". Returns "" on empty input.
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
		return "Admins: " + strings.Join(cleaned, ", ")
	}
	return fmt.Sprintf("Admins: %s and %d others", strings.Join(cleaned[:2], ", "), len(cleaned))
}

func registerHint(registerURL string) string {
	if registerURL == "" {
		return "You haven't linked an account yet. Please ask an admin for the Parsar web URL and sign in via \"Feishu login\" before using the bot."
	}
	return fmt.Sprintf("You haven't linked an account yet. Please go to the Parsar web UI (%s) and sign in via \"Feishu login\" before using the bot.", registerURL)
}

func guestRegisterHint(registerURL string) string {
	if registerURL == "" {
		return "You haven't linked an account yet. Please ask an admin for the Parsar web URL and sign in via \"Feishu login\" before using the bot."
	}
	return fmt.Sprintf("You haven't linked an account yet. Please go to the Parsar web UI (%s) and sign in via \"Feishu login\" before using the bot.", registerURL)
}
