package agentdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/render"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// CapabilityRuntimeStore is the capability surface the agent_daemon
// connector needs. GetEnabledCapabilitiesForAgent lists what's enabled
// on the agent; GetUserCredentialByUserKind resolves per-user
// credentials for MCP env placeholder substitution;
// IsBuiltinCapabilityEnabled reports the per-agent on/off flag for a
// runtime-injected built-in tool (default ON).
type CapabilityRuntimeStore interface {
	GetEnabledCapabilitiesForAgent(ctx context.Context, agentID string) ([]store.EnabledCapabilityRead, error)
	GetUserCredentialByUserKind(ctx context.Context, userID, kind string) (store.UserCredentialRead, bool, error)
	IsBuiltinCapabilityEnabled(ctx context.Context, agentID, key string) (bool, error)
}

// OSSPresigner is the narrow surface for the object-storage backend:
// given a capability's oss_key, return a short-lived presigned GET URL
// the daemon will fetch. Used for both plugin and skill zip downloads.
// *oss.Client (server/internal/storage/oss) satisfies this; passing nil
// keeps the connector silently zip-less (both plugin and skill capability
// types are skipped with a log warning).
type OSSPresigner interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error)
}

// ossPresignTTL is the TTL for capability zip download URLs. Long enough
// for daemon-side download (skill + plugin both), short enough that a
// leaked URL has bounded blast radius.
const ossPresignTTL = time.Hour

// ResolvedPlugin is the per-plugin descriptor the connector embeds in
// agent_options["plugins"]. Daemon-side code reads this list, downloads
// each Zip from DownloadURL, verifies SHA256, extracts to a workspace
// directory, then spawns Claude with one --plugin-dir flag per entry.
//
// JSON shape MUST stay in sync with apps/parsar-daemon plugin-install code;
// renaming a field is a wire break.
type ResolvedPlugin struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256"`
}

// ResolvedSkill is the per-skill descriptor the connector embeds in
// agent_options["skills"]. Daemon-side code reads this list, downloads
// each Zip from DownloadURL, verifies SHA256, extracts to
// <workDir>/.claude/skills/<name>/. Claude Code's startup auto-scans
// that directory and registers each skill via the native Skill tool —
// no CLI flag is needed.
//
// JSON shape is byte-identical to ResolvedPlugin so the daemon's
// generic zip installer can decode both with the same code.
type ResolvedSkill struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256"`
}

// credentialPlaceholderRe matches ${PARSAR_CREDENTIAL:<kind>}.
var credentialPlaceholderRe = regexp.MustCompile(`^\$\{PARSAR_CREDENTIAL:([a-zA-Z0-9_]+)\}$`)

// capabilityAdditions holds the results of resolveCapabilityAdditions.
type capabilityAdditions struct {
	Skills        []ResolvedSkill        // skill descriptors for opts["skills"]
	MCPServers    map[string]any         // server_name → config object for opts["mcp_servers"]
	Plugins       []ResolvedPlugin       // plugin descriptors for opts["plugins"]
	SystemPrompts []ResolvedSystemPrompt // merged into opts["system_prompt"] / opts["override_system_prompt"]
	Disabled      []DisabledCapability   // MCPs skipped because a required credential was missing
	// CredentialEmits records every successfully-resolved credential so
	// a caller wiring an audit pipeline can fire one
	// tool_call.credential_delivered event per entry. agent_daemon does
	// not own the audit emit path today; the field is captured upstream
	// so the same payload shape can be emitted when the daemon-side
	// hook lands.
	CredentialEmits []CredentialEmit
}

// ResolvedSystemPrompt is the per-capability descriptor that
// mergeSystemPromptsIntoOptions folds into agent_options. Name is
// preserved for log lines; the runtime keys on Mode + Content.
type ResolvedSystemPrompt struct {
	Name    string
	Mode    canonical.SystemPromptMode
	Content string
}

// DisabledCapability is the channel-layer payload describing a
// capability that was disabled mid-resolution (e.g. credential
// missing) so the channel can render a credential-form card.
type DisabledCapability struct {
	CapabilityID        string
	CapabilityVersionID string
	CapabilityName      string
	MissingCredentials  []MissingCredentialRef
	// SubKind, when non-empty, overrides the default
	// CapabilityCredentialMissing sub_kind on the emitted system
	// message. Used to distinguish "version unavailable" (oss_key
	// empty on the resolved version) from credential failures, so
	// the renderer can surface a more specific nudge ("re-upload
	// this skill / switch to a newer version") instead of a credential
	// form. Empty falls back to CapabilityCredentialMissing for wire
	// compatibility with existing emitters.
	SubKind string
}

// MissingCredentialRef names one credential the resolver could not fulfill.
type MissingCredentialRef struct {
	Kind string
}

// CredentialEmit describes one successfully-resolved credential
// injection for the audit pipeline.
//
// Source distinguishes "personal" (resolved via user_credentials by
// initiator) from "shared" (resolved via secrets table by agent binding).
// For "shared" emits, UserCredentialID is empty and SharedSecretID
// carries the secret row id; for "personal" emits, the reverse.
type CredentialEmit struct {
	AgentCapabilityID   string
	CapabilityID        string
	CapabilityVersionID string
	CapabilityName      string
	CredentialKind      string
	Source              CredentialBindingSource
	UserCredentialID    string
	SharedSecretID      string
	CredentialOwnerID   string
	InitiatorUserID     string
}

// CapabilitySystemMessageStore is the narrow surface the agentdaemon
// connector needs to surface ADR-003 soft-degrade nudges as
// runtime_error system_messages. *store.Store satisfies it.
type CapabilitySystemMessageStore interface {
	CreateRuntimeErrorSystemMessage(ctx context.Context, input store.CreateRuntimeErrorSystemMessageInput) (string, error)
	CreateSandboxOfflineNotice(ctx context.Context, input store.CreateSandboxOfflineNoticeInput) (string, error)
}

