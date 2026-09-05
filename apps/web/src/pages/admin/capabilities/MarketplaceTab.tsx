import { useMemo, useState, type KeyboardEvent, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { ArrowUpRight, ChevronDown, ChevronRight, Download, File, FileText, Folder, FolderOpen, PackageCheck, Trash2 } from "lucide-react"

import { PageHeader } from "../../../components/layout/PageHeader"
import { ActionIconButton, RowActions } from "../../../components/ui/action-button"
import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import { RailSection } from "../../../components/ui/detail-rail"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Ledger, LedgerHeader, LedgerNum, LedgerRow, col } from "../../../components/ui/ledger"
import { PropertyList, Property } from "../../../components/ui/property-list"
import { Skeleton } from "../../../components/ui/skeleton"
import {
  marketplaceSourceName,
  useMarketplaceDetail,
  useMarketplaceList,
  type MarketplaceCapability,
  type MarketplaceCapabilityDetail,
  type MarketplaceMCPEnvValue,
  type MarketplaceSkillDetail,
} from "../../../lib/api-marketplace"
import { useWorkspaceId } from "../../../lib/workspace"
import { cn } from "../../../lib/utils"
import { requiredCredentialsLabel } from "../capability-ui"
import { CapabilityTypeBadge } from "./CapabilityTypeBadge"
import { BackLink, ExternalLinkValue, safeExternalURL } from "./notices"
import { MCPDirectory } from "./mcp-directory/MCPDirectory"
import type { DirectoryFilterState } from "./mcp-directory/filters"
import { SkillsDirectory } from "./SkillsDirectory"

interface MarketplaceTabProps {
  view: "marketplace" | "connectors" | "skills"
  itemID: string | null
  query: string
  typeFilter: "" | "mcp" | "skill"
  hideInstalled: boolean
  directoryFilters: DirectoryFilterState
  canImport: boolean
  canManage: boolean
  onSelectItem: (id: string | null) => void
  onInstall: (capability: MarketplaceCapability) => void
  onDelete: (capability: MarketplaceCapability) => void
  onViewCapability: (capabilityID: string) => void
}

export function MarketplaceTab(props: MarketplaceTabProps) {
  const mcpItemID = props.itemID?.startsWith("mcp:") ? props.itemID.slice(4) : null
  if (mcpItemID !== null || props.view === "connectors") {
    return (
      <MCPDirectory
        itemID={mcpItemID}
        query={props.query}
        filters={props.directoryFilters}
        canImport={props.canImport}
        onSelectItem={(id) => props.onSelectItem(id ? `mcp:${id}` : null)}
        onViewCapability={props.onViewCapability}
      />
    )
  }

  if (props.view === "skills") {
    return <SkillsDirectory query={props.query} canImport={props.canImport} onViewCapability={props.onViewCapability} />
  }

  return <PublishedMarketplaceTab {...props} />
}

/** name (+type, +state, +description) · source · version · workspaces added · credentials · actions */
const MARKET_COLUMNS = [col.title(), col.meta(120), col.id(96, 0.5), col.num(112), col.meta(150), col.actions(2)]

