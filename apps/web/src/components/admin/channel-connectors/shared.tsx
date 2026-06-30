/**
 * Shared UI primitives + tiny helpers for all channel-connector panels
 * (Feishu / Slack / Discord). Each platform-specific fields module owns its
 * own SecretFieldSpec + config-equal logic, but the visual shell — Card,
 * Field, SecretInput, randomHex — lives here so all three panels look the
 * same and any tweak to spacing/colors applies uniformly.
 */
import type { ReactNode } from "react"
import { CheckCircle2, ExternalLink, Loader2, QrCode, XCircle } from "lucide-react"

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
    <section className={`rounded-lg border border-line bg-surface p-4 ${className ?? ""}`}>
      <header className="mb-3">
        <h3 className="text-sm font-semibold uppercase tracking-wider text-fg-subtle">{title}</h3>
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
      <label className="mb-1 flex items-center gap-2 text-xs uppercase tracking-wider text-fg-faint">
        <span>
          {label}
          {required && <span className="ml-1 text-danger">*</span>}
        </span>
        {badge}
      </label>
      {children}
      {hint && <p className="mt-1 text-xs text-fg-subtle">{hint}</p>}
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
            className="inline-flex items-center gap-1 rounded-full bg-success-subtle px-1.5 py-0.5 text-[10px] font-medium normal-case tracking-normal text-success-emphasis"
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
        className="h-9 w-full rounded-md border border-line bg-surface px-3 font-mono text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-slate-300 disabled:bg-surface-subtle"
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

/** ProvisionStatusIcon is Feishu-specific (it has the QR device-flow). Slack
 *  and Discord don't have an equivalent inline provisioning UI yet, but the
 *  icon helper is generic enough that all three can reuse it once they grow
 *  one. Keeping it exported from shared so the surface is uniform. */
export function ProvisionStatusIcon({
  status,
  loading,
  labels,
}: {
  status: "pending" | "success" | "error" | "expired"
  loading: boolean
  labels: { waiting: string; connected: string; stopped: string }
}) {
  if (status === "success") {
    return (
      <p className="inline-flex items-center gap-1 text-success">
        <CheckCircle2 className="h-3.5 w-3.5" />
        <span>{labels.connected}</span>
      </p>
    )
  }
  if (status === "error" || status === "expired") {
    return (
      <p className="inline-flex items-center gap-1 text-danger">
        <XCircle className="h-3.5 w-3.5" />
        <span>{labels.stopped}</span>
      </p>
    )
  }
  return (
    <p className="inline-flex items-center gap-1 text-fg-subtle">
      {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <QrCode className="h-3.5 w-3.5" />}
      <span>{labels.waiting}</span>
    </p>
  )
}