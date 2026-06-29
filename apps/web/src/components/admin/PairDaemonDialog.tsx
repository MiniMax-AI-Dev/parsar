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
import {
  useCreateRuntimePairing,
  useWorkspaceRuntimes,
  type Runtime,
} from "../../lib/api-runtimes"

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
            {t("runtime.agentDaemon.pair.title", { defaultValue: "接入新设备" })}
          </DialogTitle>
          <DialogDescription>
            {t("runtime.agentDaemon.pair.description", {
              defaultValue:
                "为这台设备生成一次性 token,然后在目标机器上运行 parsar-daemon connect。daemon 会主动通过 WebSocket 连回 Parsar。",
            })}
          </DialogDescription>
        </DialogHeader>

        {!result ? (
          <div className="space-y-3">
            <label className="block text-[13px]">
              <span className="mb-1 block text-slate-700">
                {t("runtime.agentDaemon.pair.nameLabel", { defaultValue: "设备名称" })}
              </span>
              <input
                className="w-full rounded border border-slate-300 px-2 py-1 font-mono text-[13px]"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="my-laptop"
                data-testid="agent-daemon-pair-name"
              />
            </label>
            <div className="rounded-md border border-emerald-100 bg-emerald-50 px-3 py-2 text-[13px] text-emerald-900">
              <p className="mb-1 font-medium">
                {t("runtime.agentDaemon.pair.safetyTitle", { defaultValue: "连接说明" })}
              </p>
              <ul className="space-y-1 pl-4">
                <li>
                  {t("runtime.agentDaemon.pair.safetyOutbound", {
                    defaultValue: "由本机主动出站连接,无需开放任何入站端口",
                  })}
                </li>
                <li>
                  {t("runtime.agentDaemon.pair.safetyClaude", {
                    defaultValue: "Agent CLI、文件和密钥都留在这台机器上",
                  })}
                </li>
                <li>
                  {t("runtime.agentDaemon.pair.safetyOnce", {
                    defaultValue: "Token 仅显示一次,关闭窗口后无法恢复",
                  })}
                </li>
              </ul>
            </div>
            {create.error && (
              <p className="text-[13px] text-red-600">
                {(create.error as Error).message}
              </p>
            )}
            <DialogFooter>
              <Button variant="outline" size="sm" onClick={close}>
                {t("common.actions.cancel", { defaultValue: "取消" })}
              </Button>
              <Button
                size="sm"
                disabled={!name.trim() || create.isPending}
                onClick={() => void submit()}
                data-testid="agent-daemon-pair-submit"
              >
                {create.isPending
                  ? t("runtime.agentDaemon.pair.minting", {
                      defaultValue: "生成中…",
                    })
                  : t("runtime.agentDaemon.pair.mint", {
                      defaultValue: "生成连接命令",
                    })}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <div className="space-y-3">
            <p className="text-[13px] text-emerald-700">
              {t("runtime.agentDaemon.pair.successOneLine", {
                defaultValue:
                  "在 {{name}} 上执行这一条命令即可连接（自动下载并连接,无需手动安装二进制):",
                name: result.runtimeName,
              })}
            </p>
            <DaemonCommandBlock
              command={buildOneLineCommand(result.token, result.runtimeName)}
              label={t("runtime.agentDaemon.pair.oneLineLabel", {
                defaultValue: "复制并在目标机器上运行",
              })}
              description={t("runtime.agentDaemon.pair.oneLineHint", {
                defaultValue:
                  "目标机器需已安装 Claude Code / OpenCode / Codex 之一;成功后这台设备会变为「在线」",
              })}
              onCopy={copyToClipboard}
              testId="agent-daemon-pair-copy-oneline"
            />
            <div className="flex items-center gap-2 rounded-md border border-slate-200 bg-slate-50 px-3 py-2 text-[13px]">
              {connected ? (
                <>
                  <span className="h-2 w-2 shrink-0 rounded-full bg-emerald-500" />
                  <span className="text-emerald-700">
                    {t("runtime.agentDaemon.pair.connected", { defaultValue: "设备已连接" })}
                  </span>
                </>
              ) : (
                <>
                  <span className="h-2 w-2 shrink-0 animate-pulse rounded-full bg-amber-400" />
                  <span className="text-slate-600">
                    {t("runtime.agentDaemon.pair.waitingConnection", { defaultValue: "等待设备连接…" })}
                  </span>
                </>
              )}
            </div>
            <DialogFooter>
              <Button size="sm" onClick={close} data-testid="agent-daemon-pair-done">
                {t("common.actions.done", { defaultValue: "完成" })}
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
    <div className="rounded-md border border-slate-200 bg-slate-50 p-3 text-[12px] text-slate-700">
      <div className="mb-2 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="font-medium text-slate-800">{label}</p>
          <p className="mt-0.5 text-[12px] text-slate-500">{description}</p>
        </div>
        <button
          type="button"
          onClick={() => onCopy(command)}
          className="inline-flex shrink-0 items-center gap-1 rounded border border-slate-200 bg-white px-2 py-1 text-[12px] text-slate-600 hover:bg-slate-100"
          data-testid={testId}
          title={t("runtime.agentDaemon.pair.copyCommand", { defaultValue: "复制命令" })}
        >
          <Copy className="h-3 w-3" />
          {t("runtime.agentDaemon.pair.copy", { defaultValue: "复制" })}
        </button>
      </div>
      <code className="block break-all rounded bg-white p-2 font-mono text-[12px] leading-relaxed text-slate-800">
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
function buildOneLineCommand(token: string, deviceName: string): string {
  const origin = serverOrigin()
  return [
    `curl -fsSL ${origin}/api/v1/parsar-daemon/install.sh |`,
    `PARSAR_DAEMON_CONNECT_URL=${origin}`,
    `PARSAR_DAEMON_CONNECT_TOKEN=${token}`,
    `PARSAR_DAEMON_CONNECT_DEVICE_NAME=${shellEscape(deviceName)}`,
    `bash`,
  ].join(" ")
}

function serverOrigin(): string {
  return typeof window !== "undefined" && window.location?.origin
    ? window.location.origin.replace(/\/+$/, "")
    : "https://<your-parsar-server>"
}

function shellEscape(s: string): string {
  if (/^[A-Za-z0-9._-]+$/.test(s)) return s
  return `'${s.replace(/'/g, "'\\''")}'`
}
