package gateway

import (
	"strings"
	"testing"
)

// TestVisibilityGate_AllNineCases is the canonical 9-case matrix the
// design doc (docs/feishu-routing.md §4.1) demands: 3 visibility tiers
// × 3 sender states (workspace member, registered non-member, unregistered).
//
// Add new cases by name; never re-purpose an existing case slot.
func TestVisibilityGate_AllNineCases(t *testing.T) {
	t.Parallel()

	const (
		wsName     = "Engineering"
		registerAt = "https://parsar.example/register"
	)
	ws := WorkspaceInfo{Name: wsName}
	cfg := GateConfig{RegisterURL: registerAt}

	cases := []struct {
		name       string
		visibility Visibility
		sender     SenderState

		wantAllowed      bool
		wantReason       string
		wantReplyHintHas string // substring; empty means "must be empty"
		wantGuestHintHas string // substring; empty means "must be empty"
	}{
		// visibility=workspace
		{
			name:             "workspace_member_passes",
			visibility:       VisibilityWorkspace,
			sender:           SenderState{Registered: true, WorkspaceMember: true},
			wantAllowed:      true,
			wantReason:       ReasonAllowedWorkspaceMember,
			wantReplyHintHas: "",
		},
		{
			name:             "workspace_registered_nonmember_rejected",
			visibility:       VisibilityWorkspace,
			sender:           SenderState{Registered: true, WorkspaceMember: false},
			wantAllowed:      false,
			wantReason:       ReasonDeniedNotWorkspace,
			wantReplyHintHas: wsName,
		},
		{
			name:             "workspace_unregistered_rejected",
			visibility:       VisibilityWorkspace,
			sender:           SenderState{Registered: false, WorkspaceMember: false},
			wantAllowed:      false,
			wantReason:       ReasonDeniedNotWorkspace,
			wantReplyHintHas: wsName,
		},

		// visibility=tenant
		{
			name:             "tenant_workspace_member_passes",
			visibility:       VisibilityTenant,
			sender:           SenderState{Registered: true, WorkspaceMember: true},
			wantAllowed:      true,
			wantReason:       ReasonAllowedWorkspaceMember,
			wantReplyHintHas: "",
		},
		{
			name:             "tenant_registered_nonmember_passes",
			visibility:       VisibilityTenant,
			sender:           SenderState{Registered: true, WorkspaceMember: false},
			wantAllowed:      true,
			wantReason:       ReasonAllowedTenantUser,
			wantReplyHintHas: "",
		},
		{
			name:             "tenant_unregistered_rejected_with_register_hint",
			visibility:       VisibilityTenant,
			sender:           SenderState{Registered: false, WorkspaceMember: false},
			wantAllowed:      false,
			wantReason:       ReasonDeniedNotRegistered,
			wantReplyHintHas: registerAt,
		},

		// visibility=public
		{
			name:             "public_workspace_member_passes",
			visibility:       VisibilityPublic,
			sender:           SenderState{Registered: true, WorkspaceMember: true},
			wantAllowed:      true,
			wantReason:       ReasonAllowedPublicUser,
			wantReplyHintHas: "",
		},
		{
			name:             "public_registered_nonmember_passes",
			visibility:       VisibilityPublic,
			sender:           SenderState{Registered: true, WorkspaceMember: false},
			wantAllowed:      true,
			wantReason:       ReasonAllowedPublicUser,
			wantReplyHintHas: "",
		},
		{
			name:             "public_unregistered_passes_with_guest_hint",
			visibility:       VisibilityPublic,
			sender:           SenderState{Registered: false, WorkspaceMember: false},
			wantAllowed:      true,
			wantReason:       ReasonAllowedPublicGuest,
			wantReplyHintHas: "",
			wantGuestHintHas: registerAt,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := VisibilityGate(tc.visibility, tc.sender, ws, cfg)
			if got.Allowed != tc.wantAllowed {
				t.Fatalf("Allowed = %v, want %v (decision=%+v)", got.Allowed, tc.wantAllowed, got)
			}
			if got.Reason != tc.wantReason {
				t.Fatalf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if tc.wantReplyHintHas == "" {
				if got.ReplyHint != "" && tc.wantAllowed {
					t.Fatalf("ReplyHint = %q, want empty when allowed", got.ReplyHint)
				}
			} else if !strings.Contains(got.ReplyHint, tc.wantReplyHintHas) {
				t.Fatalf("ReplyHint = %q, want substring %q", got.ReplyHint, tc.wantReplyHintHas)
			}
			if tc.wantGuestHintHas == "" {
				if got.GuestReplyHint != "" {
					t.Fatalf("GuestReplyHint = %q, want empty", got.GuestReplyHint)
				}
			} else if !strings.Contains(got.GuestReplyHint, tc.wantGuestHintHas) {
				t.Fatalf("GuestReplyHint = %q, want substring %q", got.GuestReplyHint, tc.wantGuestHintHas)
			}
		})
	}
}

