import type { LucideIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Construction } from "lucide-react"
import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { EmptyState } from "../../components/ui/empty-state"
import { Button } from "../../components/ui/button"
import type { AdminView } from "../../lib/admin-router"

interface StubPageProps {
  view: AdminView
  /** key under nav.items.* — same key used in sidebar */
  itemKey: string
  icon?: LucideIcon
  /** optional richer description */
  hint?: string
}

/**
 * Reusable placeholder for pages that aren't fully built yet.
 * Renders a proper PageHeader + EmptyState so navigation works
 * end-to-end while individual pages get filled in.
 */
export function StubPage({ view, itemKey, icon, hint }: StubPageProps) {
  const { t } = useTranslation("common")
  return (
    <AdminLayout activeMenu={view}>
      <PageHeader
        title={t(`nav.items.${itemKey}` as never)}
        description={hint}
      />
      <EmptyState
        icon={icon ?? Construction}
        title={t("stub.title")}
        action={
          <Button size="sm" variant="outline" onClick={() => history.back()}>
            {t("stub.back")}
          </Button>
        }
      />
    </AdminLayout>
  )
}
