/**
 * Provider-type picker: searchable dropdown over the model-provider catalog.
 * Select-only (provider_type must be a known key — unlike the model-key
 * combobox there is no free-text fallback).
 *
 * Same Radix dropdown + Input filter shape as ModelKeyCombobox /
 * CredentialKindCombobox.
 */
import { useMemo, useRef, useState } from "react"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { Check, ChevronDown } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Input } from "../../components/ui/input"
import { cn } from "../../lib/utils"
import { useWheelScroll } from "../../lib/use-wheel-scroll"
import { COMBOBOX_ITEM_CLASS, COMBOBOX_MENU_CLASS, COMBOBOX_TRIGGER_CLASS } from "./ModelKeyCombobox"

export interface ProviderTypeChoice {
  key: string
  /** Display label (already resolved — literal brand name or translated). */
  label: string
  adapter: string
  modelCount?: number
  /** Wire protocols this provider serves, so a dual-protocol provider reads
   * as "Anthropic + OpenAI" instead of a single adapter. */
  protocols?: string[]
}

/** Human label for a wire-protocol id. */
function protocolLabel(id: string): string {
  switch (id) {
    case "anthropic":
      return "Anthropic"
    case "openai":
      return "OpenAI"
    case "google":
      return "Google"
    default:
      return id
  }
}

interface Props {
  value: string
  onChange: (key: string) => void
  options: ProviderTypeChoice[]
  id?: string
}

export function ProviderTypeCombobox({ value, onChange, options, id }: Props) {
  const { t } = useTranslation("admin")
  const [search, setSearch] = useState("")
  const listRef = useRef<HTMLDivElement>(null)
  useWheelScroll(listRef)

  const selected = useMemo(() => options.find((o) => o.key === value), [options, value])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return options
    return options.filter(
      (o) =>
        o.label.toLowerCase().includes(q) ||
        o.key.toLowerCase().includes(q) ||
        o.adapter.toLowerCase().includes(q),
    )
  }, [options, search])

  function commit(next: string) {
    onChange(next)
    setSearch("")
  }

  return (
    <DropdownMenu.Root modal={false}>
      <DropdownMenu.Trigger asChild>
        <button id={id} type="button" className={COMBOBOX_TRIGGER_CLASS}>
          <span className={cn("truncate", !selected && "text-fg-muted")}>
            {selected?.label ?? value ?? t("models.createProvider.fields.providerType")}
          </span>
          <ChevronDown
            className="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted"
            strokeWidth={1.5}
            aria-hidden="true"
          />
        </button>
      </DropdownMenu.Trigger>

      {/* No Portal: when this combobox lives inside a Radix Dialog (modal),
          portaling to <body> lands outside the Dialog's pointer-events
          scope and the trigger click reaches a locked layer, so the menu
          never opens. Rendering in-place keeps the menu inside the
          DialogContent subtree. */}
      <DropdownMenu.Content align="start" sideOffset={4} className={COMBOBOX_MENU_CLASS}>
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={t("models.createProvider.fields.providerSearch", "Search providers…")}
          onKeyDown={(e) => {
            e.stopPropagation()
            if (e.key === "Enter" && filtered.length > 0) {
              e.preventDefault()
              commit(filtered[0].key)
            }
          }}
          autoFocus
        />
        <div className="my-1 border-t border-line" />

        {/* Wheel scroll driven by a non-passive listener (see useWheelScroll) —
            an inline React onWheel is passive in a Dialog and gets eaten by
            react-remove-scroll, so the wheel wouldn't reach the list at all. */}
        <div ref={listRef} className="max-h-[240px] overflow-auto">
          {filtered.length === 0 ? (
            <p className="px-2 py-1.5 text-sm text-fg-muted">
              {t("models.createProvider.fields.providerEmpty", "No matching providers")}
            </p>
          ) : (
            filtered.map((o) => (
              <DropdownMenu.Item
                key={o.key}
                onSelect={() => commit(o.key)}
                className={cn(COMBOBOX_ITEM_CLASS, "items-start")}
              >
                <div className="min-w-0 flex-1">
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="truncate font-medium">{o.label}</span>
                    {o.modelCount != null && o.modelCount > 0 && (
                      <span className="shrink-0 text-xs tabular-nums text-fg-muted">{o.modelCount.toLocaleString()} models</span>
                    )}
                  </div>
                  {o.protocols && o.protocols.length > 0 ? (
                    <div className="truncate text-xs text-fg-muted">
                      {o.protocols.map(protocolLabel).join(" + ")}
                    </div>
                  ) : (
                    <code className="block truncate font-mono text-xs text-fg-muted">{o.adapter}</code>
                  )}
                </div>
                {o.key === value && (
                  <Check className="mt-1 h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
                )}
              </DropdownMenu.Item>
            ))
          )}
        </div>
      </DropdownMenu.Content>
    </DropdownMenu.Root>
  )
}