// CapabilityCredentialMissing is the metadata.sub_kind value emitted
// for an ADR-003 soft-degrade notice. The Feishu outbound driver greps
// on this.
const CapabilityCredentialMissing = "capability_credential_missing"

// CapabilityVersionUnavailable is the metadata.sub_kind value emitted
// when the resolved capability version has no usable storage
// breadcrumb (empty oss_key/sha256). This happens after a schema-level
// upload-format change (b77a1c1c) for capabilities whose pre-change
// version is still pinned. The renderer should nudge the user to either
// re-upload the capability or switch the binding to a newer version /
// flip pinning_mode to "latest".
const CapabilityVersionUnavailable = "capability_version_unavailable"

// errCapabilityVersionUnavailable is the sentinel error returned by
// resolveSkillCapability / resolvePluginCapability when the version
// they were asked to use (pinned column or joined latest) has an empty
// oss_key. The caller wraps it into a DisabledCapability via
// disabledForUnavailableVersion so the user sees a system-message nudge
// instead of the historical silent skip. Returning a sentinel (rather
// than a *DisabledCapability via a new signature) keeps the existing
// (nil, nil) "this binding produced nothing for unrelated reasons"
// short-circuit intact for OSS-not-configured / non-skill canonical
// rows.
var errCapabilityVersionUnavailable = errors.New("agent_daemon: capability version unavailable (empty oss_key)")

// agentKindToRenderTarget maps the daemon-side agent_kind discriminant
// onto the render.Target the capability serializer should produce. Unknown
// kinds fall back to TargetClaudeCode so legacy rows (empty agent_kind)
// keep working.
func agentKindToRenderTarget(agentKind string) render.Target {
	switch strings.TrimSpace(agentKind) {
	case "opencode":
		return render.TargetOpenCode
	case "codex":
		return render.TargetCodex
	case "pi":
		return render.TargetPi
	default:
		return render.TargetClaudeCode
	}
}

// disabledForUnsupportedCapability builds the DisabledCapability surfaced
// when a renderer returns render.ErrUnsupported — e.g. an opencode/codex
// agent enabling a skill or plugin capability. MissingCredentials is left
// empty; emitDisabledCapabilityNotices treats that as a "non-credential"
// disable and posts a generic notice.
func disabledForUnsupportedCapability(cap store.EnabledCapabilityRead) DisabledCapability {
	return DisabledCapability{
		CapabilityID:        cap.CapabilityID,
		CapabilityVersionID: cap.CapabilityVersionID,
		CapabilityName:      cap.Name,
	}
}

// disabledForUnavailableVersion is the parallel of
// disabledForUnsupportedCapability for the "we picked a version but it
// has no usable storage breadcrumb" case — i.e. pinning_mode="pinned"
// and the pinned version's oss_key is empty (pre-b77a1c1c), or
// pinning_mode="latest" and the capability's current latest version
// has no oss_key (capability with no zip ever uploaded). Both replace
// the old silent-skip path so the user gets a system-message nudge
// instead of an invisible failure.
func disabledForUnavailableVersion(cap store.EnabledCapabilityRead) DisabledCapability {
	return DisabledCapability{
		CapabilityID:        cap.CapabilityID,
		CapabilityVersionID: cap.CapabilityVersionID,
		CapabilityName:      cap.Name,
		SubKind:             CapabilityVersionUnavailable,
	}
}

// resolvedVersionFields collects the four storage-relevant fields the
// daemon resolver needs from cap, picking either the pinned-column
// fields or the joined latest-* fields based on cap.PinningMode.
// Centralising the switch keeps the per-kind resolve functions free of
// pinning_mode awareness.
type resolvedVersionFields struct {
	OssKey        string
	SHA256        string
	CanonicalSpec []byte
	Version       string // capability_version.version literal, surfaced to the daemon for log / wire purposes
}

// resolveVersionFields returns the storage / version fields to use for
// this binding. PinningMode "latest" picks the lateral subquery joined
// LatestOssKey/LatestSHA256/LatestCanonicalSpec/LatestVersion; anything
// else (including the empty string, treated as the safer "pinned"
// default) picks the cv.* columns.
func resolveVersionFields(cap store.EnabledCapabilityRead) resolvedVersionFields {
	if strings.TrimSpace(cap.PinningMode) == store.PinningModeLatest {
		return resolvedVersionFields{
			OssKey:        cap.LatestOssKey,
			SHA256:        cap.LatestSHA256,
			CanonicalSpec: cap.LatestCanonicalSpec,
			Version:       cap.LatestVersion,
		}
	}
	return resolvedVersionFields{
		OssKey:        cap.OssKey,
		SHA256:        cap.SHA256,
		CanonicalSpec: cap.CanonicalSpec,
		Version:       cap.Version,
	}
}