// TestVisibilityGate_InvalidVisibilityRejects guards against schema drift
// or default-value typos. The check constraint at the DB layer already
// rejects this, but defence in depth — if a corrupt row somehow slips
// through, the gate must deny by default.
func TestVisibilityGate_InvalidVisibilityRejects(t *testing.T) {
	t.Parallel()

	cases := []Visibility{"", "private", "PUBLIC", "everyone", "anon"}
	for _, v := range cases {
		v := v
		t.Run(string(v), func(t *testing.T) {
			t.Parallel()
			d := VisibilityGate(v, SenderState{Registered: true, WorkspaceMember: true}, WorkspaceInfo{}, GateConfig{})
			if d.Allowed {
				t.Fatalf("visibility=%q expected denied", v)
			}
			if d.Reason != ReasonInvalidVisibility {
				t.Fatalf("Reason = %q, want %q", d.Reason, ReasonInvalidVisibility)
			}
		})
	}
}

// TestVisibilityGate_EmptyURLFallbacks asserts that callers passing an
// empty RegisterURL still get a sensible non-empty bot reply. Avoids
// leaking literal "https://" or empty parentheses to the user.
func TestVisibilityGate_EmptyURLFallbacks(t *testing.T) {
	t.Parallel()

	// tenant + unregistered → register hint
	d := VisibilityGate(VisibilityTenant, SenderState{}, WorkspaceInfo{}, GateConfig{RegisterURL: ""})
	if d.Allowed {
		t.Fatal("expected tenant + unregistered to be denied")
	}
	if d.ReplyHint == "" {
		t.Fatal("expected non-empty fallback ReplyHint when RegisterURL is empty")
	}
	if strings.Contains(d.ReplyHint, "%s") || strings.Contains(d.ReplyHint, "%!") {
		t.Fatalf("ReplyHint must not leak unfilled format placeholder: %q", d.ReplyHint)
	}

	// public + unregistered → guest hint
	d = VisibilityGate(VisibilityPublic, SenderState{}, WorkspaceInfo{}, GateConfig{RegisterURL: ""})
	if !d.Allowed {
		t.Fatal("expected public + unregistered to be allowed")
	}
	if d.GuestReplyHint == "" {
		t.Fatal("expected non-empty fallback GuestReplyHint when RegisterURL is empty")
	}
	if strings.Contains(d.GuestReplyHint, "%s") || strings.Contains(d.GuestReplyHint, "%!") {
		t.Fatalf("GuestReplyHint must not leak unfilled format placeholder: %q", d.GuestReplyHint)
	}

	// workspace + non-member with empty workspace name → fallback message
	d = VisibilityGate(VisibilityWorkspace, SenderState{Registered: true}, WorkspaceInfo{Name: ""}, GateConfig{})
	if d.Allowed {
		t.Fatal("expected workspace + non-member to be denied")
	}
	if d.ReplyHint == "" {
		t.Fatal("expected non-empty fallback ReplyHint when workspace name is empty")
	}
}

// TestVisibilityIsValid covers the three accepted enums and a sample of
// rejections. Kept tiny — it's a guard against the constants accidentally
// being renamed without updating IsValid.
func TestVisibilityIsValid(t *testing.T) {
	t.Parallel()

	for _, v := range []Visibility{VisibilityWorkspace, VisibilityTenant, VisibilityPublic} {
		if !v.IsValid() {
			t.Errorf("expected %q to be valid", v)
		}
	}
	for _, v := range []Visibility{"", "Workspace", "tenant ", "private"} {
		if Visibility(v).IsValid() {
			t.Errorf("expected %q to be invalid", v)
		}
	}
}

