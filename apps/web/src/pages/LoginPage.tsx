import { useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"

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
    <main className="relative grid min-h-screen place-items-center overflow-hidden bg-surface px-6 text-fg">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 [background-image:radial-gradient(circle,var(--color-line-strong)_1px,transparent_1px)] [background-size:26px_26px] [mask-image:radial-gradient(ellipse_60%_50%_at_50%_42%,black,transparent)] opacity-50"
      />
      <section className="relative w-full max-w-[460px] rounded-2xl border border-line/80 bg-surface p-9 shadow-[0_1px_2px_rgb(0_0_0/0.04),0_20px_48px_-16px_rgb(0_0_0/0.16)]">
        <div className="mb-8 flex flex-col items-center text-center">
          <img src="/parsar-banner.png" alt="Parsar" className="h-auto w-[320px] max-w-full" />
          <p className="mt-4 text-base leading-relaxed text-fg-subtle">{t("login.subtitle")}</p>
        </div>

        <form className="flex flex-col gap-4" onSubmit={onSubmit} noValidate>
          <label className="flex flex-col gap-1.5">
            <span className="text-sm font-medium text-fg-muted">{t("login.emailLabel")}</span>
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder={t("login.emailPlaceholder")}
              autoComplete="email"
              required
              className="h-10 rounded-md border border-line bg-surface px-3 text-base text-fg placeholder:text-fg-faint focus:border-fg/40 focus:outline-none focus:ring-1 focus:ring-fg/20"
            />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="text-sm font-medium text-fg-muted">{t("login.passwordLabel")}</span>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t("login.passwordPlaceholder")}
              autoComplete="current-password"
              required
              className="h-10 rounded-md border border-line bg-surface px-3 text-base text-fg placeholder:text-fg-faint focus:border-fg/40 focus:outline-none focus:ring-1 focus:ring-fg/20"
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

          <button
            type="submit"
            disabled={invalid || submitting}
            className="mt-1 flex h-11 w-full items-center justify-center rounded-full bg-surface-emphasis px-5 text-lg font-medium text-white shadow-sm transition-all hover:bg-surface-inverse hover:shadow-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-fg/20 focus-visible:ring-offset-2 focus-visible:ring-offset-surface active:translate-y-px disabled:cursor-not-allowed disabled:opacity-60"
          >
            {submitting ? t("login.submitting") : t("login.submitButton")}
          </button>
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
                  className="flex h-11 w-full items-center justify-center rounded-full border border-line bg-surface px-5 text-base font-medium text-fg transition-colors hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-fg/20 focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
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
