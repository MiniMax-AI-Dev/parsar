/**
 * ParsarThread — Claude-style thread layout based on assistant-ui's
 * official component patterns.
 *
 * - User messages: right-aligned, muted rounded bubble
 * - Assistant messages: full-width markdown, chain-of-thought grouping
 * - Composer: bottom-fixed rounded card with Send/Stop toggle
 * - Auto-scroll with floating scroll-to-bottom button
 */

import type { FC } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, ArrowDown, ChevronDown, ChevronRight, Loader2, Wrench } from "lucide-react"
import { useState } from "react"

import {
  ThreadPrimitive,
  MessagePrimitive,
  ComposerPrimitive,
  AuiIf,
  groupPartByType,
  useMessagePartText,
  useMessagePartReasoning,
  useAuiState,
  type ToolCallMessagePartProps,
} from "@assistant-ui/react"
import { MarkdownTextPrimitive } from "@assistant-ui/react-markdown"

import { ParsarToolCallCard } from "./ParsarToolCallCard"
import { credentialKindLabel } from "../../lib/credential-kind-ui"
import { cn } from "../../lib/utils"

// ---------------------------------------------------------------------------
// Thread — full layout including messages + composer
// ---------------------------------------------------------------------------

export function ParsarThread({
  onStop,
  cancelling,
  disabled,
  agentName,
  conversationId,
}: {
  onStop?: () => void
  cancelling?: boolean
  disabled?: boolean
  agentName?: string
  conversationId?: string
}) {
  const { t } = useTranslation("admin")

  return (
    <ThreadPrimitive.Root
      className="flex h-full flex-col"
      style={{
        ["--thread-max-width" as string]: "48rem",
      }}
    >
      <ThreadPrimitive.Viewport className="relative flex flex-1 flex-col overflow-y-auto scroll-smooth">
        <div className="mx-auto flex w-full max-w-[var(--thread-max-width)] flex-1 flex-col px-4 pt-6">
          {/* Empty state */}
          <AuiIf condition={(s) => s.thread.isEmpty}>
            <div className="flex flex-1 items-center justify-center">
              <p className="text-sm text-fg-faint">
                {t("conversations.detail.emptyTimeline")}
              </p>
            </div>
          </AuiIf>

          {/* Messages */}
          <div className="mb-8 flex flex-col gap-y-6 empty:hidden">
            <ThreadPrimitive.Messages>
              {() => <ThreadMessage />}
            </ThreadPrimitive.Messages>

            {/* Thinking indicator — shown when running */}
            <AuiIf condition={(s) => s.thread.isRunning}>
              <div className="text-sm text-fg-faint font-medium">
                Thinking
              </div>
            </AuiIf>
          </div>

          {/* Footer: scroll-to-bottom + composer pinned at bottom */}
          <ThreadPrimitive.ViewportFooter className="sticky bottom-0 mt-auto flex flex-col gap-3 rounded-t-2xl bg-surface pb-4">
            <ThreadScrollToBottom />
            <ParsarComposerInline
              onStop={onStop}
              cancelling={cancelling}
              disabled={disabled}
              agentName={agentName}
              conversationId={conversationId}
            />
          </ThreadPrimitive.ViewportFooter>
        </div>
      </ThreadPrimitive.Viewport>
    </ThreadPrimitive.Root>
  )
}

// ---------------------------------------------------------------------------
// Message router
// ---------------------------------------------------------------------------

const ThreadMessage: FC = () => {
  const role = useAuiState((s) => s.message.role)
  if (role === "user") return <UserMessage />
  return <AssistantMessage />
}

// ---------------------------------------------------------------------------
// Scroll to bottom
// ---------------------------------------------------------------------------

const ThreadScrollToBottom: FC = () => {
  return (
    <ThreadPrimitive.ScrollToBottom asChild>
      <button
        type="button"
        className="absolute -top-10 z-10 self-center rounded-full border border-line bg-surface p-2 shadow-md transition-colors hover:bg-surface-subtle disabled:invisible"
      >
        <ArrowDown className="h-4 w-4 text-fg-subtle" />
      </button>
    </ThreadPrimitive.ScrollToBottom>
  )
}

// ---------------------------------------------------------------------------
// User Message — right-aligned muted bubble (Claude style)
// ---------------------------------------------------------------------------

const UserMessage: FC = () => {
  return (
    <MessagePrimitive.Root className="flex justify-end">
      <div className="max-w-[85%] rounded-2xl bg-surface-muted px-4 py-2.5 text-fg">
        <MessagePrimitive.Content />
      </div>
    </MessagePrimitive.Root>
  )
}

// ---------------------------------------------------------------------------
// Assistant Message — full-width, chain-of-thought grouped
// NOTE: "View run →" link for failed runs is not yet ported.
// ---------------------------------------------------------------------------

