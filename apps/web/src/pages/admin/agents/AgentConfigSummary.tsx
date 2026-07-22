import { useTranslation } from "react-i18next"

import { Badge } from "../../../components/ui/badge"
import {
  agentConnectorLabel,
  agentCodexModeOf,
  agentEngineLabel,
  agentEngineOf,
  agentExecutionModeOf,
  agentSandboxSizeOf,
  agentWorkdirOf,
} from "../../../lib/agent-view-model"
import type { AgentDetail } from "../../../lib/api-types"

export function AgentConfigSummary({
  agent,
  modelLabel,
}: {
  agent: AgentDetail
  modelLabel: string
}) {
  const { t, i18n } = useTranslation("admin")
  const config = agent.config ?? {}
  const profile = (config.profile ?? {}) as Record<string, unknown>
  const systemPrompt = String(config.system_prompt ?? profile.system_prompt ?? "").trim()
  const executionMode = agentExecutionModeOf(agent)
  const agentEngine = agentEngineOf(agent)

  return (
    <>
      <SummaryCard title={t("agents.detail.config.identity.title")}>
        <SummaryField label={t("agents.detail.config.identity.slug")} value={agent.slug} mono />
        <SummaryField
          label={t("agents.detail.config.identity.visibility")}
          value={t(`agents.visibility.${agent.visibility ?? "workspace"}`)}
        />
        <SummaryField label={t("agents.detail.config.identity.agentId")} value={agent.id} mono />
        <SummaryField
          label={t("agents.detail.config.identity.created")}
          value={formatDate(agent.created_at, i18n.language)}
        />
        <SummaryField
          label={t("agents.detail.config.identity.updated")}
          value={formatDate(agent.updated_at, i18n.language)}
        />
      </SummaryCard>

      <SummaryCard title={t("agents.detail.config.intelligence.title")}>
        <SummaryField
          label={t("agents.detail.config.intelligence.engine")}
          value={t(agentEngineLabel(agentEngine))}
        />
        {agentEngine === "codex" && (
          <SummaryField
            label={t("agents.detail.config.intelligence.codexMode")}
            value={t(`agents.form.codexMode.${agentCodexModeOf(agent)}`)}
          />
        )}
        <SummaryField label={t("agents.detail.config.runtime.model")} value={modelLabel} mono />
        <SummaryField
          label={t("agents.detail.config.intelligence.systemPrompt")}
          value={
            systemPrompt ? (
              <p className="whitespace-pre-wrap break-words text-fg-muted">{systemPrompt}</p>
            ) : (
              <span className="text-fg-faint">—</span>
            )
          }
        />
      </SummaryCard>

      <SummaryCard title={t("agents.detail.config.runtime.title")}>
        <SummaryField
          label={t("agents.detail.config.runtime.execution")}
          value={
            <Badge variant={executionMode === "sandbox" ? "success" : "neutral"} dot>
              {t(
                `agents.execution.${executionMode === "local_device" ? "localDevice" : executionMode}.title`,
              )}
            </Badge>
          }
        />
        <SummaryField
          label={t("agents.detail.config.runtime.connector")}
          value={
            <span>
              {agentConnectorLabel(agent.connector_type)}
              <span className="ml-1 text-sm text-fg-subtle">· {agent.connector_type}</span>
            </span>
          }
        />
        <SummaryField
          label={t("agents.detail.config.runtime.workdir")}
          value={agentWorkdirOf(agent) || "—"}
          mono
        />
        {executionMode === "sandbox" && (
          <SummaryField
            label={t("agents.detail.config.runtime.sandboxSize")}
            value={t(`agents.form.sandboxSize.${agentSandboxSizeOf(agent)}`)}
          />
        )}
        <SummaryField
          label={t("agents.detail.config.runtime.binding")}
          value={agent.runtime_name || agent.runtime_id || "—"}
          mono
        />
      </SummaryCard>
    </>
  )
}

function SummaryCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="rounded-lg border border-line bg-surface p-4">
      <h3 className="mb-3 text-base font-semibold text-fg">{title}</h3>
      <dl>{children}</dl>
    </section>
  )
}

function SummaryField({
  label,
  value,
  mono,
}: {
  label: string
  value: React.ReactNode
  mono?: boolean
}) {
  return (
    <div className="mb-2 last:mb-0">
      <dt className="mb-0.5 text-xs uppercase tracking-wider text-fg-faint">{label}</dt>
      <dd className={`text-sm text-fg-emphasis ${mono ? "font-mono" : ""}`}>{value}</dd>
    </div>
  )
}

function formatDate(value: string | undefined, language: string): string {
  return value ? new Date(value).toLocaleString(language) : "—"
}
