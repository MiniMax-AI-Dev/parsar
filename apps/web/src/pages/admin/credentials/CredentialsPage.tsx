import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"

import { AdminLayout } from "../../../components/layout/AdminLayout"
import { PageHeader } from "../../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../../components/admin/ScopeRequiredState"
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "../../../components/ui/tabs"
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

  return (
    <AdminLayout activeMenu="secrets">
      <PageHeader
        title={t("credentialsPage.title")}
        description={t("credentialsPage.description")}
      />

      {!wsId ? (
        <ScopeRequiredState scope="workspace" resourceName={t("credentialsPage.title")} />
      ) : (
        <Tabs value={tab} onValueChange={(value) => setTab(value as TabKey)}>
          <TabsList>
            <TabsTrigger value="personal">{t("credentialsPage.tabs.personal")}</TabsTrigger>
            {isAdmin && (
              <TabsTrigger value="org">{t("credentialsPage.tabs.org")}</TabsTrigger>
            )}
          </TabsList>

          <TabsContent value="personal">
            <PersonalCredentialsTab />
          </TabsContent>

          {isAdmin && (
            <TabsContent value="org">
              <OrgSecretsTab workspaceID={wsId} />
            </TabsContent>
          )}
        </Tabs>
      )}
    </AdminLayout>
  )
}
