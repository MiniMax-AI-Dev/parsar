import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { Box, AlertTriangle, Info, ExternalLink } from "lucide-react"

import type { RuntimeStatus } from "../../lib/api-runtime"

interface RuntimeEmptyStateProps {
  status: RuntimeStatus
  /** Only rendered for the newDeployment cell. */
  tryCta?: ReactNode
}

export function RuntimeEmptyState({ status, tryCta }: RuntimeEmptyStateProps) {
  const cell = classifyEmptyCell(status)
  switch (cell) {
    case "newDeployment":
      return <NewDeploymentCell tryCta={tryCta} />
    case "localOss":
      return <LocalOssCell />
    case "localManaged":
      return <LocalManagedCell />
    case "localSelfhost":
      return <LocalSelfhostCell />
    case "misconfiguredOss":
      return <MisconfiguredOssCell />
    case "misconfiguredManaged":
      return <MisconfiguredManagedCell />
    case "misconfiguredSelfhost":
      return <MisconfiguredSelfhostCell />
  }
}

export type EmptyCell =
  | "newDeployment"
  | "localOss"
  | "localManaged"
  | "localSelfhost"
  | "misconfiguredOss"
  | "misconfiguredManaged"
  | "misconfiguredSelfhost"

export function classifyEmptyCell(status: RuntimeStatus): EmptyCell {
  // Translates v4's mode=local/sandbox cells onto v5's has_credential
  // axis so the existing copy bundles stay rendering. PR-B replaces
  // this mechanical mapping with sandbox_agent_count keying.
  if (status.profile === "managed") {
    return status.available ? "newDeployment" : "misconfiguredManaged"
  }
  if (!status.has_credential) {
    if (status.profile === "selfhost") return "localSelfhost"
    return "localOss"
  }
  if (!status.available) {
    if (status.profile === "selfhost") return "misconfiguredSelfhost"
    return "misconfiguredOss"
  }
  return "newDeployment"
}

type Shape = "info" | "warn"

function EmptyShell({
  shape,
  title,
  children,
  testId,
}: {
  shape: Shape
  title: string
  children: ReactNode
  testId: string
}) {
  const Icon = shape === "warn" ? AlertTriangle : Info
  const styles = SHAPE_STYLES[shape]
  return (
    <div
      className={`flex flex-col items-start gap-3 rounded-lg border border-dashed px-6 py-10 ${styles.container}`}
      data-testid={testId}
    >
      <div className="flex items-center gap-2.5">
        <div className={`rounded-full p-2 ${styles.iconWrap}`}>
          <Icon className={`h-4 w-4 ${styles.icon}`} strokeWidth={1.75} />
        </div>
        <p className={`text-base font-medium ${styles.title}`}>{title}</p>
      </div>
      <div className="max-w-xl space-y-2 text-sm leading-relaxed text-fg-muted">
        {children}
      </div>
    </div>
  )
}

const SHAPE_STYLES: Record<Shape, { container: string; iconWrap: string; icon: string; title: string }> = {
  info: {
    container: "border-line bg-surface-subtle/60",
    iconWrap: "bg-surface-muted/60",
    icon: "text-fg-muted",
    title: "text-fg",
  },
  warn: {
    container: "border-warning-border bg-warning-subtle/60",
    iconWrap: "bg-warning-subtle/60",
    icon: "text-warning",
    title: "text-warning-emphasis",
  },
}

function DocsLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="inline-flex items-center gap-0.5 text-fg-muted underline decoration-slate-300 underline-offset-2 hover:text-fg hover:decoration-slate-500"
    >
      {children}
      <ExternalLink className="h-3 w-3" />
    </a>
  )
}

function NewDeploymentCell({ tryCta }: { tryCta?: ReactNode }) {
  const { t } = useTranslation("admin")
  return (
    <div
      className="flex flex-col items-center gap-3 rounded-lg border border-dashed border-line bg-surface px-6 py-12 text-center"
      data-testid="runtime-empty-new-deployment"
    >
      <div className="rounded-full bg-surface-muted p-3 text-fg-subtle">
        <Box className="h-5 w-5" />
      </div>
      <div className="max-w-md space-y-1.5">
        <p className="text-sm font-medium text-fg">
          {t("runtime.list.empty.newDeployment.title")}
        </p>
        <p className="text-xs text-fg-subtle">
          {t("runtime.list.empty.newDeployment.description")}
        </p>
        <p className="pt-1 text-xs text-fg-subtle">
          {t("runtime.list.empty.newDeployment.tryCta")}
        </p>
      </div>
      {tryCta && <div className="mt-2">{tryCta}</div>}
    </div>
  )
}

