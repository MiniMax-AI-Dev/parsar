/**
 * Shared UI primitives + tiny helpers for all channel-connector panels
 * (Feishu / Slack / Discord). Each platform-specific fields module owns its
 * own SecretFieldSpec + config-equal logic, but the visual shell — Card,
 * Field, SecretInput, randomHex — lives here so all three panels look the
 * same and any tweak to spacing/colors applies uniformly.
 */
import type { ReactNode } from "react"
import { CheckCircle2, ExternalLink } from "lucide-react"

export function Card({
  title,
  description,
  docHref,
  docLabel,
  className,
  children,
}: {
  title: string
  description?: string
  docHref?: string
  docLabel?: string
  className?: string
  children: ReactNode
}) {
  return (
    <section className={`rounded-lg border border-line bg-surface px-5 py-4 ${className ?? ""}`}>
      <header className="mb-4">
        <h3 className="text-lg font-semibold text-fg">{title}</h3>
        {description && <p className="mt-1 text-sm text-fg-subtle">{description}</p>}
        {docHref && docLabel && (
          <a
            href={docHref}
            target="_blank"
            rel="noreferrer noopener"
            className="mt-2 inline-flex items-center gap-1 text-xs font-medium text-info-emphasis hover:underline"
          >
            <ExternalLink className="h-3 w-3" />
            {docLabel}
          </a>
        )}
      </header>
      {children}
    </section>
  )
}

export function Field({
  label,
  hint,
  required,
  badge,
  children,
}: {
  label: string
  hint?: string
  required?: boolean
  badge?: ReactNode
  children: ReactNode
}) {
  return (
    <div className="mb-3 last:mb-0">
      <label className="mb-1 flex items-center gap-2 text-xs font-medium text-fg-faint">
        <span>
          {label}
          {required && <span className="ml-1 text-danger">*</span>}
        </span>
        {badge}
      </label>
      {children}
      {hint && <p className="mt-0.5 text-xs leading-tight text-fg-subtle">{hint}</p>}
    </div>
  )
}

export function SecretInput({
  label,
  hint,
  savedHint,
  savedBadge,
  value,
  onChange,
  required,
  hasSavedValue,
  disabled,
  testId,
}: {
  label: string
  hint: string
  savedHint: string
  savedBadge?: string
  value: string
  onChange: (v: string) => void
  required: boolean
  hasSavedValue: boolean
  disabled: boolean
  testId: string
}) {
  return (
    <Field
      label={label}
      hint={hasSavedValue ? savedHint : hint}
      required={required}
      badge={
        hasSavedValue ? (
          <span
            className="inline-flex items-center gap-1 rounded-full bg-success-subtle px-1.5 py-0.5 text-xs font-medium normal-case tracking-normal text-success-emphasis"
            data-testid={`${testId}-saved-badge`}
          >
            <CheckCircle2 className="h-3 w-3" />
            {savedBadge ?? "Saved"}
          </span>
        ) : undefined
      }
    >
      <input
        type="password"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        autoComplete="new-password"
        placeholder={hasSavedValue ? "••••••••" : undefined}
        className="h-9 w-full rounded-md border border-line bg-surface px-3 font-mono text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-line-strong disabled:bg-surface-subtle"
        data-testid={testId}
      />
    </Field>
  )
}

export function randomHex(bytes: number): string {
  const values = new Uint8Array(bytes)
  crypto.getRandomValues(values)
  return Array.from(values, (value) => value.toString(16).padStart(2, "0")).join("")
}
