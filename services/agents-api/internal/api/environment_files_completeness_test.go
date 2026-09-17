package api

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestEnvironmentFilesRejectsIncompleteOrMalformedDirectories(t *testing.T) {
	for name, result := range map[string]proto.WorkspaceDirectoryResult{
		"truncated":       {Entries: []proto.WorkspaceDirectoryEntry{environmentFileEntry("secret", 1)}, Truncated: true},
		"missing entries": {},
		"invalid name":    {Entries: []proto.WorkspaceDirectoryEntry{environmentFileEntry("../secret", 1)}},
		"duplicate":       {Entries: []proto.WorkspaceDirectoryEntry{environmentFileEntry("secret", 1), environmentFileEntry("secret", 1)}},
		"missing size":    {Entries: []proto.WorkspaceDirectoryEntry{{Name: "secret", Kind: "file"}}},
		"negative size":   {Entries: []proto.WorkspaceDirectoryEntry{environmentFileEntry("secret", -1)}},
		"unknown kind":    {Entries: []proto.WorkspaceDirectoryEntry{{Name: "secret", Kind: "unknown"}}},
		"oversized":       {Entries: make([]proto.WorkspaceDirectoryEntry, proto.WorkspaceDirectoryMaxEntries+1)},
	} {
		t.Run(name, func(t *testing.T) {
			h, f := environmentFilesHandler(t, true)
			f.result = result
			w := requestEnvironmentFiles(h, f.environment.ID, "?limit=1", "files-key")
			if w.Code != 503 || strings.Contains(w.Body.String(), `"data"`) || strings.Contains(w.Body.String(), `"next"`) || strings.Contains(w.Body.String(), "secret") {
				t.Fatal("unsafe incomplete response", w.Code, w.Body)
			}
		})
	}
}

func TestEnvironmentFilesCursorRejectsChangesAndAcceptsNativeReordering(t *testing.T) {
	for _, change := range []string{"limit", "order", "path", "environment", "size", "name", "removed", "added", "native order"} {
		t.Run(change, func(t *testing.T) {
			h, f := environmentFilesHandler(t, true)
			f.result.Entries = []proto.WorkspaceDirectoryEntry{environmentFileEntry("A", 1), environmentFileEntry("B", 2)}
			page := decodeEnvironmentFiles(t, requestEnvironmentFiles(h, f.environment.ID, "?limit=1", "files-key"))
			q := url.Values{"limit": {"1"}, "page": {*page.Next}}
			switch change {
			case "limit":
				q.Set("limit", "2")
			case "order":
				q.Set("order", "asc")
			case "path":
				q.Set("path", "/private/workspace/sub")
			case "environment":
				f.environment.ID = "other-authorized-environment"
			case "size":
				f.result.Entries[0] = environmentFileEntry("A", 3)
			case "name":
				f.result.Entries[0] = environmentFileEntry("C", 1)
			case "removed":
				f.result.Entries = f.result.Entries[:1]
			case "added":
				f.result.Entries = append(f.result.Entries, environmentFileEntry("C", 3))
			case "native order":
				f.result.Entries[0], f.result.Entries[1] = f.result.Entries[1], f.result.Entries[0]
			}
			w := requestEnvironmentFiles(h, f.environment.ID, "?"+q.Encode(), "files-key")
			if change == "native order" {
				decodeEnvironmentFiles(t, w)
			} else if w.Code != 400 || strings.Contains(w.Body.String(), `"data"`) {
				t.Fatal("cursor accepted changed listing", w.Code, w.Body)
			}
		})
	}
}

func TestEnvironmentFilesMalformedCursorBounds(t *testing.T) {
	for _, change := range []string{"version", "binding", "fingerprint", "negative", "overflow", "unknown", "trailing", "non-page offset"} {
		t.Run(change, func(t *testing.T) {
			h, f := environmentFilesHandler(t, true)
			f.result.Entries = []proto.WorkspaceDirectoryEntry{environmentFileEntry("A", 1), environmentFileEntry("B", 2), environmentFileEntry("C", 3)}
			page := decodeEnvironmentFiles(t, requestEnvironmentFiles(h, f.environment.ID, "?limit=2", "files-key"))
			raw, _ := base64.RawURLEncoding.DecodeString(*page.Next)
			var cursor map[string]any
			_ = json.Unmarshal(raw, &cursor)
			switch change {
			case "version":
				cursor["v"] = 2
			case "binding":
				cursor["b"] = "other"
			case "fingerprint":
				cursor["f"] = strings.Repeat("0", 64)
			case "negative":
				cursor["o"] = -1
			case "overflow":
				cursor["o"] = 1e30
			case "unknown":
				cursor["x"] = 1
			case "non-page offset":
				cursor["o"] = 1
			}
			raw, _ = json.Marshal(cursor)
			if change == "trailing" {
				raw = append(raw, []byte(` {}`)...)
			}
			q := url.Values{"limit": {"2"}, "page": {base64.RawURLEncoding.EncodeToString(raw)}}
			w := requestEnvironmentFiles(h, f.environment.ID, "?"+q.Encode(), "files-key")
			if w.Code != 400 {
				t.Fatal("malformed cursor accepted", w.Code, w.Body)
			}
		})
	}
}