// resolveCapabilityAdditions enumerates all enabled capabilities on the
// agent and returns skill fragments + MCP server configs in a
// single DB round-trip.
func (c *Connector) resolveCapabilityAdditions(ctx context.Context, in connector.PromptInput, agentKind string) (capabilityAdditions, error) {
	result := capabilityAdditions{}
	// fetch-chat-history is injected universally (all agent kinds) via
	// system_prompt + env in applyIMHistoryPromptInjection, not as an MCP
	// server, so nothing is mounted here.
	if c.capabilities == nil {
		c.log.Warn("agent_daemon: resolveCapabilityAdditions skipped — capabilities store is nil")
		return result, nil
	}
	if strings.TrimSpace(in.AgentID) == "" {
		c.log.Warn("agent_daemon: resolveCapabilityAdditions skipped — agent_id is empty")
		return result, nil
	}
	caps, err := c.capabilities.GetEnabledCapabilitiesForAgent(ctx, in.AgentID)
	if err != nil {
		return result, fmt.Errorf("agent_daemon: list enabled capabilities: %w", err)
	}
	if len(caps) == 0 {
		c.log.Info("agent_daemon: no enabled capabilities for agent", "agent_id", in.AgentID)
		return result, nil
	}
	target := agentKindToRenderTarget(agentKind)
	renderer, err := render.For(target)
	if err != nil {
		return result, fmt.Errorf("agent_daemon: render lookup: %w", err)
	}
	// First-wins dedup on plugin/skill Name: a workspace with two
	// capabilities vending the same name would otherwise have the
	// second silently overwrite the first on disk (the daemon-side
	// installer keys on Name).
	seenPluginNames := map[string]string{}
	seenSkillNames := map[string]string{}
	credentialCache := map[string]store.UserCredentialRead{}
	for _, cap := range caps {
		switch cap.Type {
		case "skill":
			skill, err := c.resolveSkillCapability(ctx, cap, renderer)
			if err != nil {
				if errors.Is(err, render.ErrUnsupported) {
					c.log.Warn("agent_daemon: skill capability not supported by agent_kind, skipping",
						"capability_id", cap.CapabilityID,
						"capability_name", cap.Name,
						"agent_kind", agentKind,
						"target", string(target))
					result.Disabled = append(result.Disabled, disabledForUnsupportedCapability(cap))
					continue
				}
				if errors.Is(err, errCapabilityVersionUnavailable) {
					// Pinned-but-empty oss_key (b77a1c1c artefact) or
					// 'latest' on a never-uploaded capability. Convert
					// to a DisabledCapability so the user gets a
					// system-message nudge instead of a silent skip.
					result.Disabled = append(result.Disabled, disabledForUnavailableVersion(cap))
					continue
				}
				return result, err
			}
			if skill == nil {
				continue
			}
			if prev, dup := seenSkillNames[skill.Name]; dup {
				c.log.Warn("agent_daemon: duplicate skill name across capabilities; keeping first",
					"skill_name", skill.Name,
					"first_capability_id", prev,
					"dropped_capability_id", cap.CapabilityID)
				continue
			}
			seenSkillNames[skill.Name] = cap.CapabilityID
			result.Skills = append(result.Skills, *skill)
		case "mcp":
			servers, disabled, emits, err := c.resolveMCPCapability(ctx, in, cap, renderer, credentialCache)
			if err != nil {
				if errors.Is(err, render.ErrUnsupported) {
					c.log.Warn("agent_daemon: mcp capability not supported by agent_kind, skipping",
						"capability_id", cap.CapabilityID,
						"capability_name", cap.Name,
						"agent_kind", agentKind,
						"target", string(target))
					result.Disabled = append(result.Disabled, disabledForUnsupportedCapability(cap))
					continue
				}
				return result, err
			}
			if disabled != nil {
				result.Disabled = append(result.Disabled, *disabled)
				c.log.Info("agent_daemon: mcp capability disabled (missing credentials)",
					"capability_id", cap.CapabilityID,
					"capability_name", cap.Name,
					"missing_count", len(disabled.MissingCredentials))
				continue
			}
			if result.MCPServers == nil {
				result.MCPServers = map[string]any{}
			}
			for name, config := range servers {
				result.MCPServers[name] = config
			}
			result.CredentialEmits = append(result.CredentialEmits, emits...)
		case "plugin":
			plugin, err := c.resolvePluginCapability(ctx, cap, renderer)
			if err != nil {
				if errors.Is(err, render.ErrUnsupported) {
					c.log.Warn("agent_daemon: plugin capability not supported by agent_kind, skipping",
						"capability_id", cap.CapabilityID,
						"capability_name", cap.Name,
						"agent_kind", agentKind,
						"target", string(target))
					result.Disabled = append(result.Disabled, disabledForUnsupportedCapability(cap))
					continue
				}
				if errors.Is(err, errCapabilityVersionUnavailable) {
					result.Disabled = append(result.Disabled, disabledForUnavailableVersion(cap))
					continue
				}
				return result, err
			}
			if plugin == nil {
				continue
			}
			if prev, dup := seenPluginNames[plugin.Name]; dup {
				c.log.Warn("agent_daemon: duplicate plugin name across capabilities; keeping first",
					"plugin_name", plugin.Name,
					"first_capability_id", prev,
					"dropped_capability_id", cap.CapabilityID)
				continue
			}
			seenPluginNames[plugin.Name] = cap.CapabilityID
			result.Plugins = append(result.Plugins, *plugin)
		case "system_prompt":
			sp, err := c.resolveSystemPromptCapability(ctx, cap, renderer)
			if err != nil {
				if errors.Is(err, render.ErrUnsupported) {
					c.log.Warn("agent_daemon: system_prompt capability not supported by agent_kind, skipping",
						"capability_id", cap.CapabilityID,
						"capability_name", cap.Name,
						"agent_kind", agentKind,
						"target", string(target))
					result.Disabled = append(result.Disabled, disabledForUnsupportedCapability(cap))
					continue
				}
				return result, err
			}
			if sp == nil {
				continue
			}
			result.SystemPrompts = append(result.SystemPrompts, *sp)
		default:
			c.log.Warn("agent_daemon: skip unknown capability type",
				"capability_id", cap.CapabilityID,
				"capability_type", cap.Type)
		}
	}
	return result, nil
}

