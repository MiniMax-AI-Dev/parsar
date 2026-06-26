// Input lives INSIDE DropdownMenu.Content, not as an asChild trigger:
// Radix treats the trigger as a button and steals focus/clicks for its
// open-toggle, so an input nested via asChild never receives keystrokes.
import { useEffect, useMemo, useRef, useState } from "react"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { Check, ChevronsUpDown, Loader2, Search, X } from "lucide-react"
import { useTranslation } from "react-i18next"

import { ApiError } from "../lib/api-client"
import { useUserSearchQuery } from "../lib/api-users"
import type { PlatformUser } from "../lib/api-types"
import { Button } from "./ui/button"
import { Input } from "./ui/input"
import { cn } from "../lib/utils"

interface Props {
  excludeWorkspace?: string
  selected: PlatformUser[]
  onChange: (next: PlatformUser[]) => void
  className?: string
  disabled?: boolean
}

export function UserSearchCombobox({
  excludeWorkspace,
  selected,
  onChange,
  className,
  disabled,
}: Props) {
  const { t } = useTranslation("admin")
  const [input, setInput] = useState("")
  const [debouncedQ, setDebouncedQ] = useState("")
  const debounceRef = useRef<number | null>(null)

  useEffect(() => {
    if (debounceRef.current) window.clearTimeout(debounceRef.current)
    debounceRef.current = window.setTimeout(() => {
      setDebouncedQ(input)
    }, 300)
    return () => {
      if (debounceRef.current) window.clearTimeout(debounceRef.current)
    }
  }, [input])

  const searchQ = useUserSearchQuery({
    q: debouncedQ,
    excludeWorkspace,
  })

  const selectedIds = useMemo(
    () => new Set(selected.map((u) => u.id)),
    [selected]
  )

  const toggle = (user: PlatformUser) => {
    if (selectedIds.has(user.id)) {
      onChange(selected.filter((u) => u.id !== user.id))
    } else {
      onChange([...selected, user])
    }
  }

  const remove = (id: string) => {
    onChange(selected.filter((u) => u.id !== id))
  }

  const errMsg =
    searchQ.error instanceof ApiError
      ? searchQ.error.envelope.message
      : searchQ.error instanceof Error
        ? searchQ.error.message
        : null

  const items = searchQ.data?.items ?? []
  const trimmed = debouncedQ.trim()
  const isLoading = trimmed.length > 0 && searchQ.isFetching
  const showEmpty = trimmed.length > 0 && !searchQ.isFetching && items.length === 0

  // Trigger label: "搜索成员…" placeholder when nothing picked, otherwise
  // a count like "已选 3 人".
  const triggerLabel =
    selected.length === 0
      ? t("members.add.search.placeholder")
      : t("members.add.search.selectedCount", {
          count: selected.length,
          defaultValue: "已选 {{count}} 人",
        })

  return (
    <div className={cn("space-y-2", className)}>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild disabled={disabled}>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className={cn(
              "w-full justify-between font-normal",
              selected.length === 0 && "text-slate-500"
            )}
          >
            <span className="flex min-w-0 items-center gap-2">
              <Search className="h-3.5 w-3.5 shrink-0 text-slate-400" />
              <span className="truncate text-[12px]">{triggerLabel}</span>
            </span>
            <ChevronsUpDown className="h-3.5 w-3.5 shrink-0 text-slate-400" />
          </Button>
        </DropdownMenu.Trigger>

        <DropdownMenu.Portal>
          <DropdownMenu.Content
            align="start"
            sideOffset={4}
            className="z-50 max-h-[360px] w-[var(--radix-dropdown-menu-trigger-width)] min-w-[320px] overflow-hidden rounded-md border border-slate-200 bg-white p-1 shadow-lg"
            // Stay open across multiple selections — picking several
            // teammates at once is the expected flow.
            onCloseAutoFocus={(e) => e.preventDefault()}
          >
            <div className="border-b border-slate-100 p-1">
              <Input
                value={input}
                onChange={(e) => setInput(e.target.value)}
                placeholder={t("members.add.search.placeholder")}
                // Radix DropdownMenu binds arrow keys / typeahead at
                // Content level; without stopPropagation those steal
                // the input's cursor movement.
                onKeyDown={(e) => e.stopPropagation()}
                className="h-8 text-[12px]"
                autoFocus
              />
            </div>

            <div className="max-h-[280px] overflow-auto py-1">
              {trimmed.length === 0 ? (
                <p className="px-3 py-2 text-[12px] text-slate-500">
                  {t("members.add.search.typeToSearch")}
                </p>
              ) : isLoading ? (
                <div className="flex items-center gap-2 px-3 py-2 text-[12px] text-slate-500">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  {t("members.add.search.loading")}
                </div>
              ) : errMsg ? (
                <p className="px-3 py-2 text-[12px] text-red-700">{errMsg}</p>
              ) : showEmpty ? (
                <p className="px-3 py-2 text-[12px] text-slate-500">
                  {t("members.add.search.empty")}
                </p>
              ) : (
                items.map((user) => (
                  <UserRow
                    key={user.id}
                    user={user}
                    selected={selectedIds.has(user.id)}
                    onSelect={() => toggle(user)}
                  />
                ))
              )}
            </div>
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>

      {selected.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {selected.map((u) => (
            <button
              key={u.id}
              type="button"
              onClick={() => remove(u.id)}
              disabled={disabled}
              className="group inline-flex items-center gap-1.5 rounded-full border border-slate-200 bg-slate-50 px-2 py-0.5 text-[11.5px] text-slate-700 hover:border-slate-300 hover:bg-slate-100 disabled:cursor-not-allowed disabled:opacity-50"
            >
              <UserAvatar user={u} size="xs" />
              <span className="max-w-[140px] truncate">
                {u.name || u.email}
              </span>
              <X className="h-3 w-3 text-slate-400 group-hover:text-slate-600" />
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function UserRow({
  user,
  selected,
  onSelect,
}: {
  user: PlatformUser
  selected: boolean
  onSelect: () => void
}) {
  return (
    <DropdownMenu.Item
      onSelect={(e) => {
        // Keep popover open across selections.
        e.preventDefault()
        onSelect()
      }}
      className={cn(
        "flex cursor-pointer items-center gap-2.5 rounded px-2 py-1.5 outline-none",
        selected ? "bg-slate-100" : "hover:bg-slate-50 focus:bg-slate-50"
      )}
    >
      <UserAvatar user={user} size="sm" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-[12.5px] font-medium text-slate-900">
          {user.name || user.email.split("@")[0]}
        </div>
        {user.name && (
          <div className="truncate text-[11px] text-slate-500">
            {user.email}
          </div>
        )}
      </div>
      {selected && <Check className="h-3.5 w-3.5 shrink-0 text-slate-700" />}
    </DropdownMenu.Item>
  )
}

function UserAvatar({
  user,
  size,
}: {
  user: PlatformUser
  size: "xs" | "sm"
}) {
  const dims = size === "xs" ? "h-4 w-4 text-[8px]" : "h-6 w-6 text-[10px]"
  if (user.avatar_url) {
    return (
      <img
        src={user.avatar_url}
        alt=""
        className={cn("rounded-full object-cover", dims)}
      />
    )
  }
  const seed = (user.name || user.email || "?").trim()
  const initial = seed.charAt(0).toUpperCase()
  return (
    <div
      className={cn(
        "flex shrink-0 items-center justify-center rounded-full bg-slate-200 font-medium text-slate-600",
        dims
      )}
    >
      {initial}
    </div>
  )
}
