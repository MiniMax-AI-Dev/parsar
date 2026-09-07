/**
 * Shared pieces for the channel-connector forms (Feishu / Slack / Discord /
 * Teams). Each platform module owns its secret-ref logic; the visual shell
 * (section head, fields, secret input, footer) lives here so every form
 * reads the same.
 */
import type { ReactNode } from "react"
import { ExternalLink } from "lucide-react"

import { Badge } from "../../ui/badge"
import { Button } from "../../ui/button"
import { Input } from "../../ui/input"
import { Field as UiField } from "../../ui/label"
import { StatusIcon } from "../../ui/status-icon"
import { cn } from "../../../lib/utils"

/** A form section: 13px/500 head with the state chip and the doc link, then the fields 12px apart. */
export function FormSection({
  title,
  status,
  docHref,
  docLabel,
  className,
  children,
}: {
  title: string
  status?: ReactNode
  docHref?: string
  docLabel?: string
  className?: string
  children: ReactNode
}) {
  return (
    <section className={cn("max-w-2xl", className)}>
      <div className="mb-3 flex h-7 items-center gap-2">
        <h2 className="text-sm font-medium text-fg">{title}</h2>
        {status}
        {docHref && docLabel && (
          <Button asChild variant="link" size="sm" className="ml-auto px-0 text-fg-muted hover:text-fg">
            <a href={docHref} target="_blank" rel="noreferrer noopener">
              {docLabel}
              <ExternalLink strokeWidth={1.5} aria-hidden="true" />
            </a>
          </Button>
        )}
      </div>
      <div className="flex flex-col gap-3">{children}</div>
    </section>
  )
}

export function Field({
  label,
  hint,
  required,
  badge,
  htmlFor,
  children,
}: {
  label: string
  hint?: string
  required?: boolean
  badge?: ReactNode
  htmlFor?: string
  children: ReactNode
}) {
  return (
    <UiField
      htmlFor={htmlFor}
      hint={hint}
      label={
        <span className="inline-flex items-center gap-1.5">
          <span>
            {label}
            {required && <span aria-hidden="true"> *</span>}
          </span>
          {badge}
        </span>
      }
    >
      {children}
    </UiField>
  )
}

/**
 * Password field for a secret; the "Saved" chip carries the stored-ref
 * state and the placeholder carries the expected format (e.g. "xoxb-…").
 */
export function SecretInput({
  label,
  savedBadge,
  placeholder,
  value,
  onChange,
  required,
  hasSavedValue,
  disabled,
  testId,
}: {
  label: string
  savedBadge?: string
  placeholder?: string
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
      required={required}
      htmlFor={testId}
      badge={
        hasSavedValue ? (
          <Badge variant="success" dot data-testid={`${testId}-saved-badge`}>
            {savedBadge ?? "Saved"}
          </Badge>
        ) : undefined
      }
    >
      <Input
        id={testId}
        type="password"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        autoComplete="new-password"
        placeholder={hasSavedValue ? "••••••••" : placeholder}
        className="font-mono"
        data-testid={testId}
      />
    </Field>
  )
}

/** The enabled switch: one 28px row with a checkbox and its label. */
export function EnabledField({
  label,
  checked,
  onChange,
  disabled,
  testId,
}: {
  label: string
  checked: boolean
  onChange: (next: boolean) => void
  disabled: boolean
  testId: string
}) {
  return (
    <label className="flex h-7 items-center gap-2 text-sm text-fg">
      <input
        type="checkbox"
        className="h-3.5 w-3.5 accent-accent"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        disabled={disabled}
        data-testid={testId}
      />
      {label}
    </label>
  )
}

/** Right-aligned footer with a top hairline; holds the form's one save button. */
export function FormFooter({ children }: { children: ReactNode }) {
  return <div className="mt-1 flex items-center justify-end gap-2 border-t border-line pt-3">{children}</div>
}

export function ProvisionStatusIcon({
  status,
  loading,
  labels,
}: {
  status: "pending" | "success" | "error" | "expired"
  loading: boolean
  labels: { waiting: string; connected: string; stopped: string }
}) {
  const kind = status === "success" ? "completed" : status === "pending" ? (loading ? "running" : "queued") : "failed"
  const text = status === "success" ? labels.connected : status === "pending" ? labels.waiting : labels.stopped
  return (
    <p className="flex items-center gap-1.5 text-sm text-fg" role="status">
      <StatusIcon status={kind} />
      <span>{text}</span>
    </p>
  )
}
