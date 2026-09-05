import { useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "../components/ui/button"
import { EntryFooter, EntryPage, EntryPanel } from "../components/ui/entry-panel"
import { InlineError } from "../components/ui/error-state"
import { Input } from "../components/ui/input"
import { Field } from "../components/ui/label"
import { ApiError } from "../lib/api-client"
import { useRegisterFirstOwner } from "../lib/api-bootstrap"
import { validateNewPassword } from "../lib/password-policy"
import { workspaceOwnerName } from "../lib/workspace-defaults"

/**
 * SetupPage — first-owner registration.
 *
 * Rendered by LoginPage when GET /api/v1/bootstrap/status returns
 * needed=true. On successful POST /api/v1/bootstrap the server sets
 * the parsar_session cookie; the mutation invalidates ["me"] so the
 * AuthProvider re-reads the session and drops the caller into
 * AuthedRoot.
 *
 * Password policy is validated server-side by password.Validate. The client
 * mirrors the same simple checks so users get immediate feedback before the
 * server's bootstrap_weak_password envelope is surfaced inline.
 */
export function SetupPage() {
  const { t } = useTranslation("common")
  const register = useRegisterFirstOwner()

  const [email, setEmail] = useState("")
  const [name, setName] = useState("")
  const [workspace, setWorkspace] = useState(() => t("workspaceDefaults.generic"))
  const [workspaceEdited, setWorkspaceEdited] = useState(false)
  const [password, setPassword] = useState("")

  function suggestedWorkspaceName(nextName: string, nextEmail: string): string {
    const owner = workspaceOwnerName({ name: nextName, email: nextEmail })
    return owner
      ? t("workspaceDefaults.personal", { name: owner })
      : t("workspaceDefaults.generic")
  }

  function updateName(nextName: string) {
    setName(nextName)
    if (!workspaceEdited) {
      setWorkspace(suggestedWorkspaceName(nextName, email))
    }
  }

  function updateEmail(nextEmail: string) {
    setEmail(nextEmail)
    if (!workspaceEdited) {
      setWorkspace(suggestedWorkspaceName(name, nextEmail))
    }
  }

  const submitting = register.isPending
  const errorMsg =
    register.error instanceof ApiError
      ? register.error.envelope.message
      : register.error instanceof Error
        ? register.error.message
        : ""

  const passwordPolicyError = validateNewPassword(password)
  const passwordPolicyErrorMsg =
    password === "" || passwordPolicyError === null
      ? undefined
      : t(`passwordPolicy.errors.${passwordPolicyError}`)
  const invalid = email.trim() === "" || workspace.trim() === "" || passwordPolicyError !== null

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (invalid || submitting) return
    try {
      await register.mutateAsync({
        email: email.trim(),
        name: name.trim(),
        workspace_name: workspace.trim(),
        password,
      })
      // Cookie is now set. Hard-reload so any bootstrap-time state
      // (query cache, i18n language detection, etc.) starts clean
      // and the AuthProvider mounts the authed shell.
      window.location.assign("/")
    } catch {
      /* mutation state carries the error; nothing else to do */
    }
  }

  return (
    <EntryPage>
      <EntryPanel title={t("setup.title")} description={t("setup.subtitle")}>
        <form className="flex flex-col gap-3" onSubmit={onSubmit} noValidate>
          <Field label={t("setup.nameLabel")} htmlFor="setup-name">
            <Input
              id="setup-name"
              value={name}
              onChange={(e) => updateName(e.target.value)}
              placeholder={t("setup.namePlaceholder")}
              autoComplete="name"
            />
          </Field>
          <Field label={t("setup.emailLabel")} htmlFor="setup-email">
            <Input
              id="setup-email"
              type="email"
              value={email}
              onChange={(e) => updateEmail(e.target.value)}
              placeholder={t("setup.emailPlaceholder")}
              autoComplete="email"
              spellCheck={false}
              required
            />
          </Field>
          <Field label={t("setup.workspaceLabel")} htmlFor="setup-workspace">
            <Input
              id="setup-workspace"
              value={workspace}
              onChange={(e) => {
                setWorkspaceEdited(true)
                setWorkspace(e.target.value)
              }}
              placeholder={t("setup.workspacePlaceholder")}
              autoComplete="organization"
              required
            />
          </Field>
          <Field
            label={t("setup.passwordLabel")}
            htmlFor="setup-password"
            hint={
              passwordPolicyErrorMsg ? (
                <InlineError>{passwordPolicyErrorMsg}</InlineError>
              ) : (
                t("passwordPolicy.hint")
              )
            }
          >
            <Input
              id="setup-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t("passwordPolicy.placeholder")}
              autoComplete="new-password"
              required
            />
          </Field>

          <EntryFooter message={errorMsg && <InlineError>{errorMsg}</InlineError>}>
            <Button type="submit" disabled={invalid || submitting}>
              {submitting ? t("setup.submitting") : t("setup.submitButton")}
            </Button>
          </EntryFooter>
        </form>
      </EntryPanel>
    </EntryPage>
  )
}
