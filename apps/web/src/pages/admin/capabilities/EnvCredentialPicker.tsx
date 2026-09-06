/**
 * EnvCredentialPicker — one MCP env placeholder that must be resolved during
 * import.
 *
 * Ordinary env entries are not rendered here. Once an env value reaches this
 * component it needs an explicit source: either a team-shared encrypted secret
 * or the caller's personal credential.
 */
import { KeyRound, Lock } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Badge } from "../../../components/ui/badge"
import { Input } from "../../../components/ui/input"
import { Label } from "../../../components/ui/label"
import { Tabs, TabsList, TabsTrigger } from "../../../components/ui/tabs"

import { CredentialKindCombobox } from "./CredentialKindCombobox"
import type { CanonicalEnvValue, EnvMode } from "./types"

interface Props {
  workspaceID: string | null
  serverName: string
  envKey: string
  value: CanonicalEnvValue
  /**
   * Cleartext for inline_secret mode. Held by the parent so it can be sent
   * in the commit payload's `inline_secrets[]` array. Undefined when mode
   * is not inline_secret.
   */
  inlineSecretPlaintext: string | undefined
  onChange: (next: CanonicalEnvValue) => void
  /** Update the inline secret plaintext bag in the parent. */
  onInlineSecretPlaintextChange: (plaintext: string) => void
}

type CredentialMode = Exclude<EnvMode, "literal">

const MODE_OPTIONS: { value: CredentialMode; labelKey: string; fallback: string }[] = [
  { value: "inline_secret", labelKey: "capabilities.import.envMode.inlineSecret", fallback: "Team shared secret" },
  { value: "credential_ref", labelKey: "capabilities.import.envMode.credentialRef", fallback: "Personal credential" },
]

function startsWithEnvPlaceholder(value: string | undefined): boolean {
  return (value ?? "").trimStart().startsWith("$")
}

export function EnvCredentialPicker({
  workspaceID,
  serverName,
  envKey,
  value,
  inlineSecretPlaintext,
  onChange,
  onInlineSecretPlaintextChange,
}: Props) {
  const { t } = useTranslation("admin")
  const activeMode: CredentialMode =
    value.mode === "inline_secret" ? "inline_secret" : "credential_ref"
  const fieldID = `env-${serverName}-${envKey}`

  const setMode = (mode: CredentialMode) => {
    switch (mode) {
      case "inline_secret":
        // Placeholder literals such as ${TOKEN} are references, not the
        // secret value itself, so do not prefill them into the secret field.
        onInlineSecretPlaintextChange(
          value.mode === "literal" && value.literal && !startsWithEnvPlaceholder(value.literal)
            ? value.literal
            : "",
        )
        onChange({ mode: "inline_secret" })
        break
      case "credential_ref":
        onChange({ mode: "credential_ref", credential_kind_code: value.credential_kind_code ?? "" })
        break
    }
  }

  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-center gap-2">
        <code title={envKey} className="min-w-0 truncate font-mono text-xs font-medium text-fg">
          {envKey}
        </code>
        <Badge variant="warning" dot>{t("capabilities.import.envBadge.credential", "Credential")}</Badge>
      </div>

      <div className="mt-2">
        <Label>{t("capabilities.import.envMode.label", "Credential source")}</Label>
        <Tabs value={activeMode} onValueChange={(next) => setMode(next as CredentialMode)}>
          <TabsList>
            {MODE_OPTIONS.map((opt) => (
              <TabsTrigger key={opt.value} value={opt.value}>{t(opt.labelKey, opt.fallback)}</TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </div>

      <div className="mt-2">
        {activeMode === "inline_secret" && (
          <div>
            <Input
              id={fieldID}
              type="password"
              value={inlineSecretPlaintext ?? ""}
              onChange={(e) => onInlineSecretPlaintextChange(e.target.value)}
              className="font-mono"
              placeholder={t(
                "capabilities.import.envValue.inlineSecretPlaceholder",
                "Paste a team-shared token; we encrypt it on import.",
              )}
            />
            <p className="mt-1 flex items-start gap-1.5 text-xs text-fg-muted">
              <Lock className="mt-0.5 h-3 w-3 shrink-0" strokeWidth={1.5} aria-hidden="true" />
              {t(
                "capabilities.import.envValue.inlineSecretNote",
                "Best for shared service-account tokens. The config only stores a reference; plaintext is never persisted.",
              )}
            </p>
          </div>
        )}

        {activeMode === "credential_ref" && (
          <div>
            <CredentialKindCombobox
              workspaceID={workspaceID}
              value={value.mode === "credential_ref" ? value.credential_kind_code ?? "" : ""}
              onChange={(code) => onChange({ mode: "credential_ref", credential_kind_code: code })}
            />
            <p className="mt-1 flex items-start gap-1.5 text-xs text-fg-muted">
              <KeyRound className="mt-0.5 h-3 w-3 shrink-0" strokeWidth={1.5} aria-hidden="true" />
              {t(
                "capabilities.import.envValue.credentialRefNote",
                "Best for personal tokens like a GitLab PAT — at runtime we use the caller's value from My Credentials.",
              )}
            </p>
          </div>
        )}
      </div>
    </div>
  )
}
