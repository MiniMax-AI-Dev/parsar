import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { RuntimeCredentialCard } from "../../../components/runtime/RuntimeCredentialCard"
import { RuntimeStatusBanner } from "../../../components/runtime/RuntimeStatusBanner"
import { ErrorState } from "../../../components/ui/error-state"
import { SectionHead } from "../../../components/ui/section"
import { VerbatimBlock } from "../../../components/ui/verbatim"
import type { RuntimeStatus } from "../../../lib/api-runtime"

type CloudState = "loading" | "notConfigured" | "ready" | "error" | "unknown"

export function RuntimeCloudPanel({
  workspaceID,
  status,
  statusLoading,
  statusError,
  isAdmin,
  children,
}: {
  workspaceID: string | null
  status: RuntimeStatus | undefined
  statusLoading: boolean
  statusError: boolean
  isAdmin: boolean
  children: ReactNode
}) {
  const { t } = useTranslation("admin")
  const cloudState = resolveCloudState({ status, statusLoading, statusError })
  const showCredentialControl =
    cloudState !== "loading" && cloudState !== "unknown" && status?.profile !== "managed"
  const showInstances = !statusError && Boolean(workspaceID)

  return (
    <div className="pt-4">
      <section className="max-w-2xl border-b border-line pb-4">
        <SectionHead title={t("runtime.cloud.setupTitle")} />
        <RuntimeStatusBanner workspaceID={workspaceID} />
        {showCredentialControl && (
          <RuntimeCredentialCard workspaceID={workspaceID} isAdmin={isAdmin} className="mt-4" />
        )}
      </section>
      {showInstances ? children : null}
    </div>
  )
}

export function RuntimeInstancesError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const { t } = useTranslation("admin")
  return (
    <div role="alert">
      <ErrorState title={t("runtime.list.errors.loadFailed")} onRetry={onRetry} className="pb-2" />
      <details className="ml-6 max-w-2xl pb-4">
        <summary className="cursor-pointer text-xs text-fg-muted hover:text-fg focus-visible:outline-accent">
          {t("runtime.list.errors.details")}
        </summary>
        <VerbatimBlock className="mt-2 max-h-40 w-fit max-w-full break-all">
          {error instanceof Error ? error.message : String(error)}
        </VerbatimBlock>
      </details>
    </div>
  )
}

function resolveCloudState({
  status,
  statusLoading,
  statusError,
}: {
  status: RuntimeStatus | undefined
  statusLoading: boolean
  statusError: boolean
}): CloudState {
  if (statusLoading && !status) return "loading"
  if (statusError || !status) return "unknown"
  if (status.profile === "managed") return status.available ? "ready" : "error"
  if (!status.has_credential) return "notConfigured"
  return status.available ? "ready" : "error"
}
