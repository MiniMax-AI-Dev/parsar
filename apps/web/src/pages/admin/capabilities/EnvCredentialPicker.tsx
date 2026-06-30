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

import { Input } from "../../../components/ui/input"
import { Button } from "../../../components/ui/button"
import { cn } from "../../../lib/utils"

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
  { value: "inline_secret", labelKey: "capabilities.import.envMode.inlineSecret", fallback: "团队共享密钥" },
  { value: "credential_ref", labelKey: "capabilities.import.envMode.credentialRef", fallback: "个人凭据" },
]

function startsWithEnvPlaceholder(value: string | undefined): boolean {
  return (value ?? "").trimStart().startsWith("$")
}

export function EnvCredentialPicker({
  workspaceID,
  serverName: _serverName,
  envKey,
  value,
  inlineSecretPlaintext,
  onChange,
  onInlineSecretPlaintextChange,
}: Props) {
  const { t } = useTranslation("admin")
  const activeMode: CredentialMode =
    value.mode === "inline_secret" ? "inline_secret" : "credential_ref"

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
    <div className="min-w-0 overflow-hidden rounded-md border border-line bg-surface p-3">
      <div className="flex min-w-0 flex-col gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <code
            title={envKey}
            className="block max-w-full flex-1 break-all rounded bg-surface-subtle px-1.5 py-1 font-mono text-sm font-medium text-fg"
          >
            {envKey}
          </code>
          <span className="shrink-0 rounded bg-warning-subtle px-1.5 py-0.5 text-xs font-medium text-warning">
            {t("capabilities.import.envBadge.credential", "凭据")}
          </span>
        </div>

        <div className="space-y-1.5">
          <div className="text-xs font-medium text-fg-subtle">
            {t("capabilities.import.envMode.label", "凭据来源")}
          </div>
          <ModeToggle value={activeMode} onChange={setMode} />
        </div>
      </div>

      <div className="mt-2.5">
        {activeMode === "inline_secret" && (
          <div className="space-y-1.5">
            <Input
              type="password"
              value={inlineSecretPlaintext ?? ""}
              onChange={(e) => onInlineSecretPlaintextChange(e.target.value)}
              className="font-mono text-sm"
              placeholder={t(
                "capabilities.import.envValue.inlineSecretPlaceholder",
                "粘贴团队共用 token，导入时加密保存",
              )}
            />
            <p className="flex items-center gap-1.5 text-xs text-success">
              <Lock className="h-3 w-3" />
              {t(
                "capabilities.import.envValue.inlineSecretNote",
                "适合团队共用的服务账号 token；提交后配置里只保留密钥引用，不保存明文。",
              )}
            </p>
          </div>
        )}

        {activeMode === "credential_ref" && (
          <div className="space-y-1.5">
            <CredentialKindCombobox
              workspaceID={workspaceID}
              value={value.mode === "credential_ref" ? value.credential_kind_code ?? "" : ""}
              onChange={(code) => onChange({ mode: "credential_ref", credential_kind_code: code })}
              className="w-full"
            />
            <p className="flex items-center gap-1.5 text-xs text-fg-subtle">
              <KeyRound className="h-3 w-3" />
              {t(
                "capabilities.import.envValue.credentialRefNote",
                "适合 GitLab PAT 这类个人 token；运行时使用调用者在「我的凭据」里绑定的值。",
              )}
            </p>
          </div>
        )}
      </div>
    </div>
  )
}

function ModeToggle({
  value,
  onChange,
}: {
  value: CredentialMode
  onChange: (mode: CredentialMode) => void
}) {
  const { t } = useTranslation("admin")
  return (
    <div className="flex w-full flex-wrap gap-1 rounded-md border border-line bg-surface-subtle p-1">
      {MODE_OPTIONS.map((opt) => {
        const active = opt.value === value
        return (
          <Button
            key={opt.value}
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => onChange(opt.value)}
            className={cn(
              "h-7 flex-1 rounded px-2 text-xs sm:flex-none",
              active
                ? "bg-surface text-fg shadow-inner"
                : "text-fg-subtle hover:bg-surface-muted hover:text-fg-muted",
            )}
            aria-pressed={active}
          >
            {t(opt.labelKey, opt.fallback)}
          </Button>
        )
      })}
    </div>
  )
}