// resolvePluginCapability renders one plugin capability into a
// ResolvedPlugin descriptor that the daemon downloads + installs.
//
// Render() is pure (no I/O); this function mints a fresh presigned URL
// against the oss_key.
//
// Returns nil + no error when:
//
//   - PluginPresigner is nil (plugin support not enabled on this
//     deployment) → log a warning
//   - canonical_spec is empty (legacy or malformed row) → skip silently
//
// All other failures bubble up so the prompt fails loudly — silent
// loss of a plugin a user explicitly enabled is worse than a clear
// "plugin X failed to install" error.
func (c *Connector) resolvePluginCapability(
	ctx context.Context,
	cap store.EnabledCapabilityRead,
	renderer render.Renderer,
) (*ResolvedPlugin, error) {
	if c.oss == nil {
		c.log.Warn("agent_daemon: plugin capability skipped — OSSPresigner not configured",
			"capability_id", cap.CapabilityID,
			"capability_name", cap.Name)
		return nil, nil
	}
	// resolved picks pinned-column vs latest-* fields based on
	// cap.PinningMode — for a 'latest' binding, a re-upload of the
	// capability flows through immediately without rewriting the
	// agent_capabilities row.
	resolved := resolveVersionFields(cap)
	ossKey := strings.TrimSpace(resolved.OssKey)
	if ossKey == "" {
		// Pre-b77a1c1c row (pinned to a column-less version) OR a
		// 'latest' binding pointing at a capability with no zip ever
		// uploaded. Surface as a system message instead of silent skip.
		c.log.Warn("agent_daemon: plugin capability has empty oss_key, emitting DisabledCapability",
			"capability_id", cap.CapabilityID,
			"capability_name", cap.Name,
			"pinning_mode", cap.PinningMode,
			"pinned_version_id", cap.CapabilityVersionID,
			"latest_version_id", cap.LatestVersionID)
		return nil, errCapabilityVersionUnavailable
	}
	if len(resolved.CanonicalSpec) == 0 {
		// Without canonical_spec we can't tell the daemon the plugin's
		// declared name/version. Skip rather than guess.
		c.log.Warn("agent_daemon: plugin capability has empty canonical_spec, skipping",
			"capability_id", cap.CapabilityID,
			"capability_name", cap.Name,
			"pinning_mode", cap.PinningMode)
		return nil, nil
	}

	var spec canonical.Spec
	if err := json.Unmarshal(resolved.CanonicalSpec, &spec); err != nil {
		return nil, fmt.Errorf("agent_daemon: plugin capability %s canonical_spec decode: %w", cap.CapabilityID, err)
	}
	if spec.Kind != canonical.KindPlugin {
		return nil, fmt.Errorf("agent_daemon: capability %s has type=plugin but canonical_spec.kind=%q", cap.CapabilityID, spec.Kind)
	}
	if spec.Plugin == nil {
		return nil, fmt.Errorf("agent_daemon: capability %s canonical_spec.plugin is nil", cap.CapabilityID)
	}

	// Render is called for wire-shape consistency; the rendered struct
	// is discarded since the authoritative fields below come from the
	// column / spec body directly.
	if _, err := renderer.Render(ctx, spec); err != nil {
		return nil, fmt.Errorf("agent_daemon: render plugin %s: %w", cap.CapabilityID, err)
	}

	url, _, err := c.oss.PresignGet(ctx, ossKey, ossPresignTTL)
	if err != nil {
		// Strip embedded URL from the error before returning so the
		// presigned URL doesn't leak via the wrapped error chain —
		// *url.Error.Error() embeds the full URL.
		return nil, fmt.Errorf("agent_daemon: presign plugin %s (capability_id=%s): %s", spec.Plugin.Name, cap.CapabilityID, sanitizeOSSError(err))
	}

	sha256 := strings.ToLower(strings.TrimSpace(resolved.SHA256))
	if sha256 == "" {
		// Pre-fix row: column empty. Fall back to jsonb copy rather
		// than letting the daemon verify against "".
		sha256 = strings.ToLower(strings.TrimSpace(spec.Plugin.SHA256))
	}

	return &ResolvedPlugin{
		Name:        spec.Plugin.Name,
		Version:     spec.Plugin.Version,
		DownloadURL: url,
		SHA256:      sha256,
	}, nil
}

// sanitizeOSSError strips any embedded URL from a wrapped *url.Error so
// presigned download URLs never leak through error messages. Returns
// the original error string when the format doesn't match.
func sanitizeOSSError(err error) string {
	msg := err.Error()
	// *url.Error.Error() format: <method> "<url>": <inner>
	open := strings.Index(msg, `"`)
	if open < 0 {
		return msg
	}
	close := strings.Index(msg[open+1:], `"`)
	if close < 0 {
		return msg
	}
	closeAbs := open + 1 + close
	if closeAbs+2 > len(msg) {
		return msg
	}
	return msg[:open] + "<redacted-url>" + msg[closeAbs+1:]
}

// claudeCodePluginEnvelope mirrors render.claudeCodePluginDocument —
// the bridge between the pure renderer (no I/O, no URL) and this layer
// (mints a presigned URL).
type claudeCodePluginEnvelope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	OssKey  string `json:"oss_key"`
	SHA256  string `json:"sha256"`
}