function LocalOssCell() {
  const { t } = useTranslation("admin")
  return (
    <EmptyShell
      shape="info"
      title={t("runtime.list.empty.localOss.title")}
      testId="runtime-empty-local-oss"
    >
      <p>{t("runtime.list.empty.localOss.description")}</p>
      <p>
        {t("runtime.list.empty.localOss.afterSnippet")}
        <DocsLink href="#deploy-docs">
          {t("runtime.list.empty.localOss.docsLink")} →
        </DocsLink>
      </p>
      <p>
        <DocsLink href="https://e2b.dev">
          {t("runtime.list.empty.localOss.e2bSignup")} →
        </DocsLink>
      </p>
    </EmptyShell>
  )
}

function LocalManagedCell() {
  const { t } = useTranslation("admin")
  return (
    <EmptyShell
      shape="info"
      title={t("runtime.list.empty.localManaged.title")}
      testId="runtime-empty-local-managed"
    >
      <p>{t("runtime.list.empty.localManaged.description")}</p>
      <p className="text-fg-subtle">{t("runtime.list.empty.localManaged.fallback")}</p>
    </EmptyShell>
  )
}

function LocalSelfhostCell() {
  const { t } = useTranslation("admin")
  return (
    <EmptyShell
      shape="info"
      title={t("runtime.list.empty.localSelfhost.title")}
      testId="runtime-empty-local-selfhost"
    >
      <p>{t("runtime.list.empty.localSelfhost.description")}</p>
      <p className="text-fg-subtle">{t("runtime.list.empty.localSelfhost.afterSnippet")}</p>
    </EmptyShell>
  )
}

function MisconfiguredOssCell() {
  const { t } = useTranslation("admin")
  return (
    <EmptyShell
      shape="warn"
      title={t("runtime.list.empty.misconfiguredOss.title")}
      testId="runtime-empty-misconfigured-oss"
    >
      <p>{t("runtime.list.empty.misconfiguredOss.description")}</p>
      <ol className="ml-4 list-decimal space-y-1">
        <li>{t("runtime.list.empty.misconfiguredOss.step1")}</li>
        <li>{t("runtime.list.empty.misconfiguredOss.step2")}</li>
        <li>{t("runtime.list.empty.misconfiguredOss.step3")}</li>
      </ol>
      <p>
        <DocsLink href="#deploy-docs">
          {t("runtime.list.empty.misconfiguredOss.docsLink")} →
        </DocsLink>
      </p>
    </EmptyShell>
  )
}

function MisconfiguredManagedCell() {
  const { t } = useTranslation("admin")
  return (
    <EmptyShell
      shape="warn"
      title={t("runtime.list.empty.misconfiguredManaged.title")}
      testId="runtime-empty-misconfigured-managed"
    >
      <p>{t("runtime.list.empty.misconfiguredManaged.description")}</p>
      <p>
        <DocsLink href="#status-page">
          {t("runtime.list.empty.misconfiguredManaged.statusLink")} →
        </DocsLink>
      </p>
    </EmptyShell>
  )
}

function MisconfiguredSelfhostCell() {
  const { t } = useTranslation("admin")
  return (
    <EmptyShell
      shape="warn"
      title={t("runtime.list.empty.misconfiguredSelfhost.title")}
      testId="runtime-empty-misconfigured-selfhost"
    >
      <p>{t("runtime.list.empty.misconfiguredSelfhost.description")}</p>
      <ul className="ml-4 list-disc space-y-1">
        <li>{t("runtime.list.empty.misconfiguredSelfhost.step1")}</li>
        <li>{t("runtime.list.empty.misconfiguredSelfhost.step2")}</li>
      </ul>
    </EmptyShell>
  )
}
