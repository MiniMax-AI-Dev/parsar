import { useTranslation } from "react-i18next"
import { ArrowUpRight, Loader2 } from "lucide-react"

import { Button } from "../../../components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../../components/ui/dialog"
import { ErrorState } from "../../../components/ui/error-state"
import { Property, PropertyList } from "../../../components/ui/property-list"
import { useSkillPreview, type SkillsCatalogItem } from "../../../lib/api-skills"
import { useWorkspaceId } from "../../../lib/workspace"
import { ImportPreview } from "./ImportPreview"
import { isImportSpecReady } from "./importValidation"
import { SkillFileTree } from "./SkillFileTree"

export function SkillPreviewDialog({ skill, canImport, installedCapabilityID, installing, onInstall, onViewCapability, onClose, onRestoreFocus }: {
  skill: SkillsCatalogItem
  canImport: boolean
  installedCapabilityID?: string
  installing: boolean
  onInstall: () => void
  onViewCapability: (capabilityID: string) => void
  onClose: () => void
  onRestoreFocus: () => void
}) {
  const { t } = useTranslation("admin")
  const { t: common } = useTranslation("common")
  const preview = useSkillPreview(useWorkspaceId(), skill, canImport)
  const spec = preview.data?.canonical_spec
  const content = spec?.skill
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent
        className="flex w-[calc(100%-2rem)] max-w-3xl max-h-[calc(100vh-2rem)] flex-col overflow-hidden"
        onCloseAutoFocus={(event) => { event.preventDefault(); onRestoreFocus() }}
      >
        <DialogHeader className="shrink-0">
          <DialogTitle className="[overflow-wrap:anywhere]">{preview.data?.suggested_name || skill.name || skill.slug}</DialogTitle>
          <DialogDescription>{t("capabilities.skillsDirectory.preview.description")}</DialogDescription>
        </DialogHeader>
        <div className="min-h-0 min-w-0 space-y-4 overflow-y-auto overflow-x-hidden">
          <PropertyList>
            <Property label={t("capabilities.marketplaceDetail.source.title")} className="h-auto min-h-7 whitespace-normal py-1">
              <a href={`https://github.com/${skill.source}`} target="_blank" rel="noreferrer" className="inline-flex min-w-0 items-center gap-1 text-accent hover:underline [overflow-wrap:anywhere]">
                {skill.source}<ArrowUpRight className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
              </a>
            </Property>
          </PropertyList>
          {!canImport ? (
            <p className="text-sm text-fg-muted">{t("capabilities.permission.adminOnly")}</p>
          ) : preview.isPending ? (
            <p role="status" className="flex items-center gap-2 text-sm text-fg-muted">
              <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
              {t("capabilities.skillsDirectory.preview.loading")}
            </p>
          ) : preview.error ? (
            <ErrorState
              appearance="panel"
              title={t("capabilities.skillsDirectory.preview.failed")}
              detail={preview.error.message}
              onRetry={() => { void preview.refetch() }}
            />
          ) : content ? (
            <div className="min-w-0 space-y-3">
              <p className="text-sm [overflow-wrap:anywhere]">{content.description || t("capabilities.skillsDirectory.preview.noDescription")}</p>
              {!!preview.data?.warnings.length && <ImportPreview status="ready" warnings={preview.data.warnings} />}
              <SkillFileTree skill={content} />
            </div>
          ) : null}
        </div>
        <DialogFooter className="shrink-0">
          <Button variant="outline" onClick={onClose}>{common("actions.close")}</Button>
          {installedCapabilityID ? (
            <Button onClick={() => { onClose(); onViewCapability(installedCapabilityID) }}>{t("capabilities.mcpDirectory.actions.viewCapability")}</Button>
          ) : canImport ? (
            <Button disabled={installing || preview.isFetching || !!preview.error || !spec || !isImportSpecReady("skill", spec, [])} onClick={() => { onInstall(); onClose() }}>
              {t(installing ? "capabilities.skillsDirectory.install.installing" : "capabilities.skillsDirectory.install.action")}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
