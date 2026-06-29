/**
 * Picker for one credential_kinds row with an inline-create footer.
 * Invoked by EnvCredentialPicker when an env row switches to
 * mode=credential_ref. Selection is by `code`.
 */
import { useMemo, useState } from "react"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { Check, ChevronsUpDown, Loader2, Plus } from "lucide-react"
import { useTranslation } from "react-i18next"

import { ApiError } from "../../../lib/api-client"
import { Button } from "../../../components/ui/button"
import { Input } from "../../../components/ui/input"
import { cn } from "../../../lib/utils"

import { useCredentialKindsQuery } from "./api"
import { NewCredentialKindInlineDialog } from "./NewCredentialKindInlineDialog"
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

  const items = kindsQ.data?.items ?? []
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

  const errMsg = kindsQ.error instanceof ApiError
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
            size="sm"
            className={cn(
              "justify-between font-normal",
              !selected && "text-slate-500",
              className,
            )}
          >
            <span className="truncate text-[13px]">
              {selected
                ? selected.display_name
                : value
                  ? value
                  : t("capabilities.import.kindPicker.placeholder", "选择凭据类型")}
              {selected && (
                <code className="ml-2 rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] text-slate-500">
                  {selected.code}
                </code>
              )}
            </span>
            <ChevronsUpDown className="h-3.5 w-3.5 shrink-0 text-slate-400" />
          </Button>
        </DropdownMenu.Trigger>

        <DropdownMenu.Portal>
          <DropdownMenu.Content
            align="start"
            sideOffset={4}
            className="z-50 max-h-[320px] w-[var(--radix-dropdown-menu-trigger-width)] min-w-[280px] overflow-hidden rounded-md border border-slate-200 bg-white p-1 shadow-lg"
          >
            <div className="border-b border-slate-100 p-1">
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t("capabilities.import.kindPicker.search", "搜索…")}
                onKeyDown={(e) => e.stopPropagation() /* keep arrow keys in input */}
                className="h-8 text-[13px]"
                autoFocus
              />
            </div>

            <div className="max-h-[200px] overflow-auto py-1">
              {kindsQ.isLoading ? (
                <div className="flex items-center gap-2 px-3 py-2 text-[13px] text-slate-500">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  {t("capabilities.import.kindPicker.loading", "加载中…")}
                </div>
              ) : errMsg ? (
                <p className="px-3 py-2 text-[13px] text-red-700">{errMsg}</p>
              ) : filtered.length === 0 ? (
                <p className="px-3 py-2 text-[13px] text-slate-500">
                  {t("capabilities.import.kindPicker.empty", "没有匹配的凭据类型")}
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

            <DropdownMenu.Separator className="my-1 h-px bg-slate-100" />

            <DropdownMenu.Item
              onSelect={(e) => {
                e.preventDefault()
                setCreateOpen(true)
              }}
              className="flex cursor-pointer items-center gap-2 rounded px-3 py-2 text-[13px] font-medium text-slate-900 outline-none hover:bg-slate-50 focus:bg-slate-50"
            >
              <Plus className="h-3.5 w-3.5" />
              {t("capabilities.import.kindPicker.createNew", "新建凭据类型…")}
            </DropdownMenu.Item>
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
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
    <DropdownMenu.Item
      onSelect={(e) => {
        e.preventDefault()
        onSelect()
      }}
      className={cn(
        "flex cursor-pointer items-start justify-between gap-2 rounded px-3 py-2 outline-none",
        selected ? "bg-slate-100" : "hover:bg-slate-50 focus:bg-slate-50",
      )}
    >
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-[13px] font-medium text-slate-900">
            {kind.display_name || kind.code}
          </span>
          {kind.built_in && (
            <span className="rounded bg-slate-200 px-1.5 py-0.5 text-[11px] text-slate-700">
              built-in
            </span>
          )}
        </div>
        <div className="mt-0.5 flex items-center gap-2">
          <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] text-slate-500">
            {kind.code}
          </code>
        </div>
        {kind.description && (
          <p className="mt-1 line-clamp-2 text-[12px] text-slate-500">{kind.description}</p>
        )}
      </div>
      {selected && <Check className="h-3.5 w-3.5 shrink-0 text-slate-700" />}
    </DropdownMenu.Item>
  )
}
