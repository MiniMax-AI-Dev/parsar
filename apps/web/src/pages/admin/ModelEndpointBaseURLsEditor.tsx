import { Input } from "../../components/ui/input"
import type { EndpointBaseURLRow } from "../../lib/model-endpoint-base-urls"
import { endpointTypeDisplayLabel } from "../../lib/model-endpoint-base-urls"

interface ModelEndpointBaseURLsEditorProps {
  rows: EndpointBaseURLRow[]
  title: string
  description: string
  onChange: (rows: EndpointBaseURLRow[]) => void
}

export function ModelEndpointBaseURLsEditor({
  rows,
  title,
  description,
  onChange,
}: ModelEndpointBaseURLsEditorProps) {
  if (rows.length === 0) return null

  function updateRow(endpointType: string, baseURL: string) {
    onChange(rows.map((row) => (
      row.endpointType === endpointType ? { ...row, baseURL } : row
    )))
  }

  return (
    <section className="grid gap-2 rounded-md border border-line bg-surface-subtle/40 p-3">
      <div className="space-y-0.5">
        <div className="text-sm font-medium text-fg-muted">{title}</div>
        <div className="text-xs text-fg-faint">{description}</div>
      </div>
      <div className="grid gap-2">
        {rows.map((row) => (
          <div key={row.endpointType} className="grid gap-1.5">
            <label
              className="text-xs font-medium text-fg-muted"
              htmlFor={`endpoint-base-url-${row.endpointType}`}
            >
              {endpointTypeDisplayLabel(row.endpointType)}
            </label>
            <Input
              id={`endpoint-base-url-${row.endpointType}`}
              value={row.baseURL}
              onChange={(event) => updateRow(row.endpointType, event.target.value)}
              placeholder="https://api.example.com/v1"
              className="font-mono text-sm"
            />
          </div>
        ))}
      </div>
    </section>
  )
}
