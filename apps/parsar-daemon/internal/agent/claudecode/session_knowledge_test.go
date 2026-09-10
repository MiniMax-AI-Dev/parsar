package claudecode

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestStartupLogsDoNotContainReferenceDocuments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var output bytes.Buffer
	const privateDocument = "PRIVATE-REFERENCE-9481"
	_, err := newSession(t.Context(), proto.PromptRequestPayload{
		RunID: "knowledge-log-check", WorkDir: t.TempDir(), Prompt: "Answer from the reference.",
		AgentOptions: map[string]any{"system_prompt": privateDocument},
	}, make(chan proto.Envelope, 8), sessionConfig{
		claudeBinary: filepath.Join(t.TempDir(), "missing-claude"),
		logger:       slog.New(slog.NewTextHandler(&output, nil)),
	})
	if err == nil || !strings.Contains(output.String(), "starting subprocess") {
		t.Fatalf("did not exercise subprocess startup: %v", err)
	}
	if strings.Contains(output.String(), privateDocument) {
		t.Fatal("reference documents leaked into startup logs")
	}
}
