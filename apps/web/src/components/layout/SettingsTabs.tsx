import { useTranslation } from "react-i18next"
import { navigateAdmin, type AdminView } from "../../lib/admin-router"
import { cn } from "../../lib/utils"
import { Tabs, TabsList, TabsTrigger } from "../ui/tabs"

export type SettingsTab =
  | "general"
  | "credentials"
  | "runtime"
  | "connectors"
  | "usage"
  | "audit"

// view per tab — drives both URL and which page component to render.
const TAB_TO_VIEW: Record<SettingsTab, AdminView> = {
  general: "settings",
  credentials: "secrets",
  runtime: "runtime",
  connectors: "connectors",
  usage: "usage",
  audit: "audit",
}

const TABS: SettingsTab[] = [
  "general",
  "credentials",
  "runtime",
  "usage",
  "audit",
]

interface SettingsTabsProps {
  active: SettingsTab
  className?: string
}

/**
 * The settings sub-navigation as the shared segmented control. Each
 * segment navigates to its own admin view; the sidebar keeps the single
 * "Settings" entry highlighted. Rendered once per page, in the
 * PageHeader action slot.
 */
export function SettingsTabs({ active, className }: SettingsTabsProps) {
  const { t } = useTranslation("common")

  return (
    <Tabs
      value={active}
      onValueChange={(next) => {
        if (next !== active) navigateAdmin(TAB_TO_VIEW[next as SettingsTab])
      }}
      className={cn("shrink-0", className)}
    >
      <TabsList aria-label={t("nav.items.settings")}>
        {TABS.map((tab) => (
          <TabsTrigger key={tab} value={tab}>
            {t(`nav.settingsTabs.${tab}` as never)}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  )
}
