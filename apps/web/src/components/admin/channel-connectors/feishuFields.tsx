import { useEffect, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import QRCode from "qrcode"
import { ExternalLink, Loader2, QrCode, RefreshCw } from "lucide-react"

import { ApiError } from "../../../lib/api-client"
import {
  useBeginWorkspaceFeishuProvisioning,
  usePollWorkspaceFeishuProvisioning,
  type FeishuConnectorInput,
} from "../../../lib/api-connectors"
import { Button } from "../../ui/button"
import { PropertyList, Property } from "../../ui/property-list"
import { InlineError } from "../../runtime/InlineError"
import { FormSection, ProvisionStatusIcon } from "./shared"

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
  /** State chip rendered in the section head. */
  status?: ReactNode
}

export function FeishuConnectorFields({
  workspaceID,
  current,
  masterKeyConfigured,
  canEdit,
  onToast,
  status,
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
      status={status}
    />
  )
}

function FeishuConnectorFieldsInner({
  workspaceID,
  current,
  masterKeyConfigured,
  canEdit,
  onToast,
  status,
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
          // The QR must stay black-on-white for phone cameras regardless of theme.
          const qrDataUrl = await QRCode.toDataURL(begin.verification_uri_complete, {
            width: 224,
            margin: 2,
            color: { dark: "#000000", light: "#ffffff" },
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
    <FormSection title={t("connections.connector.feishu.title")} status={status}>
      {masterKeyMissing && (
        <InlineError>{t("connections.connector.feishu.masterKeyMissing")}</InlineError>
      )}

      <div className="flex h-8 items-center gap-2 border-b border-line text-sm text-fg">
        <QrCode className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate">
          {provisionConnected
            ? t("connections.connector.feishu.provision.connected")
            : t("connections.connector.feishu.provision.title")}
        </span>
        <Button
          size="sm"
          variant={provisionConnected ? "outline" : "default"}
          onClick={onBeginProvision}
          disabled={!canEdit || masterKeyMissing || busy || provision?.status === "pending"}
          data-testid="feishu-provision-begin-button"
        >
          {busy
            ? <Loader2 className="animate-spin" />
            : provisionConnected
              ? <RefreshCw strokeWidth={1.5} aria-hidden="true" />
              : <QrCode strokeWidth={1.5} aria-hidden="true" />}
          {provisionConnected
            ? t("connections.connector.feishu.provision.reconnect")
            : t("connections.connector.feishu.provision.start")}
        </Button>
      </div>

      {connected && (
        <PropertyList>
          <Property label="App ID" mono>{current.app_id}</Property>
          {current.bot_open_id && <Property label="Bot" mono>{current.bot_open_id}</Property>}
        </PropertyList>
      )}

      {provision && (
        <div className="flex gap-4">
          {provision.qrDataUrl && provision.status === "pending" && (
            <img
              src={provision.qrDataUrl}
              alt={t("connections.connector.feishu.provision.qrAlt")}
              width={160}
              height={160}
              className="h-40 w-40 shrink-0 rounded-md border border-line"
              data-testid="feishu-provision-qr"
            />
          )}
          <div className="flex min-w-0 flex-col gap-2">
            <ProvisionStatusIcon
              status={provision.status}
              loading={pollProvisionPending}
              labels={{
                waiting: t("connections.connector.feishu.provision.status.waiting"),
                connected: t("connections.connector.feishu.provision.status.connected"),
                stopped: t("connections.connector.feishu.provision.status.stopped"),
              }}
            />
            {/* The pairing code is the one thing read across a room: mono, 20px. */}
            <p className="font-mono text-xl tabular-nums text-fg">{provision.userCode}</p>
            <Button asChild variant="link" size="sm" className="self-start px-0">
              <a href={provision.verificationUrl} target="_blank" rel="noreferrer">
                {t("connections.connector.feishu.provision.openLink")}
                <ExternalLink strokeWidth={1.5} aria-hidden="true" />
              </a>
            </Button>
            {provision.message && (
              provision.status === "success"
                ? <p className="text-sm text-fg">{provision.message}</p>
                : <InlineError>{provision.message}</InlineError>
            )}
          </div>
        </div>
      )}

      {!canEdit && <p className="text-xs text-fg-muted">{t("connections.connector.adminOnly")}</p>}
      {errorMsg && <InlineError data-testid="feishu-error">{errorMsg}</InlineError>}
    </FormSection>
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
  ].join(" ")
}
