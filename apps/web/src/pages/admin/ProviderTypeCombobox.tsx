/**
 * Provider-type picker: searchable dropdown over the model-provider catalog.
 * Select-only (provider_type must be a known key — unlike the model-key
 * combobox there is no free-text fallback).
 *
 * Same Radix dropdown + Input filter shape as ModelKeyCombobox /
 * CredentialKindCombobox.
 */
import { useMemo, useState } from "react"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { Check, ChevronsUpDown } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Input } from "../../components/ui/input"
import { cn } from "../../lib/utils"

export interface ProviderTypeChoice {
  key: string
  /** Display label (already resolved — literal brand name or translated). */
  label: string
  adapter: string
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
        <button
          id={id}
          type="button"
          className={cn(
            "flex h-9 w-full items-center justify-between rounded-md border border-line bg-surface px-3 py-1.5 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-300",
            !selected && "text-fg-faint",
          )}
        >
          <span className="truncate text-sm">
            {selected?.label ?? value ?? t("models.createProvider.fields.providerType")}
          </span>
          <ChevronsUpDown className="h-3.5 w-3.5 shrink-0 text-fg-faint" />
        </button>
      </DropdownMenu.Trigger>

      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="start"
          sideOffset={4}
          className="z-50 max-h-[320px] w-[var(--radix-dropdown-menu-trigger-width)] min-w-[280px] overflow-hidden rounded-md border border-line bg-surface p-1 shadow-lg"
        >
          <div className="border-b border-line-muted p-1">
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
              className="h-8 text-sm"
              autoFocus
            />
          </div>

          {/* Radix DropdownMenu swallows wheel events; re-drive scroll so the
              nested list responds to the wheel, not only the scrollbar thumb. */}
          <div
            className="max-h-[240px] overflow-auto py-1"
            onWheel={(e) => {
              e.currentTarget.scrollTop += e.deltaY
              e.stopPropagation()
            }}
          >
            {filtered.length === 0 ? (
              <p className="px-3 py-2 text-sm text-fg-subtle">
                {t("models.createProvider.fields.providerEmpty", "No matching providers")}
              </p>
            ) : (
              filtered.map((o) => (
                <DropdownMenu.Item
                  key={o.key}
                  onSelect={() => commit(o.key)}
                  className={cn(
                    "flex cursor-pointer items-center justify-between gap-2 rounded px-3 py-2 outline-none",
                    o.key === value
                      ? "bg-surface-muted"
                      : "hover:bg-surface-subtle focus:bg-surface-subtle",
                  )}
                >
                  <div className="min-w-0 flex-1">
                    <span className="truncate text-sm font-medium text-fg">{o.label}</span>
                    <code className="mt-0.5 block truncate font-mono text-xs text-fg-subtle">
                      {o.adapter}
                    </code>
                  </div>
                  {o.key === value && <Check className="h-3.5 w-3.5 shrink-0 text-fg-muted" />}
                </DropdownMenu.Item>
              ))
            )}
          </div>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}
