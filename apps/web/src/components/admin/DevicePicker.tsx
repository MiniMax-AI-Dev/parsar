import { useEffect, useMemo } from "react"
import { useTranslation } from "react-i18next"
import { Plus } from "lucide-react"

import { Button } from "../ui/button"
import { Select } from "../ui/select"
import { Skeleton } from "../ui/skeleton"
import { InlineError } from "../runtime/InlineError"
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
    if (value || disabled || q.isLoading || q.isFetching || q.error) return
    if (selectableDevices.length !== 1) return
    onChange(selectableDevices[0].id)
  }, [disabled, onChange, q.error, q.isFetching, q.isLoading, selectableDevices, value])

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
    return <Skeleton className="h-7 w-full" />
  }
  if (q.error) {
    return <InlineError>{(q.error as Error).message}</InlineError>
  }

  const addLabel = t("agents.form.devicePicker.addDevice", { defaultValue: "Pair a new device" })

  if (selectableDevices.length === 0) {
    const hasOnlineDevices = onlineDevices.length > 0
    return (
      <div className="flex min-h-7 flex-wrap items-center gap-x-3 gap-y-1 text-sm">
        <span className="text-fg">
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
        </span>
        {onAddDevice ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={onAddDevice}
            disabled={disabled}
            data-testid="device-picker-add-empty"
          >
            <Plus strokeWidth={1.5} aria-hidden="true" />
            {addLabel}
          </Button>
        ) : (
          <span className="text-xs text-fg-muted">
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
          </span>
        )}
      </div>
    )
  }

  return (
    <div className="flex gap-2">
      <Select
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        data-testid="agent-daemon-device-picker"
        wrapperClassName="min-w-0 flex-1"
      >
        <option value="">
          {t("agents.form.devicePicker.placeholder", { defaultValue: "Pick a device…" })}
        </option>
        {selectableDevices.map((r) => (
          <option key={r.id} value={r.id}>
            {formatDeviceLabel(r)}
          </option>
        ))}
      </Select>
      {onAddDevice && (
        <Button
          type="button"
          variant="outline"
          onClick={onAddDevice}
          disabled={disabled}
          data-testid="device-picker-add"
          title={addLabel}
        >
          <Plus strokeWidth={1.5} aria-hidden="true" />
          {addLabel}
        </Button>
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
