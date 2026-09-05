import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, ExternalLink } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { SettingsTabs } from "../../components/layout/SettingsTabs"
import { ActionIconButton, RowActions } from "../../components/ui/action-button"
import { Badge } from "../../components/ui/badge"
import { Ledger, LedgerHeader, LedgerId, LedgerRow } from "../../components/ui/ledger"
import { Property, PropertyList } from "../../components/ui/property-list"
import { Skeleton } from "../../components/ui/skeleton"
import { Tabs, TabsList, TabsTrigger } from "../../components/ui/tabs"
import { SUPPORTED_LANGUAGES, type SupportedLanguage } from "../../i18n"
import { useWorkspaceAuthProviders, type WorkspaceAuthProvider } from "../../lib/api-auth"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import { useWorkspaceId } from "../../lib/workspace"

/** provider · status · callback url · missing env · docs */
const PROVIDER_COLUMNS = "minmax(0,1fr) 104px minmax(0,1.4fr) minmax(0,1fr) 32px"

export function SettingsPage() {
  const { t, i18n } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const wsId = useWorkspaceId()
  const workspacesQ = useMyWorkspaces()
  const authProvidersQ = useWorkspaceAuthProviders(wsId)
  const workspace = workspacesQ.data?.workspaces.find((w) => w.id === wsId)
  const currentLang = (i18n.resolvedLanguage ?? "en-US") as SupportedLanguage
  const providers = authProvidersQ.data?.providers ?? []
  const languageLabel = tc("languageSwitcher.label")

  return (
    <AdminLayout activeMenu="settings" fullBleed>
      <div className="flex min-h-0 flex-1 flex-col">
        <PageHeader
          className="static mx-0 mb-0"
          title={t("settings.page.title")}
          subtitleFor="settings.page.title"
          action={<SettingsTabs active="general" />}
        />

        <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-10 pt-4">
          <div className="space-y-6">
            <Section title={t("settings.workspace.title")} description={t("settings.workspace.description")}>
              <PropertyList className="grid-cols-[160px_minmax(0,1fr)]">
                <Property label={t("settings.workspace.name")}>{workspace?.name ?? "—"}</Property>
                <Property label={t("settings.workspace.slug")} mono>{workspace?.slug ?? "—"}</Property>
                <Property label={languageLabel}>
                  <Tabs
                    value={currentLang}
                    onValueChange={(next) => void i18n.changeLanguage(next as SupportedLanguage)}
                  >
                    <TabsList aria-label={languageLabel}>
                      {SUPPORTED_LANGUAGES.map((lang) => (
                        <TabsTrigger key={lang} value={lang}>
                          {tc(`languageSwitcher.${lang}` as never)}
                        </TabsTrigger>
                      ))}
                    </TabsList>
                  </Tabs>
                </Property>
              </PropertyList>
            </Section>

            <Section
              title={t("settings.authentication.title")}
              description={t("settings.authentication.description")}
            >
              {authProvidersQ.isLoading ? (
                <div className="-mx-4">
                  {Array.from({ length: 3 }).map((_, i) => (
                    <div key={i} className="flex h-9 items-center gap-3 border-b border-line px-4">
                      <Skeleton className="h-3 w-32" />
                      <Skeleton className="h-3 w-20" />
                      <Skeleton className="h-3 flex-1" />
                    </div>
                  ))}
                </div>
              ) : authProvidersQ.isError ? (
                <p className="flex items-center gap-1.5 text-sm text-fg">
                  <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
                  {t("settings.authentication.error")}
                </p>
              ) : providers.length === 0 ? (
                <p className="text-sm text-fg-muted">{tc("states.emptyTitle")}</p>
              ) : (
                <Ledger
                  columns={PROVIDER_COLUMNS}
                  className="-mx-4 flex-none overflow-visible"
                  role="list"
                  aria-label={t("settings.authentication.title")}
                >
                  <LedgerHeader className="static">
                    <span>{t("settings.authentication.title")}</span>
                    <span>{t("connectors.table.status")}</span>
                    <span>{t("settings.authentication.callbackUrl")}</span>
                    <span>{t("settings.authentication.missingEnv")}</span>
                    <span />
                  </LedgerHeader>
                  <ul className="m-0 list-none p-0">
                    {providers.map((provider) => (
                      <AuthProviderRow key={provider.id} provider={provider} />
                    ))}
                  </ul>
                </Ledger>
              )}
            </Section>

            <Section
              title={t("settings.runtime.policy.title")}
              description={t("settings.runtime.policy.description")}
            >
              <PropertyList className="grid-cols-[160px_minmax(0,1fr)]">
                <Property label={t("settings.runtime.policy.workdirs.title")} mono>
                  /absolute/path, ~/path
                </Property>
                <Property label={t("settings.runtime.policy.runtimeState.title")} mono>
                  ~/.parsar/
                </Property>
                <Property label={t("settings.runtime.policy.agentRuntime.title")}>
                  {t("settings.runtime.policy.agentRuntime.value")}
                </Property>
                <Property label={t("settings.runtime.policy.capabilities.title")}>
                  {t("settings.runtime.policy.capabilities.value")}
                </Property>
              </PropertyList>
            </Section>
          </div>
        </div>
      </div>
    </AdminLayout>
  )
}

function AuthProviderRow({ provider }: { provider: WorkspaceAuthProvider }) {
  const { t } = useTranslation("admin")
  const missing = provider.missing_env ?? []
  const statusLabel = provider.enabled
    ? t("settings.authentication.status.enabled")
    : provider.configured
      ? t("settings.authentication.status.configured")
      : t("settings.authentication.status.missing")
  const variant = provider.enabled ? "success" : provider.configured ? "primary" : "neutral"

  return (
    <LedgerRow role="listitem" tabIndex={-1}>
      <span className="flex min-w-0 items-center gap-1.5">
        <span className="truncate font-medium">{provider.label}</span>
        <span className="shrink-0 text-xs text-fg-muted">{provider.type}</span>
      </span>
      <span>
        <Badge variant={variant} dot>{statusLabel}</Badge>
      </span>
      <LedgerId>{provider.callback_url ?? "—"}</LedgerId>
      {missing.length > 0 ? (
        <span className="truncate font-mono text-xs text-fg" title={missing.join(", ")}>
          {missing.join(", ")}
        </span>
      ) : (
        <span className="text-xs text-fg-muted">—</span>
      )}
      <RowActions>
        {provider.docs_url && (
          <ActionIconButton
            icon={ExternalLink}
            label={t("settings.authentication.docsHint")}
            onClick={() => window.open(provider.docs_url, "_blank", "noopener,noreferrer")}
          />
        )}
      </RowActions>
    </LedgerRow>
  )
}

/* ------------------------------------------------------------------ */
/*  Section head                                                       */
/* ------------------------------------------------------------------ */

/**
 * A page section: a 12px/500 head, then content. `description` is
 * accepted for call-site compatibility and never rendered — the design
 * system has no helper paragraphs.
 */
function Section({
  title,
  children,
}: {
  title: string
  description?: string
  children: ReactNode
}) {
  return (
    <section>
      <h2 className="mb-2 text-xs font-medium text-fg">{title}</h2>
      {children}
    </section>
  )
}
