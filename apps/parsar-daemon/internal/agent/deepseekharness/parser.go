package deepseekharness

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// usageProvider tags the done frame so downstream usage attribution can
// tell a dsh run apart even though the CLI reports no token counts.
const usageProvider = "deepseek-harness"

// translator collects the headless run's stdout. `dsh --profile headless`
// prints the final assistant text and nothing else — no event stream — so
// the whole answer is buffered and emitted as one delta before done.
type translator struct {
	runID string
	seq   atomic.Uint64

	answer strings.Builder
}

func newTranslator(runID string) *translator { return &translator{runID: runID} }

func (t *translator) appendLine(line string) {
	if t.answer.Len() > 0 {
		t.answer.WriteByte('\n')
	}
	t.answer.WriteString(line)
}

func (t *translator) terminalEnvelopes(waitErr error, stderr string, cancelled bool) []proto.Envelope {
	var envs []proto.Envelope
	content := strings.TrimSpace(t.answer.String())
	if content != "" {
		if env, err := proto.NewEnvelope(proto.TypeDelta, t.runID, proto.DeltaPayload{
			Delta:    content,
			Sequence: t.seq.Add(1),
		}); err == nil {
			envs = append(envs, env)
		}
	}
	if waitErr != nil || cancelled {
		if env, err := proto.NewEnvelope(proto.TypeError, t.runID, proto.ErrorPayload{
			Error: terminalErrorMessage(waitErr, stderr, cancelled),
		}); err == nil {
			envs = append(envs, env)
		}
	}
	usage := proto.Usage{Provider: usageProvider}
	if env, err := proto.NewEnvelope(proto.TypeDone, t.runID, proto.DonePayload{
		Content:    content,
		Transcript: content,
		Usage:      usage,
		Metadata:   map[string]any{"connector_path": "dsh_headless"},
	}); err == nil {
		envs = append(envs, env)
	}
	return envs
}

// terminalErrorMessage folds the exit status and stderr into one message.
// dsh exits non-zero for any turn that did not complete and writes the
// durable error code plus message to stderr, so stderr is the useful part.
func terminalErrorMessage(waitErr error, stderr string, cancelled bool) string {
	if cancelled {
		return "deepseek-harness: cancelled"
	}
	msg := "deepseek-harness: dsh exited without completing the turn"
	if waitErr != nil {
		msg = fmt.Sprintf("deepseek-harness: dsh exited: %v", waitErr)
	}
	if trimmed := strings.TrimSpace(stderr); trimmed != "" {
		msg += ": " + truncate(trimmed, 400)
	}
	return msg
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
