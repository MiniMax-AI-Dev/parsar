import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "../components/ui/button"
import { EntryFooter, EntryPage, EntryPanel } from "../components/ui/entry-panel"
import { InlineError } from "../components/ui/error-state"
import { Field } from "../components/ui/label"
import { StatusIcon } from "../components/ui/status-icon"
import { Textarea } from "../components/ui/textarea"
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
import { JOIN_INTENT_KEY } from "../lib/join-intent"

/**
 * Landing page for the Feishu rejection card's "Join" link. Reads
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
    return <Notice>{t("workspaceCrud.join.landing.loading")}</Notice>
  }

  if (!isAuthenticated) {
    // A tiny CTA before the OAuth bounce — an automatic redirect would
    // leave the user wondering why they're suddenly on a login page.
    return (
      <EntryPage>
        <EntryPanel>
          <p className="text-base text-fg">
            {t("workspaceCrud.join.landing.loginRequired", { name: workspaceId })}
          </p>
          <EntryFooter>
            <Button asChild>
              <a href={feishuLoginUrl()}>{t("workspaceCrud.join.landing.loginCta")}</a>
            </Button>
          </EntryFooter>
        </EntryPanel>
      </EntryPage>
    )
  }

  return <Authenticated workspaceId={workspaceId} />
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
    return <Notice>{t("workspaceCrud.join.landing.loading")}</Notice>
  }

  if (alreadyMember) {
    return <Notice>{t("workspaceCrud.join.landing.alreadyMember")}</Notice>
  }

  // Missing match = nonexistent, archived, or private (createJoinRequest
  // 404s on private by design to prevent enumeration). Single dead-end
  // UX — we don't surface the distinction to a non-member.
  if (!match) {
    return (
      <EntryPage>
        <EntryPanel
          title={t("workspaceCrud.join.landing.privateTitle")}
          description={t("workspaceCrud.join.landing.privateDescription")}
        >
          <EntryFooter className="mt-0">
            <Button asChild variant="outline">
              <a href="/">{t("workspaceCrud.join.landing.backToHome")}</a>
            </Button>
          </EntryFooter>
        </EntryPanel>
      </EntryPage>
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
      <EntryPage>
        <EntryPanel
          title={t("workspaceCrud.join.landing.successTitle")}
          description={t("workspaceCrud.join.landing.successDescription")}
        >
          <p className="flex items-center gap-2 text-sm text-fg">
            <StatusIcon status="queued" />
            {t("workspaceCrud.join.landing.pendingTitle")}
          </p>
          <EntryFooter message={errMsg && <InlineError>{errMsg}</InlineError>}>
            <Button
              type="button"
              variant="outline"
              onClick={() => withdraw.mutate({ wsId: workspace.id })}
              disabled={withdraw.isPending}
            >
              {withdraw.isPending
                ? t("states.loading")
                : t("workspaceCrud.join.landing.withdrawAction")}
            </Button>
            <Button asChild>
              <a href="/">{t("workspaceCrud.join.landing.backToHome")}</a>
            </Button>
          </EntryFooter>
        </EntryPanel>
      </EntryPage>
    )
  }

  return (
    <EntryPage>
      <EntryPanel title={t("workspaceCrud.join.title", { name: workspace.name })}>
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (tooLong || request.isPending) return
            request.mutate(
              { wsId: workspace.id, body: { reason: trimmed } },
              { onSuccess: () => setSubmitted(true) }
            )
          }}
        >
          <Field
            label={`${t("workspaceCrud.fields.reason")} ${t("workspaceCrud.fields.optional")}`}
            htmlFor="join-reason"
            hint={tooLong ? <InlineError>{t("workspaceCrud.join.reasonTooLong")}</InlineError> : undefined}
          >
            <Textarea
              id="join-reason"
              value={reason}
              autoFocus
              rows={4}
              onChange={(e) => setReason(e.target.value)}
              placeholder={t("workspaceCrud.join.reasonPlaceholder")}
            />
          </Field>

          <EntryFooter message={errMsg && <InlineError>{errMsg}</InlineError>}>
            <Button asChild variant="outline">
              <a href="/">{t("actions.cancel")}</a>
            </Button>
            <Button type="submit" disabled={request.isPending || tooLong}>
              {request.isPending
                ? t("states.loading")
                : t("workspaceCrud.actions.submitJoinRequest")}
            </Button>
          </EntryFooter>
        </form>
      </EntryPanel>
    </EntryPage>
  )
}

/** Transient states: one muted line on the paper ground, no panel. */
function Notice({ children }: { children: React.ReactNode }) {
  return (
    <EntryPage>
      <p className="text-sm text-fg-muted">{children}</p>
    </EntryPage>
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
