import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { Check, PackageCheck, Server } from "lucide-react"

import { Button } from "../../../../components/ui/button"
import { EmptyState } from "../../../../components/ui/empty-state"
import { ErrorState } from "../../../../components/ui/error-state"
import { Skeleton } from "../../../../components/ui/skeleton"
import {
  useImportMCPDirectoryItem,
  useMCPDirectory,
  useMCPDirectoryDetail,
  mcpDirectoryOAuthStartURL,
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
}

export function MCPDirectory({
  itemID,
  query,
  canImport,
  onSelectItem,
  onViewCapability,
}: MCPDirectoryProps) {
  const { t } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const directoryQ = useMCPDirectory(workspaceID)
  const importMut = useImportMCPDirectoryItem(workspaceID)
  const [category, setCategory] = useState("")
  const [verifiedOnly, setVerifiedOnly] = useState(false)
  const [sort, setSort] = useState<DirectorySort>("featured")
	const [confirmID, setConfirmID] = useState<string | null>(null)
	const [oauthError, setOAuthError] = useState(false)
  const [success, setSuccess] = useState<{ name: string; capabilityID: string } | null>(null)
  const detailID = confirmID ?? itemID
  const detailQ = useMCPDirectoryDetail(workspaceID, detailID)

  const items = useMemo(() => directoryQ.data?.items ?? [], [directoryQ.data?.items])
  const categories = useMemo(
    () => Array.from(new Set(items.flatMap((item) => item.categories))).sort((left, right) => left.localeCompare(right)),
    [items],
  )
  const filtered = useMemo(
    () => filterMCPDirectoryItems(items, { query, category, verifiedOnly, sort }),
    [items, query, category, verifiedOnly, sort],
  )
  const selectedSummary = items.find((item) => item.id === itemID) ?? null
  const selected = detailQ.data?.id === itemID ? detailQ.data : selectedSummary
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

  if (itemID) {
    return (
      <>
        {success ? <SuccessBanner success={success} onViewCapability={onViewCapability} /> : null}
		{oauthError ? <p className="mb-3 rounded-lg border border-line bg-surface px-4 py-3 text-sm text-danger">{t("capabilities.mcpDirectory.oauth.failed")}</p> : null}
        <DirectoryDetail
          item={selected}
          loading={detailQ.isLoading}
          error={detailQ.error}
          canImport={canImport}
          onBack={() => onSelectItem(null)}
          onRetry={() => void detailQ.refetch()}
          onImport={() => requestImport(itemID)}
		  onConnect={() => connectOAuth(itemID)}
          onViewCapability={onViewCapability}
        />
        {importDialog}
      </>
    )
  }

  return (
    <div className="space-y-4" data-testid="mcp-directory">
      <div className="rounded-xl border border-line bg-surface px-5 py-4">
        <div className="flex flex-wrap items-start gap-3">
          <div>
            <div className="flex items-center gap-2">
              <Server className="h-4 w-4 text-fg-subtle" />
              <h2 className="text-lg font-semibold text-fg">{t("capabilities.mcpDirectory.title")}</h2>
            </div>
            <p className="mt-1 max-w-2xl text-sm leading-5 text-fg-muted">{t("capabilities.mcpDirectory.description")}</p>
          </div>
        </div>
        <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <div className="flex min-w-0 flex-1 flex-wrap gap-1.5" aria-label={t("capabilities.mcpDirectory.filters.category")}>
            <FilterChip active={!category} onClick={() => setCategory("")}>{t("capabilities.mcpDirectory.filters.allCategories")}</FilterChip>
            {categories.map((value) => <FilterChip key={value} active={category === value} onClick={() => setCategory(value)}>{value}</FilterChip>)}
          </div>
          <label className="inline-flex h-8 select-none items-center gap-2 rounded-md border border-line bg-surface px-2.5 text-sm text-fg-muted">
            <input type="checkbox" checked={verifiedOnly} onChange={(event) => setVerifiedOnly(event.target.checked)} className="h-3.5 w-3.5 rounded border-line-strong" />
            {t("capabilities.mcpDirectory.filters.verified")}
          </label>
          <select aria-label={t("capabilities.mcpDirectory.filters.sort")} value={sort} onChange={(event) => setSort(event.target.value as DirectorySort)} className="h-8 rounded-md border border-line bg-surface px-2.5 text-sm text-fg-muted outline-none focus:border-line-strong">
            <option value="featured">{t("capabilities.mcpDirectory.sort.featured")}</option>
            <option value="name">{t("capabilities.mcpDirectory.sort.name")}</option>
          </select>
        </div>
      </div>

      {success ? <SuccessBanner success={success} onViewCapability={onViewCapability} /> : null}
	  {oauthError ? <p className="rounded-lg border border-line bg-surface px-4 py-3 text-sm text-danger">{t("capabilities.mcpDirectory.oauth.failed")}</p> : null}
      {directoryQ.isLoading ? (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3" data-testid="mcp-directory-loading">
          {Array.from({ length: 6 }).map((_, index) => <Skeleton key={index} className="h-52 w-full" />)}
        </div>
      ) : directoryQ.error ? (
        <ErrorState title={t("capabilities.mcpDirectory.loadError.title")} description={t("capabilities.mcpDirectory.loadError.description")} onRetry={() => void directoryQ.refetch()} />
      ) : filtered.length === 0 ? (
        <EmptyState icon={PackageCheck} title={t("capabilities.mcpDirectory.empty.title")} description={t("capabilities.mcpDirectory.empty.description")} />
      ) : (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
		  {filtered.map((item) => <DirectoryCard key={item.id} item={item} canImport={canImport} onOpen={() => onSelectItem(item.id)} onImport={() => requestImport(item.id)} onConnect={() => connectOAuth(item.id)} onViewCapability={onViewCapability} />)}
        </div>
      )}
      {importDialog}
    </div>
  )
}

function SuccessBanner({ success, onViewCapability }: {
  success: { name: string; capabilityID: string }
  onViewCapability: (capabilityID: string) => void
}) {
  const { t } = useTranslation("admin")
  return (
    <div className="mb-3 flex flex-wrap items-center gap-3 rounded-lg border border-line bg-surface px-4 py-3" role="status">
      <span className="flex h-7 w-7 items-center justify-center rounded-full bg-surface-muted text-fg"><Check className="h-4 w-4" /></span>
      <p className="min-w-0 flex-1 text-sm text-fg">{t("capabilities.mcpDirectory.import.success", { name: success.name })}</p>
      <Button variant="outline" size="sm" onClick={() => onViewCapability(success.capabilityID)}>{t("capabilities.mcpDirectory.actions.viewCapability")}</Button>
    </div>
  )
}

function FilterChip({ active, onClick, children }: { active: boolean; onClick: () => void; children: string }) {
  return <button type="button" aria-pressed={active} onClick={onClick} className={`h-8 rounded-md border px-2.5 text-sm transition-colors ${active ? "border-line-strong bg-surface-muted text-fg" : "border-line bg-surface text-fg-muted hover:border-line-strong hover:text-fg"}`}>{children}</button>
}
