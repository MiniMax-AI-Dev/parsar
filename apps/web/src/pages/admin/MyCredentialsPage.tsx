/**
 * Standalone deep-link surface (`?profile=credentials`) for personal
 * credentials. Wraps PersonalCredentialsTab in a frame without the admin
 * sidebar so the user isn't dropped into the workspace navigator.
 */
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Plus, Search } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { Button } from "../../components/ui/button"
import { Input } from "../../components/ui/input"
import { safeReturnTo, useAppRoute } from "../../lib/admin-router"
import { PersonalCredentialsTab } from "./credentials/PersonalCredentialsTab"

export function MyCredentialsPage() {
  const { t } = useTranslation("admin")
  const route = useAppRoute()
  const [query, setQuery] = useState("")
  const [createRequest, setCreateRequest] = useState(0)
  const returnTo = route.returnTo ? safeReturnTo(route.returnTo) : null

  return (
    <AdminLayout hideSidebar activeMenu="" fullBleed>
      <div className="flex min-h-0 flex-1 flex-col">
        <PageHeader
          className="static mx-0 mb-0"
          title={t("myCredentials.page.title")}
          subtitleFor="myCredentials.page.title"
          action={
            <>
              <div className="relative w-56">
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
              {returnTo && (
                <Button variant="outline" onClick={() => window.location.assign(returnTo)}>
                  {t("myCredentials.returnBanner.action")}
                </Button>
              )}
              <Button onClick={() => setCreateRequest((n) => n + 1)}>
                <Plus strokeWidth={1.5} aria-hidden="true" />
                {t("credentialsPage.personal.add")}
              </Button>
            </>
          }
        />
        <div className="flex min-h-0 flex-1 flex-col">
          <PersonalCredentialsTab standalone query={query} createRequest={createRequest} />
        </div>
      </div>
    </AdminLayout>
  )
}
