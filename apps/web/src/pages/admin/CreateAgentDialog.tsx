import { Fragment, forwardRef, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, Check, ChevronDown, Eye, EyeOff, Search } from "lucide-react"

import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import { DevicePicker } from "../../components/admin/DevicePicker"
import { PairDaemonDialog } from "../../components/admin/PairDaemonDialog"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import { Input } from "../../components/ui/input"
import { Label } from "../../components/ui/label"
import { Select, SelectOption } from "../../components/ui/select"
import { AgentInstructionsField } from "./agents/AgentInstructionsField"
import { AgentVisibilityField } from "./agents/AgentVisibilityField"
import { AgentCloudPreflight } from "./agents/AgentCloudPreflight"
import { AgentModelPrerequisite } from "./agents/AgentModelPrerequisite"
import { AgentCloneVersionPicker } from "./agents/AgentCloneVersionPicker"
import { AgentCloneNotice } from "./agents/AgentCloneNotice"
import { useAgentCloneCapabilities } from "./agents/useAgentCloneCapabilities"
import { useAgentCloneCredentials } from "./agents/useAgentCloneCredentials"
import { AgentCloneCredentials } from "./agents/AgentCloneCredentials"
import { AgentCloneCredentialRefresh } from "./agents/AgentCloneCredentialRefresh"
import { sharedSecretsForKind } from "../../lib/credential-bindings"
import { cloneMarketplaceCapabilities, withoutCredentialBindings } from "../../lib/agent-clone"
import { Tabs, TabsList, TabsTrigger } from "../../components/ui/tabs"
import { ApiError } from "../../lib/api-client"
import { cn } from "../../lib/utils"
import { agentCodexModeOf, type CodexCollaborationMode } from "../../lib/agent-view-model"
import {
  modelProtocols,
  modelSupportedEndpointTypes,
  protocolListLabel,
  type WireProtocol,
} from "../../lib/model-protocol"
import { useCapabilitiesQuery, aggregateRequiredCredentialsByID, useCapabilityVersionsQuery, useAgentCapabilitiesQuery } from "../../lib/api-capabilities"
import { CredentialCheckPanel } from "../../components/admin/CredentialCheckPanel"
import { useSecrets } from "../../lib/api-secrets"
import { useRuntimeStatus } from "../../lib/api-runtime"
import type { AgentVisibility, UpdateAgentProfileRequest } from "../../lib/api-agents"
import type {
  AgentInlineNewSecret,
  AgentRuntime,
  Capability,
  CapabilityType,
  CreateAgentRequest,
  Model,
  Agent,
  RequiredCredential,
  Secret,
  UpdateAgentRequest,
  UserWorkspace,
} from "../../lib/api-types"
import { StatusIcon } from "../../components/ui/status-icon"

const DEFAULT_WORK_DIR = "/workspace"

type ExecutionMode = "sandbox" | "local_device" | "external"
type AgentEngine = "claude_code" | "opencode" | "codex" | "pi"
type SandboxSize = "standard" | "xl"
type RuntimeChoice = AgentRuntime
type WizardStep = 1 | 2

function connectorForExecutionMode(mode: ExecutionMode): string {
  if (mode === "external") return "http"
  return "agent_daemon"
}

function executionModeFromAgent(a?: Agent | null): ExecutionMode {
  if (!a) return "local_device"
  if (a.connector_type === "http") return "external"
  if (a.connector_type === "agent_daemon") {
    return String(agentConfig(a).daemon_mode ?? "local") === "sandbox" ? "sandbox" : "local_device"
  }
  return runtimeFromAgent(a) === "local" ? "local_device" : "sandbox"
}

function agentEngineFromAgent(a?: Agent | null): AgentEngine {
  const v = String(agentConfig(a).agent_kind ?? "claude_code")
  if (v === "opencode") return "opencode"
  if (v === "codex") return "codex"
  if (v === "pi") return "pi"
  return "claude_code"
}

/** Which wire protocols an agent engine can drive. Mirrors the per-engine
 * injector gating in model_injection.go: claude_code→Anthropic only,
 * codex→OpenAI only, pi→any of the three, opencode→any adapter. */
function engineSupportsProtocol(engine: AgentEngine, protocol: WireProtocol | null): boolean {
  switch (engine) {
    case "claude_code":
      return protocol === "anthropic"
    case "codex":
      return protocol === "openai"
    case "pi":
      return protocol === "anthropic" || protocol === "openai" || protocol === "google"
    case "opencode":
      return true
  }
}

function engineSupportsModel(engine: AgentEngine, model: Model): boolean {
  const endpointTypes = modelSupportedEndpointTypes(model)
  if (endpointTypes.length > 0) {
    switch (engine) {
      case "claude_code":
        return endpointTypes.includes("anthropic")
      case "codex":
        return endpointTypes.includes("openai") || endpointTypes.includes("openai-response")
      case "pi":
        return (
          endpointTypes.includes("anthropic") ||
          endpointTypes.includes("openai") ||
          endpointTypes.includes("google_generative_ai")
        )
      case "opencode":
        return true
    }
  }
  return modelProtocols(model).some((protocol) => engineSupportsProtocol(engine, protocol))
}

function sandboxSizeFromAgent(a?: Agent | null): SandboxSize {
  // The server reads sandbox_size from the same merged config map at sandbox
  // cold-start time, so we keep the UI and the runtime view in sync.
  const v = String(agentConfig(a).sandbox_size ?? "standard")
  return v === "xl" ? "xl" : "standard"
}

function sandboxAutoRenewFromAgent(a?: Agent | null): boolean {
  // Mirrors the daemon's autoRenewFor: agents.config.sandbox_auto_renew
  // overrides the server default. Absent means "use the server default",
  // which is on, so only an explicit false disables it.
  return agentConfig(a).sandbox_auto_renew === false ? false : true
}

export type AgentDialogMode = "create" | "edit"

export interface AgentDialogValues {
  agentID?: string
  body: CreateAgentRequest | UpdateAgentRequest
  agentProfile?: UpdateAgentProfileRequest
}

interface CreateAgentDialogProps {
  open: boolean
  mode: AgentDialogMode
  workspaceID: string | null
  workspaceName?: string
  workspaceRole?: UserWorkspace["role"]
  models: Model[]
  agent?: Agent | null
  pending: boolean
  error: unknown
  onOpenChange: (open: boolean) => void
  onSubmit: (values: AgentDialogValues) => void
}

function extractErrorMessage(err: unknown): string | null {
  if (!err) return null
  if (err instanceof ApiError) return err.envelope.message || err.message
  if (err instanceof Error) return err.message
  return String(err)
}

function agentConfig(a?: Agent | null): Record<string, unknown> {
  return (a?.config ?? {}) as Record<string, unknown>
}

function profileConfig(a?: Agent | null): Record<string, unknown> {
  return ((agentConfig(a).profile ?? {}) as Record<string, unknown>)
}

function modelIDFromAgent(a?: Agent | null): string {
  const cfg = agentConfig(a)
  const profile = profileConfig(a)
  return String(cfg.default_model_id ?? cfg.model_id ?? profile.model_id ?? "")
}

function promptFromAgent(a: Agent | null | undefined, fallback: string): string {
  const cfg = agentConfig(a)
  return String(cfg.system_prompt ?? fallback)
}

function defaultWorkDir(executionMode: ExecutionMode): string {
  return executionMode === "sandbox" ? DEFAULT_WORK_DIR : ""
}

function runtimeFromAgent(a?: Agent | null): RuntimeChoice {
  // Legacy rows predating the per-agent runtime field default to "sandbox",
  // matching the migration backfill so server and UI agree.
  return a?.runtime ?? "sandbox"
}

/**
 * What the daemon and the server both accept: an absolute path, or one under
 * the operating user's home. The form used to demand a leading `/`, which was
 * stricter than either — `codex/options.go` and `claudecode/session.go` expand
 * `~/` themselves, and the server's own check reads "must be absolute or start
 * with ~".
 */
function isUsableWorkDir(path: string): boolean {
  return path.startsWith("/") || path.startsWith("~/")
}

function deviceIDFromAgent(a?: Agent | null): string {
  return String(agentConfig(a).device_id ?? "")
}

function workDirFromAgent(a?: Agent | null): string {
  // Same fallback chain as the backend's firstConfigString reader so old rows
  // (stored under work_dir / working_directory) still surface.
  const cfg = agentConfig(a)
  return String(cfg.work_dir ?? cfg.workdir ?? cfg.working_directory ?? "")
}

function configBaseForSubmit(a: Agent | null | undefined, connector: string): Record<string, unknown> {
  const cfg = { ...agentConfig(a) }
  delete cfg.profile
  if (connector === "agent_daemon") {
    delete cfg.agent_kind
    delete cfg.daemon_mode
    delete cfg.device_id
    // `mode` has engine-specific semantics. The wizard re-emits it only for
    // Codex so a stale plan mode cannot leak into another engine.
    delete cfg.mode
    // work_dir is re-emitted from the wizard state below; strip all legacy
    // aliases so a stale value cannot win over a freshly-cleared input.
    delete cfg.work_dir
    delete cfg.workdir
    delete cfg.working_directory
  }
  return cfg
}

function isAdminRole(role?: UserWorkspace["role"]): boolean {
  return role === "owner" || role === "admin"
}

function modelLabel(model?: Model | null): string {
  if (!model) return ""
  return model.name ? `${model.name} · ${model.model_key}` : model.model_key
}

