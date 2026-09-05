// Input lives INSIDE DropdownMenu.Content, not as an asChild trigger:
// Radix treats the trigger as a button and steals focus/clicks for its
// open-toggle, so an input nested via asChild never receives keystrokes.
import { useEffect, useMemo, useRef, useState } from "react"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { AlertTriangle, Check, ChevronsUpDown, Loader2, Search, X } from "lucide-react"
import { useTranslation } from "react-i18next"

import { ApiError } from "../lib/api-client"
import { useUserSearchQuery } from "../lib/api-users"
import type { PlatformUser } from "../lib/api-types"
import { Button } from "./ui/button"
import { Input } from "./ui/input"
import { InitialTile } from "./ui/ledger"
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
    <div className={cn("flex flex-col gap-2", className)}>
      {/* Nested in a dialog; non-modal lets its footer receive the first click. */}
      <DropdownMenu.Root modal={false}>
        <DropdownMenu.Trigger asChild disabled={disabled}>
          <Button
            type="button"
            variant="outline"
            className={cn("w-full justify-between font-normal", selected.length === 0 && "text-fg-muted")}
          >
            <span className="flex min-w-0 items-center gap-1.5">
              <Search className="text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
              <span className="truncate">{triggerLabel}</span>
            </span>
            <ChevronsUpDown className="text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
          </Button>
        </DropdownMenu.Trigger>

        <DropdownMenu.Portal>
          <DropdownMenu.Content
            align="start"
            sideOffset={4}
            className="app-shadow-floating z-50 max-h-[360px] w-[var(--radix-dropdown-menu-trigger-width)] min-w-[320px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in"
            // Stay open across multiple selections — picking several
            // teammates at once is the expected flow.
            onCloseAutoFocus={(e) => e.preventDefault()}
          >
            <div className="border-b border-line p-1">
              <Input
                value={input}
                onChange={(e) => setInput(e.target.value)}
                placeholder={t("members.add.search.placeholder")}
                // Radix DropdownMenu binds arrow keys / typeahead at
                // Content level; without stopPropagation those steal
                // the input's cursor movement.
                onKeyDown={(e) => e.stopPropagation()}
                autoFocus
              />
            </div>

            <div className="max-h-[280px] overflow-auto py-1">
              {trimmed.length === 0 ? (
                <p className="px-2 py-1.5 text-sm text-fg-muted">
                  {t("members.add.search.typeToSearch")}
                </p>
              ) : isLoading ? (
                <div className="flex items-center gap-1.5 px-2 py-1.5 text-sm text-fg-muted">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" strokeWidth={1.5} aria-hidden="true" />
                  {t("members.add.search.loading")}
                </div>
              ) : errMsg ? (
                <p className="flex items-start gap-1.5 px-2 py-1.5 text-sm text-fg">
                  <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
                  <span className="break-words">{errMsg}</span>
                </p>
              ) : showEmpty ? (
                <p className="px-2 py-1.5 text-sm text-fg-muted">
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
        <div className="flex flex-wrap gap-1">
          {selected.map((u) => (
            <Button
              key={u.id}
              type="button"
              variant="outline"
              size="sm"
              shape="pill"
              onClick={() => remove(u.id)}
              disabled={disabled}
              className="font-normal"
            >
              <UserAvatar user={u} />
              <span className="max-w-[140px] truncate">{u.name || u.email}</span>
              <X className="text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
            </Button>
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
        "flex h-8 cursor-pointer items-center gap-2 rounded px-2 text-sm text-fg outline-none data-[highlighted]:app-pressed",
        selected && "app-hover",
      )}
    >
      <UserAvatar user={user} />
      <span className="min-w-0 flex-1 truncate">
        <span className="font-medium">{user.name || user.email.split("@")[0]}</span>
        {user.name && (
          <span className="font-mono text-xs text-fg-muted"> · {user.email}</span>
        )}
      </span>
      {selected && <Check className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />}
    </DropdownMenu.Item>
  )
}

/** 18px tile: the avatar image when the user has one, else their initial. */
function UserAvatar({ user }: { user: PlatformUser }) {
  if (user.avatar_url) {
    return (
      <img
        src={user.avatar_url}
        alt=""
        className="h-[18px] w-[18px] shrink-0 rounded object-cover"
      />
    )
  }
  return <InitialTile name={user.name || user.email || "?"} />
}
