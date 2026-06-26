/**
 * Standalone deep-link surface (`?profile=credentials`) for personal
 * credentials. Wraps PersonalCredentialsTab in a frame without the admin
 * sidebar so the user isn't dropped into the workspace navigator.
 */
import { useTranslation } from "react-i18next"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { PersonalCredentialsTab } from "./credentials/PersonalCredentialsTab"

export function MyCredentialsPage() {
  const { t } = useTranslation("admin")
  return (
    <AdminLayout hideSidebar activeMenu="" contentClassName="mx-auto max-w-3xl">
      <PageHeader
        title={t("myCredentials.page.title")}
        description={t("myCredentials.page.description")}
      />
      <PersonalCredentialsTab standalone />
    </AdminLayout>
  )
}