export function CreateAgentDialog({
  open,
  mode,
  workspaceID,
  workspaceName,
  workspaceRole,
  models,
  agent,
  pending,
  error,
  onOpenChange,
  onSubmit,
}: CreateAgentDialogProps) {
  const runtimeStatus = useRuntimeStatus(open ? workspaceID : null)
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const queryClient = useQueryClient()
  const workspaceDisplayName = (workspaceName ?? "").trim() || t("agents.form.defaults.workspaceName")
  const defaultAgentName = t("agents.form.defaults.name", { workspace: workspaceDisplayName })
  const defaultAgentDescription = t("agents.form.defaults.description", { workspace: workspaceDisplayName })
  const defaultSystemPrompt = t("agents.form.defaults.systemPrompt")
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [executionMode, setExecutionMode] = useState<ExecutionMode>("local_device")
  const [agentEngine, setAgentEngine] = useState<AgentEngine>("claude_code")
  const [codexMode, setCodexMode] = useState<CodexCollaborationMode>("default")
  const [sandboxSize, setSandboxSize] = useState<SandboxSize>("standard")
  // Defaults to on, matching the server-wide default; only an explicit
  // false in agent config turns it off.
  const [sandboxAutoRenew, setSandboxAutoRenew] = useState(true)
  const [modelID, setModelID] = useState("")
  const [modelSearch, setModelSearch] = useState("")
  const [modelDropdownOpen, setModelDropdownOpen] = useState(false)
  // Radix's document listener can retain the initial Escape handler.
  const modelDropdownOpenRef = useRef(false)
  useEffect(() => {
    modelDropdownOpenRef.current = modelDropdownOpen
  }, [modelDropdownOpen])
  const [highlightedModelID, setHighlightedModelID] = useState<string | null>(null)
  const [systemPrompt, setSystemPrompt] = useState(defaultSystemPrompt)
  const [selectedCapabilityIDs, setSelectedCapabilityIDs] = useState<string[]>([])
  // capabilityVersionChoices keys on capability_id and stores the user's
  // per-binding version + mode pick. The dropdown default is "latest"
  // (tracks reuploads at dispatch time); switching to a specific version
  // record sets mode="pinned" + versionID=<that version> during creation.
  // Existing bindings are managed in the Agent Config tab.
  //
  // pinnedVersion (optional) caches the version literal (e.g. "1.0.3")
  // matching versionID. Carries forward through hydration so the
  // picker's loading-state fallback option renders the right number
  // instead of mis-labelling the pinned row with the latest version.
  const [capabilityVersionChoices, setCapabilityVersionChoices] = useState<Record<string, { pinningMode: "latest" | "pinned"; versionID: string; pinnedVersion?: string }>>({})
  const [visibility, setVisibility] = useState<AgentVisibility>("workspace")
  const [capabilitySearch, setCapabilitySearch] = useState("")
  const [capabilityTypeFilter, setCapabilityTypeFilter] = useState<"all" | "mcp" | "skill">("all")
  const [deviceID, setDeviceID] = useState("")
  const [workDir, setWorkDir] = useState("")
  const [pairDialogOpen, setPairDialogOpen] = useState(false)
  const [submitAttempted, setSubmitAttempted] = useState(false)
  const [allCredentialsSatisfied, setAllCredentialsSatisfied] = useState(true)
  const [credentialBindings, setCredentialBindings] = useState<Record<string, { source: "shared"; secret_id: string }>>({})
  const [inlineNewSecrets, setInlineNewSecrets] = useState<AgentInlineNewSecret[]>([])
  /** "personal" or "shared:<secret_id>" or "shared:new:<displayName>|<plaintext>". */
  const [modelBindingChoice, setModelBindingChoice] = useState<
    | { source: "personal" }
    // Pending pick: shared selected, no secret chosen yet — required so a workspace
    // with zero shared secrets can still flip source → "shared" and open the form.
    | { source: "shared" }
    | { source: "shared"; existing_secret_id: string }
    | { source: "shared"; new_secret: { display_name: string; plaintext: string } }
  >({ source: "personal" })
  // Ephemeral inputs for the "paste a new shared secret for the model"
  // option. Persisted into modelBindingChoice on save.
  const [modelNewSecretDisplayName, setModelNewSecretDisplayName] = useState("")
  const [modelNewSecretPlaintext, setModelNewSecretPlaintext] = useState("")
  const [modelNewSecretShowPlaintext, setModelNewSecretShowPlaintext] = useState(false)
  const [modelNewSecretExpanded, setModelNewSecretExpanded] = useState(false)
  const [step, setStep] = useState<WizardStep>(1)

  const capabilitiesQ = useCapabilitiesQuery(mode === "create" ? workspaceID : null, capabilitySearch)
  const allCapabilitiesQ = useCapabilitiesQuery(mode === "create" ? workspaceID : null, "")
  const cloneSourceID = mode === "create" && open ? agent?.id ?? null : null
  const existingBindingsQ = useAgentCapabilitiesQuery(workspaceID, cloneSourceID)
  const secretsQ = useSecrets(workspaceID)
  const sharedSecrets: Secret[] = useMemo(
    () => (secretsQ.data?.secrets ?? []).filter((s) => s.kind === "capability_inline" && s.status === "active"),
    [secretsQ.data?.secrets],
  )
  const activeModels = useMemo(() => models.filter((m) => m.status === "active"), [models])
  // Default to the first model the chosen engine can actually drive, so a
  // fresh create (and any engine switch) never lands on a greyed-out model
  // that the daemon would reject at run time.
  const firstModelID =
    activeModels.find((m) => engineSupportsModel(agentEngine, m))?.id ??
    activeModels[0]?.id ??
    ""
  const selectedModelID = modelID || (mode === "create" ? firstModelID : "")
  const selectedModel = useMemo(() => activeModels.find((m) => m.id === selectedModelID) ?? null, [activeModels, selectedModelID])
  const capabilityOptions = useMemo(() => {
    // `type: ""` is a sentinel for ghost rows (deprecated bindings whose real
    // type is unknown); downstream filters treat it as wildcard.
    type PickerOption = {
      id: string
      name: string
      type: CapabilityType | ""
      description: string
      latestVersionID: string
      latestVersion: string
      deprecated: boolean
      section: "workspace" | "marketplace"
      requiredCredentials: RequiredCredential[]
    }
    const ownCaps = capabilitiesQ.data?.capabilities ?? []
    const installedCaps = (capabilitiesQ.data?.marketplace_installs ?? []).filter((cap) => cap.visibility !== "workspace")
    const availableCaps = capabilitiesQ.data?.marketplace_available ?? []
    const workspace: PickerOption[] = [...ownCaps, ...(cloneSourceID ? cloneMarketplaceCapabilities(installedCaps) : installedCaps)].map((cap) => ({
      id: cap.id,
      name: cap.name,
      type: cap.type,
      description: cap.description ?? "",
      latestVersionID: cap.latest_version_id ?? "",
      latestVersion: cap.latest_version ?? cap.latest_published_version ?? "",
      deprecated: false,
      section: "workspace",
      requiredCredentials: cap.required_credentials ?? [],
    }))
    const marketplace: PickerOption[] = availableCaps.map((cap) => ({
      id: cap.capability_id,
      name: cap.name,
      type: cap.type,
      description: cap.description ?? "",
      latestVersionID: cap.latest_version_id ?? "",
      latestVersion: cap.latest_version ?? "",
      deprecated: false,
      section: "marketplace",
      requiredCredentials: cap.required_credentials ?? [],
    }))
    marketplace.sort((a, b) => a.name.localeCompare(b.name))
    const live: PickerOption[] = [...workspace, ...marketplace]
    return live
  }, [capabilitiesQ.data, cloneSourceID])
  const capabilityTypeCounts = useMemo(() => {
    // Ghost rows have unknown type, so they're excluded from per-type tallies
    // (still count toward "all").
    const counts = { all: capabilityOptions.length, mcp: 0, skill: 0 }
    for (const cap of capabilityOptions) {
      if (cap.deprecated) continue
      if (cap.type === "mcp") counts.mcp++
      else if (cap.type === "skill") counts.skill++
    }
    return counts
  }, [capabilityOptions])
  const visibleCapabilityOptions = useMemo(
    () => capabilityTypeFilter === "all"
      ? capabilityOptions
      // Ghost rows surface under every type tab; hiding them on a non-matching
      // tab would resurrect the "binding seems to have vanished" footgun.
      : capabilityOptions.filter((cap) => cap.deprecated || cap.type === capabilityTypeFilter),
    [capabilityOptions, capabilityTypeFilter]
  )
  // Models the current engine can't drive (wrong wire protocol). Kept in the
  // list but greyed out + unselectable so the user sees the full inventory and
  // understands why a model is unavailable, rather than it silently vanishing.
  const incompatibleModelIDs = useMemo(() => {
    const out = new Set<string>()
    for (const m of activeModels) {
      if (!engineSupportsModel(agentEngine, m)) out.add(m.id)
    }
    return out
  }, [activeModels, agentEngine])
  const filteredModels = useMemo(() => {
    const q = modelSearch.trim().toLowerCase()
    const matched = q
      ? activeModels.filter((m) => modelLabel(m).toLowerCase().includes(q))
      : activeModels
    // Compatible models first, incompatible (greyed) sink to the bottom;
    // stable within each group so catalog order is otherwise preserved.
    return [...matched].sort(
      (a, b) => Number(incompatibleModelIDs.has(a.id)) - Number(incompatibleModelIDs.has(b.id)),
    )
  }, [activeModels, modelSearch, incompatibleModelIDs])
  // If the user had hand-picked a model and then switched to an engine that
  // can't drive it, clear the pick so it falls back to firstModelID (a
  // compatible default) instead of submitting an incompatible model_id.
  useEffect(() => {
    if (modelID && incompatibleModelIDs.has(modelID)) setModelID("")
  }, [modelID, incompatibleModelIDs])
  const allCapabilitiesPool = useMemo<Capability[]>(() => {
    const data = allCapabilitiesQ.data
    const own = data?.capabilities ?? []
    const installed = (data?.marketplace_installs ?? []).filter((cap) => cap.visibility !== "workspace")
    // Map MarketplaceCapability into the minimum Capability shape
    // aggregate/required-credentials helpers and submit lookup expect.
    const available: Capability[] = (data?.marketplace_available ?? []).map((cap) => ({
      id: cap.capability_id,
      capability_id: cap.capability_id,
      workspace_id: "",
      type: cap.type,
      name: cap.name,
      description: cap.description,
      status: cap.status === "disabled" ? "disabled" : "active",
      required_credentials: cap.required_credentials,
      deprecated_at: cap.deprecated_at,
      from_marketplace: true,
      source_workspace_name: cap.source_workspace_name,
      latest_version_id: cap.latest_version_id,
      latest_version: cap.latest_version,
      latest_version_created_at: cap.latest_version_created_at,
      creator_id: "",
      created_at: cap.latest_version_created_at,
      updated_at: cap.latest_version_created_at,
    }))
    return [...own, ...(cloneSourceID ? cloneMarketplaceCapabilities(installed) : installed), ...available]
  }, [allCapabilitiesQ.data, cloneSourceID])
  const aggregatedRequiredKinds = useMemo(
    () => aggregateRequiredCredentialsByID(selectedCapabilityIDs, allCapabilitiesPool),
    [selectedCapabilityIDs, allCapabilitiesPool]
  )
  const admin = isAdminRole(workspaceRole)

  const modelFieldRef = useRef<HTMLDivElement | null>(null)
  const modelComboboxRef = useRef<HTMLDivElement | null>(null)
  const fieldID = useId()
  const modelListboxID = useId()
  const modelSecretID = useId()
  const wasOpenRef = useRef(false)

  useEffect(() => {
    if (!open) {
      wasOpenRef.current = false
      return
    }
    if (wasOpenRef.current) return
    wasOpenRef.current = true
    const params = new URLSearchParams(window.location.search.replace(/^\?+/, "?"))
    if (mode === "create") {
      // Clone path: an `agent` prop in create mode means prefill from that
      // source (with a "Copy" name suffix). URL params still win — the
      // connector-wizard return-to flow relies on them.
      const cloneSource = agent
      const cloneSuffix = cloneSource?.name ? " (Copy)" : ""
      setName(params.get("agent_name") ?? (cloneSource ? `${cloneSource.name}${cloneSuffix}` : defaultAgentName))
      setDescription(params.get("agent_description") ?? cloneSource?.description ?? defaultAgentDescription)
      const initialExecutionMode = cloneSource ? executionModeFromAgent(cloneSource) : "local_device"
      setExecutionMode(initialExecutionMode)
      setAgentEngine(cloneSource ? agentEngineFromAgent(cloneSource) : "claude_code")
      setCodexMode(cloneSource ? agentCodexModeOf(cloneSource) : "default")
      setSandboxSize(cloneSource ? sandboxSizeFromAgent(cloneSource) : "standard")
      setSandboxAutoRenew(cloneSource ? sandboxAutoRenewFromAgent(cloneSource) : true)
      setModelID(cloneSource ? modelIDFromAgent(cloneSource) : "")
      setModelSearch("")
      setModelDropdownOpen(false)
      setHighlightedModelID(null)
      setSystemPrompt(params.get("agent_prompt") ?? (cloneSource ? promptFromAgent(cloneSource, defaultSystemPrompt) : defaultSystemPrompt))
      setSelectedCapabilityIDs([])
      setCapabilityVersionChoices({})
      setCapabilitySearch("")
      setVisibility("workspace")
      setDeviceID(cloneSource ? deviceIDFromAgent(cloneSource) : "")
      setWorkDir(cloneSource ? workDirFromAgent(cloneSource) || defaultWorkDir(initialExecutionMode) : defaultWorkDir(initialExecutionMode))
      // Capability credentials are resolved separately for each cloned binding.
      setCredentialBindings({})
      // Clones keep valid shared references without choosing a different secret.
      const sourceModelBinding = agentConfig(cloneSource).model_credential_binding as { source?: string; secret_id?: string } | undefined
      setModelBindingChoice(sourceModelBinding?.source === "shared" && sourceModelBinding.secret_id
        ? { source: "shared", existing_secret_id: sourceModelBinding.secret_id }
        : { source: "shared" })
      setModelNewSecretExpanded(false)
      setModelNewSecretDisplayName("")
      setModelNewSecretPlaintext("")
      setInlineNewSecrets([])
    } else if (agent) {
      setName(agent.name)
      setDescription(agent.description ?? "")
      setExecutionMode(executionModeFromAgent(agent))
      setAgentEngine(agentEngineFromAgent(agent))
      setCodexMode(agentCodexModeOf(agent))
      setSandboxSize(sandboxSizeFromAgent(agent))
      setSandboxAutoRenew(sandboxAutoRenewFromAgent(agent))
      setModelID(modelIDFromAgent(agent) || firstModelID)
      setModelSearch("")
      setModelDropdownOpen(false)
      setHighlightedModelID(null)
      setSystemPrompt(promptFromAgent(agent, ""))
      setSelectedCapabilityIDs([])
      setCapabilityVersionChoices({})
      setCapabilitySearch("")
      setVisibility(agent.visibility ?? "workspace")
      setDeviceID(deviceIDFromAgent(agent))
      setWorkDir(workDirFromAgent(agent))
      const ac = agentConfig(agent)
      const mb = ac.model_credential_binding as { source?: string; secret_id?: string } | undefined
      setModelBindingChoice(
        mb?.source === "shared" && typeof mb.secret_id === "string" && mb.secret_id
          ? { source: "shared", existing_secret_id: mb.secret_id }
          : { source: "personal" },
      )
      setInlineNewSecrets([])
    }
    setSubmitAttempted(false)
    setAllCredentialsSatisfied(true)
    setPairDialogOpen(false)
    setStep(1)
    setCapabilityTypeFilter("all")
  }, [open, mode, agent, agent?.id, defaultAgentDescription, defaultAgentName, defaultSystemPrompt, firstModelID])

  const clone = useAgentCloneCapabilities(open, cloneSourceID,
    existingBindingsQ.isFetching || existingBindingsQ.isError ? undefined : existingBindingsQ.data?.installed,
    setSelectedCapabilityIDs, setCapabilityVersionChoices)
  const cloneCredentials = useAgentCloneCredentials(cloneSourceID, workspaceID, agentConfig(agent),
    clone.bindings, allCapabilitiesPool, selectedCapabilityIDs, capabilityVersionChoices, sharedSecrets)
  const cloneLoadFailed = Boolean(cloneSourceID) && (
    (!clone.ready && existingBindingsQ.isError) || (!allCapabilitiesQ.data && allCapabilitiesQ.isError) ||
    cloneCredentials.failed || (cloneCredentials.needsCredentials && secretsQ.isError)
  )
  const unavailableCloneCapabilities = clone.bindings.filter((binding) =>
    selectedCapabilityIDs.includes(binding.capability_id) && (
      !binding.capability_version_id || !allCapabilitiesPool.some((cap) => cap.id === binding.capability_id && cap.latest_version_id)
    ))

  function selectExecutionMode(nextMode: ExecutionMode) {
    setExecutionMode(nextMode)
    setWorkDir((current) =>
      current === defaultWorkDir(executionMode) ? defaultWorkDir(nextMode) : current
    )
  }

  useEffect(() => {
    if (!modelDropdownOpen) return
    const firstSelectable = filteredModels.find((m) => !incompatibleModelIDs.has(m.id))
    const nextHighlighted = filteredModels.some((m) => m.id === highlightedModelID)
      ? highlightedModelID
      : (filteredModels.find((m) => m.id === selectedModelID)?.id ?? firstSelectable?.id ?? null)
    if (nextHighlighted !== highlightedModelID) setHighlightedModelID(nextHighlighted)
  }, [filteredModels, highlightedModelID, incompatibleModelIDs, modelDropdownOpen, selectedModelID])

  useEffect(() => {
    if (!modelDropdownOpen) return
    const onPointerDown = (event: MouseEvent) => {
      if (modelComboboxRef.current?.contains(event.target as Node)) return
      setModelDropdownOpen(false)
    }
    document.addEventListener("mousedown", onPointerDown)
    return () => document.removeEventListener("mousedown", onPointerDown)
  }, [modelDropdownOpen])

  function openModelDropdown() {
    setModelSearch("")
    setModelDropdownOpen(true)
    const firstSelectable = filteredModels.find((m) => !incompatibleModelIDs.has(m.id))
    setHighlightedModelID(selectedModelID || firstSelectable?.id || null)
  }

  function selectModel(nextModel: Model) {
    // Incompatible with the current engine — ignore clicks/enter on it.
    if (incompatibleModelIDs.has(nextModel.id)) return
    setModelID(nextModel.id)
    setModelSearch("")
    setHighlightedModelID(nextModel.id)
    setModelDropdownOpen(false)
  }

  function onModelKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (!modelDropdownOpen && (event.key === "ArrowDown" || event.key === "ArrowUp")) {
      event.preventDefault()
      openModelDropdown()
      return
    }
    if (!modelDropdownOpen) return

    if (event.key === "Enter") {
      event.preventDefault()
      const target = filteredModels.find((m) => m.id === highlightedModelID)
      if (target) selectModel(target)
      return
    }

    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault()
      // Only step across selectable (engine-compatible) models so arrow keys
      // skip greyed-out rows entirely.
      const selectable = filteredModels.filter((m) => !incompatibleModelIDs.has(m.id))
      if (selectable.length === 0) return
      const currentIndex = selectable.findIndex((m) => m.id === highlightedModelID)
      const fallbackIndex = event.key === "ArrowDown" ? -1 : 0
      const baseIndex = currentIndex >= 0 ? currentIndex : fallbackIndex
      const nextIndex = event.key === "ArrowDown"
        ? (baseIndex + 1) % selectable.length
        : (baseIndex - 1 + selectable.length) % selectable.length
      setHighlightedModelID(selectable[nextIndex]?.id ?? null)
    }
  }
  const hasConnector = true
  const connector = mode === "edit" && agent ? agent.connector_type : connectorForExecutionMode(executionMode)
  const hasModel = activeModels.length > 0
  const requiresModel = connector !== "agent_daemon" || agentEngine === "claude_code" || agentEngine === "codex" || agentEngine === "pi" || agentEngine === "opencode"
  const selectedModelUnavailable = mode === "edit" && requiresModel && selectedModelID !== "" && selectedModel === null
  const hasRequiredModel = !requiresModel || (selectedModel !== null && !incompatibleModelIDs.has(selectedModel.id))
  const publicModelBindingValid = mode !== "create" || visibility !== "public"
    || !requiresModel || selectedModel?.credential_mode !== "credential_ref"
    || (modelBindingChoice.source === "shared" && (
      ("existing_secret_id" in modelBindingChoice && Boolean(modelBindingChoice.existing_secret_id))
      || ("new_secret" in modelBindingChoice && Boolean(modelBindingChoice.new_secret.plaintext.trim()))
    ))
  // Create opens model binding on a pending "shared" pick because secrets may
  // still be loading; once they land, resolve to the first existing one. Gated
  // on the credential_ref UI so no-credential models stay untouched; with zero
  // secrets it stays pending and the inline new-secret form takes over.
  useEffect(() => {
    if (mode !== "create" || cloneSourceID) return
    if (!requiresModel || selectedModel?.credential_mode !== "credential_ref") return
    if (modelBindingChoice.source !== "shared") return
    if ("existing_secret_id" in modelBindingChoice || "new_secret" in modelBindingChoice) return
    if (modelNewSecretExpanded || sharedSecrets.length === 0) return
    setModelBindingChoice({ source: "shared", existing_secret_id: sharedSecrets[0].id })
  }, [mode, cloneSourceID, requiresModel, selectedModel, modelBindingChoice, modelNewSecretExpanded, sharedSecrets])
  const modelSharedSecrets = cloneSourceID
    ? sharedSecretsForKind(sharedSecrets, selectedModel?.credential_kind_code || "model_api_key")
    : sharedSecrets
  const cloneModelCredentialValid = !cloneSourceID || !requiresModel || selectedModel?.credential_mode !== "credential_ref"
    || (visibility !== "public" && modelBindingChoice.source === "personal") || (
    modelBindingChoice.source === "shared" && (
      ("existing_secret_id" in modelBindingChoice && modelSharedSecrets.some((secret) => secret.id === modelBindingChoice.existing_secret_id)) ||
      ("new_secret" in modelBindingChoice && !!modelBindingChoice.new_secret.plaintext.trim())
    )
  )
  const daemonExecutionEditable = connector === "agent_daemon"
  const needsCloudPreflight = daemonExecutionEditable && executionMode === "sandbox"
    && (mode === "create" || executionModeFromAgent(agent) !== "sandbox")
  const cloudReady = !needsCloudPreflight || (!runtimeStatus.isError
    && runtimeStatus.data?.available === true
    && (runtimeStatus.data.profile === "managed" || runtimeStatus.data.has_credential))
  const showExecutionChoices = mode === "create" || daemonExecutionEditable
  const showDevicePicker = connector === "agent_daemon" && executionMode === "local_device" && Boolean(workspaceID)
  const errMsg = extractErrorMessage(error)

  function toggleInitialCapability(capabilityID: string, latestVersionID?: string) {
    let wasChecked = false
    setSelectedCapabilityIDs((prev) => {
      wasChecked = prev.includes(capabilityID)
      return wasChecked ? prev.filter((id) => id !== capabilityID) : [...prev, capabilityID]
    })
    setCapabilityVersionChoices((prev) => {
      if (wasChecked) {
        const { [capabilityID]: _, ...rest } = prev
        return rest
      }
      if (prev[capabilityID]) return prev
      return { ...prev, [capabilityID]: { pinningMode: "latest", versionID: latestVersionID ?? "" } }
    })
  }

  function setCapabilityVersionChoice(capabilityID: string, choice: { pinningMode: "latest" | "pinned"; versionID: string; pinnedVersion?: string }) {
    setCapabilityVersionChoices((prev) => ({ ...prev, [capabilityID]: choice }))
  }

  function submit() {
    setSubmitAttempted(true)
    if (!cloudReady) return
    if (!hasRequiredModel || !publicModelBindingValid) {
      modelFieldRef.current?.scrollIntoView({ behavior: "smooth", block: "center" })
      return
    }
    if (!name.trim() || !hasConnector) return
    if (connector === "agent_daemon" && executionMode === "local_device" && !deviceID) return
    const trimmedWorkDir = workDir.trim()
    if (connector === "agent_daemon" && trimmedWorkDir !== "" && !isUsableWorkDir(trimmedWorkDir)) {
      // The daemon also enforces absolute paths, but failing fast here gives
      // the user a clearer error tied to the input instead of a stream error.
      return
    }
    if (mode === "create" && (allCapabilitiesQ.isLoading || !clone.ready || !cloneCredentials.valid || !cloneModelCredentialValid || cloneLoadFailed || unavailableCloneCapabilities.length > 0)) return
    const selectedCapabilities = mode === "create"
      ? allCapabilitiesPool.filter((cap) => selectedCapabilityIDs.includes(cap.id) && cap.latest_version_id)
      : []
    const capabilityNames = selectedCapabilities.map((cap) => cap.name)
    // initialCapabilities carries the per-binding pin choice. Empty
    // versionID falls back to the capability's latest_version_id so the
    // server's NOT NULL capability_version_id constraint is satisfied
    // even in "latest" mode (the column tracks "last known version" as
    // a fallback for the daemon).
    const initialCapabilities = selectedCapabilities.map((cap) => {
      const choice = capabilityVersionChoices[cap.id]
      const pinningMode: "latest" | "pinned" = choice?.pinningMode ?? "latest"
      const versionID = pinningMode === "pinned"
        ? (choice?.versionID || (cap.latest_version_id as string))
        : (cap.latest_version_id as string)
      return { capability_version_id: cloneSourceID ? cloneCredentials.rows.find((row) => row.capabilityID === cap.id)!.versionID : versionID, pinning_mode: pinningMode,
        ...(cloneSourceID ? { configuration: cloneCredentials.rows.find((row) => row.capabilityID === cap.id)?.configuration } : {}) }
    })
    const profile = {
      ...(requiresModel ? { model_id: selectedModelID } : {}),
      ...(mode === "create" ? { capabilities: capabilityNames, skills: capabilityNames } : {}),
    }
    const mergedConfig: Record<string, unknown> = {
      ...(cloneSourceID ? withoutCredentialBindings(configBaseForSubmit(agent, connector)) : configBaseForSubmit(agent, connector)),
      profile,
      ...(connector === "agent_daemon" ? {
        agent_kind: agentEngine,
        ...(agentEngine === "codex" ? { mode: codexMode } : {}),
        ...(executionMode === "sandbox" ? { daemon_mode: "sandbox", sandbox_size: sandboxSize, sandbox_auto_renew: sandboxAutoRenew } : {}),
        ...(executionMode === "local_device" ? { daemon_mode: "local", device_id: deviceID } : {}),
        // Daemon read order is work_dir > workdir > working_directory; emit the
        // canonical key. Empty string omits the field, falling back to the
        // per-conversation scratch dir on the daemon side.
        ...(trimmedWorkDir !== "" ? { work_dir: trimmedWorkDir } : {}),
      } : {}),
    }
    const agentProfileConfig: Record<string, unknown> = {
      profile,
      agent_kind: agentEngine,
      ...(agentEngine === "codex"
        ? { mode: codexMode }
        : agentEngineFromAgent(agent) === "codex"
          ? { mode: null }
          : {}),
      daemon_mode: executionMode === "sandbox" ? "sandbox" : "local",
      sandbox_size: executionMode === "sandbox" ? sandboxSize : null,
      sandbox_auto_renew: executionMode === "sandbox" ? sandboxAutoRenew : null,
      device_id: executionMode === "local_device" ? deviceID : null,
      work_dir: trimmedWorkDir || null,
    }
    // Embed credential_bindings + model_credential_binding into the agent
    // config so the runtime resolver and visibility-bindings validator
    // both see them.
    //
    // Only creation carries capability credentials. Editing preserves them
    // server-side; model credential changes remain part of this form.
    //
    // These belong to the agent config the update body carries, so we build
    // them on a separate object — the agentProfile request below
    // intentionally doesn't see them.
    const agentBodyConfig: Record<string, unknown> = { ...mergedConfig }
    if (mode === "edit") {
      delete agentBodyConfig.credential_bindings
    } else if (Object.keys(credentialBindings).length > 0) {
      agentBodyConfig.credential_bindings = credentialBindings
    }
    if (modelBindingChoice.source === "shared" && "existing_secret_id" in modelBindingChoice && modelBindingChoice.existing_secret_id) {
      agentBodyConfig.model_credential_binding = {
        source: "shared",
        secret_id: modelBindingChoice.existing_secret_id,
      }
    } else if (mode === "edit") {
      // Explicit null tells the backend to delete the stored binding.
      agentBodyConfig.model_credential_binding = null
    } else {
      delete agentBodyConfig.model_credential_binding
    }
    // Compose the inline_new_secrets the server will materialise + bind.
    const inlineSecretsToCreate: AgentInlineNewSecret[] = [...inlineNewSecrets]
    if (modelBindingChoice.source === "shared" && "new_secret" in modelBindingChoice && modelBindingChoice.new_secret.plaintext.trim()) {
      inlineSecretsToCreate.push({
        kind: selectedModel?.credential_kind_code || "model_api_key",
        is_model: true,
        display_name: modelBindingChoice.new_secret.display_name || undefined,
        plaintext: modelBindingChoice.new_secret.plaintext,
      })
    }
    const body = {
      name: name.trim(),
      description: description.trim() || undefined,
      system_prompt: systemPrompt.trim(),
      connector_type: connector,
      ...(requiresModel ? { default_model_id: selectedModelID } : {}),
      ...(mode === "create" ? { capabilities: capabilityNames, initial_capabilities: initialCapabilities, visibility } : {}),
      config: agentBodyConfig,
      ...(inlineSecretsToCreate.length > 0 ? { inline_new_secrets: inlineSecretsToCreate } : {}),
    } satisfies CreateAgentRequest | UpdateAgentRequest
    const agentProfile = mode === "edit" && connector === "agent_daemon"
      ? {
          ...(requiresModel ? { model_id: selectedModelID } : {}),
          system_prompt: systemPrompt.trim() || undefined,
          config: agentProfileConfig,
        } satisfies UpdateAgentProfileRequest
      : undefined
    onSubmit({ agentID: agent?.id, body, agentProfile })
  }

  const workDirTrimmed = workDir.trim()
  const workDirValid = connector !== "agent_daemon" || workDirTrimmed === "" || isUsableWorkDir(workDirTrimmed)
  const canSubmit =
    !pending &&
    name.trim() !== "" &&
    hasConnector &&
    hasRequiredModel &&
    publicModelBindingValid &&
    cloudReady &&
    (connector !== "agent_daemon" || executionMode !== "local_device" || deviceID !== "") &&
    workDirValid &&
    (mode !== "create" || (!allCapabilitiesQ.isLoading && clone.ready && !cloneLoadFailed && unavailableCloneCapabilities.length === 0)) &&
    cloneModelCredentialValid &&
    (cloneSourceID ? cloneCredentials.valid : aggregatedRequiredKinds.length === 0 || allCredentialsSatisfied)

  const step1Valid =
    name.trim() !== "" &&
    hasConnector &&
    hasRequiredModel &&
    publicModelBindingValid &&
    cloudReady &&
    (connector !== "agent_daemon" || executionMode !== "local_device" || deviceID !== "") &&
    workDirValid && cloneModelCredentialValid
  const totalSteps = mode === "create" ? 2 : 1
  const progressPercent = Math.round((step / totalSteps) * 100)

  function tryAdvance(target: WizardStep) {
    if (target <= step) {
      setStep(target)
      return
    }
    setSubmitAttempted(true)
    if (step === 1 && !step1Valid) {
      if (!hasRequiredModel) {
        modelFieldRef.current?.scrollIntoView({ behavior: "smooth", block: "center" })
      }
      return
    }
    setStep(target)
  }

  return (
    <>
    <Dialog
      open={open}
      onOpenChange={(next) => {
        // Keep the Agent form open while the nested pairing modal is active.
        if (!next && pairDialogOpen) return
        onOpenChange(next)
      }}
    >
      <DialogContent
        className="flex max-h-[86vh] flex-col overflow-hidden sm:max-w-2xl"
        onEscapeKeyDown={(event) => {
          if (modelDropdownOpenRef.current) {
            event.preventDefault()
            setModelDropdownOpen(false)
          }
        }}
        onPointerDownOutside={(event) => {
          if (pairDialogOpen) event.preventDefault()
        }}
        onInteractOutside={(event) => {
          if (pairDialogOpen) event.preventDefault()
        }}
      >
        <DialogHeader className="shrink-0">
          <DialogTitle>{mode === "edit" ? t("agents.form.title.edit") : t("agents.form.title.create")}</DialogTitle>
          {mode === "edit" && <DialogDescription>{t("agents.form.editCapabilitiesHint")}</DialogDescription>}
        </DialogHeader>

        {mode === "create" && (
          <WizardProgress
            step={step}
            totalSteps={totalSteps}
            progressPercent={progressPercent}
            title={t(`agents.form.wizard.steps.${step === 1 ? "setup" : "capabilities"}.title` as never)}
            summary={t(`agents.form.wizard.steps.${step === 1 ? "setup" : "capabilities"}.summary` as never)}
            stepOfLabel={t("agents.form.wizard.stepOf", { current: step, total: totalSteps })}
            completeLabel={t("agents.form.wizard.complete", { percent: progressPercent })}
          />
        )}

        {needsCloudPreflight && !cloudReady && workspaceID && (
          <AgentCloudPreflight workspaceID={workspaceID} checking={runtimeStatus.isFetching} failed={runtimeStatus.isError} onRetry={() => void runtimeStatus.refetch()} />
        )}

        <form
          className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto overflow-x-hidden pr-1"
          onSubmit={(e) => {
            // Swallow accidental form submissions (e.g. Enter in a text input)
            // so the wizard never creates the agent from a wrong step.
            e.preventDefault()
          }}
        >
          {step === 1 && (
            <div className="flex flex-col gap-5">
              <section className="flex flex-col gap-3">
                <Field
                  label={t("agents.form.fields.name")}
                  htmlFor={`${fieldID}-name`}
                  required
                >
                  <Input id={`${fieldID}-name`} value={name} onChange={(e) => setName(e.target.value)} placeholder={t("agents.form.placeholders.name")} autoFocus />
                </Field>
                <Field label={t("agents.form.fields.description")} htmlFor={`${fieldID}-description`}>
                  <Input id={`${fieldID}-description`} value={description} onChange={(e) => setDescription(e.target.value)} placeholder={t("agents.form.placeholders.description")} />
                </Field>
                <AgentInstructionsField value={systemPrompt} onChange={setSystemPrompt} disabled={pending} />
                {mode === "create" && (
                  <AgentVisibilityField value={visibility} disabled={pending} onChange={(next) => {
                    setVisibility(next)
                    if (next === "public" && modelBindingChoice.source === "personal") setModelBindingChoice({ source: "shared" })
                  }} />
                )}
              </section>
              <section className="flex flex-col gap-3">
              {showExecutionChoices ? (
                <>
                  <Field label={t("agents.form.fields.executionMode")} required>
                    <div className="flex flex-col" role="radiogroup">
                      <ChoiceCard
                        name="execution-mode"
                        title={t("agents.execution.localDevice.title")}
                        description={t("agents.execution.localDevice.description")}
                        selected={executionMode === "local_device"}
                        onSelect={() => selectExecutionMode("local_device")}
                      />
                      <ChoiceCard
                        name="execution-mode"
                        title={t("agents.execution.sandbox.title")}
                        description={t("agents.execution.sandbox.description")}
                        selected={executionMode === "sandbox"}
                        onSelect={() => selectExecutionMode("sandbox")}
                      />
                      {mode === "create" && (
                        <ChoiceCard
                          name="execution-mode"
                          title={t("agents.execution.external.title")}
                          description={t("agents.execution.external.description")}
                          selected={executionMode === "external"}
                          onSelect={() => selectExecutionMode("external")}
                          disabled
                        />
                      )}
                    </div>
                  </Field>
                  {connector === "agent_daemon" && (
                    <Field label={t("agents.form.fields.agentEngine")} required>
                      <Select
                        value={agentEngine}
                        onValueChange={(nextValue) => {
                          const next = nextValue
                          if (next === "claude_code" || next === "codex" || next === "pi" || next === "opencode") setAgentEngine(next)
                        }}
                        disabled={pending}
                        aria-label={t("agents.form.fields.agentEngine")}
                      >
                        <SelectOption value="claude_code">{t("agents.engine.claudeCode.title")}</SelectOption>
                        <SelectOption value="codex">{t("agents.engine.codex.title")}</SelectOption>
                        <SelectOption value="pi">{t("agents.engine.pi.title")}</SelectOption>
                        <SelectOption value="opencode">{t("agents.engine.opencode.title")}</SelectOption>
                      </Select>
                    </Field>
                  )}
                  {connector === "agent_daemon" && agentEngine === "codex" && (
                    <Field
                      label={t("agents.form.fields.codexMode")}
                      hint={t("agents.form.codexMode.hint")}
                    >
                      <Select
                        value={codexMode}
                        onValueChange={(nextValue) => setCodexMode(nextValue === "plan" ? "plan" : "default")}
                        disabled={pending}
                        aria-label={t("agents.form.fields.codexMode")}
                      >
                        <SelectOption value="default">{t("agents.form.codexMode.default")}</SelectOption>
                        <SelectOption value="plan">{t("agents.form.codexMode.plan")}</SelectOption>
                      </Select>
                    </Field>
                  )}
                  {connector === "agent_daemon" && executionMode === "sandbox" && (
                    <div className="flex flex-col gap-3">
                      <Field
                        label={t("agents.form.fields.sandboxSize")}
                        hint={t("agents.form.sandboxSize.hint")}
                      >
                        <Select
                          value={sandboxSize}
                          onValueChange={(nextValue) => setSandboxSize(nextValue === "xl" ? "xl" : "standard")}
                          disabled={pending}
                          aria-label={t("agents.form.fields.sandboxSize")}
                        >
                          <SelectOption value="standard">{t("agents.form.sandboxSize.standard")}</SelectOption>
                          <SelectOption value="xl">{t("agents.form.sandboxSize.xl")}</SelectOption>
                        </Select>
                      </Field>
                      <Field
                        label={t("agents.form.fields.sandboxAutoRenew")}
                        hint={t("agents.form.sandboxAutoRenew.hint")}
                      >
                        <label className="flex h-7 cursor-pointer items-center gap-2">
                          <input
                            type="checkbox"
                            className="h-3.5 w-3.5 shrink-0 accent-accent"
                            checked={sandboxAutoRenew}
                            disabled={pending}
                            onChange={(e) => setSandboxAutoRenew(e.target.checked)}
                          />
                          <span className="min-w-0 flex-1 text-sm text-fg">
                            {t("agents.form.sandboxAutoRenew.label")}
                          </span>
                        </label>
                      </Field>
                      {runtimeStatus.data?.sandbox_image ? (
                        <Field label={t("agents.form.fields.sandboxImage")} hint={t("agents.form.sandboxImage.hint")}>
                          <code className="block break-all py-1 font-mono text-xs text-fg">
                            {runtimeStatus.data.sandbox_image}
                          </code>
                        </Field>
                      ) : null}
                    </div>
                  )}
                </>
              ) : (
                <Field label={t("agents.form.fields.executionMode")} required>
                  <div className="flex h-7 items-center text-sm text-fg">
                    {t(`agents.execution.${executionMode === "local_device" ? "localDevice" : executionMode}.title` as never)}
                  </div>
                </Field>
              )}
              {showDevicePicker && workspaceID && (
                <Field
                  label={t("agents.form.fields.device")}
                  required
                  hint={t("agents.form.devicePicker.hint")}
                  error={submitAttempted && !deviceID ? t("agents.form.errors.deviceRequired") : undefined}
                >
                  <DevicePicker
                    workspaceID={workspaceID}
                    value={deviceID}
                    onChange={setDeviceID}
                    agentKind={agentEngine}
                    preserveSelected={mode === "edit"}
                    disabled={pending}
                    onAddDevice={() => setPairDialogOpen(true)}
                  />
                </Field>
              )}
              {connector === "agent_daemon" && (executionMode === "local_device" || executionMode === "sandbox") && (
                <Field
                  label={t("agents.form.fields.workDir")}
                  htmlFor={`${fieldID}-work-dir`}
                  hint={t(executionMode === "sandbox" ? "agents.form.workDir.hintSandbox" : "agents.form.workDir.hintLocal")}
                  error={!workDirValid ? t("agents.form.errors.workDirAbsolute") : undefined}
                >
                  <Input
                    id={`${fieldID}-work-dir`}
                    value={workDir}
                    onChange={(e) => setWorkDir(e.target.value)}
                    placeholder={t("agents.form.workDir.placeholder")}
                    aria-label={t("agents.form.fields.workDir")}
                    aria-invalid={!workDirValid}
                    disabled={pending}
                    spellCheck={false}
                    autoCapitalize="off"
                    autoCorrect="off"
                  />
                </Field>
              )}
              {requiresModel && (
                <div className="order-first">
                  <Field
                    ref={modelFieldRef}
                    label={t("agents.form.fields.model")}
                    htmlFor={hasModel ? `${fieldID}-model` : undefined}
                    required
                    error={selectedModelUnavailable
                      ? t("agents.form.errors.modelUnavailable")
                      : submitAttempted && !hasRequiredModel
                        ? t("agents.form.errors.modelRequired")
                        : undefined}
                  >
                {hasModel && (
                  <div ref={modelComboboxRef} className="relative">
                    <Search className="pointer-events-none absolute left-2 top-1/2 z-10 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
                    <Input
                      id={`${fieldID}-model`}
                      role="combobox"
                      aria-expanded={modelDropdownOpen}
                      aria-controls={modelListboxID}
                      aria-autocomplete="list"
                      aria-activedescendant={highlightedModelID ? `model-option-${highlightedModelID}` : undefined}
                      value={modelDropdownOpen ? modelSearch : modelLabel(selectedModel)}
                      onFocus={openModelDropdown}
                      onClick={() => {
                        if (!modelDropdownOpen) openModelDropdown()
                      }}
                      onChange={(e) => {
                        setModelSearch(e.target.value)
                        setModelDropdownOpen(true)
                      }}
                      onKeyDown={onModelKeyDown}
                      className="pl-7 pr-8"
                      placeholder={t("agents.form.placeholders.modelSearch")}
                    />
                    <ChevronDown className={cn("pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted transition-transform duration-200 ease-spring", modelDropdownOpen && "rotate-180")} strokeWidth={1.5} aria-hidden="true" />
                    {modelDropdownOpen && (
                      <div
                        id={modelListboxID}
                        role="listbox"
                        className="app-shadow-floating absolute z-50 mt-1 max-h-52 w-full overflow-y-auto rounded-lg border border-line bg-surface p-1 text-sm animate-pop-in data-[state=closed]:animate-pop-out"
                      >
                        {filteredModels.length === 0 ? (
                          <div className="px-2 py-1.5 text-sm text-fg-muted">{t("agents.form.emptyModelSearch")}</div>
                        ) : filteredModels.map((m) => {
                          const selected = selectedModelID === m.id
                          const highlighted = highlightedModelID === m.id
                          const incompatible = incompatibleModelIDs.has(m.id)
                          return (
                            <button
                              id={`model-option-${m.id}`}
                              key={m.id}
                              type="button"
                              role="option"
                              aria-selected={selected}
                              aria-disabled={incompatible}
                              disabled={incompatible}
                              title={incompatible ? t("agents.form.modelProtocolMismatch", { engine: agentEngine }) : undefined}
                              onMouseEnter={() => { if (!incompatible) setHighlightedModelID(m.id) }}
                              onClick={() => selectModel(m)}
                              className={cn("flex w-full items-center justify-between gap-3 rounded px-2 py-1.5 text-left text-sm text-fg", incompatible ? "cursor-not-allowed opacity-50" : highlighted ? "app-pressed" : selected ? "app-selected" : "hover:app-hover")}
                            >
                              <span className="min-w-0 flex-1 truncate">{modelLabel(m)}</span>
                              <span className="flex shrink-0 items-center gap-2">
                                {/* Protocol badge on every row (compatible or
                                    not) so the user can see each model's wire
                                    protocol at a glance. */}
                                <span className="font-mono text-xs text-fg-muted">
                                  {protocolListLabel(modelProtocols(m))}
                                </span>
                                {selected && !incompatible && (
                                  <span className="inline-flex items-center gap-1 text-xs text-fg-muted">
                                    <Check className="h-3.5 w-3.5" strokeWidth={1.5} aria-hidden="true" />
                                    {t("agents.form.selected")}
                                  </span>
                                )}
                              </span>
                            </button>
                          )
                        })}
                      </div>
                    )}
                  </div>
                )}
                {activeModels.every((model) => incompatibleModelIDs.has(model.id)) && (
                  <AgentModelPrerequisite workspaceID={workspaceID} />
                )}
                  </Field>
                </div>
              )}
              {requiresModel && selectedModel && selectedModel.credential_mode === "credential_ref" && (
                <Field label={t("credentialCheck.modelBindingTitle")}>
                  {!publicModelBindingValid && <p className="text-xs text-fg-muted [overflow-wrap:anywhere]" role="status">{t("agents.form.visibility.sharedModelRequired")}</p>}
                  {cloneSourceID && <AgentCloneCredentialRefresh failed={secretsQ.isError} fetching={secretsQ.isFetching}
                    onRefresh={() => { void secretsQ.refetch() }} />}
                  <div className="flex flex-col gap-1" role="radiogroup">
                    <label className={cn("flex items-start gap-2 py-1", visibility === "public" ? "cursor-not-allowed opacity-50" : "cursor-pointer")}>
                      <input
                        type="radio"
                        name="model-binding"
                        className="mt-0.5 h-3.5 w-3.5 shrink-0 accent-accent"
                        checked={modelBindingChoice.source === "personal"}
                        disabled={visibility === "public"}
                        onChange={() => setModelBindingChoice({ source: "personal" })}
                      />
                      <span className="text-sm text-fg">
                        <span className="block">{t("credentialCheck.modelBindingPersonal")}</span>
                        {visibility === "public" && (
                          <span className="block text-xs text-fg-muted">{t("credentialCheck.personalDisabledHint")}</span>
                        )}
                      </span>
                    </label>
                    <label className="flex cursor-pointer items-start gap-2 py-1">
                      <input
                        type="radio"
                        name="model-binding"
                        className="mt-0.5 h-3.5 w-3.5 shrink-0 accent-accent"
                        checked={modelBindingChoice.source === "shared"}
                        onChange={() => {
                          // Default selection on flip-in: existing secret if any, else
                          // mark shared "pending" so the radio selects + form renders.
                          if (modelSharedSecrets[0]) {
                            setModelBindingChoice({ source: "shared", existing_secret_id: modelSharedSecrets[0].id })
                          } else {
                            setModelBindingChoice({ source: "shared" })
                            setModelNewSecretExpanded(true)
                            setModelNewSecretDisplayName("")
                            setModelNewSecretPlaintext("")
                          }
                        }}
                      />
                      <span className="flex-1 text-sm text-fg">
                        <span className="block">{t("credentialCheck.modelBindingShared")}</span>
                        {modelBindingChoice.source === "shared" && (
                          <div className="mt-1.5 flex flex-col gap-2">
                            {modelSharedSecrets.length > 0 && (
                              <Select
                                aria-label={t("credentialCheck.modelBindingShared")}
                                value={"existing_secret_id" in modelBindingChoice ? (modelSharedSecrets.some((secret) => secret.id === modelBindingChoice.existing_secret_id) ? modelBindingChoice.existing_secret_id : "") : "new_secret" in modelBindingChoice || modelNewSecretExpanded || !cloneSourceID ? "__new__" : ""}
                                onValueChange={(nextValue) => {
                                  if (nextValue === "__new__") {
                                    if (cloneSourceID) setModelBindingChoice({ source: "shared" })
                                    setModelNewSecretExpanded(true)
                                    if (!("new_secret" in modelBindingChoice)) {
                                      setModelNewSecretDisplayName("")
                                      setModelNewSecretPlaintext("")
                                    }
                                  } else {
                                    setModelBindingChoice({ source: "shared", existing_secret_id: nextValue })
                                    setModelNewSecretExpanded(false)
                                  }
                                }}
                                onClick={(e) => e.stopPropagation()}
                              >
                                {cloneSourceID && <SelectOption value="">{t("credentialCheck.sharedPlaceholder")}</SelectOption>}
                                {modelSharedSecrets.map((s) => (
                                  <SelectOption key={s.id} value={s.id}>{s.name}</SelectOption>
                                ))}
                                <SelectOption value="__new__">{t("credentialCheck.createNewShared")}</SelectOption>
                              </Select>
                            )}
                            {modelSharedSecrets.length === 0 && !modelNewSecretExpanded && !("new_secret" in modelBindingChoice) && (
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                className="self-start"
                                onClick={(e) => {
                                  e.preventDefault()
                                  e.stopPropagation()
                                  setModelNewSecretExpanded(true)
                                  setModelNewSecretDisplayName("")
                                  setModelNewSecretPlaintext("")
                                }}
                              >
                                {t("credentialCheck.createNewShared")}
                              </Button>
                            )}
                            {"new_secret" in modelBindingChoice && !modelNewSecretExpanded && (
                              <div className="flex items-center gap-1.5 text-xs text-fg">
                                {/* Queued, not done: this secret is created on
                                    save. A green tick said it already existed. */}
                                <StatusIcon status="queued" />
                                <span className="flex-1 truncate">
                                  {t("credentialCheck.sharedNewQueued", { name: modelBindingChoice.new_secret.display_name || t("credentialCheck.modelBindingTitle") })}
                                </span>
                                <button
                                  type="button"
                                  className="text-fg underline underline-offset-4"
                                  onClick={(e) => {
                                    e.preventDefault()
                                    e.stopPropagation()
                                    const c = modelBindingChoice
                                    if (!("new_secret" in c)) return
                                    setModelNewSecretDisplayName(c.new_secret.display_name)
                                    setModelNewSecretPlaintext(c.new_secret.plaintext)
                                    setModelNewSecretExpanded(true)
                                  }}
                                >
                                  {t("credentialCheck.sharedNewEdit")}
                                </button>
                              </div>
                            )}
                            {modelNewSecretExpanded && (
                              <div className="flex flex-col gap-3 border-t border-line pt-3" onClick={(e) => e.stopPropagation()}>
                                <div className="flex flex-col">
                                  <Label htmlFor={`${modelSecretID}-name`}>{t("credentialCheck.form.displayName")}</Label>
                                  <Input
                                    id={`${modelSecretID}-name`}
                                    value={modelNewSecretDisplayName}
                                    onChange={(e) => setModelNewSecretDisplayName(e.target.value)}
                                    placeholder={selectedModel?.name ?? t("credentialCheck.modelBindingTitle")}
                                  />
                                </div>
                                <div className="flex flex-col">
                                  <Label htmlFor={`${modelSecretID}-value`}>
                                    {t("credentialCheck.form.value")}<span aria-hidden="true"> *</span>
                                  </Label>
                                  <div className="relative">
                                    <Input
                                      id={`${modelSecretID}-value`}
                                      type={modelNewSecretShowPlaintext ? "text" : "password"}
                                      value={modelNewSecretPlaintext}
                                      onChange={(e) => setModelNewSecretPlaintext(e.target.value)}
                                      placeholder="sk-..."
                                      className="pr-8"
                                    />
                                    <button
                                      type="button"
                                      aria-label={modelNewSecretShowPlaintext ? "Hide" : "Show"}
                                      onClick={() => setModelNewSecretShowPlaintext(!modelNewSecretShowPlaintext)}
                                      className="absolute right-2 top-1/2 -translate-y-1/2 text-fg-muted hover:text-fg"
                                    >
                                      {modelNewSecretShowPlaintext ? <EyeOff className="h-3.5 w-3.5" strokeWidth={1.5} aria-hidden="true" /> : <Eye className="h-3.5 w-3.5" strokeWidth={1.5} aria-hidden="true" />}
                                    </button>
                                  </div>
                                </div>
                                <div className="flex justify-end gap-2">
                                  <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    onClick={() => {
                                      setModelNewSecretExpanded(false)
                                      // If the user cancels and no existing secret has been chosen yet,
                                      // fall the binding back to an existing one (if any) or to personal,
                                      // so we don't leave the shared radio "selected with nothing inside".
                                      if (cloneSourceID) {
                                        setModelBindingChoice({ source: "shared" })
                                      } else if (!("new_secret" in modelBindingChoice)) {
                                        if (modelSharedSecrets[0]) {
                                          setModelBindingChoice({ source: "shared", existing_secret_id: modelSharedSecrets[0].id })
                                        } else if (visibility !== "public") {
                                          setModelBindingChoice({ source: "personal" })
                                        }
                                      }
                                    }}
                                  >
                                    {t("credentialCheck.form.cancel")}
                                  </Button>
                                  <Button
                                    type="button"
                                    size="sm"
                                    disabled={!modelNewSecretPlaintext.trim()}
                                    onClick={() => {
                                      if (!modelNewSecretPlaintext.trim()) return
                                      setModelBindingChoice({
                                        source: "shared",
                                        new_secret: {
                                          display_name: modelNewSecretDisplayName.trim(),
                                          plaintext: modelNewSecretPlaintext.trim(),
                                        },
                                      })
                                      setModelNewSecretExpanded(false)
                                    }}
                                  >
                                    {t("credentialCheck.form.save")}
                                  </Button>
                                </div>
                              </div>
                            )}
                          </div>
                        )}
                      </span>
                    </label>
                  </div>
                </Field>
              )}
              </section>
            </div>
          )}

          {mode === "create" && step === 2 && (
            <>
              {cloneSourceID && <AgentCloneNotice
                ready={clone.ready && cloneCredentials.ready && !allCapabilitiesQ.isLoading}
                failed={cloneLoadFailed}
                unavailable={unavailableCloneCapabilities}
                onRetry={() => { void existingBindingsQ.refetch(); void allCapabilitiesQ.refetch(); void secretsQ.refetch(); cloneCredentials.retry() }}
                onRemove={(id) => setSelectedCapabilityIDs((current) => current.filter((selected) => selected !== id))}
              />}
              <section className="flex flex-col gap-3">
                <Input value={capabilitySearch} onChange={(e) => setCapabilitySearch(e.target.value)} placeholder={t("agents.form.placeholders.capabilitySearch")} />
                {capabilityOptions.length === 0 ? (
                  <p className="py-2 text-sm text-fg-muted">
                    {capabilitySearch.trim() ? tc("states.noResults") : admin ? t("agents.form.noTagsAdmin") : t("agents.form.noTagsMember")}
                  </p>
                ) : (
                  <>
                    <Tabs value={capabilityTypeFilter} onValueChange={(v) => setCapabilityTypeFilter(v as typeof capabilityTypeFilter)}>
                      <TabsList>
                        <TabsTrigger value="all">{t("agents.form.capabilityTypeTabs.all")} ({capabilityTypeCounts.all})</TabsTrigger>
                        <TabsTrigger value="mcp">{t("agents.form.capabilityTypeTabs.mcp")} ({capabilityTypeCounts.mcp})</TabsTrigger>
                        <TabsTrigger value="skill">{t("agents.form.capabilityTypeTabs.skill")} ({capabilityTypeCounts.skill})</TabsTrigger>
                      </TabsList>
                    </Tabs>
                    <div className="max-h-56 overflow-y-auto border-t border-line">
                      {visibleCapabilityOptions.length === 0 ? (
                        <p className="py-3 text-sm text-fg-muted">{tc("states.noResults")}</p>
                      ) : (() => {
                        const sections = (["workspace", "marketplace"] as const)
                          .map((sec) => ({ sec, rows: visibleCapabilityOptions.filter((o) => o.section === sec) }))
                          .filter((g) => g.rows.length > 0)
                        let rowCounter = 0
                        return sections.map(({ sec, rows }, sectionIdx) => (
                          <Fragment key={sec}>
                            <div className={cn("flex h-7 items-center border-b border-line text-xs text-fg-muted", sectionIdx > 0 && "mt-2")}>
                              {t(`agents.form.capabilitySections.${sec}`)}
                            </div>
                            {rows.map((cap) => {
                              const index = rowCounter++
                              const checked = selectedCapabilityIDs.includes(cap.id)
                              const lockedNoVersion = mode === "create" && !cap.latestVersionID
                              const lockedDeprecatedAndUnchecked = cap.deprecated && !checked
                              const disabled = lockedNoVersion || lockedDeprecatedAndUnchecked || !clone.ready
                              const ghostTitle = cap.deprecated ? t("agents.form.deprecatedCapabilityTooltip") : undefined
                              return (
                                <label key={`${sec}:${cap.id || cap.name}`} title={ghostTitle} data-row-index={index} className={cn("flex w-full min-w-0 items-start gap-2 border-b border-line py-2 text-left", disabled ? "cursor-not-allowed opacity-50" : "cursor-pointer hover:app-hover")}>
                                  <input
                                    type="checkbox"
                                    className="mt-0.5 h-3.5 w-3.5 shrink-0 accent-accent"
                                    checked={checked}
                                    disabled={disabled}
                                    onChange={() => toggleInitialCapability(cap.id, cap.latestVersionID)}
                                  />
                                  <span className="min-w-0 flex-1">
                                    <span className="flex min-w-0 items-center gap-2">
                                      <span className={cn("min-w-0 flex-1 truncate text-sm font-medium", cap.deprecated ? "text-fg-muted" : "text-fg")}>{cap.name}</span>
                                      {cap.type && !cap.deprecated && <span className="shrink-0"><Badge variant="neutral">{cap.type}</Badge></span>}
                                      {sec === "marketplace" && !cap.deprecated && <span className="shrink-0"><Badge variant="neutral">{t("agents.form.capabilityBadges.marketplace")}</Badge></span>}
                                      {cap.deprecated && <span className="shrink-0"><Badge variant="warning" dot>{t("agents.form.deprecatedCapabilityBadge")}</Badge></span>}
                                      {!cap.deprecated && !checked && cap.latestVersion && <span className="shrink-0 font-mono text-xs text-fg-muted">v{cap.latestVersion}</span>}
                                      {!cap.deprecated && !checked && !cap.latestVersion && <span className="shrink-0"><Badge variant="warning" dot>{t("agents.form.noCapabilityVersion")}</Badge></span>}
                                      {!cap.deprecated && checked && cap.id && (cloneSourceID ? (
                                        <AgentCloneVersionPicker
                                          label={`${cap.name} · ${t("agents.detail.capabilities.enableDialog.version")}`}
                                          row={cloneCredentials.rows.find((row) => row.capabilityID === cap.id)}
                                          choice={capabilityVersionChoices[cap.id]}
                                          onChange={(next) => setCapabilityVersionChoice(cap.id, next)}
                                        />
                                      ) : (
                                        <CapabilityVersionPicker
                                          label={`${cap.name} · ${t("agents.detail.capabilities.enableDialog.version")}`}
                                          capabilityID={cap.id}
                                          fromMarketplace={sec === "marketplace"}
                                          workspaceID={workspaceID}
                                          latestVersionID={cap.latestVersionID}
                                          latestVersion={cap.latestVersion}
                                          choice={capabilityVersionChoices[cap.id]}
                                          onChange={(next) => setCapabilityVersionChoice(cap.id, next)}
                                        />
                                      ))}
                                    </span>
                                    {cap.description && !cap.deprecated && <span className="block truncate text-xs text-fg-muted">{cap.description}</span>}
                                  </span>
                                </label>
                              )
                            })}
                          </Fragment>
                        ))
                      })()}
                    </div>
                  </>
                )}
              </section>

              {cloneSourceID ? <AgentCloneCredentials credentials={cloneCredentials} workspaceID={workspaceID}
                failed={secretsQ.isError} fetching={secretsQ.isFetching} onRefresh={() => { void secretsQ.refetch() }} /> : aggregatedRequiredKinds.length > 0 && (
                <section className="flex flex-col gap-3">
                  <h3 className="text-sm font-medium text-fg">{t("agents.form.sections.credentials")}</h3>
                  <CredentialCheckPanel
                    requiredKinds={aggregatedRequiredKinds}
                    workspaceID={workspaceID}
                    sharedSecrets={sharedSecrets}
                    visibility={visibility}
                    onChange={(bindings, inlineNew, valid) => {
                      setCredentialBindings(bindings)
                      setInlineNewSecrets(inlineNew)
                      setAllCredentialsSatisfied(valid)
                    }}
                  />
                </section>
              )}
            </>
          )}

          {errMsg && (
            <p className="flex items-start gap-1.5 text-sm text-fg" role="alert">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
              <span className="min-w-0 break-words">{errMsg}</span>
            </p>
          )}
        </form>

        <DialogFooter className="shrink-0">
          {step > 1 ? (
            <Button type="button" variant="outline" onClick={() => setStep((step - 1) as WizardStep)} disabled={pending}>
              {t("agents.form.wizard.actions.back")}
            </Button>
          ) : (
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
              {tc("actions.cancel")}
            </Button>
          )}
          {step < totalSteps ? (
            <Button
              type="button"
              onClick={() => tryAdvance((step + 1) as WizardStep)}
              disabled={pending || (step === 1 && !step1Valid)}
            >
              {t("agents.form.wizard.actions.next")}
            </Button>
          ) : (
            <Button
              type="button"
              onClick={() => submit()}
              disabled={!canSubmit}
            >
              {pending ? tc("states.loading") : mode === "edit" ? t("agents.form.submit.edit") : t("agents.form.submit.create")}
            </Button>
          )}
        </DialogFooter>
        {workspaceID && (
          <PairDaemonDialog
            open={pairDialogOpen}
            onClose={() => setPairDialogOpen(false)}
            workspaceID={workspaceID}
            onPaired={(runtimeID) => {
              setDeviceID(runtimeID)
              // DevicePicker's list query doesn't poll; nudge it so the freshly
              // paired daemon shows up without waiting for the next mount.
              void queryClient.invalidateQueries({ queryKey: ["admin", "runtimes", workspaceID] })
            }}
          />
        )}
      </DialogContent>
    </Dialog>
    </>
  )
}

