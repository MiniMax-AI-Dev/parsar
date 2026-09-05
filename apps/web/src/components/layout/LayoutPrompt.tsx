import { createPortal } from "react-dom"
import { useTranslation } from "react-i18next"
import { Button } from "../ui/button"

/**
 * Small prompt at the top of the viewport after a panel edge was dragged:
 * save (persists across reloads) · this session only · restore (springs
 * the panel back to its default width).
 */
export function LayoutPrompt({
  open,
  onSave,
  onTemporary,
  onRestore,
}: {
  open: boolean
  onSave: () => void
  onTemporary: () => void
  onRestore: () => void
}) {
  const { t } = useTranslation("common")
  if (!open || typeof document === "undefined") return null
  return createPortal(
    <div
      role="status"
      className="app-shadow-floating fixed left-1/2 top-3 z-50 flex -translate-x-1/2 items-center gap-2 rounded-lg border border-line bg-surface py-1.5 pl-3 pr-1.5 text-sm text-fg animate-pop-in"
    >
      <span className="mr-1">{t("layout.adjusted")}</span>
      <Button size="sm" onClick={onSave}>
        {t("actions.save")}
      </Button>
      <Button size="sm" variant="outline" onClick={onTemporary}>
        {t("layout.temporary")}
      </Button>
      <Button size="sm" variant="ghost" onClick={onRestore}>
        {t("layout.restore")}
      </Button>
    </div>,
    document.body,
  )
}
