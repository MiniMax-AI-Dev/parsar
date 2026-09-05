/**
 * Picker for one credential_kinds row with an inline-create footer.
 * Invoked by EnvCredentialPicker when an env row switches to
 * mode=credential_ref. Selection is by `code`.
 */
import { useMemo, useRef, useState } from "react"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { Check, ChevronsUpDown, Loader2, Plus } from "lucide-react"
import { useTranslation } from "react-i18next"

import { ApiError } from "../../../lib/api-client"
import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import { Input } from "../../../components/ui/input"
import { cn } from "../../../lib/utils"
import { useWheelScroll } from "../../../lib/use-wheel-scroll"

import { useCredentialKindsQuery } from "./api"
import { NewCredentialKindInlineDialog } from "./NewCredentialKindInlineDialog"
import { InlineNotice } from "./notices"
import type { CredentialKindRead } from "./types"

interface Props {
  workspaceID: string | null
  /** Currently-selected kind code (canonical EnvValue.credential_kind_code). */
  value: string
  onChange: (code: string) => void
  /** Width of the trigger; combobox content matches. Defaults to "full". */
  className?: string
  /** Disable the trigger (e.g. when the mode is not credential_ref). */
  disabled?: boolean
}

const MENU_ITEM_CLASS = "flex cursor-pointer items-start gap-2 rounded px-2 py-1.5 text-sm text-fg outline-none data-[highlighted]:app-pressed"

export function CredentialKindCombobox({
  workspaceID,
  value,
  onChange,
  className,
  disabled,
}: Props) {
  const { t } = useTranslation("admin")
  const kindsQ = useCredentialKindsQuery(workspaceID)
  const [search, setSearch] = useState("")
  const [createOpen, setCreateOpen] = useState(false)
  const listRef = useRef<HTMLDivElement>(null)
  useWheelScroll(listRef)

  const items = useMemo(() => kindsQ.data?.items ?? [], [kindsQ.data?.items])
  const selected = useMemo(() => items.find((k) => k.code === value), [items, value])

  // Server already returns built-ins first (ORDER BY built_in DESC).
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return items
    return items.filter(
      (k) =>
        k.code.toLowerCase().includes(q) ||
        k.display_name.toLowerCase().includes(q) ||
        k.description.toLowerCase().includes(q),
    )
  }, [items, search])

  const errMsg =
    kindsQ.error instanceof ApiError
      ? kindsQ.error.envelope.message
      : kindsQ.error instanceof Error
        ? kindsQ.error.message
        : null

  return (
    <>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild disabled={disabled}>
          <Button
            variant="outline"
            className={cn("w-full justify-between font-normal", !selected && !value && "text-fg-muted", className)}
          >
            <span className="flex min-w-0 items-center gap-2">
              <span className="truncate">
                {selected
                  ? selected.display_name
                  : value
                    ? value
                    : t("capabilities.import.kindPicker.placeholder", "Select a credential kind")}
              </span>
              {selected && <code className="shrink-0 font-mono text-xs text-fg-muted">{selected.code}</code>}
            </span>
            <ChevronsUpDown className="text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
          </Button>
        </DropdownMenu.Trigger>

        {/* No Portal: when rendered inside a Radix Dialog (modal), portaling
            to <body> lands outside the Dialog's pointer-events scope and the
            menu never opens. Keep the content inside the DialogContent subtree. */}
        <DropdownMenu.Content
          align="start"
          sideOffset={4}
          data-credential-kind-menu
          className="app-shadow-floating z-50 max-h-[320px] w-[var(--radix-dropdown-menu-trigger-width)] min-w-[280px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in"
        >
          <div className="border-b border-line p-1">
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={t("capabilities.import.kindPicker.search", "Search…")}
              onKeyDown={(e) => e.stopPropagation() /* keep arrow keys in input */}
              autoFocus
            />
          </div>

          {/* Wheel scroll driven by a non-passive listener (see useWheelScroll) —
              an inline React onWheel is passive in a Dialog and gets eaten by
              react-remove-scroll, so the wheel wouldn't reach the list at all. */}
          <div ref={listRef} className="max-h-[200px] overflow-auto py-1">
            {kindsQ.isLoading ? (
              <div className="flex items-center gap-2 px-2 py-1.5 text-sm text-fg-muted">
                <Loader2 className="h-3.5 w-3.5 animate-spin" strokeWidth={1.5} aria-hidden="true" />
                {t("capabilities.import.kindPicker.loading", "Loading…")}
              </div>
            ) : errMsg ? (
              <InlineNotice tone="error" className="px-2 py-1.5">{errMsg}</InlineNotice>
            ) : filtered.length === 0 ? (
              <p className="px-2 py-1.5 text-sm text-fg-muted">
                {t("capabilities.import.kindPicker.empty", "No matching credential kinds")}
              </p>
            ) : (
              filtered.map((kind) => (
                <KindRow
                  key={kind.id}
                  kind={kind}
                  selected={kind.code === value}
                  onSelect={() => onChange(kind.code)}
                />
              ))
            )}
          </div>

          <DropdownMenu.Separator className="my-1 h-px bg-line" />

          <DropdownMenu.Item
            onSelect={(e) => {
              e.preventDefault()
              setCreateOpen(true)
            }}
            className={cn(MENU_ITEM_CLASS, "items-center font-medium")}
          >
            <Plus className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
            {t("capabilities.import.kindPicker.createNew", "Create credential kind…")}
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Root>

      <NewCredentialKindInlineDialog
        workspaceID={workspaceID}
        open={createOpen}
        onOpenChange={setCreateOpen}
        initialCode={search}
        onCreated={(kind) => {
          onChange(kind.code)
          setSearch("")
        }}
      />
    </>
  )
}

function KindRow({
  kind,
  selected,
  onSelect,
}: {
  kind: CredentialKindRead
  selected: boolean
  onSelect: () => void
}) {
  return (
    <DropdownMenu.Item onSelect={onSelect} className={cn(MENU_ITEM_CLASS, selected && "app-selected")}>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate font-medium text-fg">{kind.display_name || kind.code}</span>
          <code className="shrink-0 font-mono text-xs text-fg-muted">{kind.code}</code>
          {kind.built_in && <Badge variant="neutral">built-in</Badge>}
        </div>
        {kind.description && <p className="mt-0.5 line-clamp-2 text-xs text-fg-muted">{kind.description}</p>}
      </div>
      {selected && <Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />}
    </DropdownMenu.Item>
  )
}
