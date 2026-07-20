import { AlertCircle, CheckCircle2, Copy } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import type {
  ModelConnectivityEndpointResult,
  ModelConnectivityResult,
} from "../../lib/api-models"

interface ModelTestDiagnosticsDialogProps {
  open: boolean
  result: { modelID: string; data: ModelConnectivityResult } | null
  onOpenChange: (open: boolean) => void
}

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

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100vh-2rem)] max-w-4xl gap-0 overflow-hidden p-0">
        <DialogHeader className="border-b border-line px-5 py-4">
          <div className="flex flex-wrap items-center gap-2 pr-8">
            <DialogTitle className="text-sm">
              {t("models.test.details.title")}
            </DialogTitle>
            {data && (
              <Badge variant={data.success ? "success" : "destructive"} dot>
                {t("models.test.details.summary", {
                  healthy: healthyCount,
                  total: totalCount,
                })}
              </Badge>
            )}
          </div>
          <DialogDescription className="text-xs">
            {data?.endpoint_type
              ? t("models.test.details.primaryEndpoint", {
                  endpoint: data.endpoint_type,
                  ms: data.latency_ms,
                })
              : t("models.test.details.noEndpoint")}
          </DialogDescription>
        </DialogHeader>

        <div className="max-h-[calc(100vh-9rem)] space-y-3 overflow-y-auto px-5 py-4">
          {endpoints.map((endpoint, index) => (
            <EndpointDiagnostics
              key={`${endpoint.endpoint_type}-${index}`}
              endpoint={endpoint}
            />
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

  return (
    <section className="rounded-md border border-line bg-surface">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-line px-4 py-3">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-xs font-semibold text-fg">
              {endpoint.endpoint_type || t("models.test.details.unknownEndpoint")}
            </span>
            <Badge variant={endpoint.success ? "success" : endpoint.supported ? "destructive" : "neutral"} dot>
              {endpoint.success
                ? t("models.health.healthy")
                : endpoint.supported
                  ? t("models.health.failed")
                  : t("models.health.unsupported")}
            </Badge>
            {endpoint.failure_stage && (
              <Badge variant="warning">{endpoint.failure_stage}</Badge>
            )}
          </div>
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-fg-muted">
            <span>{t("models.test.details.latency", { ms: endpoint.latency_ms })}</span>
            {endpoint.http_status && (
              <span>{t("models.test.details.httpStatus", { status: endpoint.http_status })}</span>
            )}
          </div>
        </div>
        {endpoint.success ? (
          <CheckCircle2 className="h-4 w-4 text-success" />
        ) : (
          <AlertCircle className="h-4 w-4 text-danger-emphasis" />
        )}
      </div>

      {endpoint.error && (
        <div className="border-b border-line bg-danger-subtle/50 px-4 py-2 text-xs text-danger-emphasis">
          {endpoint.error}
        </div>
      )}
      {endpoint.sample && (
        <div className="border-b border-line bg-success-subtle/50 px-4 py-2 text-xs text-success-emphasis">
          {endpoint.sample}
        </div>
      )}

      <div className="grid gap-3 p-4 lg:grid-cols-2">
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
    </section>
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
    <div className="min-w-0 rounded-md border border-line-muted bg-surface-subtle">
      <div className="flex min-h-10 items-center justify-between gap-2 border-b border-line-muted px-3 py-2">
        <div className="min-w-0">
          <div className="text-xs font-medium text-fg">{title}</div>
          <div className="truncate font-mono text-xs text-fg-muted">{meta}</div>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="h-7 w-7 shrink-0"
          title={t("models.test.details.copy")}
          onClick={copyValue}
        >
          <Copy className="h-3.5 w-3.5" />
        </Button>
      </div>
      <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words px-3 py-2 font-mono text-xs leading-relaxed text-fg-muted">
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