interface WizardProgressProps {
  step: WizardStep
  totalSteps: number
  progressPercent?: number
  title: string
  summary: string
  stepOfLabel: string
  completeLabel: string
}
function WizardProgress({
  step,
  totalSteps,
  title,
  summary,
  stepOfLabel,
  completeLabel,
}: WizardProgressProps) {
  return (
    <div className="flex shrink-0 items-baseline justify-between gap-2 border-b border-line pb-3">
      <h2 className="text-sm font-medium text-fg">{title}</h2>
      <p className="text-xs tabular-nums text-fg-muted">{stepOfLabel}</p>
      <div className="sr-only" role="progressbar" aria-valuemin={0} aria-valuemax={totalSteps} aria-valuenow={step} aria-valuetext={`${completeLabel} · ${summary}`} />
    </div>
  )
}

interface FieldProps {
  label: ReactNode
  htmlFor?: string
  children: ReactNode
  required?: boolean
  hint?: string
  error?: string
}
const Field = forwardRef<HTMLDivElement, FieldProps>(function Field(
  { label, htmlFor, children, required, hint, error },
  ref
) {
  return (
    <div ref={ref} className="flex flex-col">
      <Label htmlFor={htmlFor}>
        {label}{required && <span aria-hidden="true"> *</span>}
      </Label>
      {children}
      {hint && <span className="mt-1 text-xs text-fg-muted">{hint}</span>}
      {error && (
        <span className="mt-1 flex items-start gap-1.5 text-xs text-fg" role="alert">
          <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0 text-status-failed" strokeWidth={1.5} aria-hidden="true" />
          {error}
        </span>
      )}
    </div>
  )
})
/** One option of a radio group: the choice word in ink, its description muted. */
function ChoiceCard({ name, title, description, selected, onSelect, disabled = false }: { name: string; title: string; description?: string; selected: boolean; onSelect: () => void; disabled?: boolean }) {
  return (
    <label className={cn("flex items-start gap-2 py-1 text-sm text-fg", disabled ? "cursor-not-allowed opacity-50" : "cursor-pointer")}>
      <input
        type="radio"
        name={name}
        className="mt-0.5 h-3.5 w-3.5 shrink-0 accent-accent"
        checked={selected}
        disabled={disabled}
        onChange={() => { if (!disabled) onSelect() }}
      />
      <span className="min-w-0">
        <span className={cn("block", selected && "font-medium")}>{title}</span>
        {description && <span className="block text-xs text-fg-muted">{description}</span>}
      </span>
    </label>
  )
}