function PublishedMarketplaceTab({ itemID, query, typeFilter, hideInstalled, canManage, onSelectItem, onInstall, onDelete, onViewCapability }: MarketplaceTabProps) {
  const { t, i18n } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const marketplaceQ = useMarketplaceList(workspaceID)

  const items = useMemo(() => marketplaceQ.data ?? [], [marketplaceQ.data])
  const selected = items.find((item) => item.id === itemID) ?? null
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return items.filter((item) => {
      if (typeFilter && item.type !== typeFilter) return false
      // "Hide what's already in this workspace" — both rows you published
      // and rows you installed from elsewhere are available locally.
      if (hideInstalled && (item.installed || item.self_published)) return false
      if (!needle) return true
      return `${item.name} ${item.description ?? ""}`.toLowerCase().includes(needle)
    })
  }, [items, query, typeFilter, hideInstalled])

  if (itemID) {
    if (marketplaceQ.isLoading) {
      return (
        <>
          <PageHeader className="static mx-0 mb-0" backLink={<BackLink label={t("capabilities.marketplace.detail.back")} onClick={() => onSelectItem(null)} />} title={<Skeleton className="h-4 w-40" />} />
          <div className="space-y-3 px-6 pt-4">{Array.from({ length: 5 }).map((_, i) => <Skeleton key={i} className="h-3 w-full max-w-lg" />)}</div>
        </>
      )
    }
    if (!selected) {
      return (
        <>
          <PageHeader className="static mx-0 mb-0" backLink={<BackLink label={t("capabilities.marketplace.detail.back")} onClick={() => onSelectItem(null)} />} title={t("capabilities.marketplace.empty.title")} />
          <EmptyState icon={PackageCheck} title={t("capabilities.marketplace.empty.title")} />
        </>
      )
    }
    return (
      <MarketplaceItemDetail
        capability={selected}
        language={i18n.language}
        canManage={canManage}
        onBack={() => onSelectItem(null)}
        onInstall={() => onInstall(selected)}
        onDelete={() => onDelete(selected)}
        onViewCapability={() => onViewCapability(selected.id)}
      />
    )
  }

  if (marketplaceQ.isLoading) {
    return (
      <div className="px-4 pt-3">
        <div className="mb-3 h-7 border-b border-line" />
        {Array.from({ length: 5 }).map((_, index) => (
          <div key={index} className="flex h-9 items-center gap-3 border-b border-line">
            <Skeleton className="h-3 w-40" />
            <Skeleton className="h-3 flex-1" />
            <Skeleton className="h-3 w-16" />
          </div>
        ))}
      </div>
    )
  }
  if (marketplaceQ.error) {
    return (
      <div className="px-4 pt-4">
        <ErrorState
          title={t("capabilities.marketplace.loadError.title")}
          description={marketplaceQ.error instanceof Error ? marketplaceQ.error.message : t("capabilities.marketplace.loadError.description")}
          onRetry={() => void marketplaceQ.refetch()}
        />
      </div>
    )
  }
  if (filtered.length === 0) {
    return (
      <EmptyState
        icon={PackageCheck}
        title={t("capabilities.marketplace.empty.title")}
        description={t("capabilities.marketplace.empty.description")}
      />
    )
  }

  return (
    <Ledger columns={MARKET_COLUMNS} role="listbox" aria-label={t("capabilities.tabs.marketplace")}>
      <LedgerHeader>
        <span>{t("capabilities.table.name")}</span>
        <span>{t("capabilities.marketplaceDetail.source.title")}</span>
        <span>{t("capabilities.table.latestVersion")}</span>
        <span className="text-right">{t("capabilities.marketplace.detail.addedCount")}</span>
        <span>{t("capabilities.table.credentials")}</span>
        <span />
      </LedgerHeader>
      <ul className="m-0 list-none p-0">
        {filtered.map((item) => (
          <MarketplaceRow
            key={item.id}
            capability={item}
            language={i18n.language}
            canManage={canManage}
            onOpen={() => onSelectItem(item.id)}
            onInstall={() => onInstall(item)}
            onDelete={() => onDelete(item)}
            onViewCapability={() => onViewCapability(item.id)}
          />
        ))}
      </ul>
    </Ledger>
  )
}

function rowKeyHandler(onOpen: () => void) {
  return (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.target !== e.currentTarget) return
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onOpen()
    }
  }
}

function MarketplaceRow({ capability, language, canManage, onOpen, onInstall, onDelete, onViewCapability }: {
  capability: MarketplaceCapability
  language: string
  canManage: boolean
  onOpen: () => void
  onInstall: () => void
  onDelete: () => void
  onViewCapability: () => void
}) {
  const { t } = useTranslation("admin")
  const source = marketplaceSourceName(capability)
  const count = capability.install_count ?? capability.installed_workspace_count ?? 0
  const agentCount = capability.installed_agent_count ?? capability.enabled_agent_count ?? capability.install_count ?? 0
  return (
    <LedgerRow onClick={onOpen} onKeyDown={rowKeyHandler(onOpen)}>
      <span className="flex min-w-0 items-center gap-2">
        <span className="shrink-0 truncate font-medium">{capability.name}</span>
        <CapabilityTypeBadge type={capability.type} />
        {capability.self_published ? (
          <Badge variant="neutral" dot>{t("capabilities.marketplace.card.selfPublished")}</Badge>
        ) : capability.installed ? (
          <Badge variant="neutral" dot>{t("capabilities.marketplace.card.installedBadge")}</Badge>
        ) : null}
        {capability.description && <span className="min-w-0 truncate text-xs text-fg-muted">· {capability.description}</span>}
      </span>
      <span className="truncate text-xs text-fg-muted">{source || "—"}</span>
      <span className={cn("truncate font-mono text-xs", capability.latest_version ? "text-fg" : "text-fg-muted")}>{capability.latest_version ?? "—"}</span>
      <LedgerNum muted={count === 0}>{count}</LedgerNum>
      <span className="truncate text-xs text-fg-muted">
        {requiredCredentialsLabel(capability.required_credentials, language, t("capabilities.credentials.none"))}
      </span>
      <span onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
        <RowActions>
          {capability.self_published ? (
            <>
              <ActionIconButton icon={ArrowUpRight} label={t("capabilities.mcpDirectory.actions.viewCapability")} onClick={onViewCapability} />
              {canManage && <ActionIconButton icon={Trash2} tone="danger" label={t("capabilities.rowActions.delete")} onClick={onDelete} />}
            </>
          ) : (
            <ActionIconButton
              icon={Download}
              label={capability.installed ? t("capabilities.marketplace.card.installed", { count: agentCount }) : t("capabilities.marketplace.card.install")}
              onClick={onInstall}
            />
          )}
        </RowActions>
      </span>
    </LedgerRow>
  )
}

