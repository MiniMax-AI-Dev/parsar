import { useTranslation } from "react-i18next"
import { navigateAdmin, type AdminView } from "../../lib/admin-router"
import { cn } from "../../lib/utils"

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
  "connectors",
  "usage",
  "audit",
]

interface SettingsTabsProps {
  active: SettingsTab
}

/**
 * Horizontal tab strip at the top of every settings sub-page. Each tab
 * navigates to its own admin view; the sidebar always highlights the
 * single "Settings" entry while the strip handles intra-settings nav.
 */
export function SettingsTabs({ active }: SettingsTabsProps) {
  const { t } = useTranslation("common")

  return (
    <nav className="mb-5 flex gap-1 border-b border-line">
      {TABS.map((tab) => {
        const isActive = tab === active
        return (
          <button
            key={tab}
            type="button"
            onClick={() => navigateAdmin(TAB_TO_VIEW[tab])}
            className={cn(
              "relative -mb-px border-b-2 px-3 py-2 text-sm transition-colors",
              isActive
                ? "border-line-strong font-medium text-fg"
                : "border-transparent font-normal text-fg-subtle hover:text-fg-emphasis",
            )}
          >
            {t(`nav.settingsTabs.${tab}` as never)}
          </button>
        )
      })}
    </nav>
  )
}
