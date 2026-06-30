import { useTranslation } from "react-i18next"
import type { ReactNode } from "react"
import { AlertTriangle, CheckCircle2 } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { SettingsTabs } from "../../components/layout/SettingsTabs"
import { ManagedBadge } from "../../components/ui/managed-badge"

// Placeholder so the router can resolve `?admin=runtime&id=<sandbox_id>`.

interface RuntimeDetailPageProps {
  id: string
}

export function RuntimeDetailPage({ id }: RuntimeDetailPageProps) {
  const { t } = useTranslation("admin")
  return (
    <AdminLayout activeMenu="settings">
      <PageHeader
        title={t("runtime.detail.placeholderTitle")}
        description={t("runtime.detail.placeholderDescription")}
      />
      <SettingsTabs active="runtime" />
      <div
        className="rounded-md border border-dashed border-line bg-surface-subtle/60 p-6 text-center text-sm text-fg-subtle"
        data-testid="runtime-detail-placeholder"
      >
        {t("runtime.detail.placeholderBody", { id })}
      </div>
      <ManagedBoundaryCard />
    </AdminLayout>
  )
}

function ManagedBoundaryCard() {
  const { t } = useTranslation("admin")

  return (
    <section className="mt-4 rounded-lg border border-line bg-surface p-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold text-fg">
            {t("runtime.managedBoundary.title")}
          </h2>
          <p className="mt-1 text-sm leading-relaxed text-fg-subtle">
            {t("runtime.managedBoundary.description")}
          </p>
        </div>
        <ManagedBadge className="shrink-0" />
      </div>
      <div className="mt-4 grid gap-3 md:grid-cols-2">
        <BoundaryColumn
          icon={<CheckCircle2 className="h-4 w-4 text-success" />}
          title={t("runtime.managedBoundary.managedTitle")}
          items={[
            t("runtime.managedBoundary.managed.runtimeSecret"),
            t("runtime.managedBoundary.managed.actionCredentials"),
            t("runtime.managedBoundary.managed.toolCalls"),
          ]}
        />
        <BoundaryColumn
          icon={<AlertTriangle className="h-4 w-4 text-fg-subtle" />}
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
  icon,
  title,
  items,
  unmanaged,
}: {
  icon: ReactNode
  title: string
  items: string[]
  unmanaged?: boolean
}) {
  return (
    <div className="rounded-md border border-line bg-surface-subtle/40 p-3">
      <div className="mb-2 flex items-center gap-2 text-sm font-semibold text-fg-emphasis">
        {icon}
        <span>{title}</span>
        <ManagedBadge unmanaged={unmanaged} className="ml-auto" />
      </div>
      <ul className="space-y-1.5 text-sm text-fg-muted">
        {items.map((item) => (
          <li key={item}>• {item}</li>
        ))}
      </ul>
    </div>
  )
}
