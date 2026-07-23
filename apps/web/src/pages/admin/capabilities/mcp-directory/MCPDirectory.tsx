import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { Check, PackageCheck, Server } from "lucide-react"

import { Badge } from "../../../../components/ui/badge"
import { Button } from "../../../../components/ui/button"
import { EmptyState } from "../../../../components/ui/empty-state"
import { ErrorState } from "../../../../components/ui/error-state"
import { Skeleton } from "../../../../components/ui/skeleton"
import {
  mcpDirectoryOAuthStartURL,
  useImportMCPDirectoryItem,
  useMCPDirectory,
  useMCPDirectoryDetail,
  useTestMCPDirectoryConnection,
} from "../../../../lib/api-marketplace"
import { useWorkspaceId } from "../../../../lib/workspace"
import { DirectoryCard } from "./MCPDirectoryCard"
import { DirectoryDetail } from "./MCPDirectoryDetail"
import { ImportMCPDialog } from "./ImportMCPDialog"
import { filterMCPDirectoryItems, type DirectorySort } from "./filters"

interface MCPDirectoryProps {
  itemID: string | null
  query: string
  canImport: boolean
  onSelectItem: (id: string | null) => void
  onViewCapability: (capabilityID: string) => void
  onAddToAgent: (capabilityID: string) => void
}

const MCP_OAUTH_COMPLETE = "parsar:mcp-oauth-complete"

interface MCPOAuthCompleteMessage {
  type: typeof MCP_OAUTH_COMPLETE
  catalogID: string
  intent?: "import"
}

function isMCPOAuthCompleteMessage(value: unknown): value is MCPOAuthCompleteMessage {
  if (!value || typeof value !== "object") return false
  const message = value as Partial<MCPOAuthCompleteMessage>
  return (
    message.type === MCP_OAUTH_COMPLETE &&
    typeof message.catalogID === "string" &&
    message.catalogID.trim() !== "" &&
    (message.intent === undefined || message.intent === "import")
  )
}

