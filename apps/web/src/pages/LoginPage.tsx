import { useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "../components/ui/button"
import { EntryFooter, EntryPage, EntryPanel } from "../components/ui/entry-panel"
import { InlineError } from "../components/ui/error-state"
import { Input } from "../components/ui/input"
import { Field } from "../components/ui/label"
import { ApiError } from "../lib/api-client"
import { useAuthProviders, useLoginWithPassword } from "../lib/api-auth"
import { useBootstrapStatus } from "../lib/api-bootstrap"
import { SetupPage } from "./SetupPage"

/**
 * LoginPage — first unauthenticated route the caller lands on.
 *
 * Behavior branches on `GET /api/v1/bootstrap/status`:
 *   status.needed=true  -> render <SetupPage/> (first-owner registration)
 *   status.needed=false -> render the email/password form
 */
export function LoginPage() {
  const { t } = useTranslation("common")
  const statusQ = useBootstrapStatus()

  if (statusQ.isLoading) {
    return (
      <EntryPage>
        <p className="text-sm text-fg-muted">{t("login.loading")}</p>
      </EntryPage>
    )
  }
  if (statusQ.data?.needed) {
    return <SetupPage />
  }
  return <SignInView />
}

function SignInView() {
  const { t } = useTranslation("common")
  const loginM = useLoginWithPassword()
  const providersQ = useAuthProviders()

  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")

  const submitting = loginM.isPending
  const invalid = email.trim() === "" || password === ""
  const ssoProviders =
    providersQ.data?.providers.filter(
      (p) => p.enabled && p.id !== "password" && Boolean(p.login_url),
    ) ?? []

  const errorMsg = (() => {
    const err = loginM.error
    if (!err) return ""
    if (err instanceof ApiError) {
      if (err.envelope.code === "invalid_credentials") return t("login.invalidCredentials")
      return err.envelope.message || t("login.genericError")
    }
    return t("login.genericError")
  })()

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (invalid || submitting) return
    try {
      await loginM.mutateAsync({ email: email.trim(), password })
      // Cookie set on 200. Hard reload so AuthProvider re-reads /me
      // with the fresh cookie and mounts AuthedRoot.
      window.location.assign("/")
    } catch {
      /* mutation state carries the error */
    }
  }

  return (
    <EntryPage>
      <EntryPanel wordmark={false} title={<span translate="no">{t("login.title")}</span>}>
        <form className="flex flex-col gap-3" onSubmit={onSubmit} noValidate>
          <Field label={t("login.emailLabel")} htmlFor="login-email">
            <Input
              id="login-email"
              type="email"
              name="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder={t("login.emailPlaceholder")}
              autoComplete="email"
              spellCheck={false}
              required
            />
          </Field>
          <Field label={t("login.passwordLabel")} htmlFor="login-password">
            <Input
              id="login-password"
              type="password"
              name="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t("login.passwordPlaceholder")}
              autoComplete="current-password"
              required
            />
          </Field>

          <EntryFooter message={errorMsg && <InlineError>{errorMsg}</InlineError>}>
            <Button type="submit" disabled={invalid || submitting}>
              {submitting ? t("login.submitting") : t("login.submitButton")}
            </Button>
          </EntryFooter>
        </form>

        {ssoProviders.length > 0 && (
          <>
            <div className="my-4 flex items-center gap-3 text-xs text-fg-muted">
              <span className="h-px flex-1 bg-line" />
              <span>{t("login.ssoDivider")}</span>
              <span className="h-px flex-1 bg-line" />
            </div>
            <div className="flex flex-col gap-2">
              {ssoProviders.map((provider) => (
                <Button key={provider.id} asChild variant="outline" className="w-full">
                  <a href={provider.login_url}>
                    {t("login.ssoButton", { provider: provider.label })}
                  </a>
                </Button>
              ))}
            </div>
          </>
        )}
      </EntryPanel>
    </EntryPage>
  )
}