const AssistantMessage: FC = () => {
  return (
    <MessagePrimitive.Root className="relative">
      <div className="text-fg leading-relaxed">
        <MessagePrimitive.GroupedParts
          groupBy={groupPartByType({
            reasoning: ["group-chainOfThought", "group-reasoning"],
            "tool-call": ["group-chainOfThought", "group-tool"],
          })}
        >
          {({ part, children }) => {
            switch (part.type) {
              case "group-chainOfThought":
                return <div className="my-2">{children}</div>
              case "group-reasoning": {
                const running = part.status.type === "running"
                return <ReasoningBlock running={running}>{children}</ReasoningBlock>
              }
              case "group-tool":
                if (part.indices.length === 1) return <>{children}</>
                return <ToolGroup count={part.indices.length} running={part.status.type === "running"}>{children}</ToolGroup>
              case "text":
                return <AssistantTextPart />
              case "reasoning":
                return <ReasoningText />
              case "tool-call":
                return part.toolUI ?? <AssistantToolCallPart {...(part as unknown as ToolCallMessagePartProps)} />
              default:
                return null
            }
          }}
        </MessagePrimitive.GroupedParts>
      </div>
    </MessagePrimitive.Root>
  )
}

// ---------------------------------------------------------------------------
// Reasoning (Thinking) — collapsible block
// ---------------------------------------------------------------------------

function ReasoningBlock({ running, children }: { running: boolean; children: React.ReactNode }) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(running)

  return (
    <div className="my-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1.5 text-sm text-fg-subtle transition-colors hover:text-fg-muted"
      >
        {running ? (
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
        ) : open ? (
          <ChevronDown className="h-3.5 w-3.5" />
        ) : (
          <ChevronRight className="h-3.5 w-3.5" />
        )}
        <span className="font-medium">
          {running ? t("conversations.thinking.active", { defaultValue: "Thinking" }) : t("conversations.thinking.done", { defaultValue: "Thought" })}
        </span>
      </button>
      {(open || running) && (
        <div className="mt-1 border-l-2 border-line/50 pl-3 text-sm text-fg-subtle leading-relaxed">
          {children}
        </div>
      )}
    </div>
  )
}

const ReasoningText: FC = () => {
  const { text } = useMessagePartReasoning()
  return <span className="whitespace-pre-wrap">{text}</span>
}

// ---------------------------------------------------------------------------
// Tool Group — collapsible container for multiple tool calls
// ---------------------------------------------------------------------------

