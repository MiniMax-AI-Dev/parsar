import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"

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
import { PropertyList, Property } from "../../../../components/ui/property-list"
import { Skeleton } from "../../../../components/ui/skeleton"
import type { MCPDirectoryItem } from "../../../../lib/api-marketplace"
import { InlineNotice } from "../notices"

export function ImportMCPDialog({
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
  item: MCPDirectoryItem | null
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
          <DialogTitle>
            {t("capabilities.mcpDirectory.import.title", { name: item?.name ?? "" })}
          </DialogTitle>
          <DialogDescription>{t("capabilities.mcpDirectory.import.description")}</DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="space-y-2">
            <Skeleton className="h-3 w-full" />
            <Skeleton className="h-3 w-2/3" />
          </div>
        ) : error ? (
          <ErrorState
            title={t("capabilities.mcpDirectory.detail.loadError")}
            description={error instanceof Error ? error.message : ""}
            onRetry={onRetry}
          />
        ) : item ? (
          <div className="min-w-0 space-y-3">
            <PropertyList className="grid-cols-[120px_minmax(0,1fr)]">
              <Property label={t("capabilities.mcpDirectory.detail.endpoint")} mono className="h-auto min-h-7 whitespace-normal break-all py-1">
                {item.url || "—"}
              </Property>
              <Property label={t("capabilities.mcpDirectory.detail.authentication")}>
                {item.authentication === "oauth2"
                  ? item.connected
                    ? t("capabilities.mcpDirectory.oauth.connected")
                    : t("capabilities.mcpDirectory.oauth.required")
                  : t("capabilities.mcpDirectory.detail.noAuthentication")}
              </Property>
            </PropertyList>
            <p className="text-xs text-fg-muted">{t("capabilities.mcpDirectory.securityNotice")}</p>
          </div>
        ) : null}
        {mutationError ? (
          <InlineNotice tone="error">
            {mutationError instanceof Error ? mutationError.message : t("capabilities.mcpDirectory.import.failed")}
          </InlineNotice>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
            {t("capabilities.mcpDirectory.import.cancel")}
          </Button>
          <Button
            onClick={onConfirm}
            disabled={!item || loading || !!error || pending || item.installed || (item.authentication === "oauth2" && !item.connected)}
          >
            {pending && <Loader2 className="animate-spin" />}
            {pending ? t("capabilities.mcpDirectory.import.importing") : t("capabilities.mcpDirectory.actions.import")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
