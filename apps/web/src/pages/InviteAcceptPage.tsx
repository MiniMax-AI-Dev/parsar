import { useState } from "react"
import { useInviteInfo, useAcceptInvite } from "../lib/api-invitations"
import { setWorkspaceId } from "../lib/workspace"

export function InviteAcceptPage({ token }: { token: string }) {
  const infoQ = useInviteInfo(token)
  const acceptMut = useAcceptInvite()
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [errMsg, setErrMsg] = useState<string | null>(null)

  if (infoQ.isLoading) {
    return (
      <main className="grid min-h-screen place-items-center bg-surface">
        <p className="text-sm text-fg-subtle">Loading invitation...</p>
      </main>
    )
  }

  if (infoQ.isError || !infoQ.data) {
    return (
      <main className="grid min-h-screen place-items-center bg-surface">
        <div className="w-full max-w-sm space-y-3 rounded-lg border border-line p-6">
          <h1 className="text-base font-semibold text-fg">Invalid Invitation</h1>
          <p className="text-sm text-fg-subtle">
            This invitation link is invalid, expired, or has already been used.
          </p>
        </div>
      </main>
    )
  }

  const { workspace_name, email, role } = infoQ.data

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setErrMsg(null)
    if (password !== confirm) {
      setErrMsg("Passwords do not match")
      return
    }
    if (password.length < 8) {
      setErrMsg("Password must be at least 8 characters")
      return
    }
    try {
      const res = await acceptMut.mutateAsync({ token, password })
      setWorkspaceId(res.workspace_id)
      window.location.assign("/")
    } catch (err) {
      setErrMsg(err instanceof Error ? err.message : "Failed to accept invitation")
    }
  }

  return (
    <main className="grid min-h-screen place-items-center bg-surface">
      <div className="w-full max-w-sm space-y-4 rounded-lg border border-line p-6">
        <div className="space-y-1">
          <h1 className="text-base font-semibold text-fg">
            Join {workspace_name}
          </h1>
          <p className="text-sm text-fg-subtle">
            You've been invited as <span className="font-medium">{role}</span>.
            Set a password to activate your account.
          </p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-3">
          <div className="space-y-1">
            <label className="text-xs font-medium text-fg-subtle">Email</label>
            <input
              type="email"
              value={email}
              readOnly
              className="w-full rounded-md border border-line bg-surface-subtle px-3 py-2 text-sm text-fg-subtle"
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs font-medium text-fg-subtle">Password</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="Set your password"
              required
              autoFocus
              autoComplete="new-password"
              className="w-full rounded-md border border-line bg-surface px-3 py-2 text-sm text-fg placeholder:text-fg-faint focus:border-line-strong focus:outline-none focus:ring-1 focus:ring-slate-200"
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs font-medium text-fg-subtle">Confirm Password</label>
            <input
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              placeholder="Confirm password"
              required
              autoComplete="new-password"
              className="w-full rounded-md border border-line bg-surface px-3 py-2 text-sm text-fg placeholder:text-fg-faint focus:border-line-strong focus:outline-none focus:ring-1 focus:ring-slate-200"
            />
          </div>

          {errMsg && (
            <p className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-xs text-danger-emphasis">
              {errMsg}
            </p>
          )}

          <button
            type="submit"
            disabled={acceptMut.isPending}
            className="w-full rounded-md bg-fg px-3 py-2 text-sm font-medium text-bg hover:bg-fg/90 disabled:opacity-50"
          >
            {acceptMut.isPending ? "Joining..." : "Set Password & Join"}
          </button>
        </form>
      </div>
    </main>
  )
}