/**
 * CapabilityVersionPicker renders the per-binding version dropdown shown
 * next to a checked capability row. The "latest" option is a sentinel
 * that means "track this capability's newest version at every dispatch"
 * — a re-upload of the capability flows through without any further
 * user action. Selecting a specific version (e.g. "v1.0.6") flips the
 * binding to pinned mode at that version_id.
 *
 * Marketplace capabilities still get a working dropdown but the default
 * stays "pinned" on a known good version: marketplace publishes may
 * carry breaking changes, so the existing UpgradeCapabilityDialog flow
 * (which prompts on every new major) stays the recommended path. Users
 * who explicitly opt in to "latest" via this dropdown are accepting the
 * auto-follow tradeoff.
 *
 * On mount the picker lazily fetches the capability's full version list
 * (the parent has the latestVersion/latestVersionID only). While the
 * list is loading we still render the choice that's already in state so
 * the dropdown never collapses to empty mid-edit.
 */
function CapabilityVersionPicker({
  label,
  capabilityID,
  fromMarketplace,
  workspaceID,
  latestVersionID,
  latestVersion,
  choice,
  onChange,
}: {
  label: string
  capabilityID: string
  fromMarketplace: boolean
  workspaceID: string | null
  latestVersionID: string
  latestVersion: string
  choice: { pinningMode: "latest" | "pinned"; versionID: string; pinnedVersion?: string } | undefined
  onChange: (next: { pinningMode: "latest" | "pinned"; versionID: string; pinnedVersion?: string }) => void
}) {
  const { t } = useTranslation("admin")
  const versionsQ = useCapabilityVersionsQuery(workspaceID, capabilityID)
  const versions = versionsQ.data?.versions ?? []

  // selectValue is the dropdown's current option. "__latest__" is the
  // sentinel for latest-mode; anything else is a concrete version_id.
  const selectValue = choice?.pinningMode === "latest"
    ? "__latest__"
    : choice?.versionID ?? "__latest__"

  const handleChange = (value: string) => {
    if (value === "__latest__") {
      onChange({ pinningMode: "latest", versionID: latestVersionID, pinnedVersion: undefined })
      return
    }
    // Find the version literal for the chosen id so the parent can
    // render the correct number on subsequent picker rendering passes.
    const picked = versions.find((v) => v.id === value)
    onChange({ pinningMode: "pinned", versionID: value, pinnedVersion: picked?.version })
  }

  return (
    <Select
      aria-label={label}
      value={selectValue}
      onValueChange={handleChange}
      onClick={(event) => event.stopPropagation()}
      // Marketplace bindings see the same picker but the cue (label
      // suffix) reminds the user that breaking changes can land
      // without warning. We don't disable "latest" outright — opting
      // into it is a deliberate user choice we surface explicitly.
      wrapperClassName="ml-1 w-auto shrink-0"
      className="w-auto font-mono text-xs"
      title={fromMarketplace ? t("agents.form.versionPicker.marketplaceHint") : t("agents.form.versionPicker.localHint")}
    >
      <SelectOption value="__latest__">
        {t("agents.form.versionPicker.latest")}{latestVersion ? ` (v${latestVersion})` : ""}
      </SelectOption>
      {versions.length === 0 && choice?.pinningMode === "pinned" && choice.versionID && (
        // Versions list still loading — keep the current pin visible
        // so the dropdown doesn't appear to forget the user's choice
        // until the network round-trip lands. choice.pinnedVersion
        // (hydrated from binding.version on edit, set by handleChange
        // on user pick) carries the literal for the PINNED row — never
        // fall back to latestVersion here, which would mis-label the
        // pinned option with whatever the capability's newest version
        // happens to be.
        <SelectOption value={choice.versionID}>v{choice.pinnedVersion || "?"}</SelectOption>
      )}
      {versions.map((version) => (
        <SelectOption key={version.id} value={version.id}>
          v{version.version}
        </SelectOption>
      ))}
    </Select>
  )
}
