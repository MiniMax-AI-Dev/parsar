import * as Tooltip from "@radix-ui/react-tooltip"
import { useTranslation } from "react-i18next"

import { cn } from "../../lib/utils"
import { Badge } from "./badge"

interface ManagedBadgeProps {
  unmanaged?: boolean
  className?: string
}

/** Managed / unmanaged chip: the shared Badge, state carried by the dot. */
export function ManagedBadge({ unmanaged, className }: ManagedBadgeProps) {
  const { t } = useTranslation("admin")
  const label = unmanaged
    ? t("common.managedBadge.unmanaged")
    : t("common.managedBadge.managed")
  const tooltip = unmanaged
    ? t("common.managedBadge.unmanagedTooltip")
    : t("common.managedBadge.managedTooltip")

  return (
    <Tooltip.Provider delayDuration={150}>
      <Tooltip.Root>
        <Tooltip.Trigger asChild>
          <Badge variant={unmanaged ? "neutral" : "success"} dot className={cn("cursor-help", className)}>
            {label}
          </Badge>
        </Tooltip.Trigger>
        <Tooltip.Portal>
          <Tooltip.Content
            side="top"
            sideOffset={4}
            className="app-shadow-floating z-50 max-w-xs rounded-md border border-line bg-surface px-2.5 py-1.5 text-xs text-fg animate-pop-in"
          >
            {tooltip}
          </Tooltip.Content>
        </Tooltip.Portal>
      </Tooltip.Root>
    </Tooltip.Provider>
  )
}