function ToolGroup({ count, running, children }: { count: number; running: boolean; children: React.ReactNode }) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(running)

  return (
    <div className="my-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1.5 text-sm text-fg-subtle transition-colors hover:text-fg-muted"
      >
        <Wrench className="h-3.5 w-3.5" />
        {running ? (
          <Loader2 className="h-3 w-3 animate-spin" />
        ) : open ? (
          <ChevronDown className="h-3 w-3" />
        ) : (
          <ChevronRight className="h-3 w-3" />
        )}
        <span className="font-medium">
          {t("conversations.steps.totalLabel", { count, defaultValue: "{{count}} steps" })}
        </span>
      </button>
      {(open || running) && (
        <div className="mt-1 ml-5">{children}</div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Text Part — markdown with runtime_error detection
// ---------------------------------------------------------------------------

const AssistantTextPart: FC = () => {
  const { text } = useMessagePartText()

  // Detect runtime_error marker from the converter
  if (text.startsWith("\x00RUNTIME_ERROR\x00")) {
    const parts = text.split("\x00")
    const metaJson = parts[2] ?? "{}"
    const errorText = parts[3] ?? ""
    let metadata: Record<string, unknown> = {}
    try { metadata = JSON.parse(metaJson) } catch { /* ignore */ }
    return <RuntimeErrorCard text={errorText} metadata={metadata} />
  }

  return (
    <div className="prose prose-sm max-w-none prose-p:my-1.5 prose-pre:my-3 prose-pre:rounded-lg prose-pre:bg-surface-subtle prose-pre:border prose-pre:border-line/50 prose-code:text-fg-muted prose-code:before:content-none prose-code:after:content-none prose-a:text-info prose-a:no-underline hover:prose-a:underline prose-headings:text-fg prose-strong:text-fg">
      <MarkdownTextPrimitive />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Tool Call Part
// ---------------------------------------------------------------------------

const AssistantToolCallPart: FC<ToolCallMessagePartProps> = (props) => {
  return (
    <ParsarToolCallCard
      toolName={props.toolName}
      args={(props.args as Record<string, unknown>) ?? {}}
      result={props.result}
      status={props.status}
    />
  )
}

// ---------------------------------------------------------------------------
// Runtime Error Card
// ---------------------------------------------------------------------------

function RuntimeErrorCard({ text, metadata }: { text: string; metadata: Record<string, unknown> }) {
  const { t, i18n } = useTranslation("admin")
  const subKind = stringMeta(metadata, "sub_kind") || stringMeta(metadata, "payload.sub_kind") || ""
  const capabilityName = stringMeta(metadata, "capability_name") || t("conversations.runtime_error.fallbackCapability")
  const credentialKind = stringMeta(metadata, "credential_kind")
  const capabilityID = stringMeta(metadata, "capability_id")
  const kindLabel = credentialKindLabel(credentialKind, i18n.language, t("capabilities.credentials.none"))

  let message = text || t("conversations.runtime_error.generic")
  let action = ""
  let href = ""
  const current = `${window.location.pathname}${window.location.search}`

  switch (subKind) {
    case "capability_credential_missing":
      message = t("conversations.runtime_error.capability_credential_missing", { name: capabilityName, kind: kindLabel })
      action = t("conversations.runtime_error.addCredential")
      href = credentialKind ? `?profile=credentials&kind=${encodeURIComponent(credentialKind)}&returnTo=${encodeURIComponent(current)}` : ""
      break
    case "capability_credential_decrypt_failed":
      message = t("conversations.runtime_error.capability_credential_decrypt_failed", { name: capabilityName })
      break
    case "capability_credential_kind_mismatch":
      message = t("conversations.runtime_error.capability_credential_kind_mismatch", { name: capabilityName })
      action = t("conversations.runtime_error.resetCredential")
      href = credentialKind ? `?profile=credentials&kind=${encodeURIComponent(credentialKind)}&returnTo=${encodeURIComponent(current)}` : ""
      break
    case "capability_version_unavailable":
      message = t("conversations.runtime_error.capability_version_unavailable", { name: capabilityName })
      action = t("conversations.runtime_error.manageCapability")
      href = capabilityID ? `?admin=capabilities&id=${encodeURIComponent(capabilityID)}` : "?admin=capabilities"
      break
  }

  return (
    <div className="my-2 rounded-lg border border-danger-border bg-danger-subtle/50 p-4">
      <div className="mb-2 flex items-center gap-1.5 text-xs font-medium text-danger-emphasis">
        <AlertTriangle className="h-3.5 w-3.5" strokeWidth={2.25} />
        <span>{t("conversations.runtime_error.badge")}</span>
      </div>
      <p className="text-sm font-medium text-danger-emphasis">{message}</p>
      {href && action && (
        <a href={href} className="mt-2 inline-flex items-center rounded-md border border-danger-border bg-surface px-2.5 py-1 text-sm font-medium text-danger-emphasis transition-colors hover:bg-danger-subtle">
          {action}
        </a>
      )}
      <p className="mt-2 text-xs text-danger-emphasis/70">{t("conversations.runtime_error.retryHint")}</p>
    </div>
  )
}

function stringMeta(metadata: Record<string, unknown> | undefined, key: string): string {
  if (!metadata) return ""
  const value = key.includes(".")
    ? key.split(".").reduce<unknown>((acc, part) => acc && typeof acc === "object" ? (acc as Record<string, unknown>)[part] : undefined, metadata)
    : metadata[key]
  return typeof value === "string" ? value : ""
}

// ---------------------------------------------------------------------------
// Inline Composer (inside the Thread viewport footer)
// ---------------------------------------------------------------------------

function ParsarComposerInline({
  onStop,
  cancelling,
  disabled,
  agentName,
}: {
  onStop?: () => void
  cancelling?: boolean
  disabled?: boolean
  agentName?: string
  conversationId?: string
}) {
  const { t } = useTranslation("admin")
  const composerText = useAuiState((s) => s.composer?.text ?? "")
  const showStop = !!onStop && composerText.trim().length === 0

  return (
    <ComposerPrimitive.Root
      className={cn(
        "flex w-full flex-col rounded-2xl border border-line bg-surface p-2.5 shadow-sm transition-shadow",
        "focus-within:border-line-strong focus-within:shadow-md",
        disabled && "opacity-60",
      )}
    >
      <ComposerPrimitive.Input
        autoFocus
        disabled={disabled}
        placeholder={
          agentName
            ? t("conversations.composer.placeholder", { agent: agentName })
            : t("conversations.composer.placeholderGeneric", { defaultValue: "Send a message..." })
        }
        className="min-h-[28px] max-h-[200px] w-full resize-none bg-transparent px-2 py-1 text-sm leading-relaxed text-fg outline-none placeholder:text-fg-faint disabled:cursor-not-allowed"
        rows={1}
      />
      <div className="flex items-center justify-end pt-1">
        {showStop ? (
          <button
            type="button"
            onClick={onStop}
            disabled={cancelling}
            aria-label="Stop"
            className={cn(
              "flex h-8 w-8 items-center justify-center rounded-full bg-surface-inverse text-white transition-colors hover:opacity-80",
              cancelling && "opacity-60",
            )}
          >
            {cancelling ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <span className="block h-3 w-3 rounded-sm bg-white" />
            )}
          </button>
        ) : (
          <AuiIf condition={(s) => !s.thread.isRunning}>
            <ComposerPrimitive.Send
              disabled={disabled}
              className="flex h-8 w-8 items-center justify-center rounded-full bg-surface-inverse text-white transition-colors hover:opacity-80 disabled:bg-surface-muted disabled:text-fg-faint"
              aria-label="Send"
            >
              <ArrowDown className="h-4 w-4 rotate-180" strokeWidth={2.5} />
            </ComposerPrimitive.Send>
          </AuiIf>
        )}
      </div>
    </ComposerPrimitive.Root>
  )
}
