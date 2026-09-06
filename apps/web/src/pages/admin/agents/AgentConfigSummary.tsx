import { useTranslation } from "react-i18next"

import { PropertyList, Property } from "../../../components/ui/property-list"
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
import { DetailSection } from "./DetailSection"

/* The page is wide, so the label column grows past the rail's 84px. */
export const CONFIG_PROPERTY_LIST = "max-w-3xl grid-cols-[140px_minmax(0,1fr)]"

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
  const workdir = agentWorkdirOf(agent)
  const binding = agent.runtime_name || agent.runtime_id

  return (
    <>
      <DetailSection title={t("agents.detail.config.identity.title")}>
        <PropertyList className={CONFIG_PROPERTY_LIST}>
          <Property label={t("agents.detail.config.identity.slug")} mono>{agent.slug}</Property>
          <Property label={t("agents.detail.config.identity.visibility")}>
            {t(`agents.visibility.${agent.visibility ?? "workspace"}`)}
          </Property>
          <Property label={t("agents.detail.config.identity.agentId")} mono>{agent.id}</Property>
          <Property label={t("agents.detail.config.identity.created")} mono>
            {formatDate(agent.created_at, i18n.language)}
          </Property>
          <Property label={t("agents.detail.config.identity.updated")} mono>
            {formatDate(agent.updated_at, i18n.language)}
          </Property>
        </PropertyList>
      </DetailSection>

      <DetailSection title={t("agents.detail.config.intelligence.title")}>
        <PropertyList className={CONFIG_PROPERTY_LIST}>
          <Property label={t("agents.detail.config.intelligence.engine")}>
            {t(agentEngineLabel(agentEngine))}
          </Property>
          {agentEngine === "codex" && (
            <Property label={t("agents.detail.config.intelligence.codexMode")}>
              {t(`agents.form.codexMode.${agentCodexModeOf(agent)}`)}
            </Property>
          )}
          <Property label={t("agents.detail.config.runtime.model")} mono>{modelLabel}</Property>
          <Property
            label={t("agents.detail.config.intelligence.systemPrompt")}
            className={systemPrompt ? "h-auto min-h-7 whitespace-pre-wrap py-1 [overflow-wrap:anywhere]" : "text-fg-muted"}
          >
            {systemPrompt || "—"}
          </Property>
        </PropertyList>
      </DetailSection>

      <DetailSection title={t("agents.detail.config.runtime.title")}>
        <PropertyList className={CONFIG_PROPERTY_LIST}>
          <Property label={t("agents.detail.config.runtime.execution")}>
            {t(`agents.execution.${executionMode === "local_device" ? "localDevice" : executionMode}.title`)}
          </Property>
          <Property label={t("agents.detail.config.runtime.connector")}>
            <span className="truncate">{agentConnectorLabel(agent.connector_type)}</span>
            <span className="truncate font-mono text-xs text-fg-muted">{agent.connector_type}</span>
          </Property>
          <Property label={t("agents.detail.config.runtime.workdir")} mono className={workdir ? undefined : "text-fg-muted"}>
            {workdir || "—"}
          </Property>
          {executionMode === "sandbox" && (
            <Property label={t("agents.detail.config.runtime.sandboxSize")}>
              {t(`agents.form.sandboxSize.${agentSandboxSizeOf(agent)}`)}
            </Property>
          )}
          <Property label={t("agents.detail.config.runtime.binding")} mono className={binding ? undefined : "text-fg-muted"}>
            {binding || "—"}
          </Property>
        </PropertyList>
      </DetailSection>
    </>
  )
}

function formatDate(value: string | undefined, language: string): string {
  return value ? new Date(value).toLocaleString(language) : "—"
}
