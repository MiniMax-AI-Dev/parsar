import * as Tooltip from "@radix-ui/react-tooltip"
import { CheckCircle2, CircleAlert } from "lucide-react"
import { useTranslation } from "react-i18next"

import { cn } from "../../lib/utils"

interface ManagedBadgeProps {
  unmanaged?: boolean
  className?: string
}

export function ManagedBadge({ unmanaged, className }: ManagedBadgeProps) {
  const { t } = useTranslation("admin")
  const label = unmanaged
    ? t("common.managedBadge.unmanaged")
    : t("common.managedBadge.managed")
  const tooltip = unmanaged
    ? t("common.managedBadge.unmanagedTooltip")
    : t("common.managedBadge.managedTooltip")
  const Icon = unmanaged ? CircleAlert : CheckCircle2

  return (
    <Tooltip.Provider delayDuration={150}>
      <Tooltip.Root>
        <Tooltip.Trigger asChild>
          <span
            className={cn(
              "inline-flex cursor-help items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium",
              unmanaged
                ? "border-line bg-surface-subtle text-fg-muted"
                : "border-success-border bg-success-subtle text-success",
              className
            )}
          >
            <Icon className="h-3 w-3" />
            {label}
          </span>
        </Tooltip.Trigger>
        <Tooltip.Portal>
          <Tooltip.Content
            side="top"
            className="z-50 max-w-xs rounded-md border border-line bg-surface px-3 py-2 text-sm leading-relaxed text-fg-muted shadow-lg"
          >
            {tooltip}
            <Tooltip.Arrow className="fill-white" />
          </Tooltip.Content>
        </Tooltip.Portal>
      </Tooltip.Root>
    </Tooltip.Provider>
  )
}
