import * as React from "react"
import { Slot } from "@radix-ui/react-slot"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "../../lib/utils"

/**
 * The one button. 28px tall, 6px radius, 13px/500 label, spring press.
 * `outline` is the default look of the ledger (paper + strong hairline +
 * control shadow); `default` is the indigo primary, reserved for the one
 * primary action of a screen, if any.
 */
const buttonVariants = cva(
  "inline-flex items-center justify-center gap-1.5 whitespace-nowrap font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 focus-visible:ring-offset-1 focus-visible:ring-offset-surface disabled:pointer-events-none disabled:opacity-50 active:scale-[0.97] motion-reduce:active:scale-100 [&>svg]:h-3.5 [&>svg]:w-3.5 [&>svg]:shrink-0",
  {
    variants: {
      variant: {
        default:
          "app-shadow-control bg-accent text-accent-fg hover:bg-accent-emphasis",
        destructive:
          "app-shadow-control bg-danger text-fg-on-emphasis hover:bg-danger-emphasis",
        outline:
          "app-shadow-control border border-line-strong bg-surface text-fg hover:app-hover disabled:border-line disabled:bg-transparent disabled:shadow-none",
        secondary: "bg-surface-muted text-fg hover:app-pressed",
        ghost: "text-fg-muted hover:app-hover hover:text-fg",
        link: "text-fg underline-offset-4 hover:underline",
      },
      size: {
        default: "h-7 px-2.5 text-sm",
        sm: "h-6 px-2 text-xs",
        lg: "h-8 px-3 text-sm",
        // Entry surfaces (login / setup / invite) use a 40px control:
        // the one place in the product with a hero-sized button.
        xl: "h-10 px-4 text-base",
        icon: "h-7 w-7",
      },
      /* Shape follows function: rounded (default) for controls, pill for
         chips-as-buttons, circle for icon buttons, square for accents. */
      shape: {
        rounded: "rounded-md",
        pill: "rounded-full px-3",
        circle: "rounded-full",
        square: "rounded-none",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
      shape: "rounded",
    },
  },
)

interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
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
  },
)
Button.displayName = "Button"
