import { cn } from "../../lib/utils"
import { useTheme } from "../../lib/theme"

/**
 * The Parsar mark (the three-node ring from the brand lockup). The
 * artwork is raster, so there are two files: ink for paper, white for
 * the grey night. Decorative — every surface that shows it also names
 * the product in text.
 */
export function BrandMark({ className, size = 40 }: { className?: string; size?: number }) {
  const { resolvedTheme } = useTheme()
  return (
    <img
      src={resolvedTheme === "dark" ? "/parsar-mark-dark.png" : "/parsar-mark-light.png"}
      alt=""
      aria-hidden="true"
      width={size}
      height={size}
      className={cn("shrink-0 object-contain", className)}
      style={{ width: size, height: size }}
    />
  )
}
