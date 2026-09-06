import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Check, Copy, Loader2, X } from "lucide-react"

import { Button } from "../ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog"
import { Field } from "../ui/label"
import { Input } from "../ui/input"
import { StatusIcon } from "../ui/status-icon"
import { InlineError } from "../runtime/InlineError"
import { useCreateRuntimePairing, useWorkspaceRuntimes } from "../../lib/api-runtimes"
import { useBootstrapStatus } from "../../lib/api-bootstrap"
import { copyText } from "../../lib/clipboard"

interface PairDaemonDialogProps {
  open: boolean
  onClose: () => void
  workspaceID: string
  /**
   * Fires once when the freshly-minted runtime transitions out of
   * pending_pairing (i.e. daemon connected). Use this to auto-select
   * the device in a host form.
   */
  onPaired?: (runtimeID: string) => void
}

export function PairDaemonDialog({ open, onClose, workspaceID, onPaired }: PairDaemonDialogProps) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const create = useCreateRuntimePairing(workspaceID)
  // Prefer the server's configured public URL (PARSAR_PUBLIC_URL) over the
  // browser origin so the minted command is correct even when the admin
  // reaches the UI on a different host than daemons must dial back on.
  const statusQ = useBootstrapStatus()
  const serverPublicURL = statusQ.data?.public_url?.trim() ?? ""
  // Poll runtime list here (5s) so the dialog can react when the daemon
  // flips online, even when opened from a form with its own non-polling list.
  const listQ = useWorkspaceRuntimes(workspaceID, "agent_daemon")
  const [name, setName] = useState("")
  const [result, setResult] = useState<{
    token: string
    runtimeName: string
    runtimeID: string
  } | null>(null)
  const [paired, setPaired] = useState(false)

  const allRuntimes = listQ.data ?? []
  const connected = result
    ? allRuntimes.some((r) => r.id === result.runtimeID && r.liveness !== "pending_pairing")
    : false

  // Fire onPaired once when the daemon flips online; guard against
  // re-firing on every 5s list refetch.
  useEffect(() => {
    if (!connected || !result || paired) return
    const timer = window.setTimeout(() => {
      setPaired(true)
      onPaired?.(result.runtimeID)
    }, 0)
    return () => window.clearTimeout(timer)
  }, [connected, paired, result, onPaired])

  function reset() {
    setName("")
    setResult(null)
    setPaired(false)
    create.reset()
  }
  function close() {
    reset()
    onClose()
  }

  async function submit() {
    const trimmed = name.trim()
    if (!trimmed) return
    const res = await create.mutateAsync({ name: trimmed, type: "agent_daemon" })
    setResult({
      token: res.pairing_token,
      runtimeName: res.runtime.name,
      runtimeID: res.runtime.id,
    })
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) close()
      }}
    >
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {t("runtime.agentDaemon.pair.title", { defaultValue: "Pair a new device" })}
          </DialogTitle>
          <DialogDescription>
            {t("runtime.agentDaemon.pair.description", {
              defaultValue:
                "Generate a one-time token for this device, then run parsar-daemon connect on the target machine. The daemon will dial back to Parsar over WebSocket.",
            })}
          </DialogDescription>
        </DialogHeader>

        {!result ? (
          <>
            <form
              className="flex flex-col gap-3"
              onSubmit={(e) => {
                e.preventDefault()
                void submit()
              }}
            >
              <Field
                label={t("runtime.agentDaemon.pair.nameLabel", { defaultValue: "Device name" })}
                htmlFor="agent-daemon-pair-name"
              >
                <Input
                  id="agent-daemon-pair-name"
                  className="font-mono"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="my-laptop"
                  autoFocus
                  data-testid="agent-daemon-pair-name"
                />
              </Field>
              <ul className="m-0 flex list-disc flex-col gap-0.5 pl-4 text-xs text-fg-muted">
                <li>
                  {t("runtime.agentDaemon.pair.safetyOutbound", {
                    defaultValue: "This host opens an outbound connection — no inbound ports required.",
                  })}
                </li>
                <li>
                  {t("runtime.agentDaemon.pair.safetyClaude", {
                    defaultValue: "Agent CLI, files, and secrets stay on this machine.",
                  })}
                </li>
                <li>
                  {t("runtime.agentDaemon.pair.safetyOnce", {
                    defaultValue: "The token is shown once — it cannot be recovered after this dialog closes.",
                  })}
                </li>
              </ul>
              {create.error && <InlineError>{(create.error as Error).message}</InlineError>}
            </form>
            <DialogFooter>
              <Button variant="outline" onClick={close}>
                {tc("actions.cancel")}
              </Button>
              <Button
                disabled={!name.trim() || create.isPending}
                onClick={() => void submit()}
                data-testid="agent-daemon-pair-submit"
              >
                {create.isPending && <Loader2 className="animate-spin" />}
                {create.isPending
                  ? t("runtime.agentDaemon.pair.minting", { defaultValue: "Generating…" })
                  : t("runtime.agentDaemon.pair.mint", { defaultValue: "Generate connection command" })}
              </Button>
            </DialogFooter>
          </>
        ) : (
          <>
            <div className="flex flex-col gap-3">
              <p className="text-sm text-fg">
                {t("runtime.agentDaemon.pair.successOneLine", {
                  defaultValue:
                    "Run this one command on {{name}} to connect (it downloads and connects automatically — no binary to install manually):",
                  name: result.runtimeName,
                })}
              </p>
              <DaemonCommandBlock
                command={buildOneLineCommand(result.token, result.runtimeName, serverPublicURL)}
                label={t("runtime.agentDaemon.pair.oneLineLabel", {
                  defaultValue: "Copy and run on the target machine",
                })}
                description={t("runtime.agentDaemon.pair.oneLineHint", {
                  defaultValue:
                    "The target machine must have one of Claude Code / OpenCode / Codex installed. Once connected, this device flips to “Online”.",
                })}
                testId="agent-daemon-pair-copy-oneline"
              />
              <p className="flex h-8 items-center gap-2 border-t border-line text-sm text-fg" role="status">
                <StatusIcon status={connected ? "completed" : "running"} />
                {connected
                  ? t("runtime.agentDaemon.pair.connected", { defaultValue: "Device connected" })
                  : t("runtime.agentDaemon.pair.waitingConnection", {
                      defaultValue: "Waiting for the device to connect…",
                    })}
              </p>
            </div>
            <DialogFooter>
              <Button onClick={close} data-testid="agent-daemon-pair-done">
                {tc("actions.done")}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

function DaemonCommandBlock({
  command,
  description,
  label,
  testId,
}: {
  command: string
  description: string
  label: string
  testId: string
}) {
  const { t } = useTranslation("admin")
  const [copyStatus, setCopyStatus] = useState<"idle" | "copied" | "failed">("idle")

  async function copyCommand() {
    const copied = await copyText(command)
    setCopyStatus(copied ? "copied" : "failed")
    window.setTimeout(() => setCopyStatus("idle"), 2000)
  }

  return (
    <div>
      <div className="flex h-7 items-center justify-between gap-2">
        <span className="text-xs font-medium text-fg">{label}</span>
        <Button
          variant="outline"
          size="sm"
          onClick={() => void copyCommand()}
          data-testid={testId}
          title={t("runtime.agentDaemon.pair.copyCommand", { defaultValue: "Copy command" })}
        >
          {copyStatus === "copied" ? (
            <Check strokeWidth={1.5} aria-hidden="true" />
          ) : copyStatus === "failed" ? (
            <X strokeWidth={1.5} aria-hidden="true" />
          ) : (
            <Copy strokeWidth={1.5} aria-hidden="true" />
          )}
          {copyStatus === "copied"
            ? t("runtime.agentDaemon.pair.copied", { defaultValue: "Copied" })
            : copyStatus === "failed"
              ? t("runtime.agentDaemon.pair.copyFailed", { defaultValue: "Select the command below" })
              : t("runtime.agentDaemon.pair.copy", { defaultValue: "Copy" })}
        </Button>
      </div>
      <pre className="m-0 mt-1 whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
        {command}
      </pre>
      <p className="mt-1 text-xs text-fg-muted">{description}</p>
    </div>
  )
}

// Single command the operator pastes on the target machine: download via
// the server's install endpoint, then connect — all in one pipe. Pairing
// inputs ride as env vars (NOT a URL query string and NOT connect flags)
// so the one-shot token never lands in server/proxy access logs or `ps`
// output: `connect` hydrates these same vars and scrubs them from child
// argv (see apps/parsar-daemon/internal/cli/connect.go). The piped
// install script chmods the binary and execs `connect -b`, so the
// operator never sees the binary, its path, or the token.
function buildOneLineCommand(token: string, deviceName: string, publicURL?: string): string {
  const origin = serverOrigin(publicURL)
  return [
    `curl -fsSL ${origin}/api/v1/parsar-daemon/install.sh |`,
    `PARSAR_DAEMON_CONNECT_URL=${origin}`,
    `PARSAR_DAEMON_CONNECT_TOKEN=${token}`,
    `PARSAR_DAEMON_CONNECT_DEVICE_NAME=${shellEscape(deviceName)}`,
    `bash`,
  ].join(" ")
}

function serverOrigin(publicURL?: string): string {
  const configured = publicURL?.trim()
  if (configured) return configured.replace(/\/+$/, "")
  return typeof window !== "undefined" && window.location?.origin
    ? window.location.origin.replace(/\/+$/, "")
    : "https://<your-parsar-server>"
}

function shellEscape(s: string): string {
  if (/^[A-Za-z0-9._-]+$/.test(s)) return s
  return `'${s.replace(/'/g, "'\\''")}'`
}
