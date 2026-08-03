import { ArrowLeft, ExternalLink, ShieldCheck, Sparkles } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Badge } from "../../../../components/ui/badge"
import { Button } from "../../../../components/ui/button"
import { EmptyState } from "../../../../components/ui/empty-state"
import { ErrorState } from "../../../../components/ui/error-state"
import { Skeleton } from "../../../../components/ui/skeleton"
import type { SkillDirectoryItem } from "../../../../lib/api-marketplace"
import { SkillFileTree } from "../SkillFileTree"
import type { CanonicalSkillSpec } from "../types"
import { SkillDirectoryIcon } from "./SkillDirectoryCard"

export function SkillDirectoryDetail({
  item,
  loading,
  error,
  canImport,
  onBack,
  onRetry,
  onImport,
  onViewCapability,
}: {
  item: SkillDirectoryItem | null
  loading: boolean
  error: unknown
  canImport: boolean
  onBack: () => void
  onRetry: () => void
  onImport: () => void
  onViewCapability: (capabilityID: string) => void
}) {
  const { t } = useTranslation("admin")
  if (loading && !item) {
    return <div className="space-y-3"><Skeleton className="h-9 w-40" /><Skeleton className="h-[560px] w-full" /></div>
  }
  if (error) {
    return <ErrorState title={t("capabilities.skillDirectory.detail.loadError")} description={error instanceof Error ? error.message : ""} onRetry={onRetry} />
  }
  if (!item) {
    return <EmptyState icon={Sparkles} title={t("capabilities.skillDirectory.detail.notFound")} action={<Button variant="outline" size="sm" onClick={onBack}>{t("capabilities.skillDirectory.actions.back")}</Button>} />
  }

  const skill: CanonicalSkillSpec = {
    slug: item.slug ?? item.id,
    title: item.title ?? item.name,
    description: item.description,
    instruction: item.instruction ?? "",
    trigger: item.trigger,
    files: item.files,
  }

  return (
    <div className="space-y-3" data-testid="skill-directory-detail">
      <Button variant="ghost" size="sm" onClick={onBack}><ArrowLeft className="h-3.5 w-3.5" /> {t("capabilities.skillDirectory.actions.back")}</Button>
      <article className="rounded-xl border border-line bg-surface p-5">
        <div className="flex flex-wrap items-start gap-4">
          <SkillDirectoryIcon item={item} large />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-xl font-semibold text-fg">{item.name}</h2>
              {item.verified ? <Badge variant="primary"><ShieldCheck className="h-3 w-3" /> {t("capabilities.skillDirectory.verified")}</Badge> : null}
              {item.installed ? <Badge variant="success">{t("capabilities.skillDirectory.actions.installed")}</Badge> : null}
            </div>
            <p className="mt-1 text-sm text-fg-subtle">{item.publisher.name}</p>
            <p className="mt-4 max-w-3xl text-sm leading-6 text-fg-muted">{item.description}</p>
          </div>
        </div>
        <div className="mt-5 grid gap-3 sm:grid-cols-3">
          <Meta label={t("capabilities.skillDirectory.detail.version")} value={item.version} mono />
          <Meta label={t("capabilities.skillDirectory.detail.license")} value={item.license} />
          <Meta label={t("capabilities.skillDirectory.detail.files")} value={String((item.files?.length ?? 0) + 1)} />
        </div>
        <div className="mt-5 grid gap-4 lg:grid-cols-[minmax(0,1fr)_240px]">
          <div className="min-w-0"><SkillFileTree skill={skill} /></div>
          <aside className="space-y-3 rounded-lg border border-line bg-surface-muted/20 p-4">
            <ExternalLinkRow label={t("capabilities.skillDirectory.detail.publisher")} value={item.publisher.name} href={item.publisher.url} />
            <ExternalLinkRow label={t("capabilities.skillDirectory.detail.homepage")} value={t("capabilities.skillDirectory.detail.openLink")} href={item.homepage_url} />
            <ExternalLinkRow label={t("capabilities.skillDirectory.detail.repository")} value={t("capabilities.skillDirectory.detail.openLink")} href={item.repository_url} />
            <Meta label={t("capabilities.skillDirectory.detail.sourceCommit")} value={item.source_ref ?? "—"} mono />
          </aside>
        </div>
        <div className="mt-5 flex flex-wrap justify-end gap-2 border-t border-line pt-4">
          {item.installed && item.installed_capability_id ? (
            <Button variant="outline" size="sm" onClick={() => onViewCapability(item.installed_capability_id!)}>{t("capabilities.skillDirectory.actions.viewCapability")}</Button>
          ) : (
            <Button size="sm" disabled={!canImport} title={!canImport ? t("capabilities.permission.adminOnly") : undefined} onClick={onImport}>
              {canImport ? t("capabilities.skillDirectory.actions.import") : t("capabilities.permission.adminOnly")}
            </Button>
          )}
        </div>
      </article>
    </div>
  )
}

function Meta({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div className="rounded-lg border border-line p-3"><p className="text-xs text-fg-subtle">{label}</p><p className={`mt-1 text-sm text-fg ${mono ? "font-mono break-all" : ""}`}>{value}</p></div>
}

function ExternalLinkRow({ label, value, href }: { label: string; value: string; href?: string }) {
  let safeHref: string | undefined
  try {
    if (href) {
      const parsed = new URL(href)
      if (parsed.protocol === "http:" || parsed.protocol === "https:") safeHref = parsed.toString()
    }
  } catch {
    safeHref = undefined
  }
  return <div><p className="text-xs text-fg-subtle">{label}</p>{safeHref ? <a href={safeHref} target="_blank" rel="noreferrer" className="mt-1 inline-flex items-center gap-1 text-sm text-fg underline decoration-line-strong underline-offset-2"><span>{value}</span><ExternalLink className="h-3 w-3" /></a> : <p className="mt-1 text-sm text-fg-faint">—</p>}</div>
}
