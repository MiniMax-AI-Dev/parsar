package codex

import (
	"encoding/json"
	"testing"
)

func TestSilentGranularPolicy_AllFalse(t *testing.T) {
	p := SilentGranularPolicy()
	if p.Granular == nil {
		t.Fatal("SilentGranularPolicy must populate Granular")
	}
	g := *p.Granular
	if g.SandboxApproval || g.Rules || g.SkillApproval || g.RequestPermissions || g.MCPElicitations {
		t.Fatalf("silent policy must have every gate false, got %+v", g)
	}
	if !IsSilent(&p) {
		t.Fatal("IsSilent must return true for the silent default")
	}
}

func TestHumanApprovalPolicy_AllGatesEnabled(t *testing.T) {
	p := HumanApprovalPolicy()
	if p.Granular == nil {
		t.Fatal("HumanApprovalPolicy must populate Granular")
	}
	g := *p.Granular
	if !g.SandboxApproval || !g.Rules || !g.SkillApproval || !g.RequestPermissions || !g.MCPElicitations {
		t.Fatalf("human policy must enable every approval gate, got %+v", g)
	}
	if IsSilent(&p) {
		t.Fatal("HumanApprovalPolicy must surface app-server requests")
	}
}

func TestIsSilent_NilIsSilent(t *testing.T) {
	if !IsSilent(nil) {
		t.Fatal("nil policy must be treated as silent (safe default)")
	}
}

func TestIsSilent_StringPolicies(t *testing.T) {
	cases := []struct {
		name   string
		policy AskForApproval
		want   bool
	}{
		{"never silences", AskForApproval{String: "never"}, true},
		{"on-request loud", AskForApproval{String: "on-request"}, false},
		{"on-failure loud", AskForApproval{String: "on-failure"}, false},
		{"untrusted loud", AskForApproval{String: "untrusted"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSilent(&tc.policy); got != tc.want {
				t.Fatalf("IsSilent(%+v) = %v, want %v", tc.policy, got, tc.want)
			}
		})
	}
}

func TestIsSilent_PartialGranularIsLoud(t *testing.T) {
	// Flipping any single gate true must make IsSilent report false so
	// the daemon registers the loud server-request handler.
	for _, flip := range []func(*GranularAskForApproval){
		func(g *GranularAskForApproval) { g.SandboxApproval = true },
		func(g *GranularAskForApproval) { g.Rules = true },
		func(g *GranularAskForApproval) { g.SkillApproval = true },
		func(g *GranularAskForApproval) { g.RequestPermissions = true },
		func(g *GranularAskForApproval) { g.MCPElicitations = true },
	} {
		g := GranularAskForApproval{}
		flip(&g)
		p := AskForApproval{Granular: &g}
		if IsSilent(&p) {
			t.Fatalf("IsSilent must be false when any gate is true: %+v", g)
		}
	}
}

func TestAskForApproval_MarshalString(t *testing.T) {
	p := AskForApproval{String: "never"}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `"never"` {
		t.Fatalf("marshal string variant = %s", raw)
	}
}

func TestAskForApproval_MarshalGranular(t *testing.T) {
	g := GranularAskForApproval{RequestPermissions: true}
	p := AskForApproval{Granular: &g}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"granular":{"sandbox_approval":false,"rules":false,"skill_approval":false,"request_permissions":true,"mcp_elicitations":false}}`
	if string(raw) != want {
		t.Fatalf("marshal granular = %s\nwant = %s", raw, want)
	}
}

func TestAskForApproval_MarshalEmpty_DefaultsSilent(t *testing.T) {
	// Neither field set — must not produce `null` (codex rejects it).
	var p AskForApproval
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) == "null" {
		t.Fatalf("empty AskForApproval must not marshal to null: %s", raw)
	}
	if string(raw) != `{"granular":{"sandbox_approval":false,"rules":false,"skill_approval":false,"request_permissions":false,"mcp_elicitations":false}}` {
		t.Fatalf("empty AskForApproval marshal = %s", raw)
	}
}

func TestAskForApproval_MarshalBothErrors(t *testing.T) {
	g := GranularAskForApproval{}
	p := AskForApproval{String: "never", Granular: &g}
	if _, err := json.Marshal(p); err == nil {
		t.Fatal("setting both String and Granular must error on marshal")
	}
}

func TestAskForApproval_RoundTripString(t *testing.T) {
	var got AskForApproval
	if err := json.Unmarshal([]byte(`"on-request"`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.String != "on-request" || got.Granular != nil {
		t.Fatalf("round-trip = %+v", got)
	}
}

func TestAskForApproval_RoundTripGranular(t *testing.T) {
	src := `{"granular":{"sandbox_approval":true,"rules":false,"skill_approval":false,"request_permissions":false,"mcp_elicitations":false}}`
	var got AskForApproval
	if err := json.Unmarshal([]byte(src), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Granular == nil || !got.Granular.SandboxApproval || got.String != "" {
		t.Fatalf("round-trip = %+v", got)
	}
}
