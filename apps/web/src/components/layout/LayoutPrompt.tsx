import { useEffect } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "../ui/button"
import { useToast } from "../ui/toast"

/** One live prompt at a time, whichever edge was dragged. */
const KEY = "layout-adjusted"

/**
 * Asks what to do with a panel width the reader just dragged: save it, keep it
 * for this session, or spring it back.
 *
 * It draws no strip of its own. It used to, at the same coordinate the toast
 * later took, which meant a confirmation could land on top of these buttons and
 * swallow the clicks. Going through the same queue makes that impossible —
 * messages stack instead of overlapping — and the prompt inherits the queue's
 * portal, so it is announced from outside any dialog that happens to be open.
 *
 * It persists: unlike a confirmation, it is a question, and a question that
 * times out has answered itself.
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
  const { show, dismiss } = useToast()

  useEffect(() => {
    if (!open) {
      dismiss(KEY)
      return
    }
    show(t("layout.adjusted"), {
      key: KEY,
      persist: true,
      action: (
        <>
          <Button size="sm" onClick={onSave}>{t("actions.save")}</Button>
          <Button size="sm" variant="outline" onClick={onTemporary}>{t("layout.temporary")}</Button>
          <Button size="sm" variant="ghost" onClick={onRestore}>{t("layout.restore")}</Button>
        </>
      ),
    })
  }, [open, show, dismiss, t, onSave, onTemporary, onRestore])

  return null
}