export function MCPDirectory({
  itemID,
  query,
  canImport,
  onSelectItem,
  onViewCapability,
  onAddToAgent,
}: MCPDirectoryProps) {
  const { t } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const directoryQ = useMCPDirectory(workspaceID)
  const importMut = useImportMCPDirectoryItem(workspaceID)
  const connectionMut = useTestMCPDirectoryConnection(workspaceID)
  const [category, setCategory] = useState("")
  const [verifiedOnly, setVerifiedOnly] = useState(false)
  const [sort, setSort] = useState<DirectorySort>("featured")
  const [confirmID, setConfirmID] = useState<string | null>(null)
  const [success, setSuccess] = useState<{ name: string; capabilityID: string } | null>(null)
  const detailID = confirmID ?? itemID
  const detailQ = useMCPDirectoryDetail(workspaceID, detailID)

  const items = useMemo(() => directoryQ.data?.items ?? [], [directoryQ.data?.items])
  const categories = useMemo(
    () =>
      Array.from(new Set(items.flatMap((item) => item.categories))).sort((left, right) =>
        left.localeCompare(right),
      ),
    [items],
  )
  const filtered = useMemo(
    () => filterMCPDirectoryItems(items, { query, category, verifiedOnly, sort }),
    [items, query, category, verifiedOnly, sort],
  )
  const selectedSummary = items.find((item) => item.id === itemID) ?? null
  const selected = detailQ.data?.id === itemID ? detailQ.data : selectedSummary
  const connectionTestIsCurrent = connectionMut.variables?.catalogID === itemID
  const confirmItem =
    detailQ.data?.id === confirmID
      ? detailQ.data
      : (items.find((item) => item.id === confirmID) ?? null)

  useEffect(() => {
    if (!itemID || !directoryQ.isSuccess) return
    if (items.some((item) => item.id === itemID)) return
    onSelectItem(null)
  }, [directoryQ.isSuccess, itemID, items, onSelectItem])

  useEffect(() => {
    const currentURL = new URL(window.location.href)
    const connectedID = currentURL.searchParams.get("connected")?.trim()
    if (!connectedID || !window.opener || window.opener.closed) return

    const message: MCPOAuthCompleteMessage = {
      type: MCP_OAUTH_COMPLETE,
      catalogID: connectedID,
      intent: currentURL.searchParams.has("import") ? "import" : undefined,
    }
    window.opener.postMessage(message, window.location.origin)
    window.close()
  }, [])

  useEffect(() => {
    const handleOAuthComplete = (event: MessageEvent<unknown>) => {
      if (event.origin !== window.location.origin || !isMCPOAuthCompleteMessage(event.data)) return
      const { catalogID, intent } = event.data
      if (intent === "import" && canImport) setConfirmID(catalogID)
      void directoryQ.refetch()
      if (detailID === catalogID) void detailQ.refetch()
    }
    window.addEventListener("message", handleOAuthComplete)
    return () => window.removeEventListener("message", handleOAuthComplete)
  }, [canImport, detailID, detailQ, directoryQ])

  useEffect(() => {
    const currentURL = new URL(window.location.href)
    const importID = currentURL.searchParams.get("import")?.trim()
    if (!importID) return

    currentURL.searchParams.delete("import")
    window.history.replaceState(
      window.history.state,
      "",
      `${currentURL.pathname}${currentURL.search}${currentURL.hash}`,
    )
    // The OAuth callback URL is external input that must open the import
    // dialog before Strict Mode re-runs this effect after the URL is cleaned.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (canImport) setConfirmID(importID)
  }, [canImport])

  const requestImport = (id: string) => {
    if (!canImport) return
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
  const connectOAuth = (id: string, intent?: "import") => {
    if (!workspaceID) return
    const oauthURL = new URL(
      mcpDirectoryOAuthStartURL(workspaceID, id, { intent }),
      window.location.origin,
    ).toString()
    const width = 560
    const height = 720
    const left = Math.max(0, Math.round(window.screenX + (window.outerWidth - width) / 2))
    const top = Math.max(0, Math.round(window.screenY + (window.outerHeight - height) / 2))
    const popup = window.open(
      oauthURL,
      `parsar-mcp-oauth-${id}`,
      `popup=yes,width=${width},height=${height},left=${left},top=${top}`,
    )
    if (!popup) {
      window.location.assign(oauthURL)
      return
    }
    popup.focus()
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
      onConnect={() => confirmID && connectOAuth(confirmID, "import")}
      onConfirm={confirmImport}
    />
  )

  if (itemID) {
    return (
      <>
        {success ? (
          <SuccessBanner
            success={success}
            onViewCapability={onViewCapability}
            onAddToAgent={onAddToAgent}
          />
        ) : null}
        <DirectoryDetail
          item={selected}
          loading={detailQ.isLoading}
          error={detailQ.error}
          canImport={canImport}
          onBack={() => onSelectItem(null)}
          onRetry={() => void detailQ.refetch()}
          onImport={() => requestImport(itemID)}
          onConnect={() => connectOAuth(itemID)}
          onTestConnection={() => connectionMut.mutate({ catalogID: itemID })}
          testingConnection={connectionTestIsCurrent && connectionMut.isPending}
          connectionTestSucceeded={
            connectionTestIsCurrent && connectionMut.isSuccess && connectionMut.data.verified
          }
          connectionTestFailed={
            connectionTestIsCurrent && connectionMut.isSuccess && !connectionMut.data.verified
          }
          connectionTestError={connectionTestIsCurrent ? connectionMut.error : null}
          onViewCapability={onViewCapability}
          onAddToAgent={onAddToAgent}
        />
        {importDialog}
      </>
    )
  }

  return (
    <div className="space-y-4" data-testid="mcp-directory">
      <div className="rounded-xl border border-line bg-surface px-5 py-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <div className="flex items-center gap-2">
              <Server className="h-4 w-4 text-fg-subtle" />
              <h2 className="text-lg font-semibold text-fg">
                {t("capabilities.mcpDirectory.title")}
              </h2>
            </div>
            <p className="mt-1 max-w-2xl text-sm leading-5 text-fg-muted">
              {t("capabilities.mcpDirectory.description")}
            </p>
          </div>
          {directoryQ.data?.source ? (
            <Badge variant="neutral">
              {t(`capabilities.mcpDirectory.source.${directoryQ.data.source}`)}
            </Badge>
          ) : null}
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <div
            className="flex min-w-0 flex-1 flex-wrap gap-1.5"
            aria-label={t("capabilities.mcpDirectory.filters.category")}
          >
            <FilterChip active={!category} onClick={() => setCategory("")}>
              {t("capabilities.mcpDirectory.filters.allCategories")}
            </FilterChip>
            {categories.map((value) => (
              <FilterChip
                key={value}
                active={category === value}
                onClick={() => setCategory(value)}
              >
                {value}
              </FilterChip>
            ))}
          </div>
          <label className="inline-flex h-8 select-none items-center gap-2 rounded-md border border-line bg-surface px-2.5 text-sm text-fg-muted">
            <input
              type="checkbox"
              checked={verifiedOnly}
              onChange={(event) => setVerifiedOnly(event.target.checked)}
              className="h-3.5 w-3.5 rounded border-line-strong"
            />
            {t("capabilities.mcpDirectory.filters.verified")}
          </label>
          <select
            aria-label={t("capabilities.mcpDirectory.filters.sort")}
            value={sort}
            onChange={(event) => setSort(event.target.value as DirectorySort)}
            className="h-8 rounded-md border border-line bg-surface px-2.5 text-sm text-fg-muted outline-none focus:border-line-strong"
          >
            <option value="featured">{t("capabilities.mcpDirectory.sort.featured")}</option>
            <option value="name">{t("capabilities.mcpDirectory.sort.name")}</option>
          </select>
        </div>
      </div>

      {success ? (
        <SuccessBanner
          success={success}
          onViewCapability={onViewCapability}
          onAddToAgent={onAddToAgent}
        />
      ) : null}
      {directoryQ.isLoading ? (
        <div
          className="grid gap-3 md:grid-cols-2 xl:grid-cols-3"
          data-testid="mcp-directory-loading"
        >
          {Array.from({ length: 6 }).map((_, index) => (
            <Skeleton key={index} className="h-52 w-full" />
          ))}
        </div>
      ) : directoryQ.error ? (
        <ErrorState
          title={t("capabilities.mcpDirectory.loadError.title")}
          description={t("capabilities.mcpDirectory.loadError.description")}
          onRetry={() => void directoryQ.refetch()}
        />
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={PackageCheck}
          title={t("capabilities.mcpDirectory.empty.title")}
          description={t("capabilities.mcpDirectory.empty.description")}
        />
      ) : (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {filtered.map((item) => (
            <DirectoryCard
              key={item.id}
              item={item}
              canImport={canImport}
              onOpen={() => onSelectItem(item.id)}
              onImport={() => requestImport(item.id)}
              onViewCapability={onViewCapability}
            />
          ))}
        </div>
      )}
      {importDialog}
    </div>
  )
}

function SuccessBanner({
  success,
  onViewCapability,
  onAddToAgent,
}: {
  success: { name: string; capabilityID: string }
  onViewCapability: (capabilityID: string) => void
  onAddToAgent: (capabilityID: string) => void
}) {
  const { t } = useTranslation("admin")
  return (
    <div
      className="mb-3 flex flex-wrap items-center gap-3 rounded-lg border border-line bg-surface px-4 py-3"
      role="status"
    >
      <span className="flex h-7 w-7 items-center justify-center rounded-full bg-surface-muted text-fg">
        <Check className="h-4 w-4" />
      </span>
      <p className="min-w-0 flex-1 text-sm text-fg">
        {t("capabilities.mcpDirectory.import.success", { name: success.name })}
      </p>
      <Button variant="outline" size="sm" onClick={() => onViewCapability(success.capabilityID)}>
        {t("capabilities.mcpDirectory.actions.viewCapability")}
      </Button>
      <Button size="sm" onClick={() => onAddToAgent(success.capabilityID)}>
        {t("capabilities.mcpDirectory.actions.addToAgent")}
      </Button>
    </div>
  )
}

function FilterChip({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: string
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={`h-8 rounded-md border px-2.5 text-sm transition-colors ${active ? "border-line-strong bg-surface-muted text-fg" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg"}`}
    >
      {children}
    </button>
  )
}
