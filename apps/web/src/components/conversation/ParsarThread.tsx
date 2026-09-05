/**
 * ParsarThread — the assistant-ui thread on the console's type system.
 *
 * - User messages: right-aligned, paper-muted block, 6px radius
 * - Assistant messages: full-width markdown in ink, chain-of-thought grouping
 * - Composer: hairline-topped footer with a Textarea-styled input and one
 *   primary Send button (Stop while a run is in flight)
 * - Auto-scroll with a scroll-to-bottom button
 */

import type { FC } from "react"
import { useTranslation } from "react-i18next"
import { AlertTriangle, ArrowDown, ChevronDown, ChevronRight, Loader2, Send, Square, Wrench } from "lucide-react"
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
import { Button } from "../ui/button"
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
              <p className="m-0 text-sm text-fg-muted">{t("conversations.detail.emptyTimeline")}</p>
            </div>
          </AuiIf>

          {/* Messages */}
          <div className="mb-6 flex flex-col gap-y-5 empty:hidden">
            <ThreadPrimitive.Messages>
              {() => <ThreadMessage />}
            </ThreadPrimitive.Messages>

            {/* Thinking indicator — shown when running */}
            <AuiIf condition={(s) => s.thread.isRunning}>
              <p className="m-0 flex items-center gap-2 text-sm text-fg" role="status" aria-live="polite">
                <Loader2 className="h-3.5 w-3.5 animate-spin text-fg-muted motion-reduce:animate-none" strokeWidth={1.5} aria-hidden="true" />
                {t("conversations.stream.thinking")}
              </p>
            </AuiIf>
          </div>

          {/* Footer: scroll-to-bottom + composer pinned at bottom */}
          <ThreadPrimitive.ViewportFooter className="sticky bottom-0 mt-auto flex flex-col border-t border-line bg-surface py-3">
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
      <Button
        variant="outline"
        size="icon"
        shape="circle"
        className="absolute -top-10 z-10 self-center disabled:invisible"
        aria-label="Scroll to bottom"
      >
        <ArrowDown strokeWidth={1.5} aria-hidden="true" />
      </Button>
    </ThreadPrimitive.ScrollToBottom>
  )
}

// ---------------------------------------------------------------------------
// User Message — right-aligned, paper-muted block
// ---------------------------------------------------------------------------

const UserMessage: FC = () => {
  return (
    <MessagePrimitive.Root className="flex justify-end">
      <div className="max-w-[85%] rounded-md bg-surface-muted px-3 py-2 text-base text-fg">
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
      <div className="text-base text-fg">
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
                if (part.indices.length === 1) return <div className="border-t border-line">{children}</div>
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
      <Button variant="ghost" size="sm" className="-ml-2" aria-expanded={open || running} onClick={() => setOpen((v) => !v)}>
        {running ? (
          <Loader2 className="animate-spin motion-reduce:animate-none" strokeWidth={1.5} aria-hidden="true" />
        ) : open ? (
          <ChevronDown strokeWidth={1.5} aria-hidden="true" />
        ) : (
          <ChevronRight strokeWidth={1.5} aria-hidden="true" />
        )}
        {running ? t("conversations.thinking.active", { defaultValue: "Thinking" }) : t("conversations.thinking.done", { defaultValue: "Thought" })}
      </Button>
      {(open || running) && (
        <div className="mt-1 border-l border-line pl-3 text-sm text-fg-muted">
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
      <Button variant="ghost" size="sm" className="-ml-2" aria-expanded={open || running} onClick={() => setOpen((v) => !v)}>
        <Wrench strokeWidth={1.5} aria-hidden="true" />
        {running ? (
          <Loader2 className="animate-spin motion-reduce:animate-none" strokeWidth={1.5} aria-hidden="true" />
        ) : open ? (
          <ChevronDown strokeWidth={1.5} aria-hidden="true" />
        ) : (
          <ChevronRight strokeWidth={1.5} aria-hidden="true" />
        )}
        {t("conversations.steps.totalLabel", { count, defaultValue: "{{count}} steps" })}
      </Button>
      {(open || running) && (
        <div className="mt-1 border-t border-line">{children}</div>
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
    <div className="prose prose-sm max-w-none text-fg prose-p:my-1.5 prose-pre:my-3 prose-pre:rounded-md prose-pre:bg-surface-muted prose-pre:text-fg prose-code:text-fg prose-code:before:content-none prose-code:after:content-none prose-a:text-fg prose-a:underline prose-a:underline-offset-4 prose-headings:text-fg prose-strong:text-fg prose-strong:font-medium">
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
// Runtime error — a failed-red triangle and ink text, no red box
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
    <div className="my-2 flex items-start gap-1.5 text-base text-fg">
      <AlertTriangle className="mt-1 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
      <div className="min-w-0">
        <p className="m-0 font-medium">{t("conversations.runtime_error.badge")}</p>
        <p className="m-0 mt-1 break-words">{message}</p>
        {href && action && (
          <Button asChild variant="outline" size="sm" className="mt-2">
            <a href={href}>{action}</a>
          </Button>
        )}
        <p className="m-0 mt-2 text-xs text-fg-muted">{t("conversations.runtime_error.retryHint")}</p>
      </div>
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
    <ComposerPrimitive.Root className="flex w-full flex-col gap-2">
      {/* assistant-ui owns this textarea, so it mirrors ui/textarea.tsx
          rather than wrapping it. Keep the two in step. */}
      <ComposerPrimitive.Input
        autoFocus
        disabled={disabled}
        placeholder={
          agentName
            ? t("conversations.composer.placeholder", { agent: agentName })
            : t("conversations.composer.placeholderGeneric", { defaultValue: "Send a message..." })
        }
        aria-label={t("conversations.composer.label")}
        className={cn(
          "app-shadow-control flex max-h-[200px] min-h-[56px] w-full resize-none rounded-md border border-line-strong bg-surface px-2 py-1.5 text-sm leading-relaxed text-fg transition-[border-color,box-shadow] duration-150 ease-settle placeholder:text-fg-muted focus-visible:border-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent disabled:cursor-not-allowed disabled:bg-surface-muted disabled:opacity-60",
        )}
        rows={2}
      />
      <div className="flex items-center justify-end gap-2">
        {showStop ? (
          <Button type="button" variant="outline" onClick={onStop} disabled={cancelling}>
            {cancelling ? (
              <Loader2 className="animate-spin" aria-hidden="true" />
            ) : (
              <Square className="fill-current" strokeWidth={1.5} aria-hidden="true" />
            )}
            {t("conversations.composer.stopAria")}
          </Button>
        ) : (
          <AuiIf condition={(s) => !s.thread.isRunning}>
            <ComposerPrimitive.Send asChild>
              <Button type="submit" disabled={disabled}>
                <Send strokeWidth={1.5} aria-hidden="true" />
                {t("conversations.composer.send")}
              </Button>
            </ComposerPrimitive.Send>
          </AuiIf>
        )}
      </div>
    </ComposerPrimitive.Root>
  )
}
