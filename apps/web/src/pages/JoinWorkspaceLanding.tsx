import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "../components/ui/button"
import { useAuth } from "../lib/auth-context"
import { feishuLoginUrl } from "../lib/api-auth"
import { ApiError } from "../lib/api-client"
import {
  useDiscoverableWorkspaces,
  useMyWorkspaces,
  useRequestJoinWorkspace,
  useWithdrawJoinRequest,
} from "../lib/api-workspaces"
import type { DiscoverableWorkspace } from "../lib/api-types"

/**
 * Landing page for the Feishu rejection card's "申请加入" link. Reads
 * ?id=<workspace_id>&from=feishu. Standalone (vs. reusing
 * DiscoverWorkspacesDialog) so the targeted workspace context isn't lost.
 */
export function JoinWorkspaceLanding({ workspaceId }: { workspaceId: string }) {
  const { t } = useTranslation("common")
  const { isLoading: authLoading, isAuthenticated } = useAuth()

  // Stash intent so Root can bounce back here after the OAuth callback
  // returns to "/".
  useEffect(() => {
    if (authLoading || isAuthenticated) return
    try {
      sessionStorage.setItem(
        JOIN_INTENT_KEY,
        window.location.pathname + window.location.search
      )
    } catch {
      // sessionStorage throws in private windows; user will re-click the
      // rejection card link post-login.
    }
  }, [authLoading, isAuthenticated])

  if (authLoading) {
    return <LandingShell>{t("workspaceCrud.join.landing.loading")}</LandingShell>
  }

  if (!isAuthenticated) {
    // A tiny CTA before the OAuth bounce — an automatic redirect would
    // leave the user wondering why they're suddenly on a login page.
    return (
      <LandingShell>
        <p className="text-base text-fg-muted">
          {t("workspaceCrud.join.landing.loginRequired", { name: workspaceId })}
        </p>
        <a
          href={feishuLoginUrl()}
          className="mt-4 inline-flex h-9 items-center justify-center rounded-md bg-[#2952F8] px-4 text-sm font-medium text-white hover:bg-[#1f45db]"
        >
          {t("workspaceCrud.join.landing.loginCta")}
        </a>
      </LandingShell>
    )
  }

  return <Authenticated workspaceId={workspaceId} />
}

const JOIN_INTENT_KEY = "parsar.joinWorkspaceIntent"

/**
 * Returns and clears the URL stashed during the unauthenticated → OAuth
 * bounce. Used by main.tsx's `Root` post-OAuth callback.
 */
export function popPendingJoinIntent(): string | null {
  try {
    const stash = sessionStorage.getItem(JOIN_INTENT_KEY)
    if (!stash) return null
    sessionStorage.removeItem(JOIN_INTENT_KEY)
    if (!stash.startsWith("/join-workspace")) return null
    return stash
  } catch {
    return null
  }
}

function Authenticated({ workspaceId }: { workspaceId: string }) {
  const { t } = useTranslation("common")
  const myWs = useMyWorkspaces()

  // If the user is already in this workspace, bounce to home instead of
  // letting them submit a duplicate request that would 409.
  const alreadyMember = useMemo(() => {
    return myWs.data?.workspaces.some((w) => w.id === workspaceId) ?? false
  }, [myWs.data, workspaceId])

  useEffect(() => {
    if (alreadyMember) {
      window.location.replace("/?ws=" + encodeURIComponent(workspaceId))
    }
  }, [alreadyMember, workspaceId])

  // limit=100 covers any realistic tenant; the rejection-link path is
  // rare enough that pagination isn't worth complicating today.
  const discoverable = useDiscoverableWorkspaces({ limit: 100 })

  const match: DiscoverableWorkspace | undefined = useMemo(() => {
    return discoverable.data?.workspaces.find((w) => w.id === workspaceId)
  }, [discoverable.data, workspaceId])

  if (myWs.isLoading || discoverable.isLoading) {
    return <LandingShell>{t("workspaceCrud.join.landing.loading")}</LandingShell>
  }

  if (alreadyMember) {
    return (
      <LandingShell>{t("workspaceCrud.join.landing.alreadyMember")}</LandingShell>
    )
  }

  // Missing match = nonexistent, archived, or private (createJoinRequest
  // 404s on private by design to prevent enumeration). Single dead-end
  // UX — we don't surface the distinction to a non-member.
  if (!match) {
    return (
      <LandingShell>
        <h1 className="text-lg font-semibold text-fg">
          {t("workspaceCrud.join.landing.privateTitle")}
        </h1>
        <p className="mt-2 text-sm text-fg-subtle">
          {t("workspaceCrud.join.landing.privateDescription")}
        </p>
        <a
          href="/"
          className="mt-4 inline-flex h-9 items-center justify-center rounded-md border border-line px-4 text-sm text-fg-muted hover:bg-surface-subtle"
        >
          {t("workspaceCrud.join.landing.backToHome")}
        </a>
      </LandingShell>
    )
  }

  return <RequestForm workspace={match} />
}

