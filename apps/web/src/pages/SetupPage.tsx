import { useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"

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
    <main className="relative grid min-h-screen place-items-center overflow-hidden bg-surface px-6 text-fg">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 [background-image:radial-gradient(circle,var(--color-line-strong)_1px,transparent_1px)] [background-size:26px_26px] [mask-image:radial-gradient(ellipse_60%_50%_at_50%_42%,black,transparent)] opacity-50"
      />
      <section className="relative w-full max-w-[460px] rounded-2xl border border-line/80 bg-surface p-9 shadow-[0_1px_2px_rgb(0_0_0/0.04),0_20px_48px_-16px_rgb(0_0_0/0.16)]">
        <div className="mb-7 flex flex-col items-center text-center">
          <div className="rounded-xl bg-fg-on-emphasis px-5 py-3 shadow-sm ring-1 ring-line-muted">
            <img src="/parsar-banner.png" alt="Parsar" className="h-auto w-[260px] max-w-full" />
          </div>
          <h1 className="mt-4 text-lg font-semibold text-fg">{t("setup.title")}</h1>
          <p className="mt-2 text-base leading-relaxed text-fg-subtle">{t("setup.subtitle")}</p>
        </div>

        <form className="flex flex-col gap-4" onSubmit={onSubmit} noValidate>
          <Field
            label={t("setup.nameLabel")}
            placeholder={t("setup.namePlaceholder")}
            value={name}
            onChange={updateName}
            autoComplete="name"
            required={false}
          />
          <Field
            type="email"
            label={t("setup.emailLabel")}
            placeholder={t("setup.emailPlaceholder")}
            value={email}
            onChange={updateEmail}
            autoComplete="email"
            required
          />
          <Field
            label={t("setup.workspaceLabel")}
            placeholder={t("setup.workspacePlaceholder")}
            value={workspace}
            onChange={(value) => {
              setWorkspaceEdited(true)
              setWorkspace(value)
            }}
            autoComplete="organization"
            required
          />
          <Field
            type="password"
            label={t("setup.passwordLabel")}
            placeholder={t("passwordPolicy.placeholder")}
            value={password}
            onChange={setPassword}
            autoComplete="new-password"
            required
            hint={t("passwordPolicy.hint")}
            error={passwordPolicyErrorMsg}
          />

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
            {submitting ? t("setup.submitting") : t("setup.submitButton")}
          </button>
        </form>
      </section>
    </main>
  )
}

interface FieldProps {
  label: string
  value: string
  onChange: (v: string) => void
  type?: "text" | "email" | "password"
  placeholder?: string
  autoComplete?: string
  required?: boolean
  hint?: string
  error?: string
}

function Field({
  label,
  value,
  onChange,
  type = "text",
  placeholder,
  autoComplete,
  required,
  hint,
  error,
}: FieldProps) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-sm font-medium text-fg-muted">
        {label}
        {required && <span className="ml-0.5 text-danger">*</span>}
      </span>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoComplete={autoComplete}
        required={required}
        className="h-10 rounded-md border border-line bg-surface px-3 text-base text-fg placeholder:text-fg-faint focus:border-fg/40 focus:outline-none focus:ring-1 focus:ring-fg/20"
      />
      {error ? (
        <span className="text-xs leading-4 text-danger">{error}</span>
      ) : (
        hint && <span className="text-xs leading-4 text-fg-faint">{hint}</span>
      )}
    </label>
  )
}
