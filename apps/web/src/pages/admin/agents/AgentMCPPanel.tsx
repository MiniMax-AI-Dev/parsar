import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Copy } from "lucide-react"

import { Button } from "../../../components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "../../../components/ui/dialog"
import { ErrorState } from "../../../components/ui/error-state"
import { Skeleton } from "../../../components/ui/skeleton"
import { VerbatimBlock } from "../../../components/ui/verbatim"
import type { ShowToast } from "../../../components/ui/toast"
import { agentActionPermissions } from "../../../lib/agent-actions"
import { agentMCPEndpoint, createAgentMCPToken, revokeAgentMCPToken, useAgentMCPToken } from "../../../lib/api-agent-mcp"
import { useMyWorkspaces } from "../../../lib/api-workspaces"
import { useAuth } from "../../../lib/auth-context"
import { copyText } from "../../../lib/clipboard"
import { useTimeUntil } from "../../../lib/relative-time"

type Props = { agentID: string; workspaceID: string; onToast: ShowToast }

export function AgentMCPPanel(props: Props) {
  const { user } = useAuth()
  // A credential revealed to one login must not survive a user change.
  return <PersonalMCPPanel key={user?.user_id} {...props} userID={user?.user_id} />
}

function PersonalMCPPanel({ agentID, workspaceID, userID, onToast }: Props & { userID?: string }) {
  const { t } = useTranslation(["admin", "common"])
  const until = useTimeUntil()
  const workspaces = useMyWorkspaces()
  const role = workspaces.data?.workspaces.find((w) => w.id === workspaceID)?.role
  const { canChat } = agentActionPermissions(role)
  const query = useAgentMCPToken(workspaceID, agentID, userID, canChat)
  const [open, setOpen] = useState(false)
  const [token, setToken] = useState("")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<Error | null>(null)
  const credential = query.data?.credential
  const endpoint = new URL(agentMCPEndpoint(workspaceID, agentID), window.location.origin).href
  const config = (secret: string) => JSON.stringify({ mcpServers: { [`parsar-${agentID.slice(0, 8)}`]: { type: "http", url: endpoint, headers: { Authorization: `Bearer ${secret}` } } } }, null, 2)

  async function change(revoke: boolean) {
    if (pending) return
    setPending(true)
    setError(null)
    try {
      if (revoke) {
        await revokeAgentMCPToken(workspaceID, agentID)
        setToken("")
        onToast(t("agents.exposure.mcp.revoked"))
      } else {
        const response = await createAgentMCPToken(workspaceID, agentID)
        setToken(response.token)
      }
      await query.refetch()
    } catch (e) { setError(e instanceof Error ? e : new Error(String(e))) }
    finally { setPending(false) }
  }

  if (workspaces.isLoading) return <Skeleton className="h-8 w-full" />
  if (workspaces.error) return <ErrorState appearance="panel" title={t("agents.exposure.mcp.loadError")} onRetry={() => { void workspaces.refetch() }} />
  if (!canChat) return <p className="text-sm text-fg-muted">{t("agents.exposure.mcp.readOnly")}</p>

  return <div className="space-y-3">
    <p className="text-sm text-fg-muted">{t("agents.exposure.mcp.description")}</p>
    <Dialog open={open} onOpenChange={(value) => { if (!pending) { setOpen(value); setError(null); if (!value) setToken("") } }}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 text-xs text-fg-muted">{credential ? t("agents.exposure.mcp.expiry", { time: until(credential.expires_at) }) : t("agents.exposure.mcp.notConfigured")}</span>
        <DialogTrigger asChild><Button variant="outline">{t("agents.exposure.mcp.configure")}</Button></DialogTrigger>
      </div>
      <DialogContent className="w-[calc(100%-2rem)] max-w-xl max-h-[calc(100dvh-2rem)] overflow-y-auto overflow-x-hidden" onInteractOutside={(event) => { if (pending) event.preventDefault() }} onEscapeKeyDown={(event) => { if (pending) event.preventDefault() }}>
        <DialogHeader>
          <DialogTitle>{t("agents.exposure.mcp.configure")}</DialogTitle>
          <DialogDescription>{t("agents.exposure.mcp.personal")}</DialogDescription>
        </DialogHeader>
        {query.isLoading ? <Skeleton className="h-16 w-full" /> : query.error ? (
          <ErrorState appearance="panel" title={t("agents.exposure.mcp.loadError")} detail={query.error instanceof Error ? query.error.message : undefined} onRetry={() => { void query.refetch() }} />
        ) : token ? <div className="min-w-0 space-y-3">
          <p className="text-sm text-fg-muted">{t("agents.exposure.mcp.copyHint")}</p>
          <VerbatimBlock className="max-h-64 [overflow-wrap:anywhere]" tabIndex={0}>{config("<personal-token>")}</VerbatimBlock>
          <Button variant="outline" onClick={async () => {
            const copied = await copyText(config(token))
            onToast(t(copied ? "agents.exposure.mcp.copied" : "agents.exposure.mcp.copyFailed"), { tone: copied ? "success" : "error" })
          }}><Copy aria-hidden="true" />{t("agents.exposure.mcp.copy")}</Button>
        </div> : <div className="space-y-3">
          <p className="text-sm">{t(credential ? "agents.exposure.mcp.replaceHint" : "agents.exposure.mcp.createHint")}</p>
          {credential && <p className="text-xs text-fg-muted">{t("agents.exposure.mcp.expiry", { time: until(credential.expires_at) })}</p>}
        </div>}
        {error && <ErrorState appearance="panel" title={t("agents.exposure.mcp.actionFailed")} detail={error.message} />}
        <DialogFooter className="flex-wrap">
          <Button variant="outline" disabled={pending} onClick={() => { setOpen(false); setToken(""); setError(null) }}>{t("common:actions.close")}</Button>
          {!query.isLoading && !query.error && !token && <>
            {credential && <Button variant="outline" disabled={pending} onClick={() => { void change(true) }}>{t("agents.exposure.mcp.revoke")}</Button>}
            <Button disabled={pending} onClick={() => { void change(false) }}>{t(credential ? "agents.exposure.mcp.replace" : "agents.exposure.mcp.create")}</Button>
          </>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
}
