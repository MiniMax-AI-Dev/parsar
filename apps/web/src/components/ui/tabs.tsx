/* eslint-disable react-refresh/only-export-components */
import * as React from "react"
import * as TabsPrimitive from "@radix-ui/react-tabs"
import { cn } from "../../lib/utils"

/**
 * The one tab control: a segmented control. 2px inset, 24px segments,
 * 12px labels; the active segment is the pressed tint at 500 weight.
 */
export const Tabs = TabsPrimitive.Root

export const TabsList = React.forwardRef<
  React.ElementRef<typeof TabsPrimitive.List>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.List>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.List
    ref={ref}
    className={cn(
      "inline-flex h-7 items-center gap-0.5 rounded-md border border-line bg-surface p-0.5",
      className,
    )}
    {...props}
  />
))
TabsList.displayName = TabsPrimitive.List.displayName

export const TabsTrigger = React.forwardRef<
  React.ElementRef<typeof TabsPrimitive.Trigger>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Trigger>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.Trigger
    ref={ref}
    className={cn(
      "inline-flex h-6 items-center justify-center gap-1.5 whitespace-nowrap rounded px-2 text-xs text-fg-muted hover:app-hover hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 active:scale-[0.97] disabled:pointer-events-none disabled:opacity-50 data-[state=active]:app-pressed data-[state=active]:font-medium data-[state=active]:text-fg",
      className,
    )}
    {...props}
  />
))
TabsTrigger.displayName = TabsPrimitive.Trigger.displayName

export const TabsContent = React.forwardRef<
  React.ElementRef<typeof TabsPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof TabsPrimitive.Content>
>(({ className, ...props }, ref) => (
  <TabsPrimitive.Content
    ref={ref}
    className={cn("mt-4 focus-visible:outline-none", className)}
    {...props}
  />
))
TabsContent.displayName = TabsPrimitive.Content.displayName