// resolveMCPCapability renders one MCP capability into the daemon's
// mcp_servers config format, resolving credential placeholders. When a
// required credential is missing, returns
// (nil, &DisabledCapability{...}, nil, nil) so the caller can soft-
// degrade and surface a nudge. The third return is a per-credential
// CredentialEmit list for audit.
func (c *Connector) resolveMCPCapability(
	ctx context.Context,
	in connector.PromptInput,
	cap store.EnabledCapabilityRead,
	renderer render.Renderer,
	credentialCache map[string]store.UserCredentialRead,
) (map[string]any, *DisabledCapability, []CredentialEmit, error) {
	// Render canonical spec (or legacy content) into the Claude Code
	// MCP JSON shape: {"mcpServers": {"name": {"command","args","env"}}}
	content, err := resolveCapabilityMCPContent(cap, renderer)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(content) == 0 {
		c.log.Warn("agent_daemon: mcp capability has empty content, skipping",
			"capability_id", cap.CapabilityID,
			"capability_name", cap.Name)
		return nil, nil, nil, nil
	}

	var parsed claudeCodeMCPDocument
	if err := json.Unmarshal(content, &parsed); err != nil {
		return nil, nil, nil, fmt.Errorf("agent_daemon: capability %s mcp content parse: %w", cap.CapabilityID, err)
	}
	if len(parsed.MCPServers) == 0 {
		return nil, nil, nil, nil
	}

	// Resolve credentials for placeholder substitution. Each required
	// kind resolves via either an agent-level shared binding (secret
	// table lookup) or, by default, per-user user_credentials keyed by
	// the conversation initiator.
	bindings := ParseCredentialBindings(in.AgentConfig)
	credentialValues, sharedSecretIDs, missing, err := c.resolveCredentialValues(ctx, in, cap, credentialCache, bindings)
	if err != nil {
		return nil, nil, nil, err
	}

	if len(missing) > 0 {
		return nil, &DisabledCapability{
			CapabilityID:        cap.CapabilityID,
			CapabilityVersionID: cap.CapabilityVersionID,
			CapabilityName:      cap.Name,
			MissingCredentials:  missing,
		}, nil, nil
	}

	// Build the daemon-consumable map: server_name → config object.
	result := map[string]any{}
	for name, server := range parsed.MCPServers {
		if server.URL != "" {
			entry := map[string]any{"url": server.URL}
			if server.Type != "" {
				entry["type"] = server.Type
			}
			if server.Enabled != nil {
				entry["enabled"] = *server.Enabled
			}
			result[name] = entry
			continue
		}
		env := map[string]string{}
		for key, value := range server.Env {
			if match := credentialPlaceholderRe.FindStringSubmatch(value); match != nil {
				kind := match[1]
				credValue, ok := credentialValues[kind]
				if !ok {
					return nil, nil, nil, fmt.Errorf("agent_daemon: capability %s mcp server %q env %s references unresolved credential kind %q",
						cap.CapabilityID, name, key, kind)
				}
				env[key] = credValue
				continue
			}
			env[key] = value
		}
		entry := map[string]any{
			"command": server.Command,
		}
		if len(server.Args) > 0 {
			entry["args"] = server.Args
		}
		if len(env) > 0 {
			entry["env"] = env
		}
		result[name] = entry
	}

	// Build CredentialEmit entries for audit. Per-user kinds come from
	// the version-level RequiredCredentials list (declaration order,
	// deterministic). Shared-binding kinds emit a "shared" source with
	// the secret_id instead of a user_credential_id.
	var emits []CredentialEmit
	for _, rc := range cap.RequiredCredentials {
		if secretID, ok := sharedSecretIDs[rc.Kind]; ok {
			emits = append(emits, CredentialEmit{
				AgentCapabilityID:   cap.AgentCapabilityID,
				CapabilityID:        cap.CapabilityID,
				CapabilityVersionID: cap.CapabilityVersionID,
				CapabilityName:      cap.Name,
				CredentialKind:      rc.Kind,
				Source:              CredentialBindingShared,
				SharedSecretID:      secretID,
				InitiatorUserID:     in.ConversationInitiatorID,
			})
			continue
		}
		// Re-fetch from the cache (not the values map) so the audit
		// emit captures the credential row id; the values map only
		// keeps decrypted plaintext keyed by kind.
		credCacheKey := strings.TrimSpace(in.ConversationInitiatorID) + "\x00" + strings.TrimSpace(rc.Kind)
		cred, ok := credentialCache[credCacheKey]
		if !ok || cred.ID == "" {
			continue
		}
		emits = append(emits, CredentialEmit{
			AgentCapabilityID:   cap.AgentCapabilityID,
			CapabilityID:        cap.CapabilityID,
			CapabilityVersionID: cap.CapabilityVersionID,
			CapabilityName:      cap.Name,
			CredentialKind:      rc.Kind,
			Source:              CredentialBindingPersonal,
			UserCredentialID:    cred.ID,
			CredentialOwnerID:   in.ConversationInitiatorID,
			InitiatorUserID:     in.ConversationInitiatorID,
		})
	}
	return result, nil, emits, nil
}

