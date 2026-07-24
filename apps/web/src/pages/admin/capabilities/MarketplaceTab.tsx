import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { ArrowLeft, ArrowRight, ChevronDown, ChevronRight, ExternalLink, File, FileText, Folder, FolderOpen, PackageCheck, Server } from "lucide-react"

import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Skeleton } from "../../../components/ui/skeleton"
import { marketplaceSourceName, useMarketplaceDetail, useMarketplaceList, type MarketplaceCapability, type MarketplaceCapabilityDetail, type MarketplaceMCPEnvValue, type MarketplaceSkillDetail } from "../../../lib/api-marketplace"
import { useWorkspaceId } from "../../../lib/workspace"
import { requiredCredentialsLabel } from "../capability-ui"
import type { Capability } from "../../../lib/api-types"
import { MCPDirectory } from "./mcp-directory/MCPDirectory"

interface MarketplaceTabProps {
  itemID: string | null
  query: string
  typeFilter: "mcp" | "skill"
  canImport: boolean
  onSelectItem: (id: string | null) => void
  onInstall: (capability: MarketplaceCapability) => void
  onViewCapability: (capabilityID: string) => void
}

export function MarketplaceTab(props: MarketplaceTabProps) {
  const mcpItemID = props.itemID?.startsWith("mcp:") ? props.itemID.slice(4) : null
  if (mcpItemID !== null || (!props.itemID && props.typeFilter === "mcp")) {
    return <MCPDirectory
      itemID={mcpItemID}
      query={props.query}
      canImport={props.canImport}
      onSelectItem={(id) => props.onSelectItem(id ? `mcp:${id}` : null)}
      onViewCapability={props.onViewCapability}
    />
  }
  return <SkillMarketplaceTab {...props} />
}

