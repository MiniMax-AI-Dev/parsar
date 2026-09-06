/**
 * System prompt capability create form.
 *
 * Unlike MCP / Skill / Plugin imports there is no parser to call — the
 * payload is just `prompt` + `mode` + a version label. Submission goes
 * through the regular POST /workspaces/.../capabilities endpoint (not
 * /import/preview), which the parent dialog wires up via
 * systemPromptCapabilityPayload.
 */
import { useTranslation } from "react-i18next"

import { Field } from "../../../components/ui/label"
import { Input } from "../../../components/ui/input"
import { Textarea } from "../../../components/ui/textarea"
import type { SystemPromptMode } from "./types"

export interface SystemPromptDraft {
  prompt: string
  mode: SystemPromptMode
  version: string
}

interface Props {
  value: SystemPromptDraft
  onChange: (next: SystemPromptDraft) => void
}

export function ImportSystemPromptForm({ value, onChange }: Props) {
  const { t } = useTranslation("admin")
  const set = (patch: Partial<SystemPromptDraft>) => onChange({ ...value, ...patch })

  return (
    <div className="space-y-3">
      <Field label={t("capabilities.import.systemPrompt.versionLabel", "Version")} htmlFor="system-prompt-version">
        <Input
          id="system-prompt-version"
          value={value.version}
          onChange={(e) => set({ version: e.target.value })}
          placeholder="1.0.0"
        />
      </Field>

      <Field label={t("capabilities.import.systemPrompt.modeLabel", "Injection mode")}>
        <div className="flex flex-wrap gap-4 text-sm text-fg">
          <label className="inline-flex items-center gap-2">
            <input
              type="radio"
              name="system-prompt-mode"
              value="append"
              className="accent-accent"
              checked={value.mode === "append"}
              onChange={() => set({ mode: "append" })}
            />
            {t("capabilities.import.systemPrompt.modeAppend", "Append (prepended to the user system prompt)")}
          </label>
          <label className="inline-flex items-center gap-2">
            <input
              type="radio"
              name="system-prompt-mode"
              value="override"
              className="accent-accent"
              checked={value.mode === "override"}
              onChange={() => set({ mode: "override" })}
            />
            {t("capabilities.import.systemPrompt.modeOverride", "Override (replaces the default system prompt)")}
          </label>
        </div>
      </Field>

      <Field label={t("capabilities.import.systemPrompt.promptLabel", "Prompt content")} htmlFor="system-prompt-content">
        <Textarea
          id="system-prompt-content"
          className="min-h-[260px] font-mono text-xs"
          value={value.prompt}
          onChange={(e) => set({ prompt: e.target.value })}
          placeholder={t(
            "capabilities.import.systemPrompt.promptPlaceholder",
            "Write the system prompt text you want injected into the agent. Override replaces the default system prompt; append prepends it to the user-supplied system_prompt.",
          )}
        />
      </Field>
    </div>
  )
}
