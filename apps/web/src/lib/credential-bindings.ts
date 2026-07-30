import type { Secret, UserCredential } from "./api-types"

export type PerKindBindingChoice =
  | { source: "personal" }
  | { source: "shared"; existing_secret_id: string }
  | { source: "shared"; new_secret: { display_name: string; plaintext: string } }

export type CredentialBinding = {
  source: "personal" | "shared"
  secretID: string
}

export function secretCredentialKind(secret: Secret) {
  const value = secret.metadata?.credential_kind_code
  return typeof value === "string" ? value.trim() : ""
}

export function sharedSecretsForKind(secrets: Secret[], kind: string, catalogID = "") {
  return secrets.filter((secret) => {
    const secretKind = secretCredentialKind(secret)
    const matchesKind = secret.kind === "capability_inline"
      && secret.status === "active"
      && (secretKind === "" || secretKind === kind)
    if (!matchesKind) return false
    if (kind !== "mcp_oauth" || !catalogID) return true
    return secretKind === kind
      && secret.auth_type === "oauth2"
      && secret.provider === catalogID
  })
}

export function credentialBinding(config: Record<string, unknown> | undefined, kind: string): CredentialBinding | undefined {
  const bindings = config?.credential_bindings
  if (!bindings || typeof bindings !== "object" || Array.isArray(bindings)) return undefined
  const binding = (bindings as Record<string, unknown>)[kind]
  if (!binding || typeof binding !== "object" || Array.isArray(binding)) return undefined
  const value = binding as Record<string, unknown>
  if (value.source !== "personal" && value.source !== "shared") return undefined
  return {
    source: value.source,
    secretID: value.source === "shared" && typeof value.secret_id === "string" ? value.secret_id : "",
  }
}

export function hasCredentialKind(credentials: UserCredential[], kind: string) {
  return credentials.some((credential) => credential.kind === kind)
}
