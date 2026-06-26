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

import { Input } from "../../../components/ui/input"
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
    <div className="space-y-4">
      <Field
        label={t("capabilities.import.systemPrompt.versionLabel", "版本号")}
        required
      >
        <Input
          value={value.version}
          onChange={(e) => set({ version: e.target.value })}
          placeholder="1.0.0"
        />
      </Field>

      <Field label={t("capabilities.import.systemPrompt.modeLabel", "注入模式")} required>
        <div className="flex gap-4 text-[13px]">
          <label className="inline-flex items-center gap-2">
            <input
              type="radio"
              name="system-prompt-mode"
              value="append"
              checked={value.mode === "append"}
              onChange={() => set({ mode: "append" })}
            />
            {t("capabilities.import.systemPrompt.modeAppend", "Append（拼到用户 system prompt 前）")}
          </label>
          <label className="inline-flex items-center gap-2">
            <input
              type="radio"
              name="system-prompt-mode"
              value="override"
              checked={value.mode === "override"}
              onChange={() => set({ mode: "override" })}
            />
            {t("capabilities.import.systemPrompt.modeOverride", "Override（完全替换）")}
          </label>
        </div>
      </Field>

      <Field label={t("capabilities.import.systemPrompt.promptLabel", "Prompt 内容")} required>
        <textarea
          className="min-h-[260px] w-full rounded-md border border-slate-200 bg-white p-3 font-mono text-[12.5px] leading-relaxed text-slate-800 focus:outline-none focus:ring-2 focus:ring-slate-300"
          value={value.prompt}
          onChange={(e) => set({ prompt: e.target.value })}
          placeholder={t(
            "capabilities.import.systemPrompt.promptPlaceholder",
            "在这里写你想注入到 agent 的 system prompt 文本。Override 模式会替换默认 system prompt;append 模式会拼到用户自己写的 system_prompt 前面。",
          )}
        />
      </Field>
    </div>
  )
}

function Field({
  label,
  required,
  children,
}: {
  label: string
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <label className="grid gap-1.5">
      <span className="text-[12px] font-medium text-slate-700">
        {label}
        {required && <span className="text-red-500"> *</span>}
      </span>
      {children}
    </label>
  )
}