function SkillMarketplaceTab({ itemID, query, typeFilter, onSelectItem, onInstall }: MarketplaceTabProps) {
  const { t, i18n } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const marketplaceQ = useMarketplaceList(workspaceID)
  const [hideInstalled, setHideInstalled] = useState(false)

  const items = useMemo(() => marketplaceQ.data ?? [], [marketplaceQ.data])
  const selected = items.find((item) => item.id === itemID) ?? null
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return items.filter((item) => {
      if (item.type !== typeFilter) return false
      // "Hide what's already in this workspace" — both rows you published
      // and rows you installed from elsewhere are available locally.
      if (hideInstalled && (item.installed || item.self_published)) return false
      if (!needle) return true
      return `${item.name} ${item.description ?? ""}`.toLowerCase().includes(needle)
    })
  }, [items, query, typeFilter, hideInstalled])

  if (selected) {
    return (
      <MarketplaceItemDetail
        capability={selected}
        language={i18n.language}
        onBack={() => onSelectItem(null)}
        onInstall={() => onInstall(selected)}
      />
    )
  }

  return (
    <div className="space-y-3">
      <div className="flex justify-end">
        <label className="inline-flex select-none items-center gap-1.5 text-sm text-fg-muted">
          <input
            type="checkbox"
            className="h-3.5 w-3.5 rounded border-line-strong text-fg focus:ring-slate-400"
            checked={hideInstalled}
            onChange={(event) => setHideInstalled(event.target.checked)}
          />
          {t("capabilities.marketplace.filters.hideInstalled")}
        </label>
      </div>

      {marketplaceQ.isLoading ? (
        <div className="grid gap-3 md:grid-cols-2">
          {Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-32 w-full" />)}
        </div>
      ) : marketplaceQ.error ? (
        <ErrorState
          title={t("capabilities.marketplace.loadError.title")}
          description={marketplaceQ.error instanceof Error ? marketplaceQ.error.message : t("capabilities.marketplace.loadError.description")}
          onRetry={() => void marketplaceQ.refetch()}
        />
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={PackageCheck}
          title={t("capabilities.marketplace.empty.title")}
          description={t("capabilities.marketplace.empty.description")}
        />
      ) : (
        <div className="grid gap-3 md:grid-cols-2">
          {filtered.map((item) => (
            <MarketplaceCard
              key={item.id}
              capability={item}
              language={i18n.language}
              onOpen={() => onSelectItem(item.id)}
              onInstall={() => onInstall(item)}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function MarketplaceCard({ capability, language, onOpen, onInstall }: {
  capability: MarketplaceCapability
  language: string
  onOpen: () => void
  onInstall: () => void
}) {
  const { t } = useTranslation("admin")
  const source = marketplaceSourceName(capability)
  const count = capability.installed_agent_count ?? capability.enabled_agent_count ?? capability.install_count ?? 0
  return (
    <div className="rounded-lg border border-line bg-surface p-4 transition hover:border-line-strong hover:shadow-sm">
      <button type="button" className="w-full text-left" onClick={onOpen}>
        <div className="flex items-start justify-between gap-3">
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="text-base font-medium text-fg">{capability.name}</h3>
              <CapabilityTypeBadge type={capability.type} />
              {capability.self_published && <Badge variant="neutral">{t("capabilities.marketplace.card.selfPublished")}</Badge>}
              {!capability.self_published && capability.installed && <Badge variant="success">{t("capabilities.marketplace.card.installedBadge")}</Badge>}
            </div>
            {source && <p className="mt-1 text-sm text-fg-subtle">{t("capabilities.marketplace.card.source", { source })}</p>}
          </div>
          <ArrowRight className="mt-1 h-3.5 w-3.5 text-fg-faint" />
        </div>
        {capability.description && <p className="mt-3 line-clamp-2 text-sm leading-5 text-fg-muted">{capability.description}</p>}
        <div className="mt-3 flex flex-wrap items-center gap-2 text-sm text-fg-subtle">
          <span>{t("capabilities.marketplace.card.latest", { version: capability.latest_version ?? "—" })}</span>
          <span>·</span>
          <span>{t("capabilities.marketplace.card.added", { count })}</span>
          <span>·</span>
          <span>{t("capabilities.marketplace.card.credential", { kind: requiredCredentialsLabel(capability.required_credentials, language, t("capabilities.credentials.none")) })}</span>
        </div>
      </button>
      <div className="mt-4 flex justify-end">
        <Button size="sm" disabled={capability.self_published} onClick={onInstall}>
          {capability.self_published
            ? t("capabilities.marketplace.card.selfPublished")
            : capability.installed
              ? t("capabilities.marketplace.card.installed", { count })
              : t("capabilities.marketplace.card.install")}
        </Button>
      </div>
    </div>
  )
}

function MarketplaceItemDetail({ capability, language, onBack, onInstall }: {
  capability: MarketplaceCapability
  language: string
  onBack: () => void
  onInstall: () => void
}) {
  const { t } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const source = marketplaceSourceName(capability)
  const previewable = capability.type === "mcp" || capability.type === "skill"
  const detailQ = useMarketplaceDetail(workspaceID, previewable ? capability.id : null)
  return (
    <div className="space-y-3">
      <Button variant="ghost" size="sm" onClick={onBack}>
        <ArrowLeft className="h-3.5 w-3.5" />
        {t("capabilities.marketplace.detail.back")}
      </Button>
      <div className="rounded-lg border border-line bg-surface p-5">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="text-lg font-semibold text-fg">{capability.name}</h3>
          <CapabilityTypeBadge type={capability.type} />
        </div>
        {source && <p className="mt-2 text-sm text-fg-subtle">{t("capabilities.marketplace.card.source", { source })}</p>}
        {capability.description && <p className="mt-4 text-sm leading-5 text-fg-muted">{capability.description}</p>}
        <div className="mt-4 grid gap-3 md:grid-cols-3">
          <Detail label={t("capabilities.table.latestVersion")} value={capability.latest_version ? `v${capability.latest_version}` : t("capabilities.none")} mono />
          <Detail label={t("capabilities.table.credentials")} value={requiredCredentialsLabel(capability.required_credentials, language, t("capabilities.credentials.none"))} />
          <Detail label={t("capabilities.marketplace.detail.addedCount")} value={String(capability.install_count ?? capability.installed_workspace_count ?? 0)} />
        </div>
        {previewable && (
          <div className="mt-5 border-t border-line pt-5">
            <div className="mb-3">
              <h4 className="text-sm font-medium text-fg">
                {t("capabilities.marketplace.detail.contentTitle")}
              </h4>
              <p className="mt-1 text-xs leading-5 text-fg-subtle">
                {t("capabilities.marketplace.detail.contentDescription")}
              </p>
            </div>
            {detailQ.isLoading ? (
              <div className="space-y-2">
                <Skeleton className="h-9 w-full" />
                <Skeleton className="h-64 w-full" />
              </div>
            ) : detailQ.error ? (
              <ErrorState
                title={t("capabilities.marketplace.detail.loadErrorTitle")}
                description={
                  detailQ.error instanceof Error
                    ? detailQ.error.message
                    : t("capabilities.marketplace.detail.loadErrorDescription")
                }
                onRetry={() => void detailQ.refetch()}
              />
            ) : detailQ.data ? (
              <MarketplaceContentPreview key={detailQ.data.capability_id} detail={detailQ.data} />
            ) : null}
          </div>
        )}
        <div className="mt-5 flex justify-end">
          <Button size="sm" disabled={capability.self_published} onClick={onInstall}>
            {capability.self_published ? t("capabilities.marketplace.card.selfPublished") : t("capabilities.marketplace.card.install")}
          </Button>
        </div>
      </div>
    </div>
  )
}

function MarketplaceContentPreview({ detail }: { detail: MarketplaceCapabilityDetail }) {
  const { t } = useTranslation("admin")
  const sourceURL = safeExternalURL(detail.git_repo_url)
  return (
    <div className="space-y-4">
      {(detail.git_repo_url || detail.git_ref || detail.path) && (
        <div className="grid gap-3 rounded-lg border border-line bg-surface-muted/35 p-3 text-xs sm:grid-cols-3">
          <SourceDetail
            label={t("capabilities.marketplace.detail.sourceRepository")}
            value={detail.git_repo_url}
            href={sourceURL}
          />
          <SourceDetail
            label={t("capabilities.marketplace.detail.sourceCommit")}
            value={detail.git_ref}
            mono
          />
          <SourceDetail
            label={t("capabilities.marketplace.detail.sourcePath")}
            value={detail.path}
            mono
          />
        </div>
      )}
      {detail.skill ? <SkillPreview skill={detail.skill} /> : null}
      {detail.mcp ? <MCPPreview detail={detail} /> : null}
      {!detail.skill && !detail.mcp ? (
        <p className="rounded-lg border border-line bg-surface-muted/20 p-4 text-sm text-fg-subtle">
          {t("capabilities.marketplace.detail.contentUnavailable")}
        </p>
      ) : null}
    </div>
  )
}

function safeExternalURL(value?: string): string | undefined {
  if (!value) return undefined
  try {
    const url = new URL(value)
    return url.protocol === "http:" || url.protocol === "https:" ? url.toString() : undefined
  } catch {
    return undefined
  }
}

function SourceDetail({
  label,
  value,
  href,
  mono,
}: {
  label: string
  value?: string
  href?: string
  mono?: boolean
}) {
  if (!value) return <div />
  return (
    <div className="min-w-0">
      <p className="text-fg-faint">{label}</p>
      {href ? (
        <a
          href={href}
          target="_blank"
          rel="noreferrer"
          className="mt-1 flex items-center gap-1 break-all text-fg underline decoration-line-strong underline-offset-2 hover:decoration-fg"
        >
          <span>{value.replace(/^https?:\/\//, "")}</span>
          <ExternalLink className="h-3 w-3 shrink-0" />
        </a>
      ) : (
        <p className={`mt-1 break-all text-fg ${mono ? "font-mono" : ""}`}>{value}</p>
      )}
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
    <div className="overflow-hidden rounded-lg border border-line">
      <div className="flex items-center gap-2 border-b border-line bg-surface-muted/45 px-3 py-2">
        <FileText className="h-3.5 w-3.5 text-fg-subtle" />
        <span className="text-xs font-medium text-fg">{skill.title || skill.slug}</span>
        <Badge variant="neutral">{t("capabilities.marketplace.detail.skillBadge")}</Badge>
      </div>
      <div className="grid min-h-[300px] md:grid-cols-[200px_minmax(0,1fr)]">
        <div className="border-b border-line bg-surface-muted/20 p-2 md:border-b-0 md:border-r">
          <p className="px-2 pb-1.5 pt-0.5 text-2xs font-medium uppercase tracking-wider text-fg-faint">
            {t("capabilities.marketplace.detail.files")}
          </p>
          <SkillFileTree
            paths={files.map((file) => file.path)}
            selectedPath={selected.path}
            onSelect={setSelectedPath}
          />
        </div>
        <div className="min-w-0 bg-surface-emphasis/[0.025]">
          <div className="border-b border-line px-4 py-2 font-mono text-xs text-fg-subtle">
            {selected.path}
          </div>
          <pre className="max-h-[480px] overflow-auto whitespace-pre-wrap break-words p-4 font-mono text-xs leading-5 text-fg">
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
    <div className="py-0.5">
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
          className="flex w-full items-center gap-1.5 rounded-sm py-1 pr-2 text-left text-fg-subtle transition-colors hover:bg-surface-muted/70 hover:text-fg"
          style={{ paddingLeft: depth * 12 + 8 }}
        >
          <ChevronIcon className="h-3 w-3 shrink-0 text-fg-faint" />
          <FolderIcon className="h-3.5 w-3.5 shrink-0 text-fg-faint" />
          <span className="truncate text-2xs">{node.name}</span>
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
      className={`flex w-full items-center gap-1.5 rounded-sm py-1 pr-2 text-left transition-colors ${
        selected
          ? "bg-surface-muted text-fg"
          : "text-fg-subtle hover:bg-surface-muted/70 hover:text-fg"
      }`}
      style={{ paddingLeft: depth * 12 + 24 }}
    >
      <Icon className="h-3.5 w-3.5 shrink-0 text-fg-faint" />
      <span className="truncate text-2xs">{node.name}</span>
    </button>
  )
}

function MCPPreview({ detail }: { detail: MarketplaceCapabilityDetail }) {
  const { t } = useTranslation("admin")
  return (
    <div className="space-y-3">
      {(detail.mcp?.servers ?? []).map((server) => {
        const env = Object.entries(server.env ?? {}).sort(([left], [right]) =>
          left.localeCompare(right),
        )
        const command = [server.command, ...(server.args ?? [])].map(formatCommandPart).join(" ")
        return (
          <div key={server.name} className="overflow-hidden rounded-lg border border-line">
            <div className="flex items-center gap-2 border-b border-line bg-surface-muted/45 px-3 py-2">
              <Server className="h-3.5 w-3.5 text-fg-subtle" />
              <span className="font-mono text-xs font-medium text-fg">{server.name}</span>
              <Badge variant="neutral">MCP</Badge>
            </div>
            <div className="space-y-4 p-4">
              <div>
                <p className="text-xs text-fg-subtle">
                  {t("capabilities.marketplace.detail.command")}
                </p>
                <pre className="mt-1.5 overflow-x-auto rounded-md border border-line bg-surface-emphasis/[0.035] px-3 py-2 font-mono text-xs leading-5 text-fg">
                  {command}
                </pre>
              </div>
              <div>
                <p className="text-xs text-fg-subtle">
                  {t("capabilities.marketplace.detail.environment")}
                </p>
                {env.length === 0 ? (
                  <p className="mt-1.5 text-xs text-fg-faint">
                    {t("capabilities.marketplace.detail.noEnvironment")}
                  </p>
                ) : (
                  <div className="mt-1.5 divide-y divide-line rounded-md border border-line">
                    {env.map(([name, value]) => (
                      <div
                        key={name}
                        className="grid gap-1 px-3 py-2 text-xs sm:grid-cols-[minmax(120px,0.45fr)_1fr]"
                      >
                        <span className="font-mono text-fg-subtle">{name}</span>
                        <span className="break-all font-mono text-fg">
                          {formatMCPEnvValue(
                            value,
                            t("capabilities.marketplace.detail.redactedSecret"),
                          )}
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
              {server.startup_timeout_sec ? (
                <p className="text-xs text-fg-subtle">
                  {t("capabilities.marketplace.detail.timeout", {
                    seconds: server.startup_timeout_sec,
                  })}
                </p>
              ) : null}
            </div>
          </div>
        )
      })}
    </div>
  )
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

function Detail({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="rounded-md border border-line p-3">
      <p className="text-xs text-fg-subtle">{label}</p>
      <p className={`mt-1 text-sm text-fg ${mono ? "font-mono" : ""}`}>{value}</p>
    </div>
  )
}

function CapabilityTypeBadge({ type }: { type: Capability["type"] }) {
  if (type === "skill") return <Badge variant="primary">Skill</Badge>
  if (type === "plugin") return <Badge variant="success">Plugin</Badge>
  return <Badge variant="neutral">MCP</Badge>
}
