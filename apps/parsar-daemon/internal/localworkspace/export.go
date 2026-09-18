package localworkspace

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (b *Binding) CanExport() bool { return b != nil && b.exportHelper != "" }

// ExportOutputs streams only from the deployment-owned root; successful exit is mandatory.
func (b *Binding) ExportOutputs(ctx context.Context, output io.Writer) error {
	if !b.CanExport() || output == nil {
		return errors.New("workspace export unavailable")
	}
	cmd := exec.CommandContext(ctx, b.exportHelper, b.workspace)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	cmd.Stdout = &exportWriter{output: output}
	cmd.WaitDelay = time.Second
	return cmd.Run()
}

type exportWriter struct {
	output io.Writer
	size   int64
}

func (w *exportWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > proto.WorkspaceExportMaxBytes-w.size {
		return 0, errors.New("workspace export exceeds bound")
	}
	n, err := w.output.Write(data)
	w.size += int64(n)
	return n, err
}
