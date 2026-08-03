import { useTranslation } from "react-i18next"

import { Button } from "../../../../components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../../../components/ui/dialog"
import { ErrorState } from "../../../../components/ui/error-state"
import { Skeleton } from "../../../../components/ui/skeleton"
import type { SkillDirectoryItem } from "../../../../lib/api-marketplace"

export function ImportSkillDirectoryDialog({
  open,
  item,
  loading,
  error,
  pending,
  mutationError,
  onRetry,
  onOpenChange,
  onConfirm,
}: {
  open: boolean
  item: SkillDirectoryItem | null
  loading: boolean
  error: unknown
  pending: boolean
  mutationError: unknown
  onRetry: () => void
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}) {
  const { t } = useTranslation("admin")
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("capabilities.skillDirectory.import.title", { name: item?.name ?? "" })}</DialogTitle>
          <DialogDescription>{t("capabilities.skillDirectory.import.description")}</DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="space-y-2">
            <Skeleton className="h-20 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : error ? (
          <ErrorState title={t("capabilities.skillDirectory.detail.loadError")} description={error instanceof Error ? error.message : ""} onRetry={onRetry} />
        ) : item ? (
          <div className="space-y-3 text-sm">
            <div className="grid grid-cols-2 gap-2">
              <Meta label={t("capabilities.skillDirectory.detail.version")} value={item.version} />
              <Meta label={t("capabilities.skillDirectory.detail.license")} value={item.license} />
            </div>
            <div className="rounded-md border border-line bg-surface-muted/25 p-3">
              <p className="text-xs leading-5 text-fg-muted">{t("capabilities.skillDirectory.securityNotice")}</p>
              <p className="mt-2 text-xs text-fg-subtle">{item.files?.length ?? 0} {t("capabilities.skillDirectory.detail.supportingFiles")}</p>
            </div>
          </div>
        ) : null}
        {mutationError ? <p className="text-sm text-destructive">{mutationError instanceof Error ? mutationError.message : t("capabilities.skillDirectory.import.failed")}</p> : null}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>{t("capabilities.skillDirectory.import.cancel")}</Button>
          <Button onClick={onConfirm} disabled={!item || loading || !!error || pending || item.installed}>
            {pending ? t("capabilities.skillDirectory.import.importing") : t("capabilities.skillDirectory.actions.import")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Meta({ label, value }: { label: string; value: string }) {
  return <div className="rounded-md border border-line p-2.5"><p className="text-xs text-fg-subtle">{label}</p><p className="mt-1 text-sm text-fg">{value}</p></div>
}
