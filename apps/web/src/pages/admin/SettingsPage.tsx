import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { SettingsTabs } from "../../components/layout/SettingsTabs"
import { Input } from "../../components/ui/input"
import { SUPPORTED_LANGUAGES, type SupportedLanguage } from "../../i18n"
import { cn } from "../../lib/utils"
import { useWorkspaceAuthProviders, type WorkspaceAuthProvider } from "../../lib/api-auth"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import { useWorkspaceId } from "../../lib/workspace"

export function SettingsPage() {
  const { t, i18n } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const wsId = useWorkspaceId()
  const workspacesQ = useMyWorkspaces()
  const authProvidersQ = useWorkspaceAuthProviders(wsId)
  const workspace = workspacesQ.data?.workspaces.find((w) => w.id === wsId)
  const currentLang = (i18n.resolvedLanguage ?? "en-US") as SupportedLanguage

  return (
    <AdminLayout activeMenu="settings">
      <PageHeader title={t("settings.page.title")} />
      <SettingsTabs active="general" />

      <div className="grid gap-6">
        <Section
          title={t("settings.workspace.title")}
          description={t("settings.workspace.description")}
        >
          <FormRow
            label={t("settings.workspace.name")}
            hint={t("settings.workspace.nameHint")}
          >
            <Input value={workspace?.name ?? "Demo Workspace"} readOnly />
          </FormRow>
          <FormRow
            label={t("settings.workspace.runtimeRoot")}
            hint={t("settings.workspace.runtimeRootHint")}
          >
            <code className="block rounded-md border border-line bg-surface-subtle px-3 py-2 font-mono text-sm text-fg-emphasis">
              ~/.parsar/
            </code>
          </FormRow>
        </Section>

        <Section title={tc("languageSwitcher.label")}>
          <FormRow
            label={tc("languageSwitcher.label")}
            hint={tc("languageSwitcher.description")}
          >
            <div className="inline-flex rounded-md border border-line bg-surface p-0.5">
              {SUPPORTED_LANGUAGES.map((lang) => {
                const active = currentLang === lang
                return (
                  <button
                    key={lang}
                    type="button"
                    onClick={() => void i18n.changeLanguage(lang)}
                    className={cn(
                      "rounded px-3 py-1 text-sm transition-colors",
                      active
                        ? "bg-surface-emphasis text-white"
                        : "text-fg-muted hover:bg-surface-muted",
                    )}
                  >
                    {tc(`languageSwitcher.${lang}` as never)}
                  </button>
                )
              })}
            </div>
          </FormRow>
        </Section>

        <Section
          title={t("settings.authentication.title")}
          description={t("settings.authentication.description")}
        >
          {authProvidersQ.isLoading ? (
            <p className="text-sm text-fg-subtle">{t("settings.authentication.loading")}</p>
          ) : authProvidersQ.isError ? (
            <p className="text-sm text-danger">{t("settings.authentication.error")}</p>
          ) : (
            <div className="grid gap-3">
              {(authProvidersQ.data?.providers ?? []).map((provider) => (
                <AuthProviderCard key={provider.id} provider={provider} />
              ))}
            </div>
          )}
        </Section>

        <Section
          title={t("settings.runtime.policy.title")}
          description={t("settings.runtime.policy.description")}
        >
          <div className="grid gap-3 md:grid-cols-2">
            <PolicyCard
              title={t("settings.runtime.policy.workdirs.title")}
              body={t("settings.runtime.policy.workdirs.body")}
              value="/absolute/path, ~/path"
            />
            <PolicyCard
              title={t("settings.runtime.policy.runtimeState.title")}
              body={t("settings.runtime.policy.runtimeState.body")}
              value="~/.parsar/"
            />
            <PolicyCard
              title={t("settings.runtime.policy.agentRuntime.title")}
              body={t("settings.runtime.policy.agentRuntime.body")}
              value={t("settings.runtime.policy.agentRuntime.value")}
            />
            <PolicyCard
              title={t("settings.runtime.policy.capabilities.title")}
              body={t("settings.runtime.policy.capabilities.body")}
              value={t("settings.runtime.policy.capabilities.value")}
            />
          </div>
        </Section>
      </div>
    </AdminLayout>
  )
}

