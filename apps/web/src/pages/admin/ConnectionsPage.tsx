import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { ChannelConnectorPanel } from "../../components/admin/channel-connectors/ChannelConnectorPanel"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import { useWorkspaceId } from "../../lib/workspace"

export function ConnectionsPage() {
  const { t } = useTranslation("admin")
  const wsId = useWorkspaceId()
  const { data: myWorkspaces } = useMyWorkspaces()
  const [toast, setToast] = useState<string | null>(null)

  const canEdit = useMemo(() => {
    const role = myWorkspaces?.workspaces.find((w) => w.id === wsId)?.role
    return role === "owner" || role === "admin"
  }, [myWorkspaces, wsId])

  return (
    <AdminLayout activeMenu="connections">
      <PageHeader title={t("connections.page.title")} />

      {!wsId ? (
        <ScopeRequiredState scope="workspace" resourceName={t("connections.page.title")} />
      ) : (
        <>
          {toast && (
            <div className="mb-4 rounded-md border border-success-border bg-success-subtle px-3 py-2 text-sm text-success-emphasis">
              {toast}
            </div>
          )}
          <ChannelConnectorPanel workspaceID={wsId} canEdit={canEdit} onToast={setToast} />
        </>
      )}
    </AdminLayout>
  )
}
