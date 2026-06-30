/**
 * Left-paste, right-preview workspace for an MCP import. Paste
 * debounces into /import/preview; subsequent per-env mode flips stay
 * local (preview is parse-only). Edited spec + collected inline-secret
 * plaintexts bubble up via onChange / onInlineSecretsChange.
 *
 * `value` is null while the parent has no successful preview — that's
 * the idle state.
 */
import { useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { ApiError } from "../../../lib/api-client"
import { useImportPreviewMutation } from "./api"
import { EnvCredentialPicker } from "./EnvCredentialPicker"
import { ImportPreview } from "./ImportPreview"
import type {
  CanonicalEnvValue,
  CanonicalMCPServer,
  CanonicalSpec,
  ImportInlineSecretInput,
  SourceFormat,
} from "./types"

interface Props {
  workspaceID: string | null
  /** Current spec (initialized by parent from preview success). */
  value: CanonicalSpec | null
  onChange: (next: CanonicalSpec | null) => void
  /** Plaintexts the parent will turn into inline_secrets[] at submit time. */
  inlineSecrets: ImportInlineSecretInput[]
  onInlineSecretsChange: (next: ImportInlineSecretInput[]) => void
  /** Called with the suggested name on every successful preview so the parent
   *  can pre-fill its Name input if the user hasn't typed one yet. */
  onSuggestedName: (name: string) => void
  /** Bubble up the raw paste so the parent can stash it as source_payload. */
  onRawTextChange: (raw: string, format: SourceFormat) => void
  /**
   * Initial textarea content. Used by the "add new version" dialog to seed
   * the textarea from the previous version's source_payload so the user can
   * tweak from a known-good base. Empty by default.
   */
  initialRawText?: string
  /**
   * Initial source format. Same prefill use-case as initialRawText.
   * Falls back to "json" when omitted.
   */
  initialFormat?: SourceFormat
}

export function ImportMCPForm({
  workspaceID,
  value,
  onChange,
  inlineSecrets,
  onInlineSecretsChange,
  onSuggestedName,
  onRawTextChange,
  initialRawText,
  initialFormat,
}: Props) {
  const { t } = useTranslation("admin")
  const previewMut = useImportPreviewMutation(workspaceID)

  const [format, setFormat] = useState<SourceFormat>(initialFormat ?? "json")
  const [raw, setRaw] = useState(initialRawText ?? "")
  const [warnings, setWarnings] = useState<string[]>([])
  const [errorMessage, setErrorMessage] = useState<string | null>(null)
  const debounceRef = useRef<number | null>(null)

  // Debounced preview; also re-fires on format change so a JSON↔TOML
  // flip picks the right parser without retyping.
  useEffect(() => {
    onRawTextChange(raw, format)
    if (debounceRef.current) window.clearTimeout(debounceRef.current)
    if (raw.trim() === "") {
      onChange(null)
      setWarnings([])
      setErrorMessage(null)
      previewMut.reset()
      return
    }
    debounceRef.current = window.setTimeout(() => {
      previewMut.mutate(
        { kind: "mcp", raw_text: raw, source_format: format },
        {
          onSuccess: (res) => {
            const canonicalSpec = normalizeEnvCredentialPlaceholders(res.canonical_spec)
            onChange(canonicalSpec)
            setWarnings(res.warnings ?? [])
            setErrorMessage(null)
            onSuggestedName(res.suggested_name ?? "")
            // Drop stale inline secrets pointing at env entries that no
            // longer exist — prevents ghost rows in the commit payload.
            const live = new Set<string>()
            const servers = canonicalSpec.mcp?.servers ?? []
            for (const srv of servers) {
              for (const key of Object.keys(srv.env ?? {})) {
                live.add(`${srv.name}\x00${key}`)
              }
            }
            const filtered = inlineSecrets.filter((s) =>
              live.has(`${s.server_name}\x00${s.env_key}`),
            )
            if (filtered.length !== inlineSecrets.length) {
              onInlineSecretsChange(filtered)
            }
          },
          onError: (err) => {
            const msg = err instanceof ApiError
              ? err.envelope.message
              : err instanceof Error
                ? err.message
                : t("capabilities.import.preview.errorFallback", "Failed to parse")
            setErrorMessage(msg)
            setWarnings([])
            onChange(null)
          },
        },
      )
    }, 350)
    return () => {
      if (debounceRef.current) window.clearTimeout(debounceRef.current)
    }
    // Depend only on raw + format — adding the mutation/callbacks would
    // re-arm the timer on every parent render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [raw, format])

  const servers = value?.mcp?.servers ?? []
  const status = previewMut.isPending
    ? "loading"
    : errorMessage
      ? "error"
      : servers.length > 0
        ? "ready"
        : "idle"

  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(360px,1fr)]">
      {/* ---- LEFT: raw input ---- */}
      <div className="min-w-0 space-y-2">
        <FormatPicker value={format} onChange={setFormat} />
        <textarea
          value={raw}
          onChange={(e) => setRaw(e.target.value)}
          rows={20}
          placeholder={
            format === "toml"
              ? t(
                  "capabilities.import.mcp.tomlPlaceholder",
                  "[mcp_servers.github]\ncommand = \"docker\"\nargs = [\"run\", \"-i\", \"ghcr.io/github/github-mcp-server\"]\n\n[mcp_servers.github.env]\nGITHUB_PERSONAL_ACCESS_TOKEN = \"ghp_xxx\"",
                )
              : t(
                  "capabilities.import.mcp.jsonPlaceholder",
                  `{\n  "mcpServers": {\n    "github": {\n      "command": "docker",\n      "args": ["run", "-i", "ghcr.io/github/github-mcp-server"],\n      "env": {\n        "GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_xxx"\n      }\n    }\n  }\n}`,
                )
          }
          className="w-full rounded-md border border-line bg-surface px-3 py-2 font-mono text-xs leading-relaxed shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300"
          spellCheck={false}
          autoCorrect="off"
          autoCapitalize="off"
        />
        <p className="text-xs text-fg-subtle">
          {t(
            "capabilities.import.mcp.pasteHelp",
            "Supports Claude Code (env) / OpenCode (environment) JSON and Codex TOML. You can also paste just the inner mcpServers object.",
          )}
        </p>
      </div>

      {/* ---- RIGHT: parsed preview ---- */}
      <div className="min-w-0 space-y-3">
        <ImportPreview
          status={status as "idle" | "loading" | "error" | "ready"}
          errorMessage={errorMessage}
          warnings={warnings}
          suggestedName={status === "ready" ? servers[0]?.name : undefined}
          kind="mcp"
        />

        {status === "ready" && (
          <div className="space-y-3">
            {servers.map((srv) => (
              <ServerCard
                key={srv.name}
                workspaceID={workspaceID}
                server={srv}
                inlineSecrets={inlineSecrets}
                onServerChange={(next) => {
                  if (!value) return
                  const spec: CanonicalSpec = {
                    ...value,
                    mcp: {
                      servers: servers.map((s) => (s.name === srv.name ? next : s)),
                    },
                  }
                  onChange(spec)
                }}
                onInlineSecretChange={(envKey, plaintext) => {
                  const key = (s: ImportInlineSecretInput) =>
                    s.server_name === srv.name && s.env_key === envKey
                  const existing = inlineSecrets.find(key)
                  if (plaintext === "") {
                    onInlineSecretsChange(inlineSecrets.filter((s) => !key(s)))
                    return
                  }
                  if (existing) {
                    onInlineSecretsChange(
                      inlineSecrets.map((s) =>
                        key(s) ? { ...s, plaintext } : s,
                      ),
                    )
                  } else {
                    onInlineSecretsChange([
                      ...inlineSecrets,
                      { server_name: srv.name, env_key: envKey, plaintext },
                    ])
                  }
                }}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function FormatPicker({
  value,
  onChange,
}: {
  value: SourceFormat
  onChange: (f: SourceFormat) => void
}) {
  const { t } = useTranslation("admin")
  const options: { value: SourceFormat; label: string }[] = [
    { value: "json", label: "JSON" },
    { value: "toml", label: "TOML" },
  ]
  return (
    <div className="flex items-center gap-2 text-sm">
      <span className="font-medium text-fg-muted">
        {t("capabilities.import.mcp.format", "Format")}
      </span>
      <div className="flex overflow-hidden rounded-md border border-line bg-surface-subtle">
        {options.map((opt) => {
          const active = opt.value === value
          return (
            <button
              key={opt.value}
              type="button"
              onClick={() => onChange(opt.value)}
              className={`h-7 px-2.5 text-xs transition-colors ${
                active
                  ? "bg-surface text-fg shadow-inner"
                  : "text-fg-subtle hover:bg-surface-muted hover:text-fg-muted"
              }`}
              aria-pressed={active}
            >
              {opt.label}
            </button>
          )
        })}
      </div>
    </div>
  )
}

function startsWithEnvPlaceholder(value: string | undefined): boolean {
  return (value ?? "").trimStart().startsWith("$")
}

function normalizeEnvCredentialPlaceholders(spec: CanonicalSpec): CanonicalSpec {
  if (spec.kind !== "mcp" || !spec.mcp) return spec

  let changed = false
  const servers = spec.mcp.servers.map((server) => {
    const envEntries = Object.entries(server.env ?? {})
    if (envEntries.length === 0) return server

    let envChanged = false
    const env: Record<string, CanonicalEnvValue> = {}
    for (const [key, value] of envEntries) {
      if (value.mode === "literal" && startsWithEnvPlaceholder(value.literal)) {
        env[key] = { mode: "credential_ref", credential_kind_code: "" }
        envChanged = true
        changed = true
      } else {
        env[key] = value
      }
    }

    return envChanged ? { ...server, env } : server
  })

  return changed ? { ...spec, mcp: { ...spec.mcp, servers } } : spec
}

function needsEnvCredentialHandling(value: CanonicalEnvValue): boolean {
  if (value.mode !== "literal") return true
  return startsWithEnvPlaceholder(value.literal)
}

function ServerCard({
  workspaceID,
  server,
  inlineSecrets,
  onServerChange,
  onInlineSecretChange,
}: {
  workspaceID: string | null
  server: CanonicalMCPServer
  inlineSecrets: ImportInlineSecretInput[]
  onServerChange: (next: CanonicalMCPServer) => void
  onInlineSecretChange: (envKey: string, plaintext: string) => void
}) {
  const { t } = useTranslation("admin")
  const credentialEnvEntries = useMemo(
    () => Object.entries(server.env ?? {})
      .filter(([, ev]) => needsEnvCredentialHandling(ev as CanonicalEnvValue))
      .sort(([a], [b]) => a.localeCompare(b)),
    [server.env],
  )
  const inlineSecretFor = (envKey: string) =>
    inlineSecrets.find((s) => s.server_name === server.name && s.env_key === envKey)?.plaintext

  return (
    <section className="min-w-0 overflow-hidden rounded-lg border border-line bg-surface p-3">
      <header className="flex min-w-0 flex-col gap-1 border-b border-line-muted pb-2">
        <h4 className="text-sm font-semibold text-fg">{server.name}</h4>
        <code className="block max-w-full whitespace-pre-wrap break-all font-mono text-xs text-fg-subtle">
          {server.command}
          {server.args && server.args.length > 0 ? ` ${server.args.join(" ")}` : ""}
        </code>
      </header>

      {credentialEnvEntries.length === 0 ? (
        <p className="mt-2 text-sm text-fg-subtle">
          {t("capabilities.import.envEmpty.noCredentialPlaceholders", "No credential placeholders to fill")}
        </p>
      ) : (
        <div className="mt-2 space-y-2">
          {credentialEnvEntries.map(([key, ev]) => (
            <EnvCredentialPicker
              key={key}
              workspaceID={workspaceID}
              serverName={server.name}
              envKey={key}
              value={ev as CanonicalEnvValue}
              inlineSecretPlaintext={inlineSecretFor(key)}
              onChange={(next) => {
                onServerChange({
                  ...server,
                  env: { ...(server.env ?? {}), [key]: next },
                })
                // Drop held plaintext when switching away from inline_secret.
                if (next.mode !== "inline_secret") {
                  onInlineSecretChange(key, "")
                }
              }}
              onInlineSecretPlaintextChange={(plaintext) =>
                onInlineSecretChange(key, plaintext)
              }
            />
          ))}
        </div>
      )}
    </section>
  )
}
