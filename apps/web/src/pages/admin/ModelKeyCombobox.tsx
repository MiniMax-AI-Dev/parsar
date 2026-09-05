/**
 * Model-key picker: searchable list of a provider's preset models, with
 * free-text fallback so any model id can still be entered (presets go stale;
 * a missing model must never block the user).
 *
 * Modeled on CredentialKindCombobox (Radix dropdown + Input filter), but the
 * search box doubles as the free-text value: whatever is typed becomes the
 * model key on Enter / "Use …", even if it matches no preset.
 */
import { useMemo, useRef, useState } from "react"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { Check, ChevronDown, CornerDownLeft } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Input } from "../../components/ui/input"
import { cn } from "../../lib/utils"
import { useWheelScroll } from "../../lib/use-wheel-scroll"
import { modelCaption, type ModelPreset } from "../../lib/model-presets"

interface Props {
  value: string
  onChange: (modelKey: string) => void
  /** Preset models for the selected provider; empty → pure free-text input. */
  models: ModelPreset[]
  placeholder?: string
  id?: string
}

/** Trigger styled like Select: 28px, paper, strong hairline, control shadow, muted chevron. */
export const COMBOBOX_TRIGGER_CLASS =
  "app-shadow-control relative flex h-7 w-full items-center rounded-md border border-line-strong bg-surface pl-2 pr-7 text-left text-sm text-fg transition-[border-color,box-shadow] duration-150 ease-settle focus-visible:border-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent data-[state=open]:app-pressed disabled:cursor-not-allowed disabled:bg-surface-muted disabled:opacity-60"

/** Floating menu: 8px radius, hairline, floating shadow, 4px padding, pop-in. */
export const COMBOBOX_MENU_CLASS =
  "app-shadow-floating z-50 w-[var(--radix-dropdown-menu-trigger-width)] min-w-[280px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in"

/** 13px item, 4px radius, pressed tint when highlighted. */
export const COMBOBOX_ITEM_CLASS =
  "flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm text-fg outline-none data-[highlighted]:app-pressed"

export function ModelKeyCombobox({ value, onChange, models, placeholder, id }: Props) {
  const { t } = useTranslation("admin")
  const [search, setSearch] = useState("")
  const listRef = useRef<HTMLDivElement>(null)
  useWheelScroll(listRef)

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return models
    return models.filter(
      (m) => m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q),
    )
  }, [models, search])

  // Typed text that isn't an exact preset id → offer "Use <text>" so custom
  // ids commit with one action.
  const typed = search.trim()
  const showFreeText = typed !== "" && !models.some((m) => m.id === typed)

  function commit(next: string) {
    onChange(next)
    setSearch("")
  }

  return (
    <DropdownMenu.Root modal={false}>
      <DropdownMenu.Trigger asChild>
        <button id={id} type="button" className={COMBOBOX_TRIGGER_CLASS}>
          <span className={cn("truncate font-mono text-xs", !value && "font-sans text-sm text-fg-muted")}>
            {value || placeholder || t("models.createModel.fields.modelKeyPlaceholder")}
          </span>
          <ChevronDown
            className="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted"
            strokeWidth={1.5}
            aria-hidden="true"
          />
        </button>
      </DropdownMenu.Trigger>

      {/* No Portal: when rendered inside a Radix Dialog (modal), portaling
          to <body> lands outside the Dialog's pointer-events scope and the
          menu never opens. Keep the content inside the DialogContent subtree. */}
      <DropdownMenu.Content align="start" sideOffset={4} className={COMBOBOX_MENU_CLASS}>
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={t("models.createModel.fields.modelKeySearch", "Search or type a model id…")}
          onKeyDown={(e) => {
            e.stopPropagation()
            if (e.key === "Enter" && typed !== "") {
              e.preventDefault()
              commit(typed)
            }
          }}
          className="font-mono text-xs"
          autoFocus
        />
        <div className="my-1 border-t border-line" />

        {/* Wheel scroll driven by a non-passive listener (see useWheelScroll) —
            an inline React onWheel is passive in a Dialog and gets eaten by
            react-remove-scroll, so the wheel wouldn't reach the list at all. */}
        <div ref={listRef} className="max-h-[220px] overflow-auto">
          {showFreeText && (
            <DropdownMenu.Item onSelect={() => commit(typed)} className={COMBOBOX_ITEM_CLASS}>
              <CornerDownLeft className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
              <span className="text-fg-muted">{t("models.createModel.fields.modelKeyUse", "Use")}</span>
              <code className="truncate font-mono text-xs text-fg">{typed}</code>
            </DropdownMenu.Item>
          )}

          {filtered.length === 0 && !showFreeText ? (
            <p className="px-2 py-1.5 text-sm text-fg-muted">
              {t("models.createModel.fields.modelKeyEmpty", "No matching models — type any id")}
            </p>
          ) : (
            filtered.map((m) => (
              <ModelRow
                key={m.id}
                model={m}
                selected={m.id === value}
                onSelect={() => commit(m.id)}
              />
            ))
          )}
        </div>
      </DropdownMenu.Content>
    </DropdownMenu.Root>
  )
}

function ModelRow({
  model,
  selected,
  onSelect,
}: {
  model: ModelPreset
  selected: boolean
  onSelect: () => void
}) {
  const caption = modelCaption(model)
  const flags = [model.reasoning && "reasoning", model.vision && "vision"].filter(Boolean).join(" · ")
  return (
    <DropdownMenu.Item onSelect={() => onSelect()} className={cn(COMBOBOX_ITEM_CLASS, "items-start")}>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate font-medium">{model.name}</span>
          {flags && <span className="shrink-0 text-xs text-fg-muted">{flags}</span>}
        </div>
        <div className="flex items-center gap-2 text-xs text-fg-muted">
          <code className="truncate font-mono">{model.id}</code>
          {caption && <span className="shrink-0">{caption}</span>}
        </div>
      </div>
      {selected && <Check className="mt-1 h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />}
    </DropdownMenu.Item>
  )
}