function AuthProviderCard({ provider }: { provider: WorkspaceAuthProvider }) {
  const { t } = useTranslation("admin")
  const missing = provider.missing_env ?? []
  const required = provider.required_env ?? []
  const statusLabel = provider.enabled
    ? t("settings.authentication.status.enabled")
    : provider.configured
      ? t("settings.authentication.status.configured")
      : t("settings.authentication.status.missing")

  return (
    <article className="rounded-md border border-line bg-surface-subtle/70 p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <div className="text-sm font-semibold text-fg">{provider.label}</div>
          <div className="mt-1 text-xs text-fg-subtle">{provider.type}</div>
        </div>
        <span
          className={cn(
            "rounded-full border px-2.5 py-1 text-xs font-medium",
            provider.enabled
              ? "border-success/40 bg-success/10 text-success"
              : "border-line bg-surface text-fg-muted",
          )}
        >
          {statusLabel}
        </span>
      </div>

      {provider.callback_url && (
        <div className="mt-4">
          <div className="text-xs font-medium text-fg-muted">
            {t("settings.authentication.callbackUrl")}
          </div>
          <code className="mt-1 block break-all rounded-md border border-line bg-surface px-3 py-2 font-mono text-sm text-fg-muted">
            {provider.callback_url}
          </code>
        </div>
      )}

      {required.length > 0 && (
        <div className="mt-4 grid gap-3 md:grid-cols-2">
          <EnvList
            title={t("settings.authentication.requiredEnv")}
            items={required}
          />
          <EnvList
            title={t("settings.authentication.missingEnv")}
            items={missing}
            emptyLabel={t("settings.authentication.noneMissing")}
          />
        </div>
      )}

      {provider.docs_url && (
        <div className="mt-4 text-xs text-fg-subtle">
          {t("settings.authentication.docsHint")}{" "}
          <code className="font-mono text-fg-muted">{provider.docs_url}</code>
        </div>
      )}
    </article>
  )
}

function EnvList({
  title,
  items,
  emptyLabel,
}: {
  title: string
  items: string[]
  emptyLabel?: string
}) {
  return (
    <div>
      <div className="text-xs font-medium text-fg-muted">{title}</div>
      {items.length > 0 ? (
        <div className="mt-1 flex flex-wrap gap-1.5">
          {items.map((item) => (
            <code
              key={item}
              className="rounded border border-line bg-surface px-2 py-1 font-mono text-xs text-fg-muted"
            >
              {item}
            </code>
          ))}
        </div>
      ) : (
        <p className="mt-1 text-sm text-fg-subtle">{emptyLabel ?? ""}</p>
      )}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Form helpers                                                       */
/* ------------------------------------------------------------------ */

function Section({
  title,
  description,
  children,
  className,
}: {
  title: string
  description?: string
  children: ReactNode
  className?: string
}) {
  return (
    <section className={`rounded-lg border border-line bg-surface p-5 ${className ?? ""}`}>
      <header className="mb-4">
        <h2 className="text-base font-semibold text-fg">{title}</h2>
        {description && (
          <p className="mt-0.5 text-sm text-fg-subtle">{description}</p>
        )}
      </header>
      <div className="space-y-4">{children}</div>
    </section>
  )
}

function FormRow({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <div className="grid grid-cols-1 gap-2 md:grid-cols-[200px_1fr] md:gap-6">
      <div>
        <label className="text-sm font-medium text-fg-emphasis">{label}</label>
        {hint && <p className="mt-0.5 text-xs text-fg-subtle">{hint}</p>}
      </div>
      <div>{children}</div>
    </div>
  )
}

function PolicyCard({
  title,
  body,
  value,
}: {
  title: string
  body: string
  value: string
}) {
  return (
    <article className="rounded-md border border-line bg-surface-subtle/70 p-4">
      <div className="text-sm font-semibold text-fg">{title}</div>
      <p className="mt-1 min-h-10 text-sm leading-5 text-fg-subtle">{body}</p>
      <div className="mt-3 rounded-md border border-line bg-surface px-3 py-2 font-mono text-sm text-fg-muted">
        {value}
      </div>
    </article>
  )
}
