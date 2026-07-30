import type { Secret } from "../../lib/api-types"

interface CredentialBindingSelectProps {
  value: string
  secrets: Secret[]
  allowPersonal: boolean
  allowCreateNew?: boolean
  personalLabel: string
  sharedLabel: string
  personalPlaceholder?: string
  createNewLabel?: string
  onChange: (value: string) => void
  className?: string
}

/** Shared source selector used by Agent creation and Capability enabling. */
export function CredentialBindingSelect({
  value,
  secrets,
  allowPersonal,
  allowCreateNew = false,
  personalLabel,
  sharedLabel,
  personalPlaceholder,
  createNewLabel,
  onChange,
  className = "h-7 w-full rounded border border-line bg-surface px-2 text-sm",
}: CredentialBindingSelectProps) {
  return (
    <select
      value={value}
      onChange={(event) => onChange(event.target.value)}
      onClick={(event) => event.stopPropagation()}
      className={className}
    >
      {allowPersonal && <option value="">{personalLabel}</option>}
      {!allowPersonal && !value && <option value="">{personalPlaceholder ?? personalLabel}</option>}
      {secrets.map((secret) => (
        <option key={secret.id} value={secret.id}>
          {sharedLabel}: {secret.name}
        </option>
      ))}
      {allowCreateNew && <option value="__new__">{createNewLabel ?? sharedLabel}</option>}
    </select>
  )
}
