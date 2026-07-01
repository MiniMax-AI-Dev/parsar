import * as React from "react"
import { Slot } from "@radix-ui/react-slot"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "../../lib/utils"

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap text-sm font-medium transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-fg/15 focus-visible:ring-offset-1 focus-visible:ring-offset-surface disabled:pointer-events-none disabled:opacity-50 active:translate-y-px",
  {
    variants: {
      variant: {
        default: "bg-surface-emphasis text-white shadow-sm hover:bg-surface-inverse",
        destructive: "bg-danger text-white shadow-sm hover:bg-danger-emphasis",
        outline: "border border-line bg-surface text-fg shadow-sm hover:bg-surface-subtle hover:border-line-strong",
        secondary: "bg-surface-muted text-fg hover:bg-surface-muted/70",
        ghost: "text-fg-muted hover:bg-surface-muted hover:text-fg",
        link: "text-fg underline-offset-4 hover:underline",
      },
      size: {
        default: "h-9 px-3.5 py-2",
        sm: "h-8 px-3 text-xs",
        lg: "h-10 px-6",
        icon: "h-9 w-9",
      },
      /* Shape follows function: pill for hero CTAs, circle for icon buttons,
         square for geometric accents, rounded (default) for utility buttons.
         Radius lives here (not the base) so shape can fully override it. */
      shape: {
        rounded: "rounded-md",
        pill: "rounded-full px-5",
        circle: "rounded-full",
        square: "rounded-none",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
      shape: "rounded",
    },
  }
)

interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, shape, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button"
    return (
      <Comp
        className={cn(buttonVariants({ variant, size, shape }), className)}
        ref={ref}
        {...props}
      />
    )
  }
)
Button.displayName = "Button"

export { buttonVariants }
