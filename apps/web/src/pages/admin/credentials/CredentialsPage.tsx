import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Plus, Search } from "lucide-react"

import { AdminLayout } from "../../../components/layout/AdminLayout"
import { PageHeader } from "../../../components/layout/PageHeader"
import { SettingsTabs } from "../../../components/layout/SettingsTabs"
import { ScopeRequiredState } from "../../../components/admin/ScopeRequiredState"
import { Button } from "../../../components/ui/button"
import { Input } from "../../../components/ui/input"
import { Tabs, TabsList, TabsTrigger } from "../../../components/ui/tabs"
import { useAppRoute } from "../../../lib/admin-router"
import { useMyWorkspaces } from "../../../lib/api-workspaces"
import { useWorkspaceId } from "../../../lib/workspace"
import { OrgSecretsTab } from "./OrgSecretsTab"
import { PersonalCredentialsTab } from "./PersonalCredentialsTab"
import { isWorkspaceAdmin } from "./shared"

type TabKey = "personal" | "org"

export function CredentialsPage() {
  const { t } = useTranslation("admin")
  const wsId = useWorkspaceId()
  const route = useAppRoute()
  const workspacesQ = useMyWorkspaces()
  const isAdmin = isWorkspaceAdmin(wsId, workspacesQ.data?.workspaces)

  const initialFromURL = route.tab === "org" && isAdmin ? "org" : "personal"
  const [tab, setTab] = useState<TabKey>(initialFromURL)
  const [query, setQuery] = useState("")
  const [createRequest, setCreateRequest] = useState(0)

  useEffect(() => {
    if (tab === "org" && !isAdmin) setTab("personal")
  }, [tab, isAdmin])

  // replaceState (not push) — tabs aren't a navigation primitive, the
  // back button shouldn't stair-step through them.
  useEffect(() => {
    const url = new URL(window.location.href)
    const current = url.searchParams.get("tab")
    if (current === tab) return
    url.searchParams.set("tab", tab)
    window.history.replaceState(window.history.state, "", url.toString())
  }, [tab])

  const showOrg = tab === "org" && isAdmin
  const pageTitle = t("credentialsPage.title")

  return (
    <AdminLayout activeMenu="settings" fullBleed>
      <div className="flex min-h-0 flex-1 flex-col">
        <PageHeader
          className="static mx-0 mb-0"
          title={pageTitle}
          subtitleFor="credentialsPage.title"
          action={
            <>
              <SettingsTabs active="credentials" />
              {wsId && (
                <>
                  <div className="relative w-72">
                    <Search
                      className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted"
                      strokeWidth={1.5}
                      aria-hidden="true"
                    />
                    <Input
                      type="search"
                      placeholder={t("myCredentials.search.placeholder")}
                      aria-label={t("myCredentials.search.placeholder")}
                      className="pl-7"
                      value={query}
                      onChange={(e) => setQuery(e.target.value)}
                    />
                  </div>
                  <Button onClick={() => setCreateRequest((n) => n + 1)}>
                    <Plus strokeWidth={1.5} aria-hidden="true" />
                    {showOrg ? t("credentialsPage.org.add") : t("credentialsPage.personal.add")}
                  </Button>
                </>
              )}
            </>
          }
        />

        {!wsId ? (
          <ScopeRequiredState scope="workspace" resourceName={pageTitle} />
        ) : (
          <>
            {isAdmin && (
              <div className="flex h-10 shrink-0 items-center border-b border-line px-4">
                <Tabs value={tab} onValueChange={(value) => setTab(value as TabKey)}>
                  <TabsList aria-label={pageTitle}>
                    <TabsTrigger value="personal">{t("credentialsPage.tabs.personal")}</TabsTrigger>
                    <TabsTrigger value="org">{t("credentialsPage.tabs.org")}</TabsTrigger>
                  </TabsList>
                </Tabs>
              </div>
            )}
            <div className="flex min-h-0 flex-1 flex-col">
              {showOrg ? (
                <OrgSecretsTab workspaceID={wsId} query={query} createRequest={createRequest} />
              ) : (
                <PersonalCredentialsTab query={query} createRequest={createRequest} />
              )}
            </div>
          </>
        )}
      </div>
    </AdminLayout>
  )
}
