import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { Input } from "../../components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../../components/ui/tabs"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import { useWorkspaceId } from "../../lib/workspace"

export function SettingsPage() {
  const { t } = useTranslation("admin")
  const wsId = useWorkspaceId()
  const workspacesQ = useMyWorkspaces()
  const workspace = workspacesQ.data?.workspaces.find((w) => w.id === wsId)

  return (
    <AdminLayout activeMenu="settings">
      <PageHeader
        title={t("settings.page.title")}
      />

      <div className="grid gap-6">
        <Tabs defaultValue="basic">
          <TabsList>
            <TabsTrigger value="basic">{t("settings.tabs.basic")}</TabsTrigger>
            <TabsTrigger value="runtime">{t("settings.tabs.runtime")}</TabsTrigger>
          </TabsList>

          <TabsContent value="basic">
            <Section title={t("settings.workspace.title")} description={t("settings.workspace.description")}>
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
                <code className="block rounded-md border border-slate-200 bg-slate-50 px-3 py-2 font-mono text-[13px] text-slate-800">
                  ~/.parsar/
                </code>
              </FormRow>
            </Section>
          </TabsContent>

          <TabsContent value="runtime">
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
          </TabsContent>
        </Tabs>
      </div>
    </AdminLayout>
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
    <section className={`rounded-lg border border-slate-200 bg-white p-5 ${className ?? ""}`}>
      <header className="mb-4">
        <h2 className="text-[14px] font-semibold text-slate-900">{title}</h2>
        {description && (
          <p className="mt-0.5 text-[13px] text-slate-500">{description}</p>
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
        <label className="text-[13px] font-medium text-slate-800">{label}</label>
        {hint && <p className="mt-0.5 text-[12px] text-slate-500">{hint}</p>}
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
    <article className="rounded-md border border-slate-200 bg-slate-50/70 p-4">
      <div className="text-[13px] font-semibold text-slate-900">{title}</div>
      <p className="mt-1 min-h-10 text-[13px] leading-5 text-slate-500">{body}</p>
      <div className="mt-3 rounded-md border border-slate-200 bg-white px-3 py-2 font-mono text-[13px] text-slate-700">
        {value}
      </div>
    </article>
  )
}
