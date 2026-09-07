/**
 * The floating menu's two measurements, in one place. Every dropdown in the
 * console — row actions, filters, comboboxes — is the same panel: 8px radius,
 * hairline, floating shadow, 4px padding, 13px items with a pressed tint when
 * highlighted. Pages import these rather than restating them, so a menu opened
 * in one corner of the product matches a menu opened in another.
 */
export const menuContentClass =
  "app-shadow-floating z-50 min-w-[200px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in data-[state=closed]:animate-pop-out"

export const menuItemClass =
  "flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm text-fg outline-none data-[highlighted]:app-pressed"