function MarketplaceItemDetail({ capability, language, canManage, onBack, onInstall, onDelete, onViewCapability }: {
  capability: MarketplaceCapability
  language: string
  canManage: boolean
  onBack: () => void
  onInstall: () => void
  onDelete: () => void
  onViewCapability: () => void
}) {
  const { t } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const source = marketplaceSourceName(capability)
  const previewable = capability.type === "mcp" || capability.type === "skill"
  const detailQ = useMarketplaceDetail(workspaceID, previewable ? capability.id : null)
  return (
    <>
      <PageHeader
        className="static mx-0 mb-0"
        backLink={<BackLink label={t("capabilities.marketplace.detail.back")} onClick={onBack} />}
        title={
          <span className="inline-flex min-w-0 items-center gap-2">
            <span className="truncate">{capability.name}</span>
            <CapabilityTypeBadge type={capability.type} />
            {capability.self_published ? (
              <Badge variant="neutral" dot>{t("capabilities.marketplace.card.selfPublished")}</Badge>
            ) : capability.installed ? (
              <Badge variant="neutral" dot>{t("capabilities.marketplace.card.installedBadge")}</Badge>
            ) : null}
          </span>
        }
        action={
          capability.self_published ? (
            <>
              {canManage && <Button variant="outline" onClick={onDelete}>{t("capabilities.rowActions.delete")}</Button>}
              <Button variant="outline" onClick={onViewCapability}>
                {t("capabilities.mcpDirectory.actions.viewCapability")}
                <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
              </Button>
            </>
          ) : (
            <Button onClick={onInstall}>{t("capabilities.marketplace.card.install")}</Button>
          )
        }
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-6 pb-10 pt-4">
        {capability.description && <p className="mb-4 max-w-3xl text-sm text-fg">{capability.description}</p>}
        <RailSection title={t("capabilities.detail.basic.title")}>
          <PropertyList className="grid-cols-[160px_minmax(0,1fr)]">
            <Property label={t("capabilities.marketplaceDetail.source.workspace")}>{source || t("capabilities.none")}</Property>
            <Property label={t("capabilities.table.latestVersion")} mono>{capability.latest_version ? `v${capability.latest_version}` : t("capabilities.none")}</Property>
            <Property label={t("capabilities.marketplace.detail.addedCount")} mono>{capability.install_count ?? capability.installed_workspace_count ?? 0}</Property>
            <Property label={t("capabilities.table.credentials")}>
              {requiredCredentialsLabel(capability.required_credentials, language, t("capabilities.credentials.none"))}
            </Property>
          </PropertyList>
        </RailSection>
        {previewable && (
          <RailSection title={t("capabilities.marketplace.detail.contentTitle")} className="mt-6">
            {detailQ.isLoading ? (
              <div className="space-y-2 pt-2">
                <Skeleton className="h-3 w-full max-w-lg" />
                <Skeleton className="h-40 w-full" />
              </div>
            ) : detailQ.error ? (
              <ErrorState
                title={t("capabilities.marketplace.detail.loadErrorTitle")}
                description={detailQ.error instanceof Error ? detailQ.error.message : t("capabilities.marketplace.detail.loadErrorDescription")}
                onRetry={() => void detailQ.refetch()}
              />
            ) : detailQ.data ? (
              <MarketplaceContentPreview key={detailQ.data.capability_id} detail={detailQ.data} />
            ) : null}
          </RailSection>
        )}
      </div>
    </>
  )
}

const CODE_BLOCK_CLASS = "m-0 overflow-x-auto whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg"

