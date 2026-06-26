import { useTranslation } from "react-i18next"
import {
  Bot,
  HelpCircle,
  PackageSearch,
  Sparkles,
} from "lucide-react"

import { useAdminView, useAppRoute } from "../../lib/admin-router"
import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { EmptyState } from "../../components/ui/empty-state"
import { Button } from "../../components/ui/button"
import { ModelsPage } from "./ModelsPage"
import { ConversationsPage } from "./ConversationsPage"
import { RunsPage, RunDetailPage } from "./RunsPage"
import { AgentsPage, AgentDetailPage } from "./AgentsPage"
import { ConnectorsPage, ConnectorDetailPage } from "./ConnectorsPage"
import { SettingsPage } from "./SettingsPage"
import { AuditPage } from "./AuditPage"
import { UsagePage } from "./UsagePage"
import { CredentialsPage } from "./credentials/CredentialsPage"
import { MembersPage } from "./MembersPage"
import { RuntimePage } from "./RuntimePage"
import { RuntimeDetailPage } from "./RuntimeDetailPage"
import { StubPage } from "./StubPage"
import { CapabilitiesPage, CapabilityDetailPage } from "./CapabilitiesPage"
import { ConnectionsPage } from "./ConnectionsPage"
import { MyCredentialsPage } from "./MyCredentialsPage"
import { SpecsPage } from "./SpecsPage"
import { MemoryPage } from "./MemoryPage"

/**
 * Top-level admin router. Reads `?admin=<view>&id=<entity?>` and renders
 * the matching page. Unknown views render <NotFoundView /> rather than
 * silently falling through.
 */
export function AdminRouter() {
  const appRoute = useAppRoute()
  const { view, entityId } = useAdminView()

  if (appRoute.mode === "profile") {
    return <MyCredentialsPage />
  }

  const v = view ?? "agents"

  if (v === "agents") {
    return entityId ? <AgentDetailPage id={entityId} /> : <AgentsPage />
  }
  if (v === "models") return <ModelsPage />
  if (v === "settings") return <SettingsPage />
  if (v === "audit") return <AuditPage />
  if (v === "usage") return <UsagePage />
  if (v === "capabilities")
    return entityId ? <CapabilityDetailPage id={entityId} /> : <CapabilitiesPage />
  if (v === "connections") return <ConnectionsPage />
  if (v === "specs") return <SpecsPage />
  if (v === "memory") return <MemoryPage />
  if (v === "runtime") {
    return entityId ? <RuntimeDetailPage id={entityId} /> : <RuntimePage />
  }
  if (v === "conversations") {
    // ConversationsPage owns both list + detail (sidebar shows the agent +
    // conv list, main shows the selected conversation). Reads ?id= internally.
    return <ConversationsPage />
  }
  if (v === "runs") {
    return entityId ? <RunDetailPage id={entityId} /> : <RunsPage />
  }
  if (v === "connectors") {
    return entityId ? <ConnectorDetailPage id={entityId} /> : <ConnectorsPage />
  }

  switch (v) {
    case "artifacts":
      return <StubPage view={v} itemKey="artifacts" icon={PackageSearch} />
    case "updates":
      return <StubPage view={v} itemKey="updates" icon={Sparkles} />
    case "members":
      return <MembersPage />
    case "secrets":
      return <CredentialsPage />
    default:
      return <NotFoundView />
  }
}

function NotFoundView() {
  const { t } = useTranslation("common")
  const { navigate } = useAdminView()
  return (
    <AdminLayout activeMenu="agents">
      <PageHeader title={t("notFound.title")} />
      <EmptyState
        icon={HelpCircle}
        title={t("notFound.empty.title")}
        description={t("notFound.empty.description")}
        action={
          <Button onClick={() => navigate("agents")} size="sm">
            <Bot className="h-3.5 w-3.5" />
            {t("notFound.empty.cta")}
          </Button>
        }
      />
    </AdminLayout>
  )
}
