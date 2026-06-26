package auth

import (
	"strings"
	"sync/atomic"
)

// Platform admins bypass workspace/project membership checks and act
// with owner-level authority anywhere. The list is process-global and
// driven by PARSAR_PLATFORM_ADMIN_USER_IDS — see SetPlatformAdminIDs.
//
// Backed by atomic.Value so reads (every RBAC check) are lock-free and
// writes (process startup, tests) don't tear.
var platformAdminIDs atomic.Value

func init() {
	platformAdminIDs.Store(map[string]struct{}{})
}

// SetPlatformAdminIDs replaces the active allowlist. Empty input
// disables the bypass. Caller normalises entries (UUID strings); we
// only trim + lowercase for case-insensitive lookup.
func SetPlatformAdminIDs(ids []string) {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		set[id] = struct{}{}
	}
	platformAdminIDs.Store(set)
}

// IsPlatformAdmin reports whether the user is on the active allowlist.
// Empty userID always returns false.
func IsPlatformAdmin(userID string) bool {
	userID = strings.ToLower(strings.TrimSpace(userID))
	if userID == "" {
		return false
	}
	set, _ := platformAdminIDs.Load().(map[string]struct{})
	_, ok := set[userID]
	return ok
}

// PlatformAdminRole is the synthetic role returned to RBAC callers
// when a platform admin bypasses the membership check. Equivalent to
// workspace/project owner for any allowed-role list.
const PlatformAdminRole = "owner"
