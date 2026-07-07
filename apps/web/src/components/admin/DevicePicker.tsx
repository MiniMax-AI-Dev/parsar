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
  /** When set, an inline "Add new device" entry is shown that opens this callback. */
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
      <div className="h-9 animate-pulse rounded-md border border-line bg-surface-subtle" />
    )
  }
  if (q.error) {
    return (
      <p className="text-sm text-danger">
        {(q.error as Error).message}
      </p>
    )
  }

  if (selectableDevices.length === 0) {
    const hasOnlineDevices = onlineDevices.length > 0
    return (
      <div className="rounded-lg border border-dashed border-line-strong bg-surface-subtle p-3">
        <p className="text-sm font-medium text-fg">
          {t(
            hasOnlineDevices
              ? "agents.form.devicePicker.noCompatibleTitle"
              : "agents.form.devicePicker.emptyTitle",
            {
              defaultValue: hasOnlineDevices
                ? "No online devices compatible with the current Agent engine"
                : "No agent daemons connected yet",
            },
          )}
        </p>
        {onAddDevice ? (
          <button
            type="button"
            onClick={onAddDevice}
            disabled={disabled}
            data-testid="device-picker-add-empty"
            className="mt-2 inline-flex items-center gap-1 rounded-md border border-line-strong bg-surface-emphasis px-3 py-1.5 text-sm font-medium text-white hover:bg-surface-emphasis disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Plus className="h-3 w-3" />
            {t("agents.form.devicePicker.addDevice", {
              defaultValue: "Pair a new device",
            })}
          </button>
        ) : (
          <p className="mt-1 text-sm text-fg-subtle">
            {t(
              hasOnlineDevices
                ? "agents.form.devicePicker.noCompatibleDescription"
                : "agents.form.devicePicker.emptyDescription",
              {
                defaultValue: hasOnlineDevices
                  ? "Open Runtime → Local devices to confirm the device has reported a heartbeat for this Agent engine, or switch to a device that supports it."
                  : "Open Runtime → Local devices to generate a pairing token, then run `parsar-daemon connect --url ... --token ...` on the target machine before returning here.",
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
        className="h-9 min-w-0 flex-1 rounded-md border border-line bg-surface px-3 text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300 disabled:cursor-not-allowed disabled:bg-surface-subtle"
      >
        <option value="">
          {t("agents.form.devicePicker.placeholder", {
            defaultValue: "Pick a device…",
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
          title={t("agents.form.devicePicker.addDevice", { defaultValue: "Pair a new device" })}
          className="inline-flex h-9 shrink-0 items-center gap-1 rounded-md border border-line bg-surface px-3 text-sm text-fg-muted shadow-sm hover:bg-surface-subtle disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Plus className="h-3 w-3" />
          {t("agents.form.devicePicker.addDevice", { defaultValue: "Pair a new device" })}
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
