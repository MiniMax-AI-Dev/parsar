import { useTranslation } from "react-i18next"
import type { ReactNode } from "react"
import { AlertTriangle, CheckCircle2 } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ManagedBadge } from "../../components/ui/managed-badge"

// Placeholder so the router can resolve `?admin=runtime&id=<sandbox_id>`.

interface RuntimeDetailPageProps {
  id: string
}

export function RuntimeDetailPage({ id }: RuntimeDetailPageProps) {
  const { t } = useTranslation("admin")
  return (
    <AdminLayout activeMenu="runtime">
      <PageHeader
        title={t("runtime.detail.placeholderTitle")}
        description={t("runtime.detail.placeholderDescription")}
      />
      <div
        className="rounded-md border border-dashed border-slate-200 bg-slate-50/60 p-6 text-center text-[13px] text-slate-500"
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
    <section className="mt-4 rounded-lg border border-slate-200 bg-white p-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h2 className="text-[13px] font-semibold text-slate-900">
            {t("runtime.managedBoundary.title")}
          </h2>
          <p className="mt-1 text-[12px] leading-relaxed text-slate-500">
            {t("runtime.managedBoundary.description")}
          </p>
        </div>
        <ManagedBadge className="shrink-0" />
      </div>
      <div className="mt-4 grid gap-3 md:grid-cols-2">
        <BoundaryColumn
          icon={<CheckCircle2 className="h-4 w-4 text-emerald-600" />}
          title={t("runtime.managedBoundary.managedTitle")}
          items={[
            t("runtime.managedBoundary.managed.runtimeSecret"),
            t("runtime.managedBoundary.managed.actionCredentials"),
            t("runtime.managedBoundary.managed.toolCalls"),
          ]}
        />
        <BoundaryColumn
          icon={<AlertTriangle className="h-4 w-4 text-slate-500" />}
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
    <div className="rounded-md border border-slate-200 bg-slate-50/40 p-3">
      <div className="mb-2 flex items-center gap-2 text-[12px] font-semibold text-slate-800">
        {icon}
        <span>{title}</span>
        <ManagedBadge unmanaged={unmanaged} className="ml-auto" />
      </div>
      <ul className="space-y-1.5 text-[12px] text-slate-600">
        {items.map((item) => (
          <li key={item}>• {item}</li>
        ))}
      </ul>
    </div>
  )
}