// TestWorkspaceOnlyReply_RendersOwnersAndJoinURL pins the rejection-card
// text shape for visibility=workspace denials. The card is the only
// outward signal the Feishu sender has — small wording drifts (missing
// owner names, broken link, leftover "管理员: " with empty list) are not
// caught elsewhere, so we lock the matrix here.
func TestWorkspaceOnlyReply_RendersOwnersAndJoinURL(t *testing.T) {
	t.Parallel()

	const (
		wsName = "Engineering"
		url    = "https://parsar.example/join-workspace?id=ws-1&from=feishu"
	)

	cases := []struct {
		name       string
		ws         WorkspaceInfo
		mustHave   []string
		mustNotHas []string
	}{
		{
			name: "owners_and_join_url",
			ws: WorkspaceInfo{
				Name:       wsName,
				OwnerNames: []string{"张三"},
				JoinURL:    url,
			},
			mustHave: []string{
				wsName, "管理员: 张三", "[申请加入 workspace](" + url + ")",
			},
			mustNotHas: []string{"请联系", "等 ", " 人"},
		},
		{
			name: "many_owners_truncate_to_two_plus_count",
			ws: WorkspaceInfo{
				Name:       wsName,
				OwnerNames: []string{"张三", "李四", "王五", "赵六"},
				JoinURL:    url,
			},
			mustHave: []string{"管理员: 张三、李四 等 4 人", url},
			// must not list the truncated owners after the first 2
			mustNotHas: []string{"王五", "赵六"},
		},
		{
			name: "two_owners_no_count_suffix",
			ws: WorkspaceInfo{
				Name:       wsName,
				OwnerNames: []string{"张三", "李四"},
				JoinURL:    url,
			},
			mustHave:   []string{"管理员: 张三、李四", url},
			mustNotHas: []string{"等 ", " 人"},
		},
		{
			name: "no_join_url_with_owners_falls_back_to_contact_admin",
			ws: WorkspaceInfo{
				Name:       wsName,
				OwnerNames: []string{"张三"},
				JoinURL:    "",
			},
			mustHave:   []string{"管理员: 张三", "请联系上述管理员加入"},
			mustNotHas: []string{"[申请加入", "https://", "请联系管理员开通"},
		},
		{
			name: "no_join_url_no_owners_falls_back_to_contact_admin_open",
			ws: WorkspaceInfo{
				Name:       wsName,
				OwnerNames: nil,
				JoinURL:    "",
			},
			mustHave:   []string{wsName, "请联系管理员开通"},
			mustNotHas: []string{"管理员: ", "[申请加入"},
		},
		{
			name: "empty_owner_strings_dropped_silently",
			ws: WorkspaceInfo{
				Name:       wsName,
				OwnerNames: []string{"", "  ", ""},
				JoinURL:    url,
			},
			// all owner entries are whitespace → no "管理员:" line at all,
			// but the link still renders
			mustHave:   []string{wsName, url},
			mustNotHas: []string{"管理员:"},
		},
		{
			name: "empty_workspace_name_falls_back_to_generic_lead",
			ws: WorkspaceInfo{
				Name:       "",
				OwnerNames: []string{"张三"},
				JoinURL:    url,
			},
			mustHave: []string{"其所在 workspace 成员可用", "管理员: 张三", url},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := VisibilityGate(
				VisibilityWorkspace,
				SenderState{Registered: true, WorkspaceMember: false},
				tc.ws,
				GateConfig{},
			)
			if d.Allowed {
				t.Fatalf("expected denied, got %+v", d)
			}
			for _, sub := range tc.mustHave {
				if !strings.Contains(d.ReplyHint, sub) {
					t.Fatalf("ReplyHint missing %q\nfull:\n%s", sub, d.ReplyHint)
				}
			}
			for _, sub := range tc.mustNotHas {
				if strings.Contains(d.ReplyHint, sub) {
					t.Fatalf("ReplyHint should NOT contain %q\nfull:\n%s", sub, d.ReplyHint)
				}
			}
		})
	}
}
