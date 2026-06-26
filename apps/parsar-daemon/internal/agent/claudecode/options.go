// Package claudecode is the agent_kind=claude_code implementation. It
// wraps the `claude` CLI in stream-json mode as a subprocess: the
// daemon writes user messages (and control responses for permission
// decisions) to stdin and translates the NDJSON event stream coming
// out of stdout into proto.Envelope frames for the dispatch router.
package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// BuildResult is the output of BuildArgs. Cleanup is always non-nil
// (no-op when nothing was written) so callers can `defer res.Cleanup()`
// blindly.
type BuildResult struct {
	Args    []string
	Env     []string
	Cleanup func()
}

// BuildArgs translates an AgentOptions map into the `claude` CLI argv.
// resumeSessionID, if non-empty, takes precedence over any
// "resume_session_id" key in opts. Unknown keys are silently ignored so
// the wire schema can add fields without bumping the daemon version.
func BuildArgs(opts map[string]any, resumeSessionID string) (BuildResult, error) {
	args := []string{
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--permission-prompt-tool", "stdio",
	}
	var cleanups []func()
	cleanup := func() {
		for _, c := range cleanups {
			c()
		}
	}
	env := []string{
		"DISABLE_TELEMETRY=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		// IS_SANDBOX=1 tells Claude Code to skip the "cannot be used
		// with root/sudo privileges" guard. envd's RunCommand only
		// passes a fixed PARSAR_* env allowlist into parsar-daemon, so the
		// sandbox image's own IS_SANDBOX=1 does NOT propagate down to
		// claude. Re-asserting it here is the actually-honored opt-out
		// (--allow-dangerously-skip-permissions alone does NOT satisfy
		// the check on 2.1.169).
		"IS_SANDBOX=1",
		// CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1 strips opt-in beta
		// fields (e.g. context_management.clear_thinking_20251015)
		// from every /v1/messages body. Internal Anthropic-compatible
		// gateways (vela-proxy) reject unknown fields with HTTP 400.
		// The desktop app ships ~/.claude/settings.json preconfigured;
		// the sandbox image doesn't, so the daemon sets it explicitly.
		"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1",
	}
	result := BuildResult{Cleanup: cleanup}

	if v, ok := opts["model"]; ok {
		s, ok := v.(string)
		if !ok {
			return result, fmt.Errorf("claudecode.BuildArgs: model must be string, got %T", v)
		}
		if s != "" {
			args = append(args, "--model", s)
		}
	}

	// bypassPermissions also appends --allow-dangerously-skip-permissions:
	// Claude Code 2.1.x refuses bypass while running as root without this
	// opt-in flag. Sandbox pods run as root, so omitting it would fail
	// every cloud-mode prompt at subprocess start. No-op for non-root.
	if v, ok := opts["mode"]; ok {
		s, ok := v.(string)
		if !ok {
			return result, fmt.Errorf("claudecode.BuildArgs: mode must be string, got %T", v)
		}
		if s != "" {
			args = append(args, "--permission-mode", s)
			if s == "bypassPermissions" {
				args = append(args, "--allow-dangerously-skip-permissions")
			}
		}
	}

	// allowed_tools: --allowedTools a,b,c
	if v, ok := opts["allowed_tools"]; ok {
		tools, err := stringSlice(v)
		if err != nil {
			return result, fmt.Errorf("claudecode.BuildArgs: allowed_tools: %w", err)
		}
		if len(tools) > 0 {
			args = append(args, "--allowedTools", strings.Join(tools, ","))
		}
	}

	// system_prompt vs override_system_prompt are mutually exclusive on
	// the CLI. If both are supplied, override wins and we strip the
	// append we already added.
	hasAppend := false
	hasOverride := false
	if v, ok := opts["system_prompt"]; ok {
		s, ok := v.(string)
		if !ok {
			return result, fmt.Errorf("claudecode.BuildArgs: system_prompt must be string, got %T", v)
		}
		if s != "" {
			hasAppend = true
			args = append(args, "--append-system-prompt", s)
		}
	}
	if v, ok := opts["override_system_prompt"]; ok {
		s, ok := v.(string)
		if !ok {
			return result, fmt.Errorf("claudecode.BuildArgs: override_system_prompt must be string, got %T", v)
		}
		if s != "" {
			if hasAppend {
				// Strip the append we just added; the pair is the last
				// two elements.
				args = args[:len(args)-2]
				hasAppend = false
			}
			hasOverride = true
			args = append(args, "--system-prompt", s)
		}
	}
	_ = hasOverride

	// mcp_servers: serialize the map to a 0o600 tempfile and pass
	// --mcp-config <path>. Tempfile is deleted in Cleanup.
	if v, ok := opts["mcp_servers"]; ok {
		mcp, ok := v.(map[string]any)
		if !ok {
			return result, fmt.Errorf("claudecode.BuildArgs: mcp_servers must be object, got %T", v)
		}
		if len(mcp) > 0 {
			path, err := writeMCPTempfile(mcp)
			if err != nil {
				return result, err
			}
			cleanups = append(cleanups, func() { _ = os.Remove(path) })
			result.Cleanup = func() {
				for _, c := range cleanups {
					c()
				}
			}
			args = append(args, "--mcp-config", path)
		}
	}

	// plugin_dirs: --plugin-dir x --plugin-dir y ...
	if v, ok := opts["plugin_dirs"]; ok {
		dirs, err := stringSlice(v)
		if err != nil {
			return result, fmt.Errorf("claudecode.BuildArgs: plugin_dirs: %w", err)
		}
		for _, d := range dirs {
			args = append(args, "--plugin-dir", d)
		}
	}

	// resume: --resume <session-id>. Explicit param wins over the map key.
	resume := resumeSessionID
	if resume == "" {
		if v, ok := opts["resume_session_id"]; ok {
			s, ok := v.(string)
			if !ok {
				return result, fmt.Errorf("claudecode.BuildArgs: resume_session_id must be string, got %T", v)
			}
			resume = s
		}
	}
	if resume != "" {
		args = append(args, "--resume", resume)
	}

	// env: passthrough KEY=value pairs.
	if v, ok := opts["env"]; ok {
		envMap, ok := v.(map[string]any)
		if !ok {
			return result, fmt.Errorf("claudecode.BuildArgs: env must be object, got %T", v)
		}
		// Sort keys so the produced env slice is deterministic.
		keys := make([]string, 0, len(envMap))
		for k := range envMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s, ok := envMap[k].(string)
			if !ok {
				return result, fmt.Errorf("claudecode.BuildArgs: env[%q] must be string, got %T", k, envMap[k])
			}
			env = append(env, k+"="+s)
		}
	}

	result.Args = args
	result.Env = env
	return result, nil
}

