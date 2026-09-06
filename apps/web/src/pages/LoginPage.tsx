import { useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"

import { BrandMark } from "../components/ui/brand-mark"
import { cn } from "../lib/utils"
import { Button } from "../components/ui/button"
import { EntryPage } from "../components/ui/entry-panel"
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
 *   status.needed=false -> render the sign-in view
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

/**
 * The front door: mark, two-line headline, the credential form, and the
 * two things a visitor can actually do — sign in, or join with the invite
 * they were sent. It is the one screen in the product that gets the 28px
 * display size and a full-width primary button.
 */
function SignInView() {
  const { t } = useTranslation("common")
  const loginM = useLoginWithPassword()
  const providersQ = useAuthProviders()
  const [mode, setMode] = useState<"signIn" | "invite">("signIn")

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
      <div className="w-full max-w-[400px]">
        <header className="mb-8 flex flex-col items-center text-center">
          <BrandMark size={44} />
          <h1 className="font-display mt-6 text-3xl leading-tight text-fg" translate="no">
            {t("login.headline")}
          </h1>
          <p className="mt-1 text-3xl font-normal leading-tight tracking-display text-fg-muted">
            {mode === "signIn" ? t("login.headlineSub") : t("login.registerTitle")}
          </p>
        </header>

        {mode === "signIn" ? (
          <>
            <form className="flex flex-col gap-4" onSubmit={onSubmit} noValidate>
              <Field label={t("login.emailLabel")} htmlFor="login-email">
                <Input
                  id="login-email"
                  size="lg"
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
                  size="lg"
                  type="password"
                  name="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder={t("login.passwordPlaceholder")}
                  autoComplete="current-password"
                  required
                />
              </Field>

              {errorMsg && <InlineError>{errorMsg}</InlineError>}

              <div className="mt-1 flex flex-col gap-2">
                <Button type="submit" size="xl" className="w-full" disabled={invalid || submitting}>
                  {submitting ? t("login.submitting") : t("login.submitButton")}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="xl"
                  className="w-full"
                  onClick={() => setMode("invite")}
                >
                  {t("login.registerButton")}
                </Button>
              </div>
            </form>

            {ssoProviders.length > 0 && (
              <>
                <div className="my-6 flex items-center gap-3 text-xs text-fg-muted">
                  <span className="h-px flex-1 bg-line" />
                  <span>{t("login.orContinue")}</span>
                  <span className="h-px flex-1 bg-line" />
                </div>
                <div className={cn("grid gap-2", ssoProviders.length > 1 ? "grid-cols-2" : "grid-cols-1")}>
                  {ssoProviders.map((provider) => (
                    <Button
                      key={provider.id}
                      asChild
                      variant="outline"
                      size="xl"
                      className="w-full"
                    >
                      <a href={provider.login_url}>{provider.label}</a>
                    </Button>
                  ))}
                </div>
              </>
            )}
          </>
        ) : (
          <InviteEntry onBack={() => setMode("signIn")} />
        )}
      </div>
    </EntryPage>
  )
}

/**
 * Parsar has no self-serve signup: after the first owner exists, people
 * arrive through an invite link. This turns the pasted link into the
 * invite route, and rejects anything that is not one.
 */
function InviteEntry({ onBack }: { onBack: () => void }) {
  const { t } = useTranslation("common")
  const [link, setLink] = useState("")
  const [error, setError] = useState("")

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const token = parseInviteToken(link)
    if (!token) {
      setError(t("login.inviteInvalid"))
      return
    }
    window.location.assign(`/invite/${token}`)
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
      <p className="m-0 text-base text-fg-muted">{t("login.registerHint")}</p>
      <Field label={t("login.inviteLabel")} htmlFor="invite-link">
        <Input
          id="invite-link"
          size="lg"
          value={link}
          onChange={(e) => {
            setLink(e.target.value)
            setError("")
          }}
          placeholder={t("login.invitePlaceholder")}
          spellCheck={false}
          autoFocus
        />
      </Field>
      {error && <InlineError>{error}</InlineError>}
      <div className="mt-1 flex flex-col gap-2">
        <Button type="submit" size="xl" className="w-full" disabled={link.trim() === ""}>
          {t("login.inviteContinue")}
        </Button>
        <Button type="button" variant="outline" size="xl" className="w-full" onClick={onBack}>
          {t("login.backToLogin")}
        </Button>
      </div>
    </form>
  )
}

/** Accepts a full invite URL or a bare token; returns null for anything else. */
function parseInviteToken(raw: string): string | null {
  const value = raw.trim()
  if (!value) return null
  const fromUrl = value.match(/\/invite\/([^/?#\s]+)/)
  if (fromUrl) return fromUrl[1]
  // A bare token: uuid-ish or opaque, but never a path or a URL.
  if (/^[A-Za-z0-9._~-]{8,}$/.test(value)) return value
  return null
}
