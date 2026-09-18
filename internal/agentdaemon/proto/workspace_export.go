package proto

const (
	TypeWorkspaceExport          = "workspace_export"
	TypeWorkspaceExportResult    = "workspace_export_result"
	WorkspaceExportChunkBytes    = 64 << 10
	WorkspaceExportMaxFrameBytes = 96 << 10
	// The archive allowance includes bounded headers and padding above 500 MiB of files.
	WorkspaceExportMaxBytes int64 = 528 << 20
)

type WorkspaceExportPayload struct {
	Step          string `json:"step"`
	Handle        string `json:"handle,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Offset        int64  `json:"offset,omitempty"`
}

type WorkspaceExportResultPayload struct {
	Outcome   string `json:"outcome"`
	Offset    int64  `json:"offset"`
	Data      []byte `json:"data,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

func ValidWorkspaceExportRequest(p WorkspaceExportPayload) bool {
	if p.Step == "begin" {
		return p.Offset == 0 && len(p.Handle) > 0 && len(p.Handle) <= 128 && len(p.EnvironmentID) > 0 && len(p.EnvironmentID) <= 128
	}
	return p.Handle == "" && p.EnvironmentID == "" && ((p.Step == "next" && p.Offset >= 0 && p.Offset <= WorkspaceExportMaxBytes) || (p.Step == "cancel" && p.Offset == 0))
}
