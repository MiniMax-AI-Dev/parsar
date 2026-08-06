import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { Check, Code2, Download, Loader2, PackageCheck } from "lucide-react"

import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Skeleton } from "../../../components/ui/skeleton"
import { useInstallSkill, useSkillsCatalog, type SkillsCatalogItem } from "../../../lib/api-skills"
import { useWorkspaceId } from "../../../lib/workspace"

interface SkillsDirectoryProps {
  query: string
  canImport: boolean
  onViewCapability: (capabilityID: string) => void
}

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
    <div className="space-y-4" data-testid="skills-directory">
      {success ? <SuccessBanner success={success} onViewCapability={onViewCapability} /> : null}

      {catalogQ.error ? (
        <ErrorState
          title={t("capabilities.skillsDirectory.loadError.title")}
          description={
            catalogQ.error instanceof Error
              ? catalogQ.error.message
              : t("capabilities.skillsDirectory.loadError.description")
          }
          onRetry={() => void catalogQ.refetch()}
        />
      ) : null}
      {installMut.error ? (
        <ErrorState
          title={t("capabilities.skillsDirectory.install.failed")}
          description={installMut.error instanceof Error ? installMut.error.message : ""}
          onRetry={() => installMut.reset()}
        />
      ) : null}

      {catalogQ.isLoading ? (
        <div
          className="grid gap-3 md:grid-cols-2 xl:grid-cols-3"
          data-testid="skills-directory-loading"
        >
          {Array.from({ length: 6 }).map((_, index) => (
            <Skeleton key={index} className="h-52 w-full" />
          ))}
        </div>
      ) : filtered.length === 0 && !catalogQ.error ? (
        <EmptyState
          icon={PackageCheck}
          title={t("capabilities.skillsDirectory.empty.title")}
          description={t("capabilities.skillsDirectory.empty.description")}
        />
      ) : filtered.length > 0 ? (
        <div
          className="grid gap-3 md:grid-cols-2 xl:grid-cols-3"
          data-testid="skills-marketplace-grid"
        >
          {filtered.map((skill) => (
            <SkillCatalogCard
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
        </div>
      ) : null}
    </div>
  )
}

function SkillCatalogCard({
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
  return (
    <article
      className="flex min-h-52 flex-col rounded-xl border border-line bg-surface p-4 transition hover:border-line-strong hover:shadow-sm"
      data-testid="skills-directory-card"
      data-catalog-id={skill.id}
    >
      <div className="flex flex-1 flex-col text-left">
        <div className="flex items-start gap-3">
          <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border border-line bg-surface-muted text-fg-subtle">
            <Code2 className="h-4 w-4" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-start justify-between gap-2">
              <h3 className="truncate text-base font-semibold text-fg">
                {skill.name || skill.slug}
              </h3>
              {skill.rank ? (
                <span className="mt-0.5 shrink-0 text-xs text-fg-faint">#{skill.rank}</span>
              ) : null}
            </div>
            <p className="mt-1 truncate text-xs text-fg-subtle">{skill.source}</p>
          </div>
        </div>
        <p className="mt-3 line-clamp-3 break-all text-sm leading-5 text-fg-muted">{skill.id}</p>
        <div className="mt-auto flex flex-wrap items-center gap-1.5 pt-4">
          <Badge variant="neutral">Skill</Badge>
          {typeof skill.installs === "number" ? (
            <Badge variant="neutral">
              {t("capabilities.skillsDirectory.installs", {
                count: numberFormatter.format(skill.installs),
              })}
            </Badge>
          ) : null}
          {(skill.sourceType ?? skill.source_type) ? (
            <Badge variant="neutral">{skill.sourceType ?? skill.source_type}</Badge>
          ) : null}
        </div>
      </div>
      <div className="mt-3 border-t border-line pt-3">
        {installedCapabilityID ? (
          <Button
            className="w-full"
            variant="outline"
            size="sm"
            onClick={() => onViewCapability(installedCapabilityID)}
          >
            <Check className="h-3.5 w-3.5" /> {t("capabilities.skillsDirectory.install.installed")}
          </Button>
        ) : (
          <Button
            className="w-full"
            size="sm"
            disabled={!canImport || installing}
            title={!canImport ? t("capabilities.permission.adminOnly") : undefined}
            onClick={onInstall}
          >
            {installing ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Download className="h-3.5 w-3.5" />
            )}
            {installing
              ? t("capabilities.skillsDirectory.install.installing")
              : canImport
                ? t("capabilities.skillsDirectory.install.action")
                : t("capabilities.permission.adminOnly")}
          </Button>
        )}
      </div>
    </article>
  )
}

function SuccessBanner({
  success,
  onViewCapability,
}: {
  success: { name: string; capabilityID: string }
  onViewCapability: (capabilityID: string) => void
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
        {t("capabilities.skillsDirectory.install.success", { name: success.name })}
      </p>
      <Button variant="outline" size="sm" onClick={() => onViewCapability(success.capabilityID)}>
        {t("capabilities.mcpDirectory.actions.viewCapability")}
      </Button>
    </div>
  )
}
