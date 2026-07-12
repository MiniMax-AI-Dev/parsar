import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { Check, Monitor, Moon, Sun, type LucideIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { cn } from "../../lib/utils"
import { useTheme, type ThemePreference } from "../../lib/theme"

interface ThemeOption {
  value: ThemePreference
  labelKey: "light" | "dark" | "system"
  icon: LucideIcon
}

const options: ThemeOption[] = [
  { value: "light", labelKey: "light", icon: Sun },
  { value: "dark", labelKey: "dark", icon: Moon },
  { value: "system", labelKey: "system", icon: Monitor },
]

export function ThemeMenu() {
  const { t } = useTranslation("common")
  const { preference, resolvedTheme, setPreference } = useTheme()
  const TriggerIcon = resolvedTheme === "dark" ? Moon : Sun

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          aria-label={t("theme.trigger")}
          title={t("theme.trigger")}
          className="inline-flex h-9 w-9 items-center justify-center rounded-md text-fg-muted transition-colors hover:bg-surface-muted hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-line-strong data-[state=open]:bg-surface-muted"
        >
          <TriggerIcon className="h-4 w-4" strokeWidth={1.75} />
        </button>
      </DropdownMenu.Trigger>

      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={6}
          className="z-50 min-w-[180px] overflow-hidden rounded-md border border-line bg-surface p-1 text-sm text-fg-muted shadow-lg data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0"
        >
          <DropdownMenu.Label className="px-2 py-1.5 text-xs font-medium text-fg-subtle">
            {t("theme.label")}
          </DropdownMenu.Label>
          {options.map((option) => {
            const Icon = option.icon
            const selected = preference === option.value
            return (
              <DropdownMenu.Item
                key={option.value}
                onSelect={() => setPreference(option.value)}
                className={cn(
                  "flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 outline-none text-fg-muted",
                  "data-[highlighted]:bg-surface-muted data-[highlighted]:text-fg"
                )}
              >
                <Icon className="h-3.5 w-3.5" strokeWidth={1.75} />
                <span className="flex-1">{t(`theme.options.${option.labelKey}` as never)}</span>
                {selected && <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={2} />}
              </DropdownMenu.Item>
            )
          })}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

