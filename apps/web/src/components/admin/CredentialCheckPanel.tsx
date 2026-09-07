import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { ChevronDown, ChevronRight, Eye, EyeOff, ExternalLink, Loader2, Users } from "lucide-react"

import { Badge } from "../ui/badge"
import { Button } from "../ui/button"
import { Input } from "../ui/input"
import { Field } from "../ui/label"
import { StatusIcon } from "../ui/status-icon"
import { CredentialBindingSelect } from "./CredentialBindingSelect"
import { useMyCredentials } from "../../lib/api-credentials"
import {
  credentialKindLabel,
  useCredentialKindOptions,
  CREDENTIAL_KIND_META,
  type KnownCredentialKind,
} from "../../lib/credential-kind-ui"
import type { AgentInlineNewSecret, RequiredCredential, Secret } from "../../lib/api-types"
import { hasCredentialKind, sharedSecretsForKind, type PerKindBindingChoice } from "../../lib/credential-bindings"
import { cn } from "../../lib/utils"

export type { PerKindBindingChoice } from "../../lib/credential-bindings"

interface CredentialCheckPanelProps {
  /** Only entries with required===true should be passed. */
  requiredKinds: RequiredCredential[]
  workspaceID: string | null
  /** Workspace-wide capability_inline secrets the user may pick. */
  sharedSecrets: Secret[]
  /** Current agent visibility — gates whether `personal` is allowed. */
  visibility: "workspace" | "tenant" | "public"
  /**
   * Already-configured bindings to hydrate the panel with on first render
   * (edit mode). Each entry is { source:"shared", secret_id } recovered
   * from agent_config.credential_bindings. Personal and inline-new aren't
   * recoverable: personal is the implicit default, inline-new is ephemeral.
   */
  initialBindings?: Record<string, { source: "shared"; secret_id: string }>
  /** Fires whenever the per-kind choices change. */
  onChange: (
    bindings: Record<string, { source: "shared"; secret_id: string }>,
    inlineNewSecrets: AgentInlineNewSecret[],
    valid: boolean,
  ) => void
}

/**
 * CredentialCheckPanel lets the agent creator pick, for each required
 * credential kind, one of three sources:
 *
 *  1. personal — every caller uses their own user_credentials (default)
 *  2. shared (existing) — bind to a workspace capability_inline secret
 *  3. shared (new) — paste a token; the server creates the secret + binds
 *
 * Public agents reject choice 1 (lark guests have no platform identity).
 */
