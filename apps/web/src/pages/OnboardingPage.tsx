/**
 * /onboarding — first-time workspace creation. Rendered by `Root` when
 * an authenticated user has no workspace memberships.
 */
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { ApiError } from "../lib/api-client"
import { useCreateWorkspace } from "../lib/api-workspaces"
import { useAuth } from "../lib/auth-context"
import { setWorkspaceId } from "../lib/workspace"
import { workspaceOwnerName } from "../lib/workspace-defaults"
import { Button } from "../components/ui/button"
import { EntryFooter, EntryPage, EntryPanel } from "../components/ui/entry-panel"
import { InlineError } from "../components/ui/error-state"
import { Input } from "../components/ui/input"
import { Field } from "../components/ui/label"

function extractErrorMessage(err: unknown): string | null {
  if (!err) return null
  if (err instanceof ApiError) {
    return err.envelope.message || err.message
  }
  if (err instanceof Error) return err.message
  return String(err)
}

export function OnboardingPage() {
  const { t } = useTranslation("common")
  const { user } = useAuth()
  const owner = workspaceOwnerName(user)
  const [name, setName] = useState(() =>
    owner
      ? t("workspaceDefaults.personal", { name: owner })
      : t("workspaceDefaults.generic")
  )
  const create = useCreateWorkspace()
  const errMsg = extractErrorMessage(create.error)

  return (
    <EntryPage>
      <EntryPanel title={t("onboarding.title")}>
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            const trimmed = name.trim()
            if (!trimmed || create.isPending) return
            create.mutate(
              { name: trimmed },
              {
                onSuccess: (data) => {
                  setWorkspaceId(data.workspace.id)
                  // Hard navigate so AuthProvider + useMyWorkspaces both
                  // refetch instead of depending on cache-invalidation
                  // timing.
                  window.location.assign("/")
                },
              }
            )
          }}
        >
          <Field label={t("onboarding.fields.name")} htmlFor="onboarding-ws-name">
            <Input
              id="onboarding-ws-name"
              value={name}
              autoFocus
              required
              maxLength={64}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("onboarding.fields.namePlaceholder")}
            />
          </Field>

          <EntryFooter message={errMsg && <InlineError>{errMsg}</InlineError>}>
            <Button type="submit" disabled={create.isPending || !name.trim()}>
              {create.isPending ? t("states.loading") : t("onboarding.actions.create")}
            </Button>
          </EntryFooter>
        </form>
      </EntryPanel>
    </EntryPage>
  )
}
