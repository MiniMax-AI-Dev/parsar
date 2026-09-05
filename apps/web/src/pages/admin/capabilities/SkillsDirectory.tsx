import { useMemo, useState, type KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import { ArrowUpRight, Download, PackageCheck } from "lucide-react"

import { ActionIconButton, RowActions } from "../../../components/ui/action-button"
import { Button } from "../../../components/ui/button"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Ledger, LedgerHeader, LedgerId, LedgerNum, LedgerRow } from "../../../components/ui/ledger"
import { Skeleton } from "../../../components/ui/skeleton"
import { useInstallSkill, useSkillsCatalog, type SkillsCatalogItem } from "../../../lib/api-skills"
import { useWorkspaceId } from "../../../lib/workspace"
import { InlineNotice } from "./notices"

interface SkillsDirectoryProps {
  query: string
  canImport: boolean
  onViewCapability: (capabilityID: string) => void
}

/** rank · skill (+source type, +id) · source · installs · actions */
const SKILL_COLUMNS = "40px minmax(0,1fr) 200px 88px 64px"

export function SkillsDirectory({ query, canImport, onViewCapability }: SkillsDirectoryProps) {
  const { t, i18n } = useTranslation("admin")
  const workspaceID = useWorkspaceId()
  const catalogQ = useSkillsCatalog()
  const installMut = useInstallSkill(workspaceID)
  const [installed, setInstalled] = useState<Record<string, string>>({})
  const [success, setSuccess] = useState<{ name: string; capabilityID: string } | null>(null)
  const numberFormatter = useMemo(() => new Intl.NumberFormat(i18n.language), [i18n.language])

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    const items = catalogQ.data?.items ?? []
    if (!needle) return items
    return items.filter((item) =>
      [item.name, item.slug, item.source, item.id].some((value) =>
        value.toLowerCase().includes(needle),
      ),
    )
  }, [catalogQ.data?.items, query])
  const pendingID = installMut.isPending ? (installMut.variables?.id ?? null) : null

  const install = (skill: SkillsCatalogItem) => {
    if (!canImport || installed[skill.id]) return
    installMut.mutate(skill, {
      onSuccess: (result) => {
        setInstalled((current) => ({ ...current, [skill.id]: result.capability.id }))
        setSuccess({ name: result.capability.name, capabilityID: result.capability.id })
      },
    })
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-testid="skills-directory">
      {success ? (
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
          {t("capabilities.skillsDirectory.install.success", { name: success.name })}
        </InlineNotice>
      ) : null}

      {catalogQ.error ? (
        <div className="px-4 pt-4">
          <ErrorState
            title={t("capabilities.skillsDirectory.loadError.title")}
            description={catalogQ.error instanceof Error ? catalogQ.error.message : t("capabilities.skillsDirectory.loadError.description")}
            onRetry={() => void catalogQ.refetch()}
          />
        </div>
      ) : null}
      {installMut.error ? (
        <div className="px-4 pt-4">
          <ErrorState
            title={t("capabilities.skillsDirectory.install.failed")}
            description={installMut.error instanceof Error ? installMut.error.message : ""}
            onRetry={() => installMut.reset()}
          />
        </div>
      ) : null}

      {catalogQ.isLoading ? (
        <div className="px-4 pt-3" data-testid="skills-directory-loading">
          <div className="mb-3 h-7 border-b border-line" />
          {Array.from({ length: 6 }).map((_, index) => (
            <div key={index} className="flex h-9 items-center gap-3 border-b border-line">
              <Skeleton className="h-3 w-6" />
              <Skeleton className="h-3 w-40" />
              <Skeleton className="h-3 flex-1" />
              <Skeleton className="h-3 w-16" />
            </div>
          ))}
        </div>
      ) : filtered.length === 0 && !catalogQ.error ? (
        <EmptyState
          icon={PackageCheck}
          title={t("capabilities.skillsDirectory.empty.title")}
          description={t("capabilities.skillsDirectory.empty.description")}
        />
      ) : filtered.length > 0 ? (
        <Ledger columns={SKILL_COLUMNS} role="listbox" aria-label={t("capabilities.tabs.skills")} data-testid="skills-marketplace-grid">
          <LedgerHeader>
            <span className="text-right">#</span>
            <span>{t("capabilities.table.name")}</span>
            <span>{t("capabilities.marketplaceDetail.source.title")}</span>
            <span className="text-right">{t("capabilities.table.installs")}</span>
            <span />
          </LedgerHeader>
          <ul className="m-0 list-none p-0">
            {filtered.map((skill) => (
              <SkillRow
                key={skill.id}
                skill={skill}
                canImport={canImport}
                installing={pendingID === skill.id}
                installedCapabilityID={installed[skill.id]}
                numberFormatter={numberFormatter}
                onInstall={() => install(skill)}
                onViewCapability={onViewCapability}
              />
            ))}
          </ul>
        </Ledger>
      ) : null}
    </div>
  )
}

function SkillRow({
  skill,
  canImport,
  installing,
  installedCapabilityID,
  numberFormatter,
  onInstall,
  onViewCapability,
}: {
  skill: SkillsCatalogItem
  canImport: boolean
  installing: boolean
  installedCapabilityID?: string
  numberFormatter: Intl.NumberFormat
  onInstall: () => void
  onViewCapability: (capabilityID: string) => void
}) {
  const { t } = useTranslation("admin")
  const sourceType = skill.sourceType ?? skill.source_type
  // Rows have no detail view; Enter triggers the row's one action.
  const primary = installedCapabilityID ? () => onViewCapability(installedCapabilityID) : onInstall
  const onKeyDown = (e: KeyboardEvent<HTMLLIElement>) => {
    if (e.target !== e.currentTarget) return
    if (e.key === "Enter") {
      e.preventDefault()
      primary()
    }
  }
  return (
    <LedgerRow onKeyDown={onKeyDown} data-testid="skills-directory-row" data-catalog-id={skill.id}>
      <LedgerNum muted>{skill.rank ?? "—"}</LedgerNum>
      <span className="flex min-w-0 items-center gap-2">
        <span className="shrink-0 truncate font-medium">{skill.name || skill.slug}</span>
        {sourceType && <span className="shrink-0 text-xs text-fg-muted">{sourceType}</span>}
        <span className="min-w-0 truncate text-xs text-fg-muted">· {skill.id}</span>
      </span>
      <LedgerId>{skill.source}</LedgerId>
      <LedgerNum muted={typeof skill.installs !== "number"}>
        {typeof skill.installs === "number" ? numberFormatter.format(skill.installs) : "—"}
      </LedgerNum>
      <span onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
        <RowActions>
          {installedCapabilityID ? (
            <ActionIconButton icon={ArrowUpRight} label={t("capabilities.skillsDirectory.install.installed")} onClick={() => onViewCapability(installedCapabilityID)} />
          ) : (
            <ActionIconButton
              icon={Download}
              label={installing ? t("capabilities.skillsDirectory.install.installing") : canImport ? t("capabilities.skillsDirectory.install.action") : t("capabilities.permission.adminOnly")}
              busy={installing}
              disabled={!canImport}
              onClick={onInstall}
            />
          )}
        </RowActions>
      </span>
    </LedgerRow>
  )
}
