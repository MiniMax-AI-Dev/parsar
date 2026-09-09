import { Select, SelectOption } from "../ui/select"
import type { Secret } from "../../lib/api-types"

interface CredentialBindingSelectProps {
  label: string
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
  label,
  value,
  secrets,
  allowPersonal,
  allowCreateNew = false,
  personalLabel,
  sharedLabel,
  personalPlaceholder,
  createNewLabel,
  onChange,
  className,
}: CredentialBindingSelectProps) {
  return (
    <Select
      aria-label={label}
      value={value}
      onValueChange={(nextValue) => onChange(nextValue)}
      onClick={(event) => event.stopPropagation()}
      className={className}
    >
      {allowPersonal && <SelectOption value="">{personalLabel}</SelectOption>}
      {!allowPersonal && !value && <SelectOption value="">{personalPlaceholder ?? personalLabel}</SelectOption>}
      {secrets.map((secret) => (
        <SelectOption key={secret.id} value={secret.id}>
          {sharedLabel}: {secret.name}
        </SelectOption>
      ))}
      {allowCreateNew && <SelectOption value="__new__">{createNewLabel ?? sharedLabel}</SelectOption>}
    </Select>
  )
}
