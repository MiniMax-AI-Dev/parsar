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
import type { MCPDirectoryItem } from "../../../../lib/api-marketplace"
import { formatCommandPart } from "./utils"

export function ImportMCPDialog({
  open,
  item,
  loading,
  error,
  pending,
  mutationError,
  onRetry,
  onOpenChange,
  onConnect,
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
  onConnect: () => void
  onConfirm: () => void
}) {
  const { t } = useTranslation("admin")
  const command = item?.command
    ? [item.command, ...(item.args ?? [])].map(formatCommandPart).join(" ")
    : ""
  const isRemote = item?.transport === "streamable-http"
  const needsOAuth = item?.authentication === "oauth2" && !item.connected
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
            <Skeleton className="h-20 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : error ? (
          <ErrorState
            title={t("capabilities.mcpDirectory.detail.loadError")}
            description={error instanceof Error ? error.message : ""}
            onRetry={onRetry}
          />
        ) : item ? (
          <div className="min-w-0 space-y-3">
            <div className="min-w-0">
              <p className="text-xs text-fg-subtle">
                {t(
                  isRemote
                    ? "capabilities.mcpDirectory.detail.endpoint"
                    : "capabilities.mcpDirectory.detail.command",
                )}
              </p>
              <pre className="mt-1.5 max-w-full overflow-x-auto rounded-md border border-line bg-surface-muted/35 p-3 font-mono text-xs leading-5 text-fg">
                {isRemote ? item.url : command}
              </pre>
            </div>
            <div>
              <p className="text-xs text-fg-subtle">
                {t(
                  isRemote
                    ? "capabilities.mcpDirectory.detail.authentication"
                    : "capabilities.mcpDirectory.detail.environment",
                )}
              </p>
              <p className="mt-1.5 font-mono text-xs text-fg">
                {isRemote
                  ? item.authentication === "oauth2"
                    ? item.connected
                      ? t("capabilities.mcpDirectory.oauth.connected")
                      : t("capabilities.mcpDirectory.oauth.authorizeBeforeImport")
                    : t("capabilities.mcpDirectory.detail.noAuthentication")
                  : item.env?.join(", ") || t("capabilities.mcpDirectory.detail.noEnvironment")}
              </p>
            </div>
            <p className="rounded-md border border-line bg-surface-muted/25 p-3 text-xs leading-5 text-fg-muted">
              {t("capabilities.mcpDirectory.securityNotice")}
            </p>
          </div>
        ) : null}
        {mutationError ? (
          <p className="text-sm text-destructive">
            {mutationError instanceof Error
              ? mutationError.message
              : t("capabilities.mcpDirectory.import.failed")}
          </p>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
            {t("capabilities.mcpDirectory.import.cancel")}
          </Button>
          {needsOAuth ? (
            <Button onClick={onConnect} disabled={!item || loading || !!error || item.installed}>
              {t("capabilities.mcpDirectory.oauth.authorizeAndContinue")}
            </Button>
          ) : (
            <Button
              onClick={onConfirm}
              disabled={!item || loading || !!error || pending || item.installed}
            >
              {pending
                ? t("capabilities.mcpDirectory.import.importing")
                : t("capabilities.mcpDirectory.actions.import")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