export function CredentialCheckPanel({
  requiredKinds,
  workspaceID,
  sharedSecrets,
  visibility,
  initialBindings,
  onChange,
}: CredentialCheckPanelProps) {
  const { t, i18n } = useTranslation("admin")
  const language = i18n.language
  const credentialsQ = useMyCredentials()
  const { byCode } = useCredentialKindOptions(workspaceID)
  // `data?.credentials ?? []` would mint a new array every render and re-fire
  // the propagate-up effect; keep the original reference and default at use.
  const credentials = credentialsQ.data?.credentials

  /** kind -> chosen source descriptor. Initialised to personal (or shared
   * for public agents, deferred until the user picks). */
  const [choices, setChoices] = useState<Record<string, PerKindBindingChoice>>({})
  const [expandedNewSecretFor, setExpandedNewSecretFor] = useState<string | null>(null)
  const [newSecretDisplayName, setNewSecretDisplayName] = useState("")
  const [newSecretPlaintext, setNewSecretPlaintext] = useState("")
  const [showPlaintext, setShowPlaintext] = useState(false)

  // Parent passes a fresh inline arrow every render; subscribing to it from
  // the propagate-up effect deps was an infinite loop (setState → re-render →
  // new onChange → effect → setState). Route via a ref so we always call the
  // latest callback without depending on its identity.
  const onChangeRef = useRef(onChange)
  useEffect(() => {
    onChangeRef.current = onChange
  })

  // Track which initialBindings payload we've already hydrated from. Parents
  // pass a fresh object identity each render; we want to apply edits ONCE
  // (when the dialog opens with an agent) and never again — otherwise typing
  // in another field would re-render the parent and clobber user picks here.
  // Sig key includes sharedSecrets length so we wait for secrets to load
  // before falling back to personal when secret_id can't be resolved.
  const hydratedSigRef = useRef<string | null>(null)

  // Reset choices when the set of required kinds, the visibility, or the
  // workspace changes. Public agents force unset (user must pick shared).
  // initialBindings (edit mode) wins over the personal default the first
  // time we see its payload.
  useEffect(() => {
    const hydrationSig = JSON.stringify({
      b: initialBindings ?? null,
      n: sharedSecrets.length,
    })
    const shouldHydrate = !!initialBindings && hydratedSigRef.current !== hydrationSig
    setChoices((prev) => {
      const next: Record<string, PerKindBindingChoice> = {}
      for (const rc of requiredKinds) {
        const existing = prev[rc.kind]
        const seed = shouldHydrate ? initialBindings![rc.kind] : undefined
        if (seed && sharedSecrets.some((s) => s.id === seed.secret_id)) {
          next[rc.kind] = { source: "shared", existing_secret_id: seed.secret_id }
          continue
        }
        if (existing) {
          // Drop stale personal choices when visibility flipped to public.
          if (visibility === "public" && existing.source === "personal") continue
          next[rc.kind] = existing
        } else if (visibility !== "public") {
          next[rc.kind] = { source: "personal" }
        }
      }
      // Bail out if next is structurally identical to prev. Without this,
      // a parent that passes a fresh initialBindings identity each render
      // would re-fire this effect and bounce back through onChange → loop.
      if (sameChoices(prev, next)) return prev
      return next
    })
    if (shouldHydrate) hydratedSigRef.current = hydrationSig
  }, [requiredKinds, visibility, workspaceID, initialBindings, sharedSecrets])

  // Propagate up. valid iff every required kind has a usable choice.
  useEffect(() => {
    const bindings: Record<string, { source: "shared"; secret_id: string }> = {}
    const inlineNew: AgentInlineNewSecret[] = []
    let valid = true
    for (const rc of requiredKinds) {
      const choice = choices[rc.kind]
      if (!choice) {
        valid = false
        continue
      }
      if (choice.source === "personal") {
        // No binding to emit; runtime falls back to user_credentials.
        // For public agents this is an invalid pick — caught here.
        if (visibility === "public") valid = false
        continue
      }
      if ("existing_secret_id" in choice) {
        bindings[rc.kind] = { source: "shared", secret_id: choice.existing_secret_id }
      } else {
        // Shared + new_secret. Empty plaintext means the radio was just
        // flipped (no kindSecrets to default to) and the inline form is
        // still being filled — keep the submit guard disabled until the
        // user saves a non-empty token.
        if (!choice.new_secret.plaintext.trim()) {
          valid = false
          continue
        }
        inlineNew.push({
          kind: rc.kind,
          display_name: choice.new_secret.display_name || undefined,
          plaintext: choice.new_secret.plaintext,
        })
      }
    }
    // For personal kinds, the creator's own user_credentials must exist
    // for the satisfied-row treatment. (Workspace/tenant only; public is
    // already invalidated above.)
    if (visibility !== "public") {
      for (const rc of requiredKinds) {
        const choice = choices[rc.kind]
        if (choice?.source !== "personal") continue
        if (!hasCredentialKind(credentials ?? [], rc.kind)) {
          // Personal but the creator has not configured this kind. Allow
          // the pick (other callers may have it), but signal invalid so
          // the create button stays disabled until they add it OR switch
          // to shared.
          valid = false
        }
      }
    }
    onChangeRef.current(bindings, inlineNew, valid && requiredKinds.length > 0 ? true : requiredKinds.length === 0)
  }, [choices, requiredKinds, visibility, credentials])

  function getKindMeta(kind: string) {
    const live = byCode.get(kind)
    const known = CREDENTIAL_KIND_META[kind as KnownCredentialKind]
    const displayName = live?.display_name || credentialKindLabel(kind, language, kind)
    const placeholder = known?.placeholder
      ? (language.startsWith("zh") ? known.placeholder.zh : known.placeholder.en)
      : ""
    const getUrl = known?.getUrl
    return { displayName, placeholder, getUrl }
  }

  function setKindChoice(kind: string, choice: PerKindBindingChoice) {
    setChoices((prev) => ({ ...prev, [kind]: choice }))
  }

  function commitNewSecret(kind: string) {
    if (!newSecretPlaintext.trim()) return
    setKindChoice(kind, {
      source: "shared",
      new_secret: {
        display_name: newSecretDisplayName.trim(),
        plaintext: newSecretPlaintext.trim(),
      },
    })
    setExpandedNewSecretFor(null)
    setNewSecretDisplayName("")
    setNewSecretPlaintext("")
    setShowPlaintext(false)
  }

  if (requiredKinds.length === 0) return null

  const personalDisabled = visibility === "public"
  const toggleLabel = showPlaintext ? t("myCredentials.dialog.hide") : t("myCredentials.dialog.show")

  return (
    <div className="space-y-3">
      {requiredKinds.map((rc) => {
        const { displayName, placeholder, getUrl } = getKindMeta(rc.kind)
        const choice = choices[rc.kind]
        const hasPersonalCredential = hasCredentialKind(credentials ?? [], rc.kind)
        const kindSecrets = sharedSecretsForKind(sharedSecrets, rc.kind)

        return (
          <section key={rc.kind} className="border-t border-line pt-2 first:border-t-0 first:pt-0">
            <h4 className="flex h-8 items-center gap-2 text-sm">
              <span className="font-medium text-fg">{displayName}</span>
              <span className="font-mono text-xs text-fg-muted">{rc.kind}</span>
              {rc.required && (
                <Badge variant="warning" dot className="ml-auto">
                  {t("credentialCheck.requiredBadge")}
                </Badge>
              )}
            </h4>

            <div className="space-y-0.5">
              {/* Option 1: personal */}
              <label
                className={cn(
                  "flex items-start gap-2 rounded-md px-2 py-1.5 text-sm",
                  personalDisabled ? "cursor-not-allowed opacity-50" : "cursor-pointer hover:app-hover",
                )}
              >
                <input
                  type="radio"
                  name={`cred-${rc.kind}`}
                  className="mt-1 accent-accent"
                  checked={choice?.source === "personal"}
                  disabled={personalDisabled}
                  onChange={() => setKindChoice(rc.kind, { source: "personal" })}
                />
                <span className="min-w-0 flex-1">
                  <span className="block text-fg">{t("credentialCheck.sourcePersonal")}</span>
                  <span className="mt-0.5 flex items-center gap-1.5 text-xs text-fg-muted">
                    {!personalDisabled && <StatusIcon status={hasPersonalCredential ? "completed" : "failed"} />}
                    {personalDisabled
                      ? t("credentialCheck.personalDisabledHint")
                      : hasPersonalCredential
                        ? t("credentialCheck.personalYouHaveIt")
                        : t("credentialCheck.personalYouMissing")}
                  </span>
                  {!personalDisabled && !hasPersonalCredential && getUrl && (
                    <a
                      href={getUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="mt-1 inline-flex items-center gap-1 text-xs text-fg underline underline-offset-4"
                    >
                      {t("credentialCheck.form.getToken")}
                      <ExternalLink className="h-3 w-3" strokeWidth={1.5} aria-hidden="true" />
                    </a>
                  )}
                </span>
              </label>

              {/* Option 2: shared (combines existing-secret picker + new-secret form) */}
              <label className="flex cursor-pointer items-start gap-2 rounded-md px-2 py-1.5 text-sm hover:app-hover">
                <input
                  type="radio"
                  name={`cred-${rc.kind}`}
                  className="mt-1 accent-accent"
                  checked={choice?.source === "shared"}
                  onChange={() => {
                    if (kindSecrets[0]) {
                      setKindChoice(rc.kind, { source: "shared", existing_secret_id: kindSecrets[0].id })
                      setExpandedNewSecretFor(null)
                    } else {
                      // No existing shared secret to bind: still flip the
                      // choice to shared so the radio reads as selected,
                      // and open the inline new-secret form. The empty
                      // plaintext keeps the submit guard `valid=false`
                      // (see propagate-up effect below) until the user
                      // pastes a token + saves.
                      setKindChoice(rc.kind, {
                        source: "shared",
                        new_secret: { display_name: "", plaintext: "" },
                      })
                      setExpandedNewSecretFor(rc.kind)
                      setNewSecretDisplayName("")
                      setNewSecretPlaintext("")
                    }
                  }}
                />
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-1.5 text-fg">
                    <Users className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
                    {t("credentialCheck.sourceShared")}
                  </span>
                  {choice?.source === "shared" && (
                    <div className="mt-1.5 space-y-1.5">
                      {kindSecrets.length > 0 && (
                        <CredentialBindingSelect
                          value={"existing_secret_id" in choice ? choice.existing_secret_id : "__new__"}
                          secrets={kindSecrets}
                          allowPersonal={false}
                          allowCreateNew
                          personalLabel={t("credentialCheck.sourcePersonal")}
                          personalPlaceholder={t("credentialCheck.sharedPlaceholder")}
                          sharedLabel={t("credentialCheck.sourceShared")}
                          createNewLabel={t("credentialCheck.createNewShared")}
                          onChange={(value) => {
                            if (value === "__new__") {
                              setExpandedNewSecretFor(rc.kind)
                              if (!("new_secret" in choice)) {
                                setNewSecretDisplayName("")
                                setNewSecretPlaintext("")
                              }
                            } else {
                              setKindChoice(rc.kind, { source: "shared", existing_secret_id: value })
                              if (expandedNewSecretFor === rc.kind) setExpandedNewSecretFor(null)
                            }
                          }}
                        />
                      )}
                      {kindSecrets.length === 0 && expandedNewSecretFor !== rc.kind && !("new_secret" in choice) && (
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={(e) => {
                            e.preventDefault()
                            e.stopPropagation()
                            setExpandedNewSecretFor(rc.kind)
                            setNewSecretDisplayName("")
                            setNewSecretPlaintext("")
                          }}
                        >
                          {t("credentialCheck.createNewShared")}
                        </Button>
                      )}
                      {"new_secret" in choice && expandedNewSecretFor !== rc.kind && (
                        <div className="flex items-center gap-1.5 text-xs text-fg">
                          <StatusIcon status="completed" />
                          <span className="min-w-0 flex-1 truncate">
                            {t("credentialCheck.sharedNewQueued", { name: choice.new_secret.display_name || rc.kind })}
                          </span>
                          <Button
                            type="button"
                            variant="link"
                            size="sm"
                            className="h-auto p-0 text-xs"
                            onClick={(e) => {
                              e.preventDefault()
                              e.stopPropagation()
                              setExpandedNewSecretFor(rc.kind)
                              setNewSecretDisplayName(choice.new_secret.display_name)
                              setNewSecretPlaintext(choice.new_secret.plaintext)
                            }}
                          >
                            {t("credentialCheck.sharedNewEdit")}
                          </Button>
                        </div>
                      )}
                    </div>
                  )}
                </span>
              </label>

              {expandedNewSecretFor === rc.kind && (
                <div className="ml-6 space-y-3 border-t border-line pt-3" onClick={(e) => e.stopPropagation()}>
                  <Field label={t("credentialCheck.form.displayName")}>
                    <Input
                      value={newSecretDisplayName}
                      onChange={(e) => setNewSecretDisplayName(e.target.value)}
                      placeholder={displayName}
                    />
                  </Field>
                  <Field label={t("credentialCheck.form.value")}>
                    <div className="relative">
                      <Input
                        type={showPlaintext ? "text" : "password"}
                        value={newSecretPlaintext}
                        onChange={(e) => setNewSecretPlaintext(e.target.value)}
                        placeholder={placeholder}
                        className="pr-8"
                        required
                      />
                      <button
                        type="button"
                        aria-label={toggleLabel}
                        title={toggleLabel}
                        onClick={() => setShowPlaintext(!showPlaintext)}
                        className="absolute right-1 top-1/2 inline-flex h-5 w-5 -translate-y-1/2 items-center justify-center rounded text-fg-muted hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
                      >
                        {showPlaintext ? <EyeOff className="h-3.5 w-3.5" strokeWidth={1.5} /> : <Eye className="h-3.5 w-3.5" strokeWidth={1.5} />}
                      </button>
                    </div>
                  </Field>
                  <div className="flex justify-end gap-2 border-t border-line pt-3">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        setExpandedNewSecretFor(null)
                        // Same fallback as the model binding cancel: if the user cancels and no
                        // new_secret has been committed (treat empty plaintext as uncommitted —
                        // we now seed an empty new_secret on radio flip so the radio reads as
                        // selected, but cancel here must still snap back), snap back to an
                        // existing secret or to personal (when allowed) so the shared radio
                        // isn't selected with nothing inside.
                        const current = choices[rc.kind]
                        const uncommitted =
                          !current ||
                          current.source !== "shared" ||
                          !("new_secret" in current) ||
                          !current.new_secret.plaintext.trim()
                        if (uncommitted) {
                          if (kindSecrets[0]) {
                            setKindChoice(rc.kind, { source: "shared", existing_secret_id: kindSecrets[0].id })
                          } else if (!personalDisabled) {
                            setKindChoice(rc.kind, { source: "personal" })
                          }
                        }
                      }}
                    >
                      {t("credentialCheck.form.cancel")}
                    </Button>
                    <Button
                      type="button"
                      size="sm"
                      disabled={!newSecretPlaintext.trim()}
                      onClick={() => commitNewSecret(rc.kind)}
                    >
                      {t("credentialCheck.form.save")}
                    </Button>
                  </div>
                </div>
              )}
            </div>
          </section>
        )
      })}
    </div>
  )
}

