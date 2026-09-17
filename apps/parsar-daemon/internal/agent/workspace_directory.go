package agent

import "context"

// WorkspaceDirectoryEntry describes an entry observed without following its final symlink.
type WorkspaceDirectoryEntry struct {
	Name      string
	Kind      string
	SizeBytes *int64
}

// WorkspaceDirectoryResult is a live, bounded observation, not a filesystem snapshot.
type WorkspaceDirectoryResult struct {
	Entries   []WorkspaceDirectoryEntry
	Truncated bool
}

// WorkspaceDirectoryLister reads one directory through an existing workspace owner.
// An empty directory selects the root; other paths contain only relative components.
// Entry names are single components. Kind is file, directory, symlink or other;
// SizeBytes is present and nonnegative only for regular files. Results have no
// prescribed order, and Truncated must not be presented as a complete inventory.
// maxEntries is positive; adapters may reject limits above their private bound.
// Successful return requires settled directory/metadata access and handle cleanup.
// Implementations retain workspace authorization and isolation and use the existing
// WorkspaceRead errors for unsupported, unavailable, busy, invalid or uncertain reads.
// This interface does not establish public Files pagination or feature admission.
type WorkspaceDirectoryLister interface {
	ListWorkspaceDirectory(context.Context, string, int) (WorkspaceDirectoryResult, error)
}
