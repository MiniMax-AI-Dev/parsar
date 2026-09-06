import { useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "../components/ui/button"
import { EntryFooter, EntryPage, EntryPanel } from "../components/ui/entry-panel"
import { InlineError } from "../components/ui/error-state"
import { Input } from "../components/ui/input"
import { Field } from "../components/ui/label"
import { useInviteInfo, useAcceptInvite } from "../lib/api-invitations"
import { validateNewPassword } from "../lib/password-policy"
import { setWorkspaceId } from "../lib/workspace"

export function InviteAcceptPage({ token }: { token: string }) {
  const { t } = useTranslation("common")
  const infoQ = useInviteInfo(token)
  const acceptMut = useAcceptInvite()
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [errMsg, setErrMsg] = useState<string | null>(null)
  const passwordPolicyError = validateNewPassword(password)
  const passwordPolicyErrorMsg =
    password === "" || passwordPolicyError === null
      ? null
      : t(`passwordPolicy.errors.${passwordPolicyError}`)

  if (infoQ.isLoading) {
    return (
      <EntryPage>
        <p className="text-sm text-fg-muted">{t("invite.loading")}</p>
      </EntryPage>
    )
  }

  if (infoQ.isError || !infoQ.data) {
    return (
      <EntryPage>
        <EntryPanel title={t("invite.invalidTitle")} description={t("invite.invalidDescription")} />
      </EntryPage>
    )
  }

  const { workspace_name, email, role } = infoQ.data

  const handleSubmit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    setErrMsg(null)
    if (password !== confirm) {
      setErrMsg(t("invite.mismatch"))
      return
    }
    if (passwordPolicyError !== null) {
      setErrMsg(t(`passwordPolicy.errors.${passwordPolicyError}`))
      return
    }
    try {
      const res = await acceptMut.mutateAsync({ token, password })
      setWorkspaceId(res.workspace_id)
      window.location.assign("/")
    } catch (err) {
      setErrMsg(err instanceof Error ? err.message : t("invite.acceptFailed"))
    }
  }

  return (
    <EntryPage>
      <EntryPanel
        title={t("invite.title", { name: workspace_name })}
        description={t("invite.description", { role })}
      >
        <form onSubmit={handleSubmit} className="flex flex-col gap-3">
          <Field label={t("login.emailLabel")} htmlFor="invite-email">
            <Input id="invite-email" type="email" value={email} readOnly disabled />
          </Field>

          <Field
            label={t("login.passwordLabel")}
            htmlFor="invite-password"
            hint={
              passwordPolicyErrorMsg ? (
                <InlineError>{passwordPolicyErrorMsg}</InlineError>
              ) : (
                t("passwordPolicy.hint")
              )
            }
          >
            <Input
              id="invite-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t("passwordPolicy.placeholder")}
              required
              autoFocus
              autoComplete="new-password"
            />
          </Field>

          <Field label={t("invite.confirmLabel")} htmlFor="invite-confirm">
            <Input
              id="invite-confirm"
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              placeholder={t("invite.confirmPlaceholder")}
              required
              autoComplete="new-password"
            />
          </Field>

          <EntryFooter message={errMsg && <InlineError>{errMsg}</InlineError>}>
            <Button type="submit" disabled={acceptMut.isPending || passwordPolicyError !== null}>
              {acceptMut.isPending ? t("invite.submitting") : t("invite.submit")}
            </Button>
          </EntryFooter>
        </form>
      </EntryPanel>
    </EntryPage>
  )
}
