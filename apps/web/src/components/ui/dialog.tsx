/* eslint-disable react-refresh/only-export-components */
import * as React from "react"
import * as DialogPrimitive from "@radix-ui/react-dialog"
import { X } from "lucide-react"
import { cn } from "../../lib/utils"

/**
 * The one dialog: a floating paper panel (8px radius, hairline, floating
 * shadow, pop-in), 16px padding, 13px/500 title, footer with a top hairline.
 */
export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogPortal = DialogPrimitive.Portal
export const DialogClose = DialogPrimitive.Close

export const DialogOverlay = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Overlay
    ref={ref}
    className={cn("fixed inset-0 z-50 bg-surface-inverse/30", className)}
    {...props}
  />
))
DialogOverlay.displayName = DialogPrimitive.Overlay.displayName

export interface DialogContentProps
  extends React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content> {
  /** Default true; set false for confirm dialogs where action buttons are the only exits. */
  showCloseButton?: boolean
}

export const DialogContent = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  DialogContentProps
>(({ className, children, showCloseButton = true, ...props }, ref) => (
  <DialogPortal>
    <DialogOverlay />
    <DialogPrimitive.Content
      ref={ref}
      className={cn(
        "app-shadow-floating fixed left-1/2 top-1/2 z-50 grid w-full max-w-md -translate-x-1/2 -translate-y-1/2 gap-4 rounded-lg border border-line bg-surface p-4 text-sm text-fg animate-pop-in focus:outline-none",
        className
      )}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogPrimitive.Close
          className="absolute right-2 top-2 inline-flex h-7 w-7 items-center justify-center rounded-md text-fg-muted hover:app-hover hover:text-fg focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
          aria-label="Close"
        >
          <X className="h-3.5 w-3.5" strokeWidth={1.5} />
        </DialogPrimitive.Close>
      )}
    </DialogPrimitive.Content>
  </DialogPortal>
))
DialogContent.displayName = DialogPrimitive.Content.displayName

export function DialogHeader({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("flex flex-col gap-1 pr-7", className)}
      {...props}
    />
  )
}
DialogHeader.displayName = "DialogHeader"

export function DialogFooter({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "-mx-4 -mb-4 flex items-center justify-end gap-2 border-t border-line px-4 py-3",
        className
      )}
      {...props}
    />
  )
}
DialogFooter.displayName = "DialogFooter"

export const DialogTitle = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Title
    ref={ref}
    className={cn("text-sm font-medium leading-tight text-fg", className)}
    {...props}
  />
))
DialogTitle.displayName = DialogPrimitive.Title.displayName

export const DialogDescription = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Description
    ref={ref}
    className={cn("text-sm text-fg-muted", className)}
    {...props}
  />
))
DialogDescription.displayName = DialogPrimitive.Description.displayName
