import * as React from "react"
import { cn } from "../../lib/utils"

/** Field label: 12px muted, sits 4px above its control. */
export const Label = React.forwardRef<
  HTMLLabelElement,
  React.LabelHTMLAttributes<HTMLLabelElement>
>(({ className, ...props }, ref) => (
  <label ref={ref} className={cn("mb-1 block text-xs text-fg-muted", className)} {...props} />
))
Label.displayName = "Label"

/** Label + control + optional hint, 12px apart from its neighbours. */
export function Field({
  label,
  htmlFor,
  hint,
  children,
  className,
}: {
  label: React.ReactNode
  htmlFor?: string
  hint?: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn("flex flex-col", className)}>
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {hint && <p className="mt-1 text-xs text-fg-muted">{hint}</p>}
    </div>
  )
}
