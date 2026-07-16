import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import QRCode from "qrcode"
import { ExternalLink, Loader2, QrCode, RefreshCw } from "lucide-react"

import { ApiError } from "../../../lib/api-client"
import {
  useBeginWorkspaceFeishuProvisioning,
  usePollWorkspaceFeishuProvisioning,
  type FeishuConnectorInput,
} from "../../../lib/api-connectors"
import { Card, ProvisionStatusIcon } from "./shared"

const EMPTY_CONFIG: FeishuConnectorInput = {
  enabled: false,
  app_id: "",
  app_secret_ref: "",
  verification_token_ref: "",
  encrypt_key_ref: "",
  bot_open_id: "",
  event_mode: "websocket",
}

type ProvisionState = {
  deviceCode: string
  userCode: string
  verificationUrl: string
  qrDataUrl: string
  expiresAt: number
  intervalSec: number
  status: "pending" | "success" | "error" | "expired"
  message?: string
}

export interface FeishuConnectorFieldsProps {
  workspaceID: string | null
  current: FeishuConnectorInput | undefined
  masterKeyConfigured?: boolean
  canEdit: boolean
  onToast: (msg: string) => void
}

export function FeishuConnectorFields({
  workspaceID,
  current,
  masterKeyConfigured,
  canEdit,
  onToast,
}: FeishuConnectorFieldsProps) {
  const currentConfig = current ?? EMPTY_CONFIG
  return (
    <FeishuConnectorFieldsInner
      key={configKey(currentConfig)}
      workspaceID={workspaceID}
      current={currentConfig}
      masterKeyConfigured={masterKeyConfigured}
      canEdit={canEdit}
      onToast={onToast}
    />
  )
}

function FeishuConnectorFieldsInner({
  workspaceID,
  current,
  masterKeyConfigured,
  canEdit,
  onToast,
}: FeishuConnectorFieldsProps & { current: FeishuConnectorInput }) {
  const { t } = useTranslation("admin")
  const beginProvisionMut = useBeginWorkspaceFeishuProvisioning(workspaceID)
  const pollProvisionMut = usePollWorkspaceFeishuProvisioning(workspaceID)
  const [provision, setProvision] = useState<ProvisionState | null>(null)
  const [errorMsg, setErrorMsg] = useState<string | null>(null)
  const pollProvision = pollProvisionMut.mutate
  const pollProvisionPending = pollProvisionMut.isPending

  useEffect(() => {
    if (!provision || provision.status !== "pending" || pollProvisionPending) return
    const remainingMs = Math.max(0, provision.expiresAt - Date.now())
    const timer = window.setTimeout(() => {
      if (Date.now() >= provision.expiresAt) {
        setProvision((prev) => prev && prev.status === "pending"
          ? {
              ...prev,
              status: "expired",
              message: t("connections.connector.feishu.provision.expired"),
            }
          : prev)
        return
      }
      pollProvision(
        {
          deviceCode: provision.deviceCode,
          intervalSec: provision.intervalSec,
        },
        {
          onSuccess: (res) => {
            if (res.status === "pending") {
              setProvision((prev) => prev
                ? { ...prev, intervalSec: res.next_interval_sec ?? prev.intervalSec }
                : prev)
              return
            }
            if (res.status === "success") {
              setProvision((prev) => prev
                ? {
                    ...prev,
                    status: "success",
                    message: res.bot_name
                      ? t("connections.connector.feishu.provision.successWithName", { name: res.bot_name })
                      : t("connections.connector.feishu.provision.success"),
                  }
                : prev)
              onToast(t("connections.connector.feishu.provision.saved"))
              return
            }
            const expired = res.error === "expired_token"
            setProvision((prev) => prev
              ? {
                  ...prev,
                  status: expired ? "expired" : "error",
                  message: expired
                    ? t("connections.connector.feishu.provision.expired")
                    : res.description ?? res.error ?? t("connections.connector.feishu.provision.failed"),
                }
              : prev)
          },
          onError: (err) => {
            setProvision((prev) => prev
              ? {
                  ...prev,
                  status: "error",
                  message: err instanceof ApiError
                    ? err.envelope.message
                    : t("connections.connector.feishu.provision.failed"),
                }
              : prev)
          },
        },
      )
    }, Math.min(Math.max(1, provision.intervalSec) * 1000, Math.max(1, remainingMs)))
    return () => window.clearTimeout(timer)
  }, [onToast, pollProvision, pollProvisionPending, provision, t])

  const onBeginProvision = () => {
    setErrorMsg(null)
    beginProvisionMut.mutate(undefined, {
      onSuccess: async (res) => {
        const begin = res.begin
        if (!begin?.device_code || !begin.verification_uri_complete) {
          setErrorMsg(t("connections.connector.feishu.provision.failed"))
          return
        }
        try {
          const qrDataUrl = await QRCode.toDataURL(begin.verification_uri_complete, {
            width: 224,
            margin: 2,
            color: { dark: "#020617", light: "#ffffff" },
          })
          setProvision({
            deviceCode: begin.device_code,
            userCode: begin.user_code,
            verificationUrl: begin.verification_uri_complete,
            qrDataUrl,
            expiresAt: Date.now() + Math.max(30, begin.expires_in) * 1000,
            intervalSec: begin.interval || 5,
            status: "pending",
          })
        } catch (err) {
          setErrorMsg(err instanceof Error
            ? err.message
            : t("connections.connector.feishu.provision.failed"))
        }
      },
      onError: (err) => {
        setErrorMsg(err instanceof ApiError
          ? err.envelope.message
          : t("connections.connector.feishu.provision.failed"))
      },
    })
  }

  const connected = current.enabled && current.app_id.trim() !== ""
  const provisionConnected = connected || provision?.status === "success"
  const masterKeyMissing = masterKeyConfigured === false
  const busy = beginProvisionMut.isPending || pollProvisionPending

  return (
    <Card
      title={t("connections.connector.feishu.title")}
      description={t("connections.connector.feishu.description")}
    >
      {masterKeyMissing && (
        <p className="mb-3 rounded-md border border-warning/40 bg-warning-subtle px-3 py-2 text-sm text-warning-emphasis">
          {t("connections.connector.feishu.masterKeyMissing")}
        </p>
      )}
      <div className="rounded-md border border-line bg-surface-subtle p-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <QrCode className="h-4 w-4 text-fg-muted" strokeWidth={1.75} />
            <div>
              <p className="text-sm font-medium text-fg">
                {provisionConnected
                  ? t("connections.connector.feishu.provision.connected")
                  : t("connections.connector.feishu.provision.title")}
              </p>
              <p className="text-sm text-fg-subtle">
                {provisionConnected
                  ? t("connections.connector.feishu.provision.connectedSubtitle")
                  : t("connections.connector.feishu.provision.subtitle")}
              </p>
            </div>
          </div>
          <button
            type="button"
            onClick={onBeginProvision}
            disabled={!canEdit || masterKeyMissing || busy || provision?.status === "pending"}
            className="inline-flex items-center gap-2 rounded-md bg-surface-emphasis px-3 py-1.5 text-sm font-medium text-white hover:bg-surface-emphasis disabled:opacity-60"
            data-testid="feishu-provision-begin-button"
          >
            {busy
              ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
              : provisionConnected
                ? <RefreshCw className="h-3.5 w-3.5" />
                : <QrCode className="h-3.5 w-3.5" />}
            {provisionConnected
              ? t("connections.connector.feishu.provision.reconnect")
              : t("connections.connector.feishu.provision.start")}
          </button>
        </div>

        {provision && (
          <div className="mt-3 grid gap-3 sm:grid-cols-[auto_1fr]">
            {provision.qrDataUrl && provision.status === "pending" && (
              <img
                src={provision.qrDataUrl}
                alt={t("connections.connector.feishu.provision.qrAlt")}
                className="h-40 w-40 rounded-md border border-line bg-surface p-2"
                data-testid="feishu-provision-qr"
              />
            )}
            <div className="min-w-0 space-y-2 text-sm text-fg-muted">
              <ProvisionStatusIcon
                status={provision.status}
                loading={pollProvisionPending}
                labels={{
                  waiting: t("connections.connector.feishu.provision.status.waiting"),
                  connected: t("connections.connector.feishu.provision.status.connected"),
                  stopped: t("connections.connector.feishu.provision.status.stopped"),
                }}
              />
              <p className="font-mono text-sm text-fg-emphasis">{provision.userCode}</p>
              <a
                href={provision.verificationUrl}
                target="_blank"
                rel="noreferrer"
                className="inline-flex max-w-full items-center gap-1 text-sm text-fg-muted underline underline-offset-2"
              >
                <span className="truncate">{t("connections.connector.feishu.provision.openLink")}</span>
                <ExternalLink className="h-3.5 w-3.5 shrink-0" />
              </a>
              {provision.status === "pending" && (
                <p className="inline-flex items-center gap-1 text-fg-subtle">
                  {t("connections.connector.feishu.provision.pending")}
                </p>
              )}
              {provision.message && (
                <p className={provision.status === "success" ? "text-success" : "text-danger"}>
                  {provision.message}
                </p>
              )}
            </div>
          </div>
        )}
      </div>

      {!canEdit && (
        <p className="mt-3 text-sm text-fg-faint">{t("connections.connector.adminOnly")}</p>
      )}
      {errorMsg && (
        <p className="mt-3 text-sm text-danger" role="alert" data-testid="feishu-error">
          {errorMsg}
        </p>
      )}
    </Card>
  )
}

function configKey(config: FeishuConnectorInput): string {
  return [
    config.enabled ? "1" : "0",
    config.app_id,
    config.app_secret_ref,
    config.verification_token_ref,
    config.encrypt_key_ref,
    config.bot_open_id,
    config.event_mode,
  ].join("\u0000")
}