function MarketplaceContentPreview({ detail }: { detail: MarketplaceCapabilityDetail }) {
  const { t } = useTranslation("admin")
  const sourceURL = safeExternalURL(detail.git_repo_url)
  const hasSource = detail.git_repo_url || detail.git_ref || detail.path
  return (
    <div className="space-y-4">
      {hasSource && (
        <PropertyList className="grid-cols-[160px_minmax(0,1fr)]">
          {detail.git_repo_url && (
            <Property label={t("capabilities.marketplace.detail.sourceRepository")} mono>
              {sourceURL ? <ExternalLinkValue href={sourceURL}>{detail.git_repo_url.replace(/^https?:\/\//, "")}</ExternalLinkValue> : detail.git_repo_url}
            </Property>
          )}
          {detail.git_ref && <Property label={t("capabilities.marketplace.detail.sourceCommit")} mono>{detail.git_ref}</Property>}
          {detail.path && <Property label={t("capabilities.marketplace.detail.sourcePath")} mono>{detail.path}</Property>}
        </PropertyList>
      )}
      {detail.skill ? <SkillPreview skill={detail.skill} /> : null}
      {detail.mcp ? <MCPPreview detail={detail} /> : null}
      {!detail.skill && !detail.mcp ? (
        <p className="text-sm text-fg-muted">{t("capabilities.marketplace.detail.contentUnavailable")}</p>
      ) : null}
    </div>
  )
}

function SkillPreview({ skill }: { skill: MarketplaceSkillDetail }) {
  const { t } = useTranslation("admin")
  const files = useMemo(
    () => [
      { path: "SKILL.md", content: skill.instruction, kind: "markdown" as const },
      ...(skill.files ?? []),
    ],
    [skill],
  )
  const [selectedPath, setSelectedPath] = useState("SKILL.md")
  const selected = files.find((file) => file.path === selectedPath) ?? files[0]

  return (
    <div>
      <div className="flex h-7 items-center gap-2 text-sm">
        <FileText className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
        <span className="font-medium text-fg">{skill.title || skill.slug}</span>
        <span className="text-xs text-fg-muted">{t("capabilities.marketplace.detail.skillBadge")}</span>
      </div>
      <div className="mt-1 grid min-h-[300px] grid-cols-[200px_minmax(0,1fr)] border-t border-line">
        <div className="border-r border-line py-1 pr-2">
          <SkillFileTree
            paths={files.map((file) => file.path)}
            selectedPath={selected.path}
            onSelect={setSelectedPath}
          />
        </div>
        <div className="min-w-0">
          <div className="flex h-7 items-center border-b border-line px-3 font-mono text-xs text-fg-muted">{selected.path}</div>
          <pre className="m-0 max-h-[480px] overflow-auto whitespace-pre-wrap break-words p-3 font-mono text-xs leading-relaxed text-fg">
            {selected.content}
          </pre>
        </div>
      </div>
    </div>
  )
}

interface SkillFileTreeNode {
  name: string
  path: string
  directory: boolean
  children: SkillFileTreeNode[]
}

function buildSkillFileTree(paths: string[]): SkillFileTreeNode[] {
  const roots: SkillFileTreeNode[] = []

  paths.forEach((path) => {
    const parts = path.split("/")
    let siblings = roots

    parts.forEach((name, index) => {
      const nodePath = parts.slice(0, index + 1).join("/")
      let node = siblings.find((candidate) => candidate.name === name)
      if (!node) {
        node = {
          name,
          path: nodePath,
          directory: index < parts.length - 1,
          children: [],
        }
        siblings.push(node)
      }
      siblings = node.children
    })
  })

  const sortNodes = (nodes: SkillFileTreeNode[]) => {
    nodes.sort((left, right) => {
      if (left.path === "SKILL.md") return -1
      if (right.path === "SKILL.md") return 1
      if (left.directory !== right.directory) return left.directory ? -1 : 1
      return left.name.localeCompare(right.name)
    })
    nodes.forEach((node) => sortNodes(node.children))
  }
  sortNodes(roots)

  return roots
}

function SkillFileTree({ paths, selectedPath, onSelect }: {
  paths: string[]
  selectedPath: string
  onSelect: (path: string) => void
}) {
  const nodes = useMemo(() => buildSkillFileTree(paths), [paths])
  return (
    <div>
      {nodes.map((node) => (
        <SkillFileTreeItem
          key={node.path}
          node={node}
          selectedPath={selectedPath}
          onSelect={onSelect}
        />
      ))}
    </div>
  )
}

const TREE_ROW_CLASS = "flex h-7 w-full items-center gap-1.5 rounded pr-2 text-left font-mono text-xs transition-colors duration-150 ease-settle focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent/40"

function SkillFileTreeItem({ node, selectedPath, onSelect, depth = 0 }: {
  node: SkillFileTreeNode
  selectedPath: string
  onSelect: (path: string) => void
  depth?: number
}) {
  const [expanded, setExpanded] = useState(true)

  if (node.directory) {
    const ChevronIcon = expanded ? ChevronDown : ChevronRight
    const FolderIcon = expanded ? FolderOpen : Folder
    return (
      <div>
        <button
          type="button"
          aria-expanded={expanded}
          title={node.path}
          onClick={() => setExpanded((value) => !value)}
          className={cn(TREE_ROW_CLASS, "text-fg-muted hover:app-hover hover:text-fg")}
          style={{ paddingLeft: depth * 12 + 8 }}
        >
          <ChevronIcon className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
          <FolderIcon className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
          <span className="truncate">{node.name}</span>
        </button>
        {expanded && node.children.map((child) => (
          <SkillFileTreeItem
            key={child.path}
            node={child}
            selectedPath={selectedPath}
            onSelect={onSelect}
            depth={depth + 1}
          />
        ))}
      </div>
    )
  }

  const Icon = node.name.endsWith(".md") || node.name.endsWith(".mdx") ? FileText : File
  const selected = node.path === selectedPath
  return (
    <button
      type="button"
      aria-pressed={selected}
      title={node.path}
      onClick={() => onSelect(node.path)}
      className={cn(TREE_ROW_CLASS, selected ? "app-pressed text-fg" : "text-fg-muted hover:app-hover hover:text-fg")}
      style={{ paddingLeft: depth * 12 + 26 }}
    >
      <Icon className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
      <span className="truncate">{node.name}</span>
    </button>
  )
}

function MCPPreview({ detail }: { detail: MarketplaceCapabilityDetail }) {
  const { t } = useTranslation("admin")
  return (
    <div className="space-y-5">
      {(detail.mcp?.servers ?? []).map((server) => {
        const env = Object.entries(server.env ?? {}).sort(([left], [right]) => left.localeCompare(right))
        const command = [server.command, ...(server.args ?? [])].map(formatCommandPart).join(" ")
        return (
          <section key={server.name}>
            <h4 className="flex h-7 items-center gap-2 text-sm font-medium text-fg">
              <span className="font-mono text-xs">{server.name}</span>
              <CapabilityTypeBadge type="mcp" />
            </h4>
            <PreviewLabel>{t("capabilities.marketplace.detail.command")}</PreviewLabel>
            <pre className={CODE_BLOCK_CLASS}>{command}</pre>
            <PreviewLabel>{t("capabilities.marketplace.detail.environment")}</PreviewLabel>
            {env.length === 0 ? (
              <p className="text-sm text-fg-muted">{t("capabilities.marketplace.detail.noEnvironment")}</p>
            ) : (
              <dl className="m-0 border-t border-line">
                {env.map(([name, value]) => (
                  <div key={name} className="grid min-h-7 grid-cols-[minmax(160px,0.4fr)_minmax(0,1fr)] items-center gap-x-3 border-b border-line py-1 font-mono text-xs">
                    <dt className="truncate text-fg-muted">{name}</dt>
                    <dd className="m-0 break-all text-fg">
                      {formatMCPEnvValue(value, t("capabilities.marketplace.detail.redactedSecret"))}
                    </dd>
                  </div>
                ))}
              </dl>
            )}
            {server.startup_timeout_sec ? (
              <p className="mt-2 text-xs text-fg-muted">
                {t("capabilities.marketplace.detail.timeout", { seconds: server.startup_timeout_sec })}
              </p>
            ) : null}
          </section>
        )
      })}
    </div>
  )
}

function PreviewLabel({ children }: { children: ReactNode }) {
  return <p className="mb-1 mt-2 text-xs text-fg-muted">{children}</p>
}

function formatCommandPart(value: string): string {
  return /^[A-Za-z0-9_@%+=:,./-]+$/.test(value) ? value : JSON.stringify(value)
}

function formatMCPEnvValue(value: MarketplaceMCPEnvValue, redactedLabel: string): string {
  if (value.mode === "literal") return value.value ?? ""
  if (value.mode === "credential_ref")
    return `\${PARSAR_CREDENTIAL:${value.credential_kind_code ?? "unknown"}}`
  return redactedLabel
}
