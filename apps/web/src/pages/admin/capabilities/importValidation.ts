/**
 * Submit-button readiness check shared by the create-capability and
 * add-version dialogs.
 */
import type {
  CanonicalKind,
  CanonicalSpec,
  ImportInlineSecretInput,
} from "./types"

/** Composite key for an env slot inside a server (server_name + env_key). */
export function envID(serverName: string, envKey: string): string {
  return serverName + "\x00" + envKey
}

/**
 * Env values whose literal starts with `$` are unresolved placeholders the
 * importer reflects back from the user's paste (e.g. `${GITHUB_TOKEN}` from a
 * docker-compose-style snippet). Treated as not-yet-handled by the readiness
 * check.
 */
export function startsWithEnvPlaceholder(value: string | undefined): boolean {
  return (value ?? "").trimStart().startsWith("$")
}

/**
 * Returns true when the spec + provisional inline secrets are commit-ready.
 *
 * skill: non-empty instruction.
 * mcp: every env entry "ready" — literal not a `$…` placeholder,
 *      credential_ref has a kind code, inline_secret has either a
 *      server-allocated secret_id or a queued plaintext.
 */
export function isImportSpecReady(
  kind: CanonicalKind,
  spec: CanonicalSpec,
  inlineSecrets: ImportInlineSecretInput[],
): boolean {
  if (kind === "skill") return (spec.skill?.instruction?.length ?? 0) > 0

  const servers = spec.mcp?.servers ?? []
  if (servers.length === 0) return false

  const inlineSecretKeys = new Set(
    inlineSecrets
      .filter((secret) => secret.plaintext !== "")
      .map((secret) => envID(secret.server_name, secret.env_key)),
  )

  for (const server of servers) {
    for (const [envKey, value] of Object.entries(server.env ?? {})) {
      if (value.mode === "literal" && startsWithEnvPlaceholder(value.literal)) return false
      if (value.mode === "credential_ref" && !value.credential_kind_code?.trim()) return false
      if (
        value.mode === "inline_secret" &&
        !value.secret_id?.trim() &&
        !inlineSecretKeys.has(envID(server.name, envKey))
      ) {
        return false
      }
    }
  }

  return true
}
