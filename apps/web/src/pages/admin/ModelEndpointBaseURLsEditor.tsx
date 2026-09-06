import { RailSection } from "../../components/ui/detail-rail"
import { Field } from "../../components/ui/label"
import { Input } from "../../components/ui/input"
import type { EndpointBaseURLRow } from "../../lib/model-endpoint-base-urls"
import { endpointTypeDisplayLabel } from "../../lib/model-endpoint-base-urls"

interface ModelEndpointBaseURLsEditorProps {
  rows: EndpointBaseURLRow[]
  title: string
  onChange: (rows: EndpointBaseURLRow[]) => void
}

/** Per-protocol base URL fields under one 12px section head; one mono Input per endpoint type. */
export function ModelEndpointBaseURLsEditor({
  rows,
  title,
  onChange,
}: ModelEndpointBaseURLsEditorProps) {
  if (rows.length === 0) return null

  function updateRow(endpointType: string, baseURL: string) {
    onChange(rows.map((row) => (
      row.endpointType === endpointType ? { ...row, baseURL } : row
    )))
  }

  return (
    <RailSection title={title}>
      <div className="grid gap-3 pt-1">
        {rows.map((row) => (
          <Field
            key={row.endpointType}
            label={endpointTypeDisplayLabel(row.endpointType)}
            htmlFor={`endpoint-base-url-${row.endpointType}`}
          >
            <Input
              id={`endpoint-base-url-${row.endpointType}`}
              value={row.baseURL}
              onChange={(event) => updateRow(row.endpointType, event.target.value)}
              placeholder="https://api.example.com/v1"
              className="font-mono text-xs"
            />
          </Field>
        ))}
      </div>
    </RailSection>
  )
}
