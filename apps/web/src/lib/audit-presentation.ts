import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import { useAgents } from "./api-agents"
import { useWorkspaceMembers } from "./api-members"
import type { AuditRecord } from "./api-types"

export interface ReadableAuditRecord extends AuditRecord {
  eventLabel: string
  actorLabel: string
  actorDetail: string
}

const EMPTY_RECORDS: AuditRecord[] = []

export function useReadableAuditRecords(workspaceID: string | null, records?: AuditRecord[]): ReadableAuditRecord[] {
  const { t } = useTranslation("admin")
  const rows = records ?? EMPTY_RECORDS
  const membersQ = useWorkspaceMembers(rows.some((row) => row.actor_type === "user" && row.actor_id) ? workspaceID : null, 500)
  const agentsQ = useAgents(rows.some((row) => row.actor_type === "agent" && row.actor_id) ? workspaceID : null, true)

  return useMemo(() => {
    const members = new Map((membersQ.data?.members ?? []).map((member) => [member.user_id, member.user_name.trim() || member.user_email]))
    const agents = new Map((agentsQ.data?.agents ?? []).map((agent) => [agent.id, agent.name]))
    const events = t("audit.events", { returnObjects: true }) as Record<string, string>
    return rows.map((record) => {
      const name = record.actor_id && (record.actor_type === "user"
        ? members.get(record.actor_id)
        : record.actor_type === "agent" ? agents.get(record.actor_id) : undefined)
      const type = t(`audit.actor.${record.actor_type}`, { defaultValue: record.actor_type })
      const event = events[record.event_type]
      const id = record.actor_id
      return {
        ...record,
        eventLabel: typeof event === "string" ? event : record.event_type,
        actorLabel: name || (id ? `${type} · ${id.length > 12 ? id.slice(0, 12) + "…" : id}` : type),
        actorDetail: id ? `${record.actor_type} · ${id}` : record.actor_type,
      }
    })
  }, [rows, membersQ.data, agentsQ.data, t])
}