// Re-export icons used by callers' fallback rendering (so external code
// doesn't need to import lucide directly).
export { ChevronDown, ChevronRight, Loader2 }

// sameChoices does a structural equality check on the choices map so the
// reset effect can return prev when nothing meaningful changed, letting
// React skip the re-render and breaking any accidental feedback loop.
function sameChoices(
  a: Record<string, PerKindBindingChoice>,
  b: Record<string, PerKindBindingChoice>,
): boolean {
  const aKeys = Object.keys(a)
  const bKeys = Object.keys(b)
  if (aKeys.length !== bKeys.length) return false
  for (const k of aKeys) {
    const av = a[k]
    const bv = b[k]
    if (!bv || av.source !== bv.source) return false
    if (av.source === "shared") {
      const ax = "existing_secret_id" in av ? av.existing_secret_id : null
      const bx = bv.source === "shared" && "existing_secret_id" in bv ? bv.existing_secret_id : null
      if (ax !== bx) return false
      const an = "new_secret" in av ? av.new_secret : null
      const bn = bv.source === "shared" && "new_secret" in bv ? bv.new_secret : null
      if ((an === null) !== (bn === null)) return false
      if (an && bn && (an.display_name !== bn.display_name || an.plaintext !== bn.plaintext)) return false
    }
  }
  return true
}
