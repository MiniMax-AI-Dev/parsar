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
import { Input } from "../components/ui/input"

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
    <main className="grid min-h-screen place-items-center bg-surface px-6 text-fg">
      <section className="w-full max-w-[440px]">
        <div className="mb-8 text-center">
          <h1 className="text-2xl font-semibold tracking-display">
            {t("onboarding.title")}
          </h1>
          <p className="mt-2 text-base text-fg-subtle">
            {t("onboarding.subtitle")}
          </p>
        </div>

        <form
          className="rounded-2xl border border-line bg-surface p-6 shadow-sm"
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
          <div className="grid gap-1.5">
            <label
              className="text-sm font-medium text-fg-muted"
              htmlFor="onboarding-ws-name"
            >
              {t("onboarding.fields.name")}
            </label>
            <Input
              id="onboarding-ws-name"
              value={name}
              autoFocus
              required
              maxLength={64}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("onboarding.fields.namePlaceholder")}
            />
          </div>

          {errMsg && (
            <p className="mt-3 rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
              {errMsg}
            </p>
          )}

          <Button
            type="submit"
            className="mt-5 w-full"
            disabled={create.isPending || !name.trim()}
          >
            {create.isPending
              ? t("states.loading")
              : t("onboarding.actions.create")}
          </Button>
        </form>
      </section>
    </main>
  )
}