// stringSlice coerces a value to []string, accepting either a typed
// []string or []any with all-string elements (which is what
// json.Unmarshal produces for a JSON array into map[string]any).
func stringSlice(v any) ([]string, error) {
	switch x := v.(type) {
	case []string:
		return x, nil
	case []any:
		out := make([]string, 0, len(x))
		for i, el := range x {
			s, ok := el.(string)
			if !ok {
				return nil, fmt.Errorf("element %d must be string, got %T", i, el)
			}
			out = append(out, s)
		}
		return out, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("must be array of strings, got %T", v)
	}
}

// writeMCPTempfile serialises the mcp_servers map to JSON and writes
// it to a 0o600 file in os.TempDir. Returns the absolute path.
func writeMCPTempfile(mcp map[string]any) (string, error) {
	body, err := json.MarshalIndent(map[string]any{"mcpServers": mcp}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("claudecode: marshal mcp_servers: %w", err)
	}
	f, err := os.CreateTemp("", "parsar-daemon-mcp-*.json")
	if err != nil {
		return "", fmt.Errorf("claudecode: create mcp tempfile: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("claudecode: chmod mcp tempfile: %w", err)
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("claudecode: write mcp tempfile: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("claudecode: close mcp tempfile: %w", err)
	}
	// Resolve to absolute — tests sometimes change cwd which would
	// make a relative path useless for the subprocess.
	abs, err := filepath.Abs(f.Name())
	if err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("claudecode: resolve mcp tempfile path: %w", err)
	}
	return abs, nil
}

// userMessage is the JSON shape we write to claude stdin to deliver
// the prompt.
type userMessage struct {
	Type    string             `json:"type"`
	Message userMessageContent `json:"message"`
}

type userMessageContent struct {
	Role string `json:"role"`
	// Content is either a bare string (text-only path) or a
	// []userContentBlock when attachments are present. Both shapes
	// are accepted by claude's stdin loop; the bare-string path keeps
	// log greps for prompt content working in the common case.
	Content any `json:"content"`
}

// userContentBlock is one entry of Claude Code's array-of-blocks user
// message shape. JSON tags match Anthropic's content-block schema
// verbatim so the CLI forwards them to the model without translation.
type userContentBlock struct {
	Type   string             `json:"type"`
	Text   string             `json:"text,omitempty"`
	Source *userContentSource `json:"source,omitempty"`
}

type userContentSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

func buildUserMessage(prompt string) ([]byte, error) {
	return buildUserMessageWithAttachments(prompt, nil)
}

// buildUserMessageWithAttachments is the multimodal-aware variant. With
// no attachments, the output is byte-identical to the bare-string
// Content path so existing log greps for prompt content keep working.
// Non-image attachments are dropped — Claude Code SDK only understands
// the image block shape on stdin.
func buildUserMessageWithAttachments(prompt string, attachments []proto.PromptAttachment) ([]byte, error) {
	if prompt == "" && len(attachments) == 0 {
		return nil, errors.New("claudecode: empty prompt")
	}
	var content any
	if len(attachments) == 0 {
		content = prompt
	} else {
		blocks := make([]userContentBlock, 0, len(attachments)+1)
		if prompt != "" {
			blocks = append(blocks, userContentBlock{Type: "text", Text: prompt})
		}
		for _, att := range attachments {
			if att.Kind != "image" || att.DataBase64 == "" {
				continue
			}
			mime := att.MIME
			if mime == "" {
				mime = "image/png"
			}
			blocks = append(blocks, userContentBlock{
				Type: "image",
				Source: &userContentSource{
					Type:      "base64",
					MediaType: mime,
					Data:      att.DataBase64,
				},
			})
		}
		if len(blocks) == 0 {
			return nil, errors.New("claudecode: empty prompt after dropping unsupported attachments")
		}
		content = blocks
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(userMessage{
		Type: "user",
		Message: userMessageContent{
			Role:    "user",
			Content: content,
		},
	}); err != nil {
		return nil, fmt.Errorf("claudecode: marshal user message: %w", err)
	}
	return buf.Bytes(), nil
}
