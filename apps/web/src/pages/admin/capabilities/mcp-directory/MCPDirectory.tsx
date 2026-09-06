import { useEffect, useMemo, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import { ArrowUpRight, Download, Link2, PackageCheck } from "lucide-react"

import { ActionIconButton, RowActions } from "../../../../components/ui/action-button"
import { Badge } from "../../../../components/ui/badge"
import { Button } from "../../../../components/ui/button"
import { EmptyState } from "../../../../components/ui/empty-state"
import { ErrorState } from "../../../../components/ui/error-state"
import { RailLayout } from "../../../../components/ui/detail-rail"
import { Ledger, LedgerHeader, LedgerRow, col } from "../../../../components/ui/ledger"
import { Skeleton } from "../../../../components/ui/skeleton"
import {
  useImportMCPDirectoryItem,
  useMCPDirectory,
  useMCPDirectoryDetail,
  mcpDirectoryOAuthStartURL,
  type MCPDirectoryItem,
} from "../../../../lib/api-marketplace"
import { useWorkspaceId } from "../../../../lib/workspace"
import { InlineNotice } from "../notices"
import { ConnectorIcon, VerifiedBadge } from "./shared"
import { DirectoryDetail } from "./MCPDirectoryDetail"
import { ImportMCPDialog } from "./ImportMCPDialog"
import { filterMCPDirectoryItems, type DirectoryFilterState } from "./filters"

interface MCPDirectoryProps {
  itemID: string | null
  query: string
  filters: DirectoryFilterState
  canImport: boolean
  onSelectItem: (id: string | null) => void
  onViewCapability: (capabilityID: string) => void
}

/** connector (+badges, +description) · publisher · version · categories · authentication · actions */
const DIRECTORY_COLUMNS = [col.title(), col.meta(132), col.id(72, 0.4), col.meta(132), col.meta(110), col.actions(1)]

export function MCPDirectory({
  itemID,
  query,
  filters,
  canImport,
  onSelectItem,
  onViewCapability,
}: MCPDirectoryProps) {
  const { t } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const directoryQ = useMCPDirectory(workspaceID)
  const importMut = useImportMCPDirectoryItem(workspaceID)
  const [confirmID, setConfirmID] = useState<string | null>(null)
  const [oauthError, setOAuthError] = useState(false)
  const [success, setSuccess] = useState<{ name: string; capabilityID: string } | null>(null)
  const detailID = confirmID ?? itemID
  const detailQ = useMCPDirectoryDetail(workspaceID, detailID)

  const items = useMemo(() => directoryQ.data?.items ?? [], [directoryQ.data?.items])
  const filtered = useMemo(
    () => filterMCPDirectoryItems(items, { query, ...filters }),
    [items, query, filters],
  )
  // Hold the id through the rail's exit so closing animates instead of
  // vanishing — the same pattern every ledger uses.
  const [railID, setRailID] = useState<string | null>(itemID)
  if (itemID && itemID !== railID) setRailID(itemID)
  const railSummary = items.find((item) => item.id === railID) ?? null
  const railItem = detailQ.data?.id === railID ? detailQ.data : railSummary
  const confirmItem = detailQ.data?.id === confirmID ? detailQ.data : items.find((item) => item.id === confirmID) ?? null

  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      if (event.origin !== window.location.origin || event.data?.type !== "parsar:mcp-oauth") return
      setOAuthError(Boolean(event.data.error))
      if (!event.data.error) {
        void directoryQ.refetch()
        void detailQ.refetch()
      }
    }
    window.addEventListener("message", onMessage)
    return () => window.removeEventListener("message", onMessage)
  }, [detailQ, directoryQ])

  const connectOAuth = (id: string) => {
    if (!workspaceID) return
    setOAuthError(false)
    const width = 620
    const height = 760
    const left = Math.max(0, Math.round(window.screenX + (window.outerWidth - width) / 2))
    const top = Math.max(0, Math.round(window.screenY + (window.outerHeight - height) / 2))
    const popup = window.open(
      mcpDirectoryOAuthStartURL(workspaceID, id),
      `parsar-mcp-oauth-${id}`,
      `popup=yes,width=${width},height=${height},left=${left},top=${top}`,
    )
    if (!popup) {
      setOAuthError(true)
      return
    }
    popup.focus()
  }

  const requestImport = (id: string) => {
    if (!canImport) return
    const item = items.find((candidate) => candidate.id === id)
    if (item?.authentication === "oauth2" && !item.connected) {
      connectOAuth(id)
      return
    }
    importMut.reset()
    setConfirmID(id)
  }
  const closeImportDialog = () => {
    importMut.reset()
    setConfirmID(null)
  }
  const confirmImport = () => {
    if (!confirmID || !confirmItem || confirmItem.installed) return
    importMut.mutate(confirmID, {
      onSuccess: (result) => {
        setSuccess({ name: confirmItem.name, capabilityID: result.capability_id })
        closeImportDialog()
      },
    })
  }

  const importDialog = (
    <ImportMCPDialog
      open={confirmID !== null}
      item={confirmItem}
      loading={detailQ.isLoading}
      error={detailQ.error}
      pending={importMut.isPending}
      mutationError={importMut.error}
      onRetry={() => void detailQ.refetch()}
      onOpenChange={(open) => !open && closeImportDialog()}
      onConfirm={confirmImport}
    />
  )

  const notices = (
    <>
      {success ? <SuccessNotice success={success} onViewCapability={onViewCapability} /> : null}
      {oauthError ? <InlineNotice tone="error" className="border-b border-line px-4 py-2">{t("capabilities.mcpDirectory.oauth.failed")}</InlineNotice> : null}
    </>
  )

  const rail = railID ? (
    <DirectoryDetail
      item={railItem}
      loading={detailQ.isLoading}
      error={detailQ.error}
      canImport={canImport}
      open={!!itemID}
      onClosed={() => setRailID(null)}
      onClose={() => onSelectItem(null)}
      onRetry={() => void detailQ.refetch()}
      onImport={() => requestImport(railID)}
      onConnect={() => connectOAuth(railID)}
      onViewCapability={onViewCapability}
    />
  ) : null

  return (
    <RailLayout rail={rail}>
      {notices}
      {directoryQ.error ? (
        <div className="px-4 pt-4">
          <ErrorState title={t("capabilities.mcpDirectory.loadError.title")} description={t("capabilities.mcpDirectory.loadError.description")} onRetry={() => void directoryQ.refetch()} />
        </div>
      ) : directoryQ.isLoading ? (
        <div className="px-4 pt-3" data-testid="mcp-directory-loading">
          <div className="mb-3 h-7 border-b border-line" />
          {Array.from({ length: 6 }).map((_, index) => (
            <div key={index} className="flex h-9 items-center gap-3 border-b border-line">
              <Skeleton className="h-[18px] w-[18px] rounded" />
              <Skeleton className="h-3 w-40" />
              <Skeleton className="h-3 flex-1" />
              <Skeleton className="h-3 w-16" />
            </div>
          ))}
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState icon={PackageCheck} title={t("capabilities.mcpDirectory.empty.title")} description={t("capabilities.mcpDirectory.empty.description")} />
      ) : (
        <Ledger columns={DIRECTORY_COLUMNS} role="listbox" aria-label={t("capabilities.mcpDirectory.title")} data-testid="mcp-directory">
          <LedgerHeader>
            <span>{t("capabilities.mcpDirectory.title")}</span>
            <span>{t("capabilities.mcpDirectory.detail.publisher")}</span>
            <span>{t("capabilities.mcpDirectory.detail.version")}</span>
            <span>{t("capabilities.mcpDirectory.filters.category")}</span>
            <span>{t("capabilities.mcpDirectory.detail.authentication")}</span>
            <span />
          </LedgerHeader>
          <ul className="m-0 list-none p-0">
            {filtered.map((item) => (
              <DirectoryRow
                key={item.id}
                item={item}
                canImport={canImport}
                selected={item.id === itemID}
                onOpen={() => onSelectItem(item.id === itemID ? null : item.id)}
                onImport={() => requestImport(item.id)}
                onConnect={() => connectOAuth(item.id)}
                onViewCapability={onViewCapability}
              />
            ))}
          </ul>
        </Ledger>
      )}
      {importDialog}
    </RailLayout>
  )
}