// resolveCredentialValues resolves and decrypts every required credential
// for an MCP capability, returning:
//
//   - values:           kind → decrypted plaintext
//   - sharedSecretIDs:  kind → secret_id (only for shared bindings; used
//     by audit emits)
//   - missing:          kinds the resolver could not fulfil (personal-binding
//     kinds whose initiator has not configured the credential)
//
// A missing entry DOES NOT short-circuit — the caller treats them as
// "this MCP must be disabled this turn". Decrypt / payload-shape errors
// bubble up because they signal data corruption, not a missing user action.
//
// Initiator-ID requirement is RELAXED: only personal-binding kinds need
// in.ConversationInitiatorID. A capability whose required kinds are all
// shared-bound runs even for guest callers (public agent + lark guest).
func (c *Connector) resolveCredentialValues(
	ctx context.Context,
	in connector.PromptInput,
	cap store.EnabledCapabilityRead,
	credentialCache map[string]store.UserCredentialRead,
	bindings map[string]CredentialBinding,
) (map[string]string, map[string]string, []MissingCredentialRef, error) {
	if len(cap.RequiredCredentials) == 0 {
		return nil, nil, nil, nil
	}
	if c.secrets == nil {
		return nil, nil, nil, fmt.Errorf("agent_daemon: capability %s requires credentials but secrets service is not configured", cap.CapabilityID)
	}

	// First pass: split required kinds into personal vs shared.
	type sharedTarget struct {
		Kind     string
		SecretID string
	}
	var (
		personalKinds []store.RequiredCredential
		sharedTargets []sharedTarget
	)
	for _, rc := range cap.RequiredCredentials {
		if b, ok := bindings[rc.Kind]; ok && b.IsShared() {
			sharedTargets = append(sharedTargets, sharedTarget{Kind: rc.Kind, SecretID: b.SecretID})
			continue
		}
		personalKinds = append(personalKinds, rc)
	}

	// Personal-binding kinds need an initiator. Shared-only capabilities
	// are allowed to run without one (guest callers on public agents).
	if len(personalKinds) > 0 && strings.TrimSpace(in.ConversationInitiatorID) == "" {
		return nil, nil, nil, fmt.Errorf("agent_daemon: capability %s requires personal credentials but conversation_initiator_id is empty", cap.CapabilityID)
	}

	values := map[string]string{}
	sharedSecretIDs := map[string]string{}
	var missing []MissingCredentialRef

	// Shared bindings: resolve once via the model resolver's secret
	// payload reader (already wired for inline_secret models).
	if len(sharedTargets) > 0 {
		if c.modelResolver == nil {
			return nil, nil, nil, fmt.Errorf("agent_daemon: capability %s has shared credential bindings but model resolver is not configured", cap.CapabilityID)
		}
		for _, st := range sharedTargets {
			secret, err := c.modelResolver.GetSecretPayload(ctx, in.WorkspaceID, st.SecretID)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("agent_daemon: load shared credential secret kind=%s secret_id=%s: %w", st.Kind, st.SecretID, err)
			}
			if secret.Status != "active" {
				return nil, nil, nil, fmt.Errorf("agent_daemon: shared credential secret kind=%s secret_id=%s status=%s", st.Kind, st.SecretID, secret.Status)
			}
			payload, err := c.secrets.Decrypt(secret.EncryptedPayload)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("agent_daemon: decrypt shared credential kind=%s secret_id=%s: %w", st.Kind, st.SecretID, err)
			}
			value := credentialPayloadValue(payload)
			if value == "" {
				return nil, nil, nil, fmt.Errorf("agent_daemon: shared credential kind=%s secret_id=%s decrypted payload has no token/api_key value", st.Kind, st.SecretID)
			}
			values[st.Kind] = value
			sharedSecretIDs[st.Kind] = st.SecretID
		}
	}

	// Personal bindings: per-user user_credentials lookup, default
	// behavior preserved.
	credsByKind := map[string]store.UserCredentialRead{}
	for _, rc := range personalKinds {
		cred, ok, err := lookupCredential(ctx, c.capabilities, credentialCache, in.ConversationInitiatorID, rc.Kind)
		if err != nil {
			return nil, nil, nil, err
		}
		if !ok {
			if rc.Required {
				missing = append(missing, MissingCredentialRef{Kind: rc.Kind})
			}
			continue
		}
		credsByKind[rc.Kind] = cred
	}

	for kind, cred := range credsByKind {
		payload, err := c.secrets.Decrypt(cred.Ciphertext)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("agent_daemon: decrypt credential kind=%s user_credential_id=%s: %w", kind, cred.ID, err)
		}
		value := credentialPayloadValue(payload)
		if value == "" {
			return nil, nil, nil, fmt.Errorf("agent_daemon: credential kind=%s user_credential_id=%s decrypted payload has no token/api_key value", kind, cred.ID)
		}
		values[kind] = value
	}

	return values, sharedSecretIDs, missing, nil
}

// lookupCredential fetches a user credential by kind, using a cache to
// avoid duplicate DB lookups within a single resolution pass. Cache key
// is (user_id, kind).
func lookupCredential(
	ctx context.Context,
	capStore CapabilityRuntimeStore,
	cache map[string]store.UserCredentialRead,
	userID, kind string,
) (store.UserCredentialRead, bool, error) {
	userID = strings.TrimSpace(userID)
	kind = strings.TrimSpace(kind)
	cacheKey := userID + "\x00" + kind
	if cred, ok := cache[cacheKey]; ok {
		return cred, true, nil
	}
	cred, ok, err := capStore.GetUserCredentialByUserKind(ctx, userID, kind)
	if err != nil {
		return store.UserCredentialRead{}, false, fmt.Errorf("agent_daemon: look up credential (user=%s kind=%s): %w", userID, kind, err)
	}
	if ok {
		cache[cacheKey] = cred
	}
	return cred, ok, nil
}

