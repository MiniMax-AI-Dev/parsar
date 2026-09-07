import { AlertTriangle, Copy } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "../../components/ui/button"
import { RailSection } from "../../components/ui/detail-rail"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import { StatusIcon } from "../../components/ui/status-icon"
import type {
  ModelConnectivityEndpointResult,
  ModelConnectivityResult,
} from "../../lib/api-models"

interface ModelTestDiagnosticsDialogProps {
  open: boolean
  result: { modelID: string; data: ModelConnectivityResult } | null
  onOpenChange: (open: boolean) => void
}

/**
 * Connectivity-test result: one headline (status icon + ink sentence), then
 * a section per endpoint with its request and response as mono `pre`
 * blocks on the muted paper tone. No coloured boxes; state lives in icons.
 */
export function ModelTestDiagnosticsDialog({
  open,
  result,
  onOpenChange,
}: ModelTestDiagnosticsDialogProps) {
  const { t } = useTranslation("admin")
  const data = result?.data
  const endpoints = data?.results?.length
    ? data.results
    : data
      ? [resultFromTopLevel(data)]
      : []
  const healthyCount = data?.healthy_count ?? endpoints.filter((item) => item.success).length
  const totalCount = data?.total_count ?? endpoints.length

  const headline = data
    ? data.success
      ? totalCount
        ? t("models.test.successWithEndpoints", { healthy: healthyCount, total: totalCount, ms: data.latency_ms })
        : t("models.test.success", { ms: data.latency_ms })
      : totalCount
        ? t("models.test.failureWithEndpoints", { healthy: healthyCount, total: totalCount })
        : data.supported
          ? t("models.test.failure")
          : t("models.test.unsupported")
    : null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100vh-2rem)] max-w-3xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("models.test.details.title")}</DialogTitle>
          {data && (
            <DialogDescription className="flex items-center gap-2 text-fg">
              <StatusIcon status={data.success ? "completed" : "failed"} />
              <span>{headline}</span>
              <span className="text-xs text-fg-muted">
                {data.endpoint_type
                  ? t("models.test.details.primaryEndpoint", { endpoint: data.endpoint_type, ms: data.latency_ms })
                  : t("models.test.details.noEndpoint")}
              </span>
            </DialogDescription>
          )}
        </DialogHeader>

        <div>
          {endpoints.map((endpoint, index) => (
            <EndpointDiagnostics key={`${endpoint.endpoint_type}-${index}`} endpoint={endpoint} />
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}

function EndpointDiagnostics({ endpoint }: { endpoint: ModelConnectivityEndpointResult }) {
  const { t } = useTranslation("admin")
  const requestJSON = prettyJSON({
    headers: endpoint.request?.headers,
    body: endpoint.request?.body,
  })
  const responseJSON = prettyJSON({
    headers: endpoint.response?.headers,
    body: endpoint.response?.body ?? endpoint.response?.raw_body,
    truncated: endpoint.response?.truncated || undefined,
  })
  const status = endpoint.success ? "completed" : endpoint.supported ? "failed" : "cancelled"
  const word = endpoint.success
    ? t("models.health.healthy")
    : endpoint.supported
      ? t("models.health.failed")
      : t("models.health.unsupported")
  const meta = [
    t("models.test.details.latency", { ms: endpoint.latency_ms }),
    endpoint.http_status ? t("models.test.details.httpStatus", { status: endpoint.http_status }) : null,
    endpoint.failure_stage,
  ]
    .filter(Boolean)
    .join(" · ")

  return (
    <RailSection
      title={
        <span className="inline-flex items-center gap-1.5">
          <StatusIcon status={status} />
          <code className="font-mono">{endpoint.endpoint_type || t("models.test.details.unknownEndpoint")}</code>
          <span className="font-normal text-fg-muted">{word}</span>
        </span>
      }
      meta={meta}
    >
      {endpoint.error && (
        <p className="mt-1 flex items-start gap-1.5 break-words text-sm text-fg">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
          <span>{endpoint.error}</span>
        </p>
      )}
      {endpoint.sample && <p className="mt-1 break-words text-sm text-fg">{endpoint.sample}</p>}

      <div className="mt-2 grid gap-3 md:grid-cols-2">
        <DiagnosticsBlock
          title={t("models.test.details.request")}
          meta={`${endpoint.request?.method ?? "POST"} ${endpoint.request?.url ?? ""}`}
          value={requestJSON}
        />
        <DiagnosticsBlock
          title={t("models.test.details.response")}
          meta={endpoint.response?.status ? String(endpoint.response.status) : t("models.test.details.noResponse")}
          value={responseJSON}
        />
      </div>
    </RailSection>
  )
}

function DiagnosticsBlock({
  title,
  meta,
  value,
}: {
  title: string
  meta: string
  value: string
}) {
  const { t } = useTranslation("admin")
  async function copyValue() {
    await navigator.clipboard?.writeText(value)
  }

  return (
    <div className="min-w-0">
      <div className="flex h-7 items-center gap-2 text-xs">
        <span className="shrink-0 text-fg-muted">{title}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-fg-muted" title={meta}>{meta}</span>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="h-6 w-6"
          aria-label={t("models.test.details.copy")}
          title={t("models.test.details.copy")}
          onClick={copyValue}
        >
          <Copy strokeWidth={1.5} />
        </Button>
      </div>
      <pre className="m-0 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
        {value}
      </pre>
    </div>
  )
}

function resultFromTopLevel(data: ModelConnectivityResult): ModelConnectivityEndpointResult {
  return {
    endpoint_type: data.endpoint_type ?? "",
    supported: data.supported,
    success: data.success,
    latency_ms: data.latency_ms,
    http_status: data.http_status,
    error: data.error,
    sample: data.sample,
  }
}

function prettyJSON(value: unknown): string {
  return JSON.stringify(value ?? {}, null, 2)
}
