import { Moon, Sun } from "lucide-react"
import { useTranslation } from "react-i18next"
import { cn } from "../../lib/utils"
import { useTheme, type ResolvedTheme } from "../../lib/theme"

/**
 * Two-segment light / dark toggle in the sidebar account row. Picking a
 * segment stores an explicit preference; a "system" preference simply
 * shows whichever segment is currently resolved.
 */
export function ThemeMenu() {
  const { t } = useTranslation("common")
  const { resolvedTheme, setPreference } = useTheme()

  const segments: { value: ResolvedTheme; icon: typeof Sun; labelKey: "light" | "dark" }[] = [
    { value: "light", icon: Sun, labelKey: "light" },
    { value: "dark", icon: Moon, labelKey: "dark" },
  ]

  return (
    <div
      role="group"
      aria-label={t("theme.label")}
      className="inline-flex shrink-0 gap-0.5 rounded-md border border-line p-0.5"
    >
      {segments.map(({ value, icon: Icon, labelKey }) => {
        const pressed = resolvedTheme === value
        return (
          <button
            key={value}
            type="button"
            aria-pressed={pressed}
            aria-label={t(`theme.options.${labelKey}` as never)}
            title={t(`theme.options.${labelKey}` as never)}
            onClick={() => setPreference(value)}
            className={cn(
              "inline-flex h-[22px] min-w-[26px] items-center justify-center rounded text-fg-muted active:scale-[0.97] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40",
              pressed ? "app-shadow-control bg-surface text-fg" : "hover:text-fg",
            )}
          >
            <Icon className="h-3.5 w-3.5" strokeWidth={1.5} aria-hidden="true" />
          </button>
        )
      })}
    </div>
  )
}
