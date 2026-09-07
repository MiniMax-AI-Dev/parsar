import { useEffect } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "../ui/button"
import { useToast } from "../ui/toast"

/**
 * Asks what to do with a panel width the reader just dragged: save it, keep it
 * for this session, or spring it back.
 *
 * It draws no strip of its own. It used to, at the same coordinate the toast
 * later took, which meant a confirmation could land on top of these buttons and
 * swallow the clicks. Going through the same queue makes that impossible:
 * messages stack instead of overlapping, and they inherit the queue's portal
 * and its z, so nothing else in the app can cover them either.
 *
 * It persists: unlike a confirmation, it is a question, and a question that
 * times out has answered itself. It is keyed per panel, because the sidebar and
 * the rail can each be mid-question, and it takes its question with it when the
 * panel unmounts — a strip with no timer and no owner would otherwise sit there
 * with dead buttons.
 */
export function LayoutPrompt({
  panel,
  open,
  onSave,
  onTemporary,
  onRestore,
}: {
  /** The panel this question is about, from `useResizableWidth`. */
  panel: string
  open: boolean
  onSave: () => void
  onTemporary: () => void
  onRestore: () => void
}) {
  const { t } = useTranslation("common")
  const { show, dismiss } = useToast()

  useEffect(() => {
    const key = `layout-adjusted:${panel}`
    if (!open) {
      dismiss(key)
      return
    }
    show(t("layout.adjusted"), {
      key,
      persist: true,
      action: (
        <>
          <Button size="sm" onClick={onSave}>{t("actions.save")}</Button>
          <Button size="sm" variant="outline" onClick={onTemporary}>{t("layout.temporary")}</Button>
          <Button size="sm" variant="ghost" onClick={onRestore}>{t("layout.restore")}</Button>
        </>
      ),
    })
    return () => dismiss(key)
  }, [panel, open, show, dismiss, t, onSave, onTemporary, onRestore])

  return null
}
