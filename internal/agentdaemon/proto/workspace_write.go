package proto

import (
	"encoding/hex"
	"io/fs"
	"strings"

	"github.com/google/uuid"
)

const (
	TypeWorkspaceWrite          = "workspace_write"
	TypeWorkspaceWriteResult    = "workspace_write_result"
	WorkspaceWriteMaxBytes      = 50 << 20
	WorkspaceWriteChunkBytes    = 64 << 10
	WorkspaceWriteMaxFrameBytes = 96 << 10
)

// WorkspaceWritePayload transfers one complete body over the existing daemon
// connection. Envelope.ID is the durable Core mutation ID, never a retry key.
type WorkspaceWritePayload struct {
	Step          string `json:"step"`
	EnvironmentID string `json:"environment_id,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	Path          string `json:"path,omitempty"`
	SizeBytes     int    `json:"size_bytes,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	Offset        int    `json:"offset,omitempty"`
	Data          []byte `json:"data,omitempty"`
}

type WorkspaceWriteResultPayload struct {
	Outcome   string `json:"outcome"`
	Offset    int    `json:"offset,omitempty"`
	SizeBytes int    `json:"size_bytes,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

func ValidWorkspaceWriteRequest(p WorkspaceWritePayload) bool {
	if p.Step == "begin" {
		for _, id := range []string{p.EnvironmentID, p.SessionID} {
			v, err := uuid.Parse(id)
			if err != nil || v == uuid.Nil || v.String() != id {
				return false
			}
		}
		digest, err := hex.DecodeString(p.SHA256)
		return err == nil && len(digest) == 32 && strings.ToLower(p.SHA256) == p.SHA256 &&
			p.SizeBytes >= 0 && p.SizeBytes <= WorkspaceWriteMaxBytes && p.Offset == 0 && len(p.Data) == 0 &&
			len(p.Path) <= 4096 && p.Path != "." && fs.ValidPath(p.Path) && !strings.ContainsAny(p.Path, "\\\x00\r\n")
	}
	if p.EnvironmentID != "" || p.SessionID != "" || p.Path != "" || p.SizeBytes != 0 || p.SHA256 != "" {
		return false
	}
	switch p.Step {
	case "chunk":
		return p.Offset >= 0 && p.Offset <= WorkspaceWriteMaxBytes && len(p.Data) > 0 && len(p.Data) <= WorkspaceWriteChunkBytes
	case "commit":
		return p.Offset == 0 && len(p.Data) == 0
	default:
		return false
	}
}