// credentialPayloadValue extracts the token/key from a decrypted
// credential payload.
func credentialPayloadValue(payload map[string]any) string {
	for _, key := range []string{"api_key", "token", "access_token", "value"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// resolveCapabilityMCPContent picks the right content source for an MCP
// capability and renders it via the Claude Code renderer.
//
// Precedence:
//  1. CanonicalSpec (M1+ imports) → render through TargetClaudeCode
//  2. Content (legacy hand-authored) → returned verbatim
func resolveCapabilityMCPContent(cap store.EnabledCapabilityRead, renderer render.Renderer) ([]byte, error) {
	if len(cap.CanonicalSpec) > 0 {
		var spec canonical.Spec
		if err := json.Unmarshal(cap.CanonicalSpec, &spec); err != nil {
			return nil, fmt.Errorf("agent_daemon: capability %s canonical_spec decode: %w", cap.CapabilityID, err)
		}
		out, err := renderer.Render(context.Background(), spec)
		if err != nil {
			return nil, fmt.Errorf("agent_daemon: capability %s render: %w", cap.CapabilityID, err)
		}
		return out.Content, nil
	}
	return cap.Content, nil
}

// claudeCodeMCPDocument mirrors the JSON shape emitted by
// render.claudeCodeRenderer for KindMCP.
type claudeCodeMCPDocument struct {
	MCPServers map[string]claudeCodeMCPServerEntry `json:"mcpServers"`
}

type claudeCodeMCPServerEntry struct {
	Type    string            `json:"type,omitempty"`
	URL     string            `json:"url,omitempty"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled *bool             `json:"enabled,omitempty"`
}

// resolveSkillCapability mirrors resolvePluginCapability — skill and
// plugin share the same OSS zip path and daemon-side installer.
//
// Returns nil + no error when OSSPresigner is missing, canonical_spec
// is empty, or oss_key is empty (legacy markdown-paste skill the
// operator must re-upload as a zip). All other failures bubble up;
// silently losing a skill the user enabled is worse than a loud
// install failure.
func (c *Connector) resolveSkillCapability(
	ctx context.Context,
	cap store.EnabledCapabilityRead,
	renderer render.Renderer,
) (*ResolvedSkill, error) {
	if c.oss == nil {
		c.log.Warn("agent_daemon: skill capability skipped — OSSPresigner not configured",
			"capability_id", cap.CapabilityID,
			"capability_name", cap.Name)
		return nil, nil
	}
	// PinningMode-aware field selection: 'latest' picks the lateral-
	// joined latest_* fields so a reupload of the skill flows through
	// without any agent_capabilities rewrite.
	resolved := resolveVersionFields(cap)
	ossKey := strings.TrimSpace(resolved.OssKey)
	if ossKey == "" {
		// Two cases land here:
		//   * pinning_mode='pinned' on a pre-b77a1c1c version (column
		//     empty);
		//   * pinning_mode='latest' but the capability has not been
		//     uploaded as a zip yet (only markdown-paste exists).
		// Either way the daemon needs a system-message nudge — silent
		// skip used to leave the user unsure why the skill never
		// loaded. errCapabilityVersionUnavailable is converted into a
		// DisabledCapability by the caller.
		c.log.Warn("agent_daemon: skill capability has empty oss_key, emitting DisabledCapability",
			"capability_id", cap.CapabilityID,
			"capability_name", cap.Name,
			"pinning_mode", cap.PinningMode,
			"pinned_version_id", cap.CapabilityVersionID,
			"latest_version_id", cap.LatestVersionID)
		return nil, errCapabilityVersionUnavailable
	}
	if len(resolved.CanonicalSpec) == 0 {
		// Same "user enabled but version isn't usable" story as the
		// empty-oss_key branch: a row with oss_key present but
		// canonical_spec missing is a corrupted version, not a legacy
		// row. Treat the same way so the user sees a nudge.
		c.log.Warn("agent_daemon: skill capability has empty canonical_spec on resolved version, emitting DisabledCapability",
			"capability_id", cap.CapabilityID,
			"capability_name", cap.Name,
			"pinning_mode", cap.PinningMode)
		return nil, errCapabilityVersionUnavailable
	}

	var spec canonical.Spec
	if err := json.Unmarshal(resolved.CanonicalSpec, &spec); err != nil {
		return nil, fmt.Errorf("agent_daemon: skill capability %s canonical_spec decode: %w", cap.CapabilityID, err)
	}
	if spec.Kind != canonical.KindSkill {
		return nil, fmt.Errorf("agent_daemon: capability %s has type=skill but canonical_spec.kind=%q", cap.CapabilityID, spec.Kind)
	}
	if spec.Skill == nil {
		return nil, fmt.Errorf("agent_daemon: capability %s canonical_spec.skill is nil", cap.CapabilityID)
	}

	// Render call kept for wire-shape consistency only; output is
	// discarded (mirrors resolvePluginCapability).
	if _, err := renderer.Render(ctx, spec); err != nil {
		return nil, fmt.Errorf("agent_daemon: render skill %s: %w", cap.CapabilityID, err)
	}

	url, _, err := c.oss.PresignGet(ctx, ossKey, ossPresignTTL)
	if err != nil {
		return nil, fmt.Errorf("agent_daemon: presign skill %s (capability_id=%s): %s", spec.Skill.Slug, cap.CapabilityID, sanitizeOSSError(err))
	}

	sha256 := strings.ToLower(strings.TrimSpace(resolved.SHA256))
	if sha256 == "" {
		return nil, fmt.Errorf("agent_daemon: skill capability %s has oss_key but empty sha256 (pinning_mode=%s)", cap.CapabilityID, cap.PinningMode)
	}

	name := strings.TrimSpace(spec.Skill.Slug)
	if name == "" {
		name = strings.TrimSpace(cap.Name)
	}
	if name == "" {
		return nil, fmt.Errorf("agent_daemon: skill capability %s has empty slug and name", cap.CapabilityID)
	}

	return &ResolvedSkill{
		Name:        name,
		Version:     resolved.Version,
		DownloadURL: url,
		SHA256:      sha256,
	}, nil
}

// resolveSystemPromptCapability decodes a capability_version.canonical_spec
// into a ResolvedSystemPrompt. The render call is kept for wire-shape
// consistency only (mirrors resolveSkillCapability); the prompt text is
// read straight from the canonical spec because the daemon consumes
// agent_options.system_prompt / override_system_prompt, not a rendered
// capability blob.
func (c *Connector) resolveSystemPromptCapability(
	ctx context.Context,
	cap store.EnabledCapabilityRead,
	renderer render.Renderer,
) (*ResolvedSystemPrompt, error) {
	if len(cap.CanonicalSpec) == 0 {
		c.log.Warn("agent_daemon: system_prompt capability has empty canonical_spec, skipping",
			"capability_id", cap.CapabilityID,
			"capability_name", cap.Name)
		return nil, nil
	}
	var spec canonical.Spec
	if err := json.Unmarshal(cap.CanonicalSpec, &spec); err != nil {
		return nil, fmt.Errorf("agent_daemon: system_prompt capability %s canonical_spec decode: %w", cap.CapabilityID, err)
	}
	if spec.Kind != canonical.KindSystemPrompt {
		return nil, fmt.Errorf("agent_daemon: capability %s has type=system_prompt but canonical_spec.kind=%q", cap.CapabilityID, spec.Kind)
	}
	if spec.SystemPrompt == nil {
		return nil, fmt.Errorf("agent_daemon: capability %s canonical_spec.system_prompt is nil", cap.CapabilityID)
	}
	if _, err := renderer.Render(ctx, spec); err != nil {
		return nil, fmt.Errorf("agent_daemon: render system_prompt %s: %w", cap.CapabilityID, err)
	}
	return &ResolvedSystemPrompt{
		Name:    strings.TrimSpace(cap.Name),
		Mode:    spec.SystemPrompt.ResolvedMode(),
		Content: spec.SystemPrompt.Prompt,
	}, nil
}

// mergeSkillsIntoOptions folds resolved skill descriptors into
// opts["skills"]. Override-wins on collision (mirrors mcp_servers /
// plugins precedence).
func mergeSkillsIntoOptions(opts map[string]any, skills []ResolvedSkill) {
	if len(skills) == 0 {
		return
	}
	if _, ok := opts["skills"]; ok {
		return
	}
	// []any so the opts map serialises over the WS without a
	// Marshal/Unmarshal round-trip at the proto boundary.
	out := make([]any, 0, len(skills))
	for _, s := range skills {
		out = append(out, map[string]any{
			"name":         s.Name,
			"version":      s.Version,
			"download_url": s.DownloadURL,
			"sha256":       s.SHA256,
		})
	}
	opts["skills"] = out
}

// mergePluginsIntoOptions folds resolved plugin descriptors into
// opts["plugins"]. The daemon downloads each zip, extracts, and spawns
// Claude with one --plugin-dir flag per entry.
//
// Override-wins: if opts already carries a "plugins" key from a hand-
// configured override, DROP the capability-resolved list. Mirrors the
// mcp_servers precedence rule.
//
// If a future change wants to merge instead of override-wins, the
// daemon will refuse two --plugin-dir entries with the same plugin name.
func mergePluginsIntoOptions(opts map[string]any, plugins []ResolvedPlugin) {
	if len(plugins) == 0 {
		return
	}
	if _, ok := opts["plugins"]; ok {
		return
	}
	// []any (not []ResolvedPlugin) so the opts map serialises cleanly
	// over the WS without a json.Marshal/Unmarshal round-trip at the
	// proto boundary. Keys MUST match ResolvedPlugin's json tags.
	out := make([]any, 0, len(plugins))
	for _, p := range plugins {
		out = append(out, map[string]any{
			"name":         p.Name,
			"version":      p.Version,
			"download_url": p.DownloadURL,
			"sha256":       p.SHA256,
		})
	}
	opts["plugins"] = out
}

// mergeMCPServersIntoOptions merges capability-resolved MCP servers into
// opts["mcp_servers"]. Static (hand-configured) servers take precedence:
// a capability-resolved server with the same name is NOT overwritten.
func mergeMCPServersIntoOptions(opts map[string]any, mcpServers map[string]any) {
	if len(mcpServers) == 0 {
		return
	}
	existing, _ := opts["mcp_servers"].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}
	names := make([]string, 0, len(mcpServers))
	for name := range mcpServers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := existing[name]; ok {
			// Static config wins — don't overwrite a hand-configured server.
			continue
		}
		existing[name] = mcpServers[name]
	}
	opts["mcp_servers"] = existing
}

// emitDisabledCapabilityNotices fans the ADR-003 soft-degrade list out
// into per-(capability, missing kind) runtime_error system_messages so
// the Feishu outbound driver (via ListCapabilityCredentialMissingForRun)
// can render a credential-form card in place of the regular DoneCard.
//
// Best-effort: nil SystemMessages or empty ConversationID short-
// circuits to a no-op. Per-row emit failures log + continue.
//
// The empty-MissingCredentials branch preserves a path for a future
// "disabled for non-credential reason" so the user signal isn't
// silently dropped.
func (c *Connector) emitDisabledCapabilityNotices(ctx context.Context, in connector.PromptInput, disabled []DisabledCapability) {
	if c.systemMessages == nil {
		if len(disabled) > 0 {
			c.log.Warn("agent_daemon: skip emitDisabledCapabilityNotices — SystemMessages dependency not wired; user will not see a credential-form nudge",
				"run_id", in.RunID,
				"disabled_count", len(disabled))
		}
		return
	}
	if strings.TrimSpace(in.ConversationID) == "" || len(disabled) == 0 {
		return
	}
	for _, d := range disabled {
		// Per-disabled SubKind override falls back to the historical
		// CapabilityCredentialMissing value so existing channel
		// renderers (Feishu outbound greps on this exact string) keep
		// firing on credential failures unchanged.
		subKind := strings.TrimSpace(d.SubKind)
		if subKind == "" {
			subKind = CapabilityCredentialMissing
		}
		if len(d.MissingCredentials) == 0 {
			if _, err := c.systemMessages.CreateRuntimeErrorSystemMessage(ctx, store.CreateRuntimeErrorSystemMessageInput{
				WorkspaceID:    in.WorkspaceID,
				AgentID:        in.AgentID,
				RunID:          in.RunID,
				ConversationID: in.ConversationID,
				SubKind:        subKind,
				CapabilityID:   d.CapabilityID,
				CapabilityName: d.CapabilityName,
			}); err != nil {
				c.log.Warn("agent_daemon: emit disabled-capability system message failed",
					"capability", d.CapabilityName,
					"sub_kind", subKind,
					"err", err)
			}
			continue
		}
		for _, missing := range d.MissingCredentials {
			if _, err := c.systemMessages.CreateRuntimeErrorSystemMessage(ctx, store.CreateRuntimeErrorSystemMessageInput{
				WorkspaceID:    in.WorkspaceID,
				AgentID:        in.AgentID,
				RunID:          in.RunID,
				ConversationID: in.ConversationID,
				SubKind:        subKind,
				CapabilityID:   d.CapabilityID,
				CapabilityName: d.CapabilityName,
				CredentialKind: missing.Kind,
			}); err != nil {
				c.log.Warn("agent_daemon: emit disabled-capability system message failed",
					"capability", d.CapabilityName,
					"kind", missing.Kind,
					"sub_kind", subKind,
					"err", err)
			}
		}
	}
}
