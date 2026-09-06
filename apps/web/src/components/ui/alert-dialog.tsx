/* eslint-disable react-refresh/only-export-components */
import * as React from "react"
import * as AlertDialogPrimitive from "@radix-ui/react-alert-dialog"
import { cn } from "../../lib/utils"

/** Confirm dialog: same panel as Dialog, no close button, actions are the exits. */
export const AlertDialog = AlertDialogPrimitive.Root
export const AlertDialogTrigger = AlertDialogPrimitive.Trigger
export const AlertDialogPortal = AlertDialogPrimitive.Portal
export const AlertDialogCancel = AlertDialogPrimitive.Cancel
export const AlertDialogAction = AlertDialogPrimitive.Action

export const AlertDialogOverlay = React.forwardRef<
  React.ElementRef<typeof AlertDialogPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof AlertDialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <AlertDialogPrimitive.Overlay
    ref={ref}
    className={cn("fixed inset-0 z-50 bg-surface-inverse/30 data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out", className)}
    {...props}
  />
))
AlertDialogOverlay.displayName = AlertDialogPrimitive.Overlay.displayName

export const AlertDialogContent = React.forwardRef<
  React.ElementRef<typeof AlertDialogPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof AlertDialogPrimitive.Content>
>(({ className, ...props }, ref) => (
  <AlertDialogPortal>
    <AlertDialogOverlay />
    <AlertDialogPrimitive.Content
      ref={ref}
      className={cn(
        "app-shadow-floating fixed left-1/2 top-1/2 z-50 grid w-full max-w-md -translate-x-1/2 -translate-y-1/2 gap-4 rounded-lg border border-line bg-surface p-4 text-sm text-fg animate-pop-in data-[state=closed]:animate-pop-out focus:outline-none",
        className,
      )}
      {...props}
    />
  </AlertDialogPortal>
))
AlertDialogContent.displayName = AlertDialogPrimitive.Content.displayName

export function AlertDialogHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("-mx-4 -mt-4 flex min-h-12 flex-col justify-center gap-0.5 border-b border-line px-4 py-2.5", className)}
      {...props}
    />
  )
}

export function AlertDialogFooter({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("-mx-4 -mb-4 flex items-center justify-end gap-2 border-t border-line px-4 py-3", className)}
      {...props}
    />
  )
}

export const AlertDialogTitle = React.forwardRef<
  React.ElementRef<typeof AlertDialogPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof AlertDialogPrimitive.Title>
>(({ className, ...props }, ref) => (
  <AlertDialogPrimitive.Title
    ref={ref}
    className={cn("text-sm font-medium leading-tight text-fg", className)}
    {...props}
  />
))
AlertDialogTitle.displayName = AlertDialogPrimitive.Title.displayName

export const AlertDialogDescription = React.forwardRef<
  React.ElementRef<typeof AlertDialogPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof AlertDialogPrimitive.Description>
>(({ className, ...props }, ref) => (
  <AlertDialogPrimitive.Description
    ref={ref}
    className={cn("text-sm text-fg-muted", className)}
    {...props}
  />
))
AlertDialogDescription.displayName = AlertDialogPrimitive.Description.displayName