function DirectoryRow({ item, canImport, selected, onOpen, onImport, onConnect, onViewCapability }: {
  item: MCPDirectoryItem
  canImport: boolean
  selected: boolean
  onOpen: () => void
  onImport: () => void
  onConnect: () => void
  onViewCapability: (capabilityID: string) => void
}) {
  const { t } = useTranslation("admin")
  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.target !== e.currentTarget) return
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onOpen()
    }
  }
  const auth = item.authentication === "oauth2"
    ? item.connected
      ? t("capabilities.mcpDirectory.oauth.connected")
      : t("capabilities.mcpDirectory.oauth.required")
    : t("capabilities.mcpDirectory.detail.noAuthentication")
  return (
    <LedgerRow selected={selected} onClick={onOpen} onKeyDown={onKeyDown} data-testid="mcp-directory-row" data-catalog-id={item.id}>
      <span className="flex min-w-0 items-center gap-2">
        <ConnectorIcon item={item} />
        <span className="shrink-0 truncate font-medium">{item.name}</span>
        {item.verified ? <VerifiedBadge /> : null}
        {item.installed ? (
          <Badge variant="neutral" dot>{t("capabilities.mcpDirectory.actions.installed")}</Badge>
        ) : item.connected ? (
          <Badge variant="neutral" dot>{t("capabilities.mcpDirectory.oauth.connected")}</Badge>
        ) : null}
        {item.description && <span className="min-w-0 truncate text-xs text-fg-muted">· {item.description}</span>}
      </span>
      <span className="truncate text-xs text-fg-muted">{item.publisher.name}</span>
      <span className="truncate font-mono text-xs text-fg">{item.version || "—"}</span>
      <span className="truncate text-xs text-fg-muted">{item.categories.join(" · ") || "—"}</span>
      <span className="truncate text-xs text-fg-muted">{auth}</span>
      <span onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
        <RowActions>
          {item.installed && item.installed_capability_id ? (
            <ActionIconButton icon={ArrowUpRight} label={t("capabilities.mcpDirectory.actions.viewCapability")} onClick={() => onViewCapability(item.installed_capability_id!)} />
          ) : item.authentication === "oauth2" && !item.connected ? (
            <ActionIconButton icon={Link2} label={t("capabilities.mcpDirectory.oauth.connect")} onClick={onConnect} />
          ) : (
            <ActionIconButton
              icon={Download}
              label={canImport ? t("capabilities.mcpDirectory.actions.import") : t("capabilities.permission.adminOnly")}
              disabled={!canImport}
              onClick={onImport}
            />
          )}
        </RowActions>
      </span>
    </LedgerRow>
  )
}

function SuccessNotice({ success, onViewCapability }: {
  success: { name: string; capabilityID: string }
  onViewCapability: (capabilityID: string) => void
}) {
  const { t } = useTranslation("admin")
  return (
    <InlineNotice
      tone="success"
      className="border-b border-line px-4 py-2"
      action={
        <Button variant="link" size="sm" onClick={() => onViewCapability(success.capabilityID)}>
          {t("capabilities.mcpDirectory.actions.viewCapability")}
          <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
        </Button>
      }
    >
      {t("capabilities.mcpDirectory.import.success", { name: success.name })}
    </InlineNotice>
  )
}
