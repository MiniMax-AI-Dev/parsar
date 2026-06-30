import { useTranslation } from "react-i18next"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { useWorkspaceId } from "../../lib/workspace"

export function ConnectionsPage() {
  const { t } = useTranslation("admin")
  const wsId = useWorkspaceId()

  return (
    <AdminLayout activeMenu="connections">
      <PageHeader title={t("connections.page.title")} />

      {!wsId ? (
        <ScopeRequiredState scope="workspace" resourceName={t("connections.page.title")} />
      ) : null}
    </AdminLayout>
  )
}
