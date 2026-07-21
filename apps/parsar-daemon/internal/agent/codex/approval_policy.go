package codex

import (
	"encoding/json"
	"fmt"
)

// SilentGranularPolicy disables every Codex approval surface. It is retained
// for wire-compatibility tests and explicit callers; daemon sessions default
// to HumanApprovalPolicy so normal runs never auto-accept requests.
func SilentGranularPolicy() AskForApproval {
	g := GranularAskForApproval{} // zero value = every gate false
	return AskForApproval{Granular: &g}
}

// HumanApprovalPolicy surfaces every Codex approval gate to Parsar. The
// daemon turns those server requests into the same permission envelopes used
// by Claude Code, so Web and IM users make the decision explicitly.
func HumanApprovalPolicy() AskForApproval {
	g := GranularAskForApproval{
		SandboxApproval: true, Rules: true, SkillApproval: true,
		RequestPermissions: true, MCPElicitations: true,
	}
	return AskForApproval{Granular: &g}
}

// IsSilent reports whether p suppresses every approval surface. The
// daemon logs this distinction when it starts a thread. Server-request
// handlers are always registered so explicit policy overrides cannot leave
// Codex waiting on an unhandled request.
//
// A nil pointer is treated as silent — the safe default when callers
// forget to wire a policy.
func IsSilent(p *AskForApproval) bool {
	if p == nil {
		return true
	}
	if p.Granular != nil {
		g := p.Granular
		return !g.SandboxApproval && !g.Rules && !g.SkillApproval &&
			!g.RequestPermissions && !g.MCPElicitations
	}
	// Bare string variants ("never" is silent; everything else surfaces).
	return p.String == "never"
}

// MarshalJSON emits codex-rs's discriminated union — either a bare
// string or the granular object. Producing both would deserialise to
// the granular branch on the codex side (it wins precedence in
// codex-rs/protocol.rs AskForApproval), but the wire would be wrong; we
// validate exclusivity here.
func (a AskForApproval) MarshalJSON() ([]byte, error) {
	hasString := a.String != ""
	hasGranular := a.Granular != nil
	if hasString && hasGranular {
		return nil, fmt.Errorf("codex: AskForApproval must set exactly one of String or Granular, got both")
	}
	if hasString {
		return json.Marshal(a.String)
	}
	if hasGranular {
		return json.Marshal(struct {
			Granular *GranularAskForApproval `json:"granular"`
		}{Granular: a.Granular})
	}
	// Neither set — preserve the historical zero-value encoding. Production
	// plans always set HumanApprovalPolicy explicitly; an empty marshal would
	// produce `null`, which codex-rs rejects.
	return json.Marshal(struct {
		Granular GranularAskForApproval `json:"granular"`
	}{})
}

// UnmarshalJSON is the inverse: codex's ThreadStartResult.approvalPolicy
// echoes back as either a string or {"granular":...}. Provided so tests
// can round-trip without surprises; production code only marshals.
func (a *AskForApproval) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		a.String = s
		a.Granular = nil
		return nil
	}
	var obj struct {
		Granular *GranularAskForApproval `json:"granular"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("codex: AskForApproval: %w", err)
	}
	a.Granular = obj.Granular
	a.String = ""
	return nil
}
