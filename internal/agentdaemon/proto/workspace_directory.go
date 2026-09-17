package proto

import (
	"strings"
	"unicode/utf8"
)

const WorkspaceDirectoryMaxEntries = 1024

type WorkspaceDirectoryEntry struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SizeBytes *int64 `json:"size_bytes"`
}

type WorkspaceDirectoryResult struct {
	Entries   []WorkspaceDirectoryEntry `json:"entries"`
	Truncated bool                      `json:"truncated"`
}

func ValidWorkspaceReadRequest(request WorkspaceReadPayload) bool {
	if (request.Handle == "") == (request.RunID == "") || request.EnvironmentID == "" {
		return false
	}
	switch request.Operation {
	case "":
		return request.MaxBytes >= 1 && request.MaxBytes <= WorkspaceReadMaxBytes && request.MaxEntries == 0
	case "directory":
		return request.MaxBytes == 0 && request.MaxEntries >= 1 && request.MaxEntries <= WorkspaceDirectoryMaxEntries
	default:
		return false
	}
}

func ValidWorkspaceDirectory(result *WorkspaceDirectoryResult, limit int) bool {
	if result == nil || result.Entries == nil || limit < 1 || limit > WorkspaceDirectoryMaxEntries || len(result.Entries) > limit {
		return false
	}
	seen := make(map[string]bool, len(result.Entries))
	for _, entry := range result.Entries {
		if entry.Name == "" || entry.Name == "." || entry.Name == ".." || len(entry.Name) > 255 || !utf8.ValidString(entry.Name) || strings.ContainsAny(entry.Name, "/\\\x00\r\n") || seen[entry.Name] {
			return false
		}
		seen[entry.Name] = true
		switch entry.Kind {
		case "file":
			if entry.SizeBytes == nil || *entry.SizeBytes < 0 {
				return false
			}
		case "directory", "symlink", "other":
			if entry.SizeBytes != nil {
				return false
			}
		default:
			return false
		}
	}
	return true
}
