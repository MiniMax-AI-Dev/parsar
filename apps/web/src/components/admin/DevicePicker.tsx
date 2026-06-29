import { useEffect, useMemo } from "react"
import { useTranslation } from "react-i18next"
import { Plus } from "lucide-react"

import {
  isLocalDeviceRuntime,
  isRuntimeSelectableForDispatch,
  runtimeSupportsAgentKind,
  useWorkspaceRuntimes,
  type Runtime,
} from "../../lib/api-runtimes"

interface DevicePickerProps {
  workspaceID: string
  value: string
  onChange: (deviceID: string) => void
  /** Selected daemon agent_kind. Empty means do not filter by engine. */
  agentKind?: string
  /** Keep the currently-bound device visible even if it is not freshly selectable. */
  preserveSelected?: boolean
  disabled?: boolean
  /** When set, an inline "接入新设备" entry is shown that opens this callback. */
  onAddDevice?: () => void
}

export function DevicePicker({ workspaceID, value, onChange, agentKind, preserveSelected = false, disabled, onAddDevice }: DevicePickerProps) {
  const { t } = useTranslation("admin")
  // No polling: a ticking "Ns ago" / online→offline shuffle while the
  // user is mid-form is just noise. Edit mode can still surface the
  // already-bound device via preserveSelected.
  const q = useWorkspaceRuntimes(workspaceID, "agent_daemon", {
    placement: "local_device",
    liveness: "online",
    refetchInterval: false,
    refetchOnMount: "always",
    staleTime: 0,
  })

  const localDevices = useMemo(
    () => (q.data ?? []).filter(isLocalDeviceRuntime),
    [q.data],
  )
  const onlineDevices = useMemo(
    () => localDevices.filter(isRuntimeSelectableForDispatch),
    [localDevices],
  )
  const compatibleDevices = useMemo(
    () => onlineDevices.filter((r) => runtimeSupportsAgentKind(r, agentKind)),
    [agentKind, onlineDevices],
  )
  const selectableDevices = useMemo(() => {
    const selected = value ? localDevices.find((r) => r.id === value) : undefined
    if (preserveSelected && selected && !compatibleDevices.some((r) => r.id === selected.id)) {
      return [selected, ...compatibleDevices]
    }
    return compatibleDevices
  }, [compatibleDevices, localDevices, preserveSelected, value])

  useEffect(() => {
    // Skip while fetching, not just isLoading: a sibling PairDaemonDialog
    // invalidates this list as the new daemon comes online, and during
    // that refetch the freshly-set value would get wiped before the new
    // row appears.
    if (!value || q.isLoading || q.isFetching || q.error) return
    if (selectableDevices.some((r) => r.id === value)) return
    onChange("")
  }, [onChange, q.error, q.isFetching, q.isLoading, selectableDevices, value])

  if (q.isLoading) {
    return (
      <div className="h-9 animate-pulse rounded-md border border-slate-200 bg-slate-50" />
    )
  }
  if (q.error) {
    return (
      <p className="text-[13px] text-red-600">
        {(q.error as Error).message}
      </p>
    )
  }

  if (selectableDevices.length === 0) {
    const hasOnlineDevices = onlineDevices.length > 0
    return (
      <div className="rounded-lg border border-dashed border-slate-300 bg-slate-50 p-3">
        <p className="text-[13px] font-medium text-slate-900">
          {t(
            hasOnlineDevices
              ? "agents.form.devicePicker.noCompatibleTitle"
              : "agents.form.devicePicker.emptyTitle",
            {
              defaultValue: hasOnlineDevices
                ? "没有兼容当前 Agent 引擎的在线设备"
                : "尚未连接任何 agent daemon",
            },
          )}
        </p>
        {onAddDevice ? (
          <button
            type="button"
            onClick={onAddDevice}
            disabled={disabled}
            data-testid="device-picker-add-empty"
            className="mt-2 inline-flex items-center gap-1 rounded-md border border-slate-900 bg-slate-900 px-3 py-1.5 text-[13px] font-medium text-white hover:bg-slate-800 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Plus className="h-3 w-3" />
            {t("agents.form.devicePicker.addDevice", {
              defaultValue: "接入新设备",
            })}
          </button>
        ) : (
          <p className="mt-1 text-[13px] text-slate-500">
            {t(
              hasOnlineDevices
                ? "agents.form.devicePicker.noCompatibleDescription"
                : "agents.form.devicePicker.emptyDescription",
              {
                defaultValue: hasOnlineDevices
                  ? "请在 Runtime -> 本地设备 页面确认设备 heartbeat 已上报该 Agent 引擎，或切换到支持当前引擎的设备。"
                  : "请先在 Runtime -> 本地设备 页面生成配对 token，并在目标机器上运行 `parsar-daemon connect --url ... --token ...` 后再回来。",
              },
            )}
          </p>
        )}
      </div>
    )
  }

  return (
    <div className="flex gap-2">
      <select
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        data-testid="agent-daemon-device-picker"
        className="h-9 min-w-0 flex-1 rounded-md border border-slate-200 bg-white px-3 text-[13px] shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300 disabled:cursor-not-allowed disabled:bg-slate-50"
      >
        <option value="">
          {t("agents.form.devicePicker.placeholder", {
            defaultValue: "请选择 device...",
          })}
        </option>
        {selectableDevices.map((r) => (
          <option key={r.id} value={r.id}>
            {formatDeviceLabel(r)}
          </option>
        ))}
      </select>
      {onAddDevice && (
        <button
          type="button"
          onClick={onAddDevice}
          disabled={disabled}
          data-testid="device-picker-add"
          title={t("agents.form.devicePicker.addDevice", { defaultValue: "接入新设备" })}
          className="inline-flex h-9 shrink-0 items-center gap-1 rounded-md border border-slate-200 bg-white px-3 text-[13px] text-slate-700 shadow-sm hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Plus className="h-3 w-3" />
          {t("agents.form.devicePicker.addDevice", { defaultValue: "接入新设备" })}
        </button>
      )}
    </div>
  )
}

function formatDeviceLabel(r: Runtime): string {
  const parts = [r.name]
  if (r.hostname && r.hostname !== r.name) parts.push(r.hostname)
  // No "Ns ago" suffix — list does not poll, so relative timestamps would
  // be misleading; staleness lives on the admin Runtime page.
  return parts.join(" · ")
}
