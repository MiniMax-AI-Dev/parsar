import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import { MessageSquareOff } from "lucide-react"

import { ConversationMain } from "../components/conversation/ConversationThread"
import { BrandMark } from "../components/ui/brand-mark"
import { EmptyState } from "../components/ui/empty-state"
import { InitialTile } from "../components/ui/ledger"
import { Skeleton } from "../components/ui/skeleton"
import { ThemeMenu } from "../components/layout/ThemeMenu"
import { agentNeedsSandbox } from "../lib/agent-runtime"
import { useAgents } from "../lib/api-agents"
import { useConversation } from "../lib/api-conversations"
import { useSandboxBinding } from "../lib/api-sandbox"
import { useMyWorkspaces } from "../lib/api-workspaces"
import { sandboxSendGuard } from "../lib/sandbox-send-guard"

/**
 * A conversation on its own, at `/c/<id>`.
 *
 * One link is one conversation: everyone who opens it lands in the same
 * thread, and that thread is the same row the console lists. The link is an
 * address, not a ticket — who may read it is decided by signing in and being
 * in the workspace, so there is no second permission to keep in step.
 *
 * The shell is the difference. No sidebar, no ledgers, no admin controls: a
 * colleague opens this to ask the agent something, not to administer it. Same
 * world — same greys, same accent, same type — with the console's density
 * relaxed, because this page is read once in a while rather than watched all
 * day.
 */
export function SharedConversationPage({ conversationId }: { conversationId: string }) {
  const { t } = useTranslation("admin")
  const convQ = useConversation(conversationId)
  const conv = convQ.data
  const workspacesQ = useMyWorkspaces()
  const agentsQ = useAgents(conv?.workspace_id ?? null)

  const agent = useMemo(
    () => (agentsQ.data?.agents ?? []).find((a) => a.id === conv?.primary_agent_id),
    [agentsQ.data, conv?.primary_agent_id],
  )
  const workspace = workspacesQ.data?.workspaces.find((w) => w.id === conv?.workspace_id)
  // Allowlist rather than "not viewer", so a role added later cannot fail open.
  const canWrite = workspace?.role === "owner" || workspace?.role === "admin" || workspace?.role === "member"
  // A sandbox agent with no live binding cannot serve a prompt; without this
  // the composer would take a send that fails with no explanation.
  const sandboxQ = useSandboxBinding(
    conv?.workspace_id ?? null,
    agentNeedsSandbox(agent) ? (agent?.id ?? null) : null,
  )
  const sandboxGuard = sandboxSendGuard(t, agent, sandboxQ.data, sandboxQ.isLoading, sandboxQ.error)

  if (convQ.isLoading || workspacesQ.isLoading || agentsQ.isLoading) {
    return (
      <SharedShell>
        <div className="mx-auto w-full max-w-[48rem] px-6 pt-10">
          <Skeleton className="h-5 w-48" />
          <Skeleton className="mt-3 h-3 w-full max-w-md" />
        </div>
      </SharedShell>
    )
  }

  // A conversation in another workspace answers 404 the same way a deleted one
  // does. Either way the reader needs to know it is about permission, not a
  // typo in the link they were sent.
  if (convQ.error || !conv || !workspace) {
    return (
      <SharedShell>
        <div className="grid flex-1 place-items-center px-6">
          <EmptyState
            icon={MessageSquareOff}
            title={t("shared.noAccess.title")}
            description={t("shared.noAccess.description")}
          />
        </div>
      </SharedShell>
    )
  }

  const headerAgent = agent ?? (conv.primary_agent_name ? {
    name: conv.primary_agent_name,
    description: conv.primary_agent_deleted ? t("conversations.composer.agentDeleted") : "",
  } : undefined)

  return (
    <SharedShell agent={headerAgent}>
      <ConversationMain
        conv={conv}
        canWrite={canWrite}
        convLoading={false}
        convError={undefined}
        agent={agent}
        conversationId={conversationId}
        chrome="bare"
        messageCount={1}
        folded
        onExpand={() => {}}
        onSendFromEmpty={async () => false}
        onRenameAfterFirstMessage={async () => {}}
        sandboxGuard={sandboxGuard}
      />
    </SharedShell>
  )
}

/**
 * The page's own chrome: a 56px bar carrying the mark, who you are talking to,
 * and the theme toggle. Everything else on screen is the conversation.
 */
function SharedShell({ agent, children }: {
  agent?: { name: string; description: string }
  children: React.ReactNode
}) {
  return (
    <div className="flex h-screen flex-col bg-surface">
      <header className="flex h-14 shrink-0 items-center gap-3 border-b border-line px-6">
        <BrandMark size={18} />
        {agent && (
          <>
            <span className="h-3.5 w-px bg-line" aria-hidden="true" />
            <InitialTile name={agent.name} />
            <span className="min-w-0 truncate text-sm font-medium text-fg">{agent.name}</span>
            {agent.description && (
              <span className="min-w-0 flex-1 truncate text-xs text-fg-muted">{agent.description}</span>
            )}
          </>
        )}
        <div className="ml-auto shrink-0">
          <ThemeMenu />
        </div>
      </header>
      <main className="flex min-h-0 flex-1 flex-col">{children}</main>
    </div>
  )
}
