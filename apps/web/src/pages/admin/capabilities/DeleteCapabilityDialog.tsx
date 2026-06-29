import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"

import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../../components/ui/alert-dialog"
import { Button } from "../../../components/ui/button"
import { ApiError } from "../../../lib/api-client"
import type { Capability } from "../../../lib/api-types"

interface DeleteCapabilityDialogProps {
  capability: Capability | null
  pending: boolean
  error: unknown
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}

// 删除 = 释放 capability.name 占用,后端写 deleted_at。若仍被 agent 绑定,
// 后端返回 409 + envelope { code: "capability_in_use", message: "中文提示",
// binding_count: N }。envelope.message 已经是可直接展示的中文文案,这里直接渲染。
// 跟 DeprecateCapabilityDialog 的写法对齐,但走独立 dialog 是因为语义
// 不同:删除是一次性的,没有 toggle / 装机数。
export function DeleteCapabilityDialog({ capability, pending, error, onOpenChange, onConfirm }: DeleteCapabilityDialogProps) {
  const { t } = useTranslation("admin")
  const errMsg = error instanceof ApiError ? error.envelope.message : error instanceof Error ? error.message : null
  if (!capability) return null
  return (
    <AlertDialog open={!!capability} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("capabilities.delete.dialog.title", { name: capability.name })}</AlertDialogTitle>
          <AlertDialogDescription>{t("capabilities.delete.dialog.description")}</AlertDialogDescription>
        </AlertDialogHeader>
        {errMsg && <p className="rounded-md bg-red-50 px-3 py-2 text-[13px] text-red-700">{errMsg}</p>}
        <AlertDialogFooter>
          <Button variant="outline" size="sm" disabled={pending} onClick={() => onOpenChange(false)}>{t("capabilities.actions.cancel")}</Button>
          <Button variant="destructive" size="sm" disabled={pending} onClick={onConfirm}>{pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{t("capabilities.delete.dialog.confirm")}</Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
