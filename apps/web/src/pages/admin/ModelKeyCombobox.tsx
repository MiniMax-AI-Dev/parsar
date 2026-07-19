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
import { Check, ChevronsUpDown, CornerDownLeft } from "lucide-react"
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
        <button
          id={id}
          type="button"
          className={cn(
            "flex h-9 w-full items-center justify-between rounded-md border border-line bg-surface px-3 py-1.5 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-line-strong",
            !value && "text-fg-faint",
          )}
        >
          <span className="truncate font-mono text-sm">
            {value || placeholder || t("models.createModel.fields.modelKeyPlaceholder")}
          </span>
          <ChevronsUpDown className="h-3.5 w-3.5 shrink-0 text-fg-faint" />
        </button>
      </DropdownMenu.Trigger>

      {/* No Portal: when rendered inside a Radix Dialog (modal), portaling
          to <body> lands outside the Dialog's pointer-events scope and the
          menu never opens. Keep the content inside the DialogContent subtree. */}
      <DropdownMenu.Content
        align="start"
        sideOffset={4}
        className="z-50 max-h-[320px] w-[var(--radix-dropdown-menu-trigger-width)] min-w-[280px] overflow-hidden rounded-md border border-line bg-surface p-1 shadow-lg"
      >
        <div className="border-b border-line-muted p-1">
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
            className="h-8 font-mono text-sm"
            autoFocus
          />
        </div>

        {/* Wheel scroll driven by a non-passive listener (see useWheelScroll) —
            an inline React onWheel is passive in a Dialog and gets eaten by
            react-remove-scroll, so the wheel wouldn't reach the list at all. */}
        <div ref={listRef} className="max-h-[220px] overflow-auto py-1">
          {showFreeText && (
            <DropdownMenu.Item
              onSelect={() => commit(typed)}
              className="flex cursor-pointer items-center gap-2 rounded px-3 py-2 text-sm outline-none hover:bg-surface-subtle focus:bg-surface-subtle"
            >
              <CornerDownLeft className="h-3.5 w-3.5 shrink-0 text-fg-faint" />
              <span className="text-fg-subtle">{t("models.createModel.fields.modelKeyUse", "Use")}</span>
              <code className="truncate font-mono text-xs text-fg">{typed}</code>
            </DropdownMenu.Item>
          )}

          {filtered.length === 0 && !showFreeText ? (
            <p className="px-3 py-2 text-sm text-fg-subtle">
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
  return (
    <DropdownMenu.Item
      onSelect={() => onSelect()}
      className={cn(
        "flex cursor-pointer items-start justify-between gap-2 rounded px-3 py-2 outline-none",
        selected ? "bg-surface-muted" : "hover:bg-surface-subtle focus:bg-surface-subtle",
      )}
    >
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium text-fg">{model.name}</span>
          {model.reasoning && (
            <span className="rounded bg-surface-muted px-1.5 py-0.5 text-xs text-fg-muted">
              reasoning
            </span>
          )}
          {model.vision && (
            <span className="rounded bg-surface-muted px-1.5 py-0.5 text-xs text-fg-muted">
              vision
            </span>
          )}
        </div>
        <div className="mt-0.5 flex items-center gap-2">
          <code className="truncate rounded bg-surface-muted px-1.5 py-0.5 font-mono text-xs text-fg-subtle">
            {model.id}
          </code>
          {caption && <span className="shrink-0 text-xs text-fg-subtle">{caption}</span>}
        </div>
      </div>
      {selected && <Check className="h-3.5 w-3.5 shrink-0 text-fg-muted" />}
    </DropdownMenu.Item>
  )
}
