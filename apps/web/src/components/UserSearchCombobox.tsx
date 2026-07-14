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

  // Trigger label: "Search members…" placeholder when nothing picked, otherwise
  // a count like "3 selected".
  const triggerLabel =
    selected.length === 0
      ? t("members.add.search.placeholder")
      : t("members.add.search.selectedCount", {
          count: selected.length,
          defaultValue: "{{count}} selected",
        })

  return (
    <div className={cn("space-y-2", className)}>
      {/* Nested in a dialog; non-modal lets its footer receive the first click. */}
      <DropdownMenu.Root modal={false}>
        <DropdownMenu.Trigger asChild disabled={disabled}>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className={cn(
              "w-full justify-between font-normal",
              selected.length === 0 && "text-fg-subtle"
            )}
          >
            <span className="flex min-w-0 items-center gap-2">
              <Search className="h-3.5 w-3.5 shrink-0 text-fg-faint" />
              <span className="truncate text-sm">{triggerLabel}</span>
            </span>
            <ChevronsUpDown className="h-3.5 w-3.5 shrink-0 text-fg-faint" />
          </Button>
        </DropdownMenu.Trigger>

        <DropdownMenu.Portal>
          <DropdownMenu.Content
            align="start"
            sideOffset={4}
            className="z-50 max-h-[360px] w-[var(--radix-dropdown-menu-trigger-width)] min-w-[320px] overflow-hidden rounded-md border border-line bg-surface p-1 shadow-lg"
            // Stay open across multiple selections — picking several
            // teammates at once is the expected flow.
            onCloseAutoFocus={(e) => e.preventDefault()}
          >
            <div className="border-b border-line-muted p-1">
              <Input
                value={input}
                onChange={(e) => setInput(e.target.value)}
                placeholder={t("members.add.search.placeholder")}
                // Radix DropdownMenu binds arrow keys / typeahead at
                // Content level; without stopPropagation those steal
                // the input's cursor movement.
                onKeyDown={(e) => e.stopPropagation()}
                className="h-8 text-sm"
                autoFocus
              />
            </div>

            <div className="max-h-[280px] overflow-auto py-1">
              {trimmed.length === 0 ? (
                <p className="px-3 py-2 text-sm text-fg-subtle">
                  {t("members.add.search.typeToSearch")}
                </p>
              ) : isLoading ? (
                <div className="flex items-center gap-2 px-3 py-2 text-sm text-fg-subtle">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  {t("members.add.search.loading")}
                </div>
              ) : errMsg ? (
                <p className="px-3 py-2 text-sm text-danger-emphasis">{errMsg}</p>
              ) : showEmpty ? (
                <p className="px-3 py-2 text-sm text-fg-subtle">
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
              className="group inline-flex items-center gap-1.5 rounded-full border border-line bg-surface-subtle px-2 py-0.5 text-xs text-fg-muted hover:border-line-strong hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
            >
              <UserAvatar user={u} size="xs" />
              <span className="max-w-[140px] truncate">
                {u.name || u.email}
              </span>
              <X className="h-3 w-3 text-fg-faint group-hover:text-fg-muted" />
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
        selected ? "bg-surface-muted" : "hover:bg-surface-subtle focus:bg-surface-subtle"
      )}
    >
      <UserAvatar user={user} size="sm" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm font-medium text-fg">
          {user.name || user.email.split("@")[0]}
        </div>
        {user.name && (
          <div className="truncate text-xs text-fg-subtle">
            {user.email}
          </div>
        )}
      </div>
      {selected && <Check className="h-3.5 w-3.5 shrink-0 text-fg-muted" />}
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
  const dims = size === "xs" ? "h-4 w-4 text-xs" : "h-6 w-6 text-xs"
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
        "flex shrink-0 items-center justify-center rounded-full bg-surface-muted font-medium text-fg-muted",
        dims
      )}
    >
      {initial}
    </div>
  )
}
