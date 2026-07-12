import { useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"
import { ArrowRight, LoaderCircle } from "lucide-react"

import { Button } from "../components/ui/button"
import { Input } from "../components/ui/input"
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
      <main className="grid min-h-screen place-items-center bg-surface text-fg-subtle">
        <p className="text-sm">{t("login.loading")}</p>
      </main>
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
    <main className="relative grid min-h-screen place-items-center overflow-hidden bg-surface-subtle px-6 py-12 text-fg">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 [background-image:radial-gradient(circle,var(--color-line-strong)_1px,transparent_1px)] [background-size:28px_28px] [mask-image:radial-gradient(ellipse_65%_58%_at_50%_45%,black,transparent)] opacity-35"
      />
      <section className="app-panel relative w-full max-w-[460px] p-9">
        <div className="mb-8 flex flex-col items-center text-center">
          <div className="rounded-2xl bg-fg-on-emphasis px-5 py-3 shadow-sm ring-1 ring-line-muted">
            <img
              src="/parsar-banner.png"
              alt="Parsar"
              width="560"
              height="96"
              className="h-auto w-[280px] max-w-full"
            />
          </div>
          <p className="mt-4 text-base leading-relaxed text-fg-subtle">{t("login.subtitle")}</p>
        </div>

        <form className="flex flex-col gap-4" onSubmit={onSubmit} noValidate>
          <label className="flex flex-col gap-1.5">
            <span className="text-sm font-medium text-fg-muted">{t("login.emailLabel")}</span>
            <Input
              type="email"
              name="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder={t("login.emailPlaceholder")}
              autoComplete="email"
              spellCheck={false}
              required
              className="h-11 text-base"
            />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="text-sm font-medium text-fg-muted">{t("login.passwordLabel")}</span>
            <Input
              type="password"
              name="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t("login.passwordPlaceholder")}
              autoComplete="current-password"
              required
              className="h-11 text-base"
            />
          </label>

          {errorMsg && (
            <div
              role="alert"
              className="rounded-md border border-danger/40 bg-danger/8 px-3 py-2 text-sm text-danger"
            >
              {errorMsg}
            </div>
          )}

          <Button
            type="submit"
            disabled={invalid || submitting}
            size="lg"
            shape="pill"
            className="mt-1 h-11 w-full text-base"
          >
            {submitting ? (
              <LoaderCircle
                className="h-4 w-4 animate-spin motion-reduce:animate-none"
                aria-hidden="true"
              />
            ) : null}
            {submitting ? t("login.submitting") : t("login.submitButton")}
            {!submitting ? <ArrowRight className="h-4 w-4" aria-hidden="true" /> : null}
          </Button>
        </form>

        {ssoProviders.length > 0 && (
          <>
            <div className="my-6 flex items-center gap-3 text-xs text-fg-faint">
              <span className="h-px flex-1 bg-line" />
              <span>{t("login.ssoDivider")}</span>
              <span className="h-px flex-1 bg-line" />
            </div>

            <div className="grid gap-2">
              {ssoProviders.map((provider) => (
                <a
                  key={provider.id}
                  href={provider.login_url}
                  className="flex h-11 w-full items-center justify-center rounded-full border border-line bg-surface px-5 text-base font-medium text-fg shadow-sm transition-[color,background-color,border-color,box-shadow] hover:border-line-strong hover:bg-surface-subtle hover:shadow-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-info/30 focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
                >
                  {t("login.ssoButton", { provider: provider.label })}
                </a>
              ))}
            </div>
          </>
        )}

        <p className="mt-6 text-center text-sm leading-5 text-fg-faint">
          {t("login.noAccountHint")}
        </p>
      </section>
    </main>
  )
}
