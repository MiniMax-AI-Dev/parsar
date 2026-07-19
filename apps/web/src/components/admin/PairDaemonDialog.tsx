import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Copy } from "lucide-react"

import { Button } from "../ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog"
import { useCreateRuntimePairing, useWorkspaceRuntimes } from "../../lib/api-runtimes"
import { useBootstrapStatus } from "../../lib/api-bootstrap"

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

export function PairDaemonDialog({
  open,
  onClose,
  workspaceID,
  onPaired,
}: PairDaemonDialogProps) {
  const { t } = useTranslation("admin")
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
  const [result, setResult] = useState<
    { token: string; runtimeName: string; runtimeID: string } | null
  >(null)
  const [paired, setPaired] = useState(false)

  const allRuntimes = listQ.data ?? []
  const connected = result
    ? allRuntimes.some((r) => r.id === result.runtimeID && r.liveness !== "pending_pairing")
    : false

  // Fire onPaired once when the daemon flips online; guard against
  // re-firing on every 5s list refetch.
  useEffect(() => {
    if (!connected || !result || paired) return
    setPaired(true)
    onPaired?.(result.runtimeID)
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
    setResult({ token: res.pairing_token, runtimeName: res.runtime.name, runtimeID: res.runtime.id })
  }

  function copyToClipboard(s: string) {
    void navigator.clipboard.writeText(s)
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) close() }}>
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
          <div className="space-y-3">
            <label className="block text-sm">
              <span className="mb-1 block text-fg-muted">
                {t("runtime.agentDaemon.pair.nameLabel", { defaultValue: "Device name" })}
              </span>
              <input
                className="w-full rounded border border-line-strong px-2 py-1 font-mono text-sm"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="my-laptop"
                data-testid="agent-daemon-pair-name"
              />
            </label>
            <div className="rounded-md border border-success-border bg-success-subtle px-3 py-2 text-sm text-success-emphasis">
              <p className="mb-1 font-medium">
                {t("runtime.agentDaemon.pair.safetyTitle", { defaultValue: "How the connection works" })}
              </p>
              <ul className="space-y-1 pl-4">
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
            </div>
            {create.error && (
              <p className="text-sm text-danger">
                {(create.error as Error).message}
              </p>
            )}
            <DialogFooter>
              <Button variant="outline" size="sm" onClick={close}>
                {t("common.actions.cancel", { defaultValue: "Cancel" })}
              </Button>
              <Button
                size="sm"
                disabled={!name.trim() || create.isPending}
                onClick={() => void submit()}
                data-testid="agent-daemon-pair-submit"
              >
                {create.isPending
                  ? t("runtime.agentDaemon.pair.minting", {
                      defaultValue: "Generating…",
                    })
                  : t("runtime.agentDaemon.pair.mint", {
                      defaultValue: "Generate connection command",
                    })}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <div className="space-y-3">
            <p className="text-sm text-success">
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
              onCopy={copyToClipboard}
              testId="agent-daemon-pair-copy-oneline"
            />
            <div className="flex items-center gap-2 rounded-md border border-line bg-surface-subtle px-3 py-2 text-sm">
              {connected ? (
                <>
                  <span className="h-2 w-2 shrink-0 rounded-full bg-success" />
                  <span className="text-success">
                    {t("runtime.agentDaemon.pair.connected", { defaultValue: "Device connected" })}
                  </span>
                </>
              ) : (
                <>
                  <span className="h-2 w-2 shrink-0 animate-pulse rounded-full bg-warning" />
                  <span className="text-fg-muted">
                    {t("runtime.agentDaemon.pair.waitingConnection", { defaultValue: "Waiting for the device to connect…" })}
                  </span>
                </>
              )}
            </div>
            <DialogFooter>
              <Button size="sm" onClick={close} data-testid="agent-daemon-pair-done">
                {t("common.actions.done", { defaultValue: "Done" })}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

function DaemonCommandBlock({
  command,
  description,
  label,
  onCopy,
  testId,
}: {
  command: string
  description: string
  label: string
  onCopy: (command: string) => void
  testId: string
}) {
  const { t } = useTranslation("admin")
  return (
    <div className="rounded-md border border-line bg-surface-subtle p-3 text-xs text-fg-muted">
      <div className="mb-2 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="font-medium text-fg-emphasis">{label}</p>
          <p className="mt-0.5 text-xs text-fg-subtle">{description}</p>
        </div>
        <button
          type="button"
          onClick={() => onCopy(command)}
          className="inline-flex shrink-0 items-center gap-1 rounded border border-line bg-surface px-2 py-1 text-xs text-fg-muted hover:bg-surface-muted"
          data-testid={testId}
          title={t("runtime.agentDaemon.pair.copyCommand", { defaultValue: "Copy command" })}
        >
          <Copy className="h-3 w-3" />
          {t("runtime.agentDaemon.pair.copy", { defaultValue: "Copy" })}
        </button>
      </div>
      <code className="block break-all rounded bg-surface p-2 font-mono text-xs leading-relaxed text-fg-emphasis">
        {command}
      </code>
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
