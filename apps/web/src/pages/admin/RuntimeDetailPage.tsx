import { useTranslation } from "react-i18next"
import { ArrowLeft, Box } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { SettingsTabs } from "../../components/layout/SettingsTabs"
import { Button } from "../../components/ui/button"
import { EmptyState } from "../../components/ui/empty-state"
import { ManagedBadge } from "../../components/ui/managed-badge"
import { useAdminView } from "../../lib/admin-router"

// Placeholder so the router can resolve `?admin=runtime&id=<sandbox_id>`.

interface RuntimeDetailPageProps {
  id: string
}

export function RuntimeDetailPage({ id }: RuntimeDetailPageProps) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  return (
    <AdminLayout activeMenu="settings">
      <PageHeader
        title={t("runtime.detail.placeholderTitle")}
        backLink={
          <Button variant="ghost" size="icon" onClick={() => navigate("runtime")} aria-label={t("runtime.page.title")}>
            <ArrowLeft strokeWidth={1.5} aria-hidden="true" />
          </Button>
        }
        action={<SettingsTabs active="runtime" />}
      />
      <div data-testid="runtime-detail-placeholder">
        <EmptyState
          icon={Box}
          title={t("runtime.detail.placeholderTitle")}
          description={t("runtime.detail.placeholderBody", { id })}
          className="py-10"
        />
      </div>
      <ManagedBoundary />
    </AdminLayout>
  )
}

/** What Parsar manages vs. what stays on the operator's side: two hairline lists. */
function ManagedBoundary() {
  const { t } = useTranslation("admin")

  return (
    <section className="mt-6">
      <h2 className="mb-2 text-sm font-medium text-fg">{t("runtime.managedBoundary.title")}</h2>
      <div className="grid gap-x-8 md:grid-cols-2">
        <BoundaryColumn
          title={t("runtime.managedBoundary.managedTitle")}
          items={[
            t("runtime.managedBoundary.managed.runtimeSecret"),
            t("runtime.managedBoundary.managed.actionCredentials"),
            t("runtime.managedBoundary.managed.toolCalls"),
          ]}
        />
        <BoundaryColumn
          title={t("runtime.managedBoundary.unmanagedTitle")}
          items={[
            t("runtime.managedBoundary.unmanaged.userEnv"),
            t("runtime.managedBoundary.unmanaged.manualSsh"),
            t("runtime.managedBoundary.unmanaged.directApi"),
          ]}
          unmanaged
        />
      </div>
    </section>
  )
}

function BoundaryColumn({
  title,
  items,
  unmanaged,
}: {
  title: string
  items: string[]
  unmanaged?: boolean
}) {
  return (
    <div>
      <h3 className="flex h-7 items-center justify-between gap-2 border-b border-line text-xs font-medium text-fg">
        <span>{title}</span>
        <ManagedBadge unmanaged={unmanaged} />
      </h3>
      <ul className="m-0 list-none p-0">
        {items.map((item) => (
          <li key={item} className="flex min-h-8 items-center border-b border-line text-sm text-fg">
            {item}
          </li>
        ))}
      </ul>
    </div>
  )
}