function RequestForm({ workspace }: { workspace: DiscoverableWorkspace }) {
  const { t } = useTranslation("common")
  const [reason, setReason] = useState("")
  const [submitted, setSubmitted] = useState(false)
  const request = useRequestJoinWorkspace()
  const withdraw = useWithdrawJoinRequest()

  const errMsg = extractErrorMessage(request.error ?? withdraw.error)
  const trimmed = reason.trim()
  const tooLong = trimmed.length > 1000

  // pending=true from the API covers the case of revisiting the link
  // after already submitting via this page.
  const pending = workspace.has_pending_request || submitted

  if (pending) {
    return (
      <LandingShell>
        <h1 className="text-lg font-semibold text-fg">
          {t("workspaceCrud.join.landing.successTitle")}
        </h1>
        <p className="mt-2 text-sm text-fg-subtle">
          {t("workspaceCrud.join.landing.successDescription")}
        </p>
        <div className="mt-2 rounded-md bg-warning-subtle px-3 py-2 text-sm text-warning-emphasis">
          {t("workspaceCrud.join.landing.pendingTitle")}
        </div>
        {errMsg && (
          <p className="mt-2 rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
            {errMsg}
          </p>
        )}
        <div className="mt-4 flex gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => withdraw.mutate({ wsId: workspace.id })}
            disabled={withdraw.isPending}
          >
            {withdraw.isPending
              ? t("states.loading")
              : t("workspaceCrud.join.landing.withdrawAction")}
          </Button>
          <a
            href="/"
            className="inline-flex h-9 items-center justify-center rounded-md border border-line px-4 text-sm text-fg-muted hover:bg-surface-subtle"
          >
            {t("workspaceCrud.join.landing.backToHome")}
          </a>
        </div>
      </LandingShell>
    )
  }

  return (
    <LandingShell>
      <h1 className="text-lg font-semibold text-fg">
        {t("workspaceCrud.join.title", { name: workspace.name })}
      </h1>
      <form
        className="mt-4 grid gap-3"
        onSubmit={(e) => {
          e.preventDefault()
          if (tooLong || request.isPending) return
          request.mutate(
            { wsId: workspace.id, body: { reason: trimmed } },
            { onSuccess: () => setSubmitted(true) }
          )
        }}
      >
        <div className="grid gap-1.5">
          <label
            className="text-sm font-medium text-fg-muted"
            htmlFor="join-reason"
          >
            {t("workspaceCrud.fields.reason")}
            <span className="ml-1 text-xs font-normal text-fg-faint">
              {t("workspaceCrud.fields.optional")}
            </span>
          </label>
          <textarea
            id="join-reason"
            value={reason}
            autoFocus
            rows={4}
            onChange={(e) => setReason(e.target.value)}
            placeholder={t("workspaceCrud.join.reasonPlaceholder")}
            className="rounded-md border border-line px-3 py-2 text-sm outline-none focus:border-line-strong"
          />
          {tooLong && (
            <p className="text-xs text-danger">
              {t("workspaceCrud.join.reasonTooLong")}
            </p>
          )}
        </div>

        {errMsg && (
          <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
            {errMsg}
          </p>
        )}

        <div className="flex justify-end gap-2">
          <a
            href="/"
            className="inline-flex h-9 items-center justify-center rounded-md border border-line px-4 text-sm text-fg-muted hover:bg-surface-subtle"
          >
            {t("actions.cancel")}
          </a>
          <Button type="submit" size="sm" disabled={request.isPending || tooLong}>
            {request.isPending
              ? t("states.loading")
              : t("workspaceCrud.actions.submitJoinRequest")}
          </Button>
        </div>
      </form>
    </LandingShell>
  )
}

function LandingShell({ children }: { children: React.ReactNode }) {
  return (
    <main className="grid min-h-screen place-items-center bg-surface px-6 text-fg">
      <section className="w-full max-w-[440px] rounded-2xl border border-line bg-surface p-8 shadow-sm">
        <div className="mb-5 flex items-center gap-2">
          <div className="grid h-8 w-8 place-items-center rounded-md bg-surface-emphasis text-base font-bold text-white">
            T
          </div>
          <span className="text-sm font-medium text-fg-subtle">
            Parsar
          </span>
        </div>
        {children}
      </section>
    </main>
  )
}

function extractErrorMessage(err: unknown): string | null {
  if (!err) return null
  if (err instanceof ApiError) {
    return err.envelope.message || err.message
  }
  if (err instanceof Error) return err.message
  return String(err)
}
