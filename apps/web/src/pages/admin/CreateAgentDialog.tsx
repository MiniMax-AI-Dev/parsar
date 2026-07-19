import { Fragment, forwardRef, useEffect, useId, useMemo, useRef, useState, type ChangeEvent, type KeyboardEvent, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowUpRight, Bot, Check, ChevronDown, Cloud, Cpu, Eye, EyeOff, Laptop, Network, Search, Server, Sparkles } from "lucide-react"

import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import { DevicePicker } from "../../components/admin/DevicePicker"
import { PairDaemonDialog } from "../../components/admin/PairDaemonDialog"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import { Input } from "../../components/ui/input"
import { Tabs, TabsList, TabsTrigger } from "../../components/ui/tabs"
import { ApiError } from "../../lib/api-client"
import { modelProtocol, protocolLabel, type WireProtocol } from "../../lib/model-protocol"
import { useCapabilitiesQuery, aggregateRequiredCredentials, aggregateRequiredCredentialsByID, useCapabilityVersionsQuery, useAgentCapabilitiesQuery, useEnableAgentCapabilityMutation } from "../../lib/api-capabilities"
import { CredentialCheckPanel } from "../../components/admin/CredentialCheckPanel"
import { useSecrets } from "../../lib/api-secrets"
import { useRuntimeStatus } from "../../lib/api-runtime"
import type { UpdateAgentProfileRequest } from "../../lib/api-agents"
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

const DEFAULT_PROMPT = "You are a helpful AI assistant for this team. Be concise and accurate."
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

function sandboxSizeFromAgent(a?: Agent | null): SandboxSize {
  // The server reads sandbox_size from the same merged config map at sandbox
  // cold-start time, so we keep the UI and the runtime view in sync.
  const v = String(agentConfig(a).sandbox_size ?? "standard")
  return v === "xl" ? "xl" : "standard"
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

function promptFromAgent(a?: Agent | null): string {
  const cfg = agentConfig(a)
  return String(cfg.system_prompt ?? DEFAULT_PROMPT)
}

function capabilitiesFromAgent(a?: Agent | null): string[] {
  const cfg = agentConfig(a)
  const profile = profileConfig(a)
  const caps = cfg.capabilities ?? profile.capabilities ?? profile.skills
  return Array.isArray(caps) ? caps.filter((v): v is string => typeof v === "string") : []
}

function runtimeFromAgent(a?: Agent | null): RuntimeChoice {
  // Legacy rows predating the per-agent runtime field default to "sandbox",
  // matching the migration backfill so server and UI agree.
  return a?.runtime ?? "sandbox"
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
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [executionMode, setExecutionMode] = useState<ExecutionMode>("local_device")
  const [agentEngine, setAgentEngine] = useState<AgentEngine>("claude_code")
  const [sandboxSize, setSandboxSize] = useState<SandboxSize>("standard")
  const [modelID, setModelID] = useState("")
  const [modelSearch, setModelSearch] = useState("")
  const [modelDropdownOpen, setModelDropdownOpen] = useState(false)
  const [highlightedModelID, setHighlightedModelID] = useState<string | null>(null)
  const [systemPrompt, setSystemPrompt] = useState(DEFAULT_PROMPT)
  const [capabilities, setCapabilities] = useState<string[]>([])
  const [selectedCapabilityIDs, setSelectedCapabilityIDs] = useState<string[]>([])
  // capabilityVersionChoices keys on capability_id and stores the user's
  // per-binding version + mode pick. The dropdown default is "latest"
  // (tracks reuploads at dispatch time); switching to a specific version
  // record sets mode="pinned" + versionID=<that version>. Used by both
  // create and edit. In edit mode we hydrate it from the existing
  // bindings so the dropdown shows the agent's current state.
  //
  // pinnedVersion (optional) caches the version literal (e.g. "1.0.3")
  // matching versionID. Carries forward through hydration so the
  // picker's loading-state fallback option renders the right number
  // instead of mis-labelling the pinned row with the latest version.
  const [capabilityVersionChoices, setCapabilityVersionChoices] = useState<Record<string, { pinningMode: "latest" | "pinned"; versionID: string; pinnedVersion?: string }>>({})
  const [visibility, setVisibility] = useState<"workspace" | "tenant" | "public">("public")
  const [capabilitySearch, setCapabilitySearch] = useState("")
  const [capabilityTypeFilter, setCapabilityTypeFilter] = useState<"all" | "mcp" | "skill" | "plugin" | "system_prompt">("all")
  const [deviceID, setDeviceID] = useState("")
  const [workDir, setWorkDir] = useState("")
  const [pairDialogOpen, setPairDialogOpen] = useState(false)
  const [submitAttempted, setSubmitAttempted] = useState(false)
  const [allCredentialsSatisfied, setAllCredentialsSatisfied] = useState(true)
  const [credentialBindings, setCredentialBindings] = useState<Record<string, { source: "shared"; secret_id: string }>>({})
  // Frozen snapshot of the bindings the agent was opened with (edit mode).
  // CredentialCheckPanel hydrates from this — NOT from credentialBindings —
  // because credentialBindings changes every time the panel reports up,
  // which would re-fire the hydration effect and loop (React error #185).
  const [initialCredentialBindings, setInitialCredentialBindings] = useState<Record<string, { source: "shared"; secret_id: string }> | undefined>(undefined)
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

  const capabilitiesQ = useCapabilitiesQuery(workspaceID, capabilitySearch)
  const allCapabilitiesQ = useCapabilitiesQuery(workspaceID, "")
  // In edit mode, fetch existing per-agent bindings so we can
  // hydrate the version dropdowns with the current pinning_mode +
  // capability_version_id. In create mode the response is empty.
  const existingBindingsQ = useAgentCapabilitiesQuery(workspaceID, mode === "edit" ? agent?.id ?? null : null)
  const enableBindingMut = useEnableAgentCapabilityMutation(workspaceID, agent?.id ?? null)
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
    activeModels.find((m) => engineSupportsProtocol(agentEngine, modelProtocol(m)))?.id ??
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
    const installedCaps = capabilitiesQ.data?.marketplace_installs ?? []
    const availableCaps = capabilitiesQ.data?.marketplace_available ?? []
    const workspace: PickerOption[] = [...ownCaps, ...installedCaps].map((cap) => ({
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
    // Ghost bindings (edit mode): when an admin deprecates a capability the
    // agent still binds, ListCapabilities hides it and the row would silently
    // vanish from the picker. Merge it back as a disabled row so the user can
    // deliberately unbind. The agent profile only stores names (not types),
    // so type is left empty and treated as wildcard downstream.
    if (mode === "edit") {
      const known = new Set(live.map((c) => c.name))
      for (const name of capabilities) {
        if (!known.has(name)) {
          live.push({
            id: `ghost:${name}`,
            name,
            type: "",
            description: "",
            latestVersionID: "",
            latestVersion: "",
            deprecated: true,
            section: "workspace",
            requiredCredentials: [],
          })
        }
      }
    }
    return live
  }, [capabilitiesQ.data, mode, capabilities])
  const capabilityTypeCounts = useMemo(() => {
    // Ghost rows have unknown type, so they're excluded from per-type tallies
    // (still count toward "all").
    const counts = { all: capabilityOptions.length, mcp: 0, skill: 0, plugin: 0, system_prompt: 0 }
    for (const cap of capabilityOptions) {
      if (cap.deprecated) continue
      if (cap.type === "mcp") counts.mcp++
      else if (cap.type === "skill") counts.skill++
      else if (cap.type === "plugin") counts.plugin++
      else if (cap.type === "system_prompt") counts.system_prompt++
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
      if (!engineSupportsProtocol(agentEngine, modelProtocol(m))) out.add(m.id)
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
    const installed = data?.marketplace_installs ?? []
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
    return [...own, ...installed, ...available]
  }, [allCapabilitiesQ.data])
  const aggregatedRequiredKinds = useMemo(
    () => mode === "create"
      ? aggregateRequiredCredentialsByID(selectedCapabilityIDs, allCapabilitiesPool)
      : aggregateRequiredCredentials(capabilities, allCapabilitiesPool),
    [capabilities, mode, selectedCapabilityIDs, allCapabilitiesPool]
  )
  const admin = isAdminRole(workspaceRole)

  const modelFieldRef = useRef<HTMLDivElement | null>(null)
  const modelComboboxRef = useRef<HTMLDivElement | null>(null)
  const modelListboxID = useId()
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
      setExecutionMode(cloneSource ? executionModeFromAgent(cloneSource) : "local_device")
      setAgentEngine(cloneSource ? agentEngineFromAgent(cloneSource) : "claude_code")
      setSandboxSize(cloneSource ? sandboxSizeFromAgent(cloneSource) : "standard")
      setModelID(cloneSource ? modelIDFromAgent(cloneSource) : "")
      setModelSearch("")
      setModelDropdownOpen(false)
      setHighlightedModelID(null)
      setSystemPrompt(params.get("agent_prompt") ?? (cloneSource ? promptFromAgent(cloneSource) : DEFAULT_PROMPT))
      setCapabilities(cloneSource ? capabilitiesFromAgent(cloneSource) : [])
      // Capability IDs aren't prefilled on clone: mapping installed names back
      // to IDs needs an extra round-trip, so users re-pick from the marketplace.
      setSelectedCapabilityIDs([])
      setCapabilityVersionChoices({})
      setCapabilitySearch("")
      // Creation is locked to public; the visibility selector is hidden.
      setVisibility("public")
      setDeviceID(cloneSource ? deviceIDFromAgent(cloneSource) : "")
      setWorkDir(cloneSource ? workDirFromAgent(cloneSource) || DEFAULT_WORK_DIR : DEFAULT_WORK_DIR)
      // Clones explicitly drop the source agent's credential bindings: the
      // copy is a fresh agent and the user re-picks. Without these resets a
      // previous clone's bindings would leak across dialog opens.
      setCredentialBindings({})
      setInitialCredentialBindings(undefined)
      // Create is locked to public, so personal binding is disabled — start on
      // shared. Left as a pending pick here (secrets may still be loading); the
      // resolver effect below upgrades it to the first existing secret if any.
      setModelBindingChoice({ source: "shared" })
      setInlineNewSecrets([])
    } else if (agent) {
      setName(agent.name)
      setDescription(agent.description ?? "")
      setExecutionMode(executionModeFromAgent(agent))
      setAgentEngine(agentEngineFromAgent(agent))
      setSandboxSize(sandboxSizeFromAgent(agent))
      setModelID(modelIDFromAgent(agent) || firstModelID)
      setModelSearch("")
      setModelDropdownOpen(false)
      setHighlightedModelID(null)
      setSystemPrompt(promptFromAgent(agent))
      setCapabilities(capabilitiesFromAgent(agent))
      setSelectedCapabilityIDs([])
      // capabilityVersionChoices for edit mode is hydrated by a separate
      // useEffect once existingBindingsQ resolves — the hook may still be
      // loading when this open-effect runs.
      setCapabilityVersionChoices({})
      setCapabilitySearch("")
      setVisibility(agent.visibility ?? "workspace")
      setDeviceID(deviceIDFromAgent(agent))
      setWorkDir(workDirFromAgent(agent))
      // Hydrate credential bindings from agent_config so step 3 shows what
      // the agent already has wired up. Only shared+secret_id is recoverable
      // — pasted-token (`new_secret`) is ephemeral and never round-trips.
      const ac = agentConfig(agent)
      const hydrated: Record<string, { source: "shared"; secret_id: string }> = {}
      const rawBindings = (ac.credential_bindings ?? {}) as Record<string, unknown>
      for (const [kind, raw] of Object.entries(rawBindings)) {
        const o = raw as { source?: string; secret_id?: string }
        if (o?.source === "shared" && typeof o.secret_id === "string" && o.secret_id) {
          hydrated[kind] = { source: "shared", secret_id: o.secret_id }
        }
      }
      setCredentialBindings(hydrated)
      // Freeze the same payload for the panel to hydrate from. We keep two
      // states deliberately: credentialBindings tracks the live picker output
      // (changes on every panel onChange), while initialCredentialBindings is
      // immutable per dialog-open so panel's hydration effect doesn't fire
      // when the parent re-renders from setCredentialBindings.
      setInitialCredentialBindings(hydrated)
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
  }, [open, mode, agent?.id, defaultAgentDescription, defaultAgentName, firstModelID])

  // Hydrate edit-mode binding choices when the listAgentCapabilities
  // response lands. We only seed entries the user hasn't touched (i.e.
  // key not already present) so a hot reload of the bindings query won't
  // clobber an in-flight version pick.
  useEffect(() => {
    if (!open || mode !== "edit") return
    const installed = existingBindingsQ.data?.installed ?? []
    if (installed.length === 0) return
    setCapabilityVersionChoices((prev) => {
      const next = { ...prev }
      for (const binding of installed) {
        if (!binding.capability_id) continue
        if (next[binding.capability_id]) continue
        // pinning_mode is a fresh field; rows written before this change
        // default to "pinned" on the server side via the migration default.
        const pinningMode: "latest" | "pinned" = binding.pinning_mode === "latest" ? "latest" : "pinned"
        next[binding.capability_id] = {
          pinningMode,
          versionID: binding.capability_version_id,
          // binding.version is the pinned version literal; carry it so
          // CapabilityVersionPicker can label the loading-state option
          // with the correct version number instead of the capability's
          // latest version number.
          pinnedVersion: binding.version,
        }
      }
      return next
    })
  }, [open, mode, existingBindingsQ.data])

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

    if (event.key === "Escape") {
      event.preventDefault()
      setModelDropdownOpen(false)
      return
    }

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
  const requiresModel = connector !== "agent_daemon" || agentEngine === "claude_code" || agentEngine === "codex" || agentEngine === "pi"
  const selectedModelUnavailable = mode === "edit" && requiresModel && selectedModelID !== "" && selectedModel === null
  const hasRequiredModel = !requiresModel || (selectedModel !== null && !incompatibleModelIDs.has(selectedModel.id))
  // Create opens model binding on a pending "shared" pick because secrets may
  // still be loading; once they land, resolve to the first existing one. Gated
  // on the credential_ref UI so no-credential models stay untouched; with zero
  // secrets it stays pending and the inline new-secret form takes over.
  useEffect(() => {
    if (mode !== "create") return
    if (!requiresModel || selectedModel?.credential_mode !== "credential_ref") return
    if (modelBindingChoice.source !== "shared") return
    if ("existing_secret_id" in modelBindingChoice || "new_secret" in modelBindingChoice) return
    if (modelNewSecretExpanded || sharedSecrets.length === 0) return
    setModelBindingChoice({ source: "shared", existing_secret_id: sharedSecrets[0].id })
  }, [mode, requiresModel, selectedModel, modelBindingChoice, modelNewSecretExpanded, sharedSecrets])
  const daemonExecutionEditable = connector === "agent_daemon"
  const showExecutionChoices = mode === "create" || daemonExecutionEditable
  const showDevicePicker = connector === "agent_daemon" && executionMode === "local_device" && Boolean(workspaceID)
  const errMsg = extractErrorMessage(error)

  function prefillQuery(target: "models" | "connectors") {
    const url = new URL(window.location.href)
    url.searchParams.set("admin", target)
    url.searchParams.delete("id")
    url.searchParams.set("return_to", "agents.create")
    if (name.trim()) url.searchParams.set("agent_name", name.trim())
    if (description.trim()) url.searchParams.set("agent_description", description.trim())
    if (systemPrompt.trim()) url.searchParams.set("agent_prompt", systemPrompt.trim())
    return `${url.pathname}${url.search}${url.hash}`
  }

  function toggleCapability(cap: string, capabilityID?: string, latestVersionID?: string) {
    let wasChecked = false
    setCapabilities((prev) => {
      wasChecked = prev.includes(cap)
      return wasChecked ? prev.filter((c) => c !== cap) : [...prev, cap]
    })
    // Keep the per-binding version choice map in sync with the checkbox
    // so a freshly-checked row gets a "latest" default and an unchecked
    // row stops carrying stale dropdown state into the submit payload.
    // We read `wasChecked` (set inside the functional update above) so
    // both batches see consistent "before-this-toggle" state — relying
    // on the closure-captured `capabilities` instead would drift if
    // React batched two toggles in the same tick.
    if (capabilityID) {
      setCapabilityVersionChoices((prev) => {
        if (wasChecked) {
          // We're toggling OFF — drop the choice.
          const { [capabilityID]: _, ...rest } = prev
          return rest
        }
        // Toggling ON: default to latest with the latest version id as the
        // fallback "last seen" pointer the server stores.
        if (prev[capabilityID]) return prev
        return { ...prev, [capabilityID]: { pinningMode: "latest", versionID: latestVersionID ?? "" } }
      })
    }
  }

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

  async function submit() {
    setSubmitAttempted(true)
    if (!hasRequiredModel) {
      modelFieldRef.current?.scrollIntoView({ behavior: "smooth", block: "center" })
      return
    }
    if (!name.trim() || !hasConnector) return
    if (connector === "agent_daemon" && executionMode === "local_device" && !deviceID) return
    const trimmedWorkDir = workDir.trim()
    if (connector === "agent_daemon" && trimmedWorkDir !== "" && !trimmedWorkDir.startsWith("/")) {
      // The daemon also enforces absolute paths, but failing fast here gives
      // the user a clearer error tied to the input instead of a stream error.
      return
    }
    if (mode === "create" && allCapabilitiesQ.isLoading) return
    const selectedCapabilities = mode === "create"
      ? allCapabilitiesPool.filter((cap) => selectedCapabilityIDs.includes(cap.id) && cap.latest_version_id)
      : []
    const capabilityNames = mode === "create" ? selectedCapabilities.map((cap) => cap.name) : capabilities
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
      return { capability_version_id: versionID, pinning_mode: pinningMode }
    })
    const profile = {
      ...(requiresModel ? { model_id: selectedModelID } : {}),
      capabilities: capabilityNames,
      skills: capabilityNames,
    }
    const mergedConfig: Record<string, unknown> = {
      ...configBaseForSubmit(agent, connector),
      profile,
      ...(connector === "agent_daemon" ? {
        agent_kind: agentEngine,
        ...(executionMode === "sandbox" ? { daemon_mode: "sandbox", sandbox_size: sandboxSize } : {}),
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
      daemon_mode: executionMode === "sandbox" ? "sandbox" : "local",
      sandbox_size: executionMode === "sandbox" ? sandboxSize : null,
      device_id: executionMode === "local_device" ? deviceID : null,
      work_dir: trimmedWorkDir || null,
    }
    // Embed credential_bindings + model_credential_binding into the agent
    // config so the runtime resolver and visibility-bindings validator
    // both see them.
    //
    // Edit mode always emits both keys — including {} / null when the user
    // cleared all shared picks back to personal — so the backend cherry-pick
    // can persist the clear. In create mode there's no stored state to
    // override, so we omit empty payloads to keep the JSON tight.
    //
    // These belong to the agent config the update body carries, so we build
    // them on a separate object — the agentProfile request below
    // intentionally doesn't see them.
    const agentBodyConfig: Record<string, unknown> = { ...mergedConfig }
    if (mode === "edit" || Object.keys(credentialBindings).length > 0) {
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
      connector_type: connector,
      ...(requiresModel ? { default_model_id: selectedModelID } : {}),
      capabilities: capabilityNames,
      ...(mode === "create" ? { initial_capabilities: initialCapabilities, visibility } : {}),
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
    // In edit mode, sync per-binding pinning_mode / version against the
    // server BEFORE firing the main update. updateAgent + the server-
    // side syncAgentCapabilities don't touch capability_version_id or
    // pinning_mode on rows that already exist (the "don't auto-upgrade"
    // contract), so we have to call enable explicitly for each binding
    // the user changed. enable's upsert path takes care of the UPDATE
    // when the (agent, capability) row already exists.
    //
    // We MUST await every per-binding mutate before calling onSubmit:
    // syncAgentCapabilities racing with the per-binding enables would
    // see a "new" row for any binding the user just checked-and-pinned
    // and re-enable it with its own default mode, clobbering the
    // pinning_mode the user just picked. Sequencing here makes the
    // outcome deterministic regardless of network latency.
    if (mode === "edit" && agent?.id && workspaceID) {
      const installed = existingBindingsQ.data?.installed ?? []
      const existingByCapID = new Map<string, { versionID: string; mode: "latest" | "pinned" }>()
      for (const binding of installed) {
        if (!binding.capability_id) continue
        existingByCapID.set(binding.capability_id, {
          versionID: binding.capability_version_id,
          mode: binding.pinning_mode === "latest" ? "latest" : "pinned",
        })
      }
      const pendingSyncs: Array<Promise<unknown>> = []
      for (const [capabilityID, choice] of Object.entries(capabilityVersionChoices)) {
        // Only sync bindings the user kept selected. Unchecked bindings
        // get removed by the server-side syncAgentCapabilities through
        // the capabilities name list.
        if (!capabilities.includes(capabilityNameForID(capabilityID))) continue
        const existing = existingByCapID.get(capabilityID)
        const sameMode = existing?.mode === choice.pinningMode
        const sameVersion = choice.pinningMode === "latest"
          ? true // version_id is just a "last seen" pointer in latest mode; don't churn on it.
          : existing?.versionID === choice.versionID
        if (existing && sameMode && sameVersion) continue
        pendingSyncs.push(
          enableBindingMut.mutateAsync({
            capabilityVersionID: choice.versionID,
            pinningMode: choice.pinningMode,
          }).catch((err) => {
            // Don't abort the rest of the edit on one binding failing —
            // the mutation hook logs + surfaces via the toast layer.
            // We still need to swallow here so Promise.all below doesn't
            // reject and block the agent update.
            console.warn("enable binding mutate failed", { capabilityID, err })
            return null
          })
        )
      }
      if (pendingSyncs.length > 0) {
        try {
          await Promise.all(pendingSyncs)
        } catch {
          // Individual failures already swallowed above; this catch is
          // defence-in-depth in case Promise.all itself rejects.
        }
      }
    }
    onSubmit({ agentID: agent?.id, body, agentProfile })
  }

  // capabilityNameForID resolves capability_id → name from the
  // local pool, used by the edit-mode sync to skip choices whose
  // capability was unchecked (the name disappears from `capabilities`).
  function capabilityNameForID(capabilityID: string): string {
    const cap = allCapabilitiesPool.find((c) => c.id === capabilityID)
    return cap?.name ?? ""
  }

  const workDirTrimmed = workDir.trim()
  const workDirValid = connector !== "agent_daemon" || workDirTrimmed === "" || workDirTrimmed.startsWith("/")
  const canSubmit =
    !pending &&
    name.trim() !== "" &&
    hasConnector &&
    hasRequiredModel &&
    (connector !== "agent_daemon" || executionMode !== "local_device" || deviceID !== "") &&
    workDirValid &&
    (mode !== "create" || !allCapabilitiesQ.isLoading) &&
    (aggregatedRequiredKinds.length === 0 || allCredentialsSatisfied)

  const step1Valid =
    name.trim() !== "" &&
    hasConnector &&
    hasRequiredModel &&
    (connector !== "agent_daemon" || executionMode !== "local_device" || deviceID !== "") &&
    workDirValid
  const totalSteps = 2
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
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[86vh] flex-col overflow-hidden sm:max-w-2xl">
        <DialogHeader className="shrink-0">
          <DialogTitle>{mode === "edit" ? t("agents.form.title.edit") : t("agents.form.title.create")}</DialogTitle>
        </DialogHeader>

        <WizardProgress
          step={step}
          totalSteps={totalSteps}
          progressPercent={progressPercent}
          title={t(`agents.form.wizard.steps.${step === 1 ? "setup" : "capabilities"}.title` as never)}
          summary={t(`agents.form.wizard.steps.${step === 1 ? "setup" : "capabilities"}.summary` as never)}
          stepOfLabel={t("agents.form.wizard.stepOf", { current: step, total: totalSteps })}
          completeLabel={t("agents.form.wizard.complete", { percent: progressPercent })}
        />

        <form
          className="min-h-0 flex-1 space-y-5 overflow-y-auto overflow-x-hidden pr-1"
          onSubmit={(e) => {
            // Swallow accidental form submissions (e.g. Enter in a text input)
            // so the wizard never creates the agent from a wrong step.
            e.preventDefault()
          }}
        >
          {step === 1 && (
            <div className="space-y-5">
              <section className="space-y-3">
                <Field
                  label={t("agents.form.fields.name")}
                  required
                >
                  <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("agents.form.placeholders.name")} autoFocus />
                </Field>
                <Field label={t("agents.form.fields.description")}>
                  <Input value={description} onChange={(e) => setDescription(e.target.value)} placeholder={t("agents.form.placeholders.description")} />
                </Field>
              </section>
              <section className="flex flex-col space-y-3">
              {showExecutionChoices ? (
                <>
                  <Field label={t("agents.form.fields.executionMode")} required>
                    <div className={"grid gap-2 " + (mode === "create" ? "sm:grid-cols-3" : "sm:grid-cols-2")}>
                      <ChoiceCard
                        icon={<Laptop className="h-4 w-4" />}
                        title={t("agents.execution.localDevice.title")}
                        description={t("agents.execution.localDevice.description")}
                        selected={executionMode === "local_device"}
                        onSelect={() => setExecutionMode("local_device")}
                      />
                      <ChoiceCard
                        icon={<Cloud className="h-4 w-4" />}
                        title={t("agents.execution.sandbox.title")}
                        description={t("agents.execution.sandbox.description")}
                        selected={executionMode === "sandbox"}
                        onSelect={() => setExecutionMode("sandbox")}
                      />
                      {mode === "create" && (
                        <ChoiceCard
                          icon={<Network className="h-4 w-4" />}
                          title={t("agents.execution.external.title")}
                          description={t("agents.execution.external.description")}
                          selected={executionMode === "external"}
                          onSelect={() => setExecutionMode("external")}
                          disabled
                        />
                      )}
                    </div>
                  </Field>
                  {connector === "agent_daemon" && (
                    <Field label={t("agents.form.fields.agentEngine")} required>
                      <div className="grid gap-2 sm:grid-cols-2">
                        <ChoiceCard
                          icon={<Cpu className="h-4 w-4" />}
                          title={t("agents.engine.claudeCode.title")}
                          selected={agentEngine === "claude_code"}
                          onSelect={() => setAgentEngine("claude_code")}
                        />
                        <ChoiceCard
                          icon={<Bot className="h-4 w-4" />}
                          title={t("agents.engine.codex.title")}
                          selected={agentEngine === "codex"}
                          onSelect={() => setAgentEngine("codex")}
                        />
                        <ChoiceCard
                          icon={<Sparkles className="h-4 w-4" />}
                          title={t("agents.engine.pi.title")}
                          selected={agentEngine === "pi"}
                          onSelect={() => setAgentEngine("pi")}
                        />
                        <ChoiceCard
                          icon={<Server className="h-4 w-4" />}
                          title={t("agents.engine.opencode.title")}
                          selected={agentEngine === "opencode"}
                          onSelect={() => setAgentEngine("opencode")}
                          disabled
                        />
                      </div>
                    </Field>
                  )}
                  {connector === "agent_daemon" && executionMode === "sandbox" && (
                    <div className="space-y-3">
                      <Field
                        label={t("agents.form.fields.sandboxSize")}
                        hint={t("agents.form.sandboxSize.hint")}
                      >
                        <select
                          value={sandboxSize}
                          onChange={(e) => setSandboxSize(e.target.value === "xl" ? "xl" : "standard")}
                          disabled={pending}
                          className="h-9 rounded-md border border-line bg-surface px-3 text-sm shadow-sm focus:outline-none focus:ring-2 focus:ring-line-strong disabled:cursor-not-allowed disabled:bg-surface-subtle"
                        >
                          <option value="standard">{t("agents.form.sandboxSize.standard")}</option>
                          <option value="xl">{t("agents.form.sandboxSize.xl")}</option>
                        </select>
                      </Field>
                      {runtimeStatus.data?.sandbox_image ? (
                        <Field label={t("agents.form.fields.sandboxImage")} hint={t("agents.form.sandboxImage.hint")}>
                          <code className="block break-all rounded-md border border-line bg-surface-subtle px-3 py-2 text-xs text-fg-muted">
                            {runtimeStatus.data.sandbox_image}
                          </code>
                        </Field>
                      ) : null}
                    </div>
                  )}
                </>
              ) : (
                <Field label={t("agents.form.fields.executionMode")} required>
                  <div className="flex h-9 items-center rounded-md border border-line bg-surface-subtle px-3 text-sm text-fg-muted">
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
                  hint={t(executionMode === "sandbox" ? "agents.form.workDir.hintSandbox" : "agents.form.workDir.hintLocal")}
                  error={submitAttempted && workDir.trim() !== "" && !workDir.trim().startsWith("/") ? t("agents.form.errors.workDirAbsolute") : undefined}
                >
                  <Input
                    value={workDir}
                    onChange={(e) => setWorkDir(e.target.value)}
                    placeholder={t("agents.form.workDir.placeholder")}
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
                    required
                    error={selectedModelUnavailable
                      ? t("agents.form.errors.modelUnavailable")
                      : submitAttempted && !hasRequiredModel
                        ? t("agents.form.errors.modelRequired")
                        : undefined}
                  >
                {hasModel ? (
                  <div ref={modelComboboxRef} className="relative">
                    <Search className="pointer-events-none absolute left-2.5 top-1/2 z-10 h-3.5 w-3.5 -translate-y-1/2 text-fg-faint" />
                    <Input
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
                      className="pr-9 pl-8"
                      placeholder={t("agents.form.placeholders.modelSearch")}
                    />
                    <ChevronDown className={"pointer-events-none absolute right-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint transition-transform " + (modelDropdownOpen ? "rotate-180" : "")} />
                    {modelDropdownOpen && (
                      <div
                        id={modelListboxID}
                        role="listbox"
                        className="absolute z-50 mt-1 max-h-52 w-full overflow-y-auto rounded-md border border-line bg-surface py-1 text-sm shadow-lg"
                      >
                        {filteredModels.length === 0 ? (
                          <div className="px-3 py-2 text-sm text-fg-subtle">{t("agents.form.emptyModelSearch")}</div>
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
                              className={"flex w-full items-center justify-between gap-3 px-3 py-2 text-left " + (incompatible ? "cursor-not-allowed opacity-40" : highlighted ? "bg-surface-muted text-fg" : selected ? "bg-surface-subtle text-fg" : "text-fg-muted hover:bg-surface-subtle")}
                            >
                              <span className="min-w-0 flex-1 truncate">{modelLabel(m)}</span>
                              <span className="flex shrink-0 items-center gap-2">
                                {/* Protocol badge on every row (compatible or
                                    not) so the user can see each model's wire
                                    protocol at a glance. */}
                                <span className="text-xs text-fg-faint">
                                  {protocolLabel(modelProtocol(m))}
                                </span>
                                {selected && !incompatible && (
                                  <span className="inline-flex items-center gap-1 text-xs text-fg-subtle">
                                    <Check className="h-3.5 w-3.5" />
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
                ) : (
                  <DependencyCard title={t("agents.form.emptyModel.title")} description={t("agents.form.emptyModel.description")} href={prefillQuery("models")} cta={t("agents.form.emptyModel.cta")} />
                )}
                  </Field>
                </div>
              )}
              {requiresModel && selectedModel && selectedModel.credential_mode === "credential_ref" && (
                <Field label={t("credentialCheck.modelBindingTitle")}>
                  <div className="space-y-2 rounded-md border border-line bg-surface p-3">
                    <label className={`flex items-start gap-2 ${visibility === "public" ? "cursor-not-allowed opacity-50" : "cursor-pointer"}`}>
                      <input
                        type="radio"
                        name="model-binding"
                        className="mt-0.5"
                        checked={modelBindingChoice.source === "personal"}
                        disabled={visibility === "public"}
                        onChange={() => setModelBindingChoice({ source: "personal" })}
                      />
                      <span className="text-sm">
                        <span className="block text-fg-emphasis">{t("credentialCheck.modelBindingPersonal")}</span>
                        {visibility === "public" && (
                          <span className="block text-xs text-fg-subtle">{t("credentialCheck.personalDisabledHint")}</span>
                        )}
                      </span>
                    </label>
                    <label className="flex items-start gap-2 cursor-pointer">
                      <input
                        type="radio"
                        name="model-binding"
                        className="mt-0.5"
                        checked={modelBindingChoice.source === "shared"}
                        onChange={() => {
                          // Default selection on flip-in: existing secret if any, else
                          // mark shared "pending" so the radio selects + form renders.
                          if (sharedSecrets[0]) {
                            setModelBindingChoice({ source: "shared", existing_secret_id: sharedSecrets[0].id })
                          } else {
                            setModelBindingChoice({ source: "shared" })
                            setModelNewSecretExpanded(true)
                            setModelNewSecretDisplayName("")
                            setModelNewSecretPlaintext("")
                          }
                        }}
                      />
                      <span className="flex-1 text-sm">
                        <span className="block text-fg-emphasis">{t("credentialCheck.modelBindingShared")}</span>
                        {modelBindingChoice.source === "shared" && (
                          <div className="mt-1 space-y-1.5">
                            {sharedSecrets.length > 0 && (
                              <select
                                value={"existing_secret_id" in modelBindingChoice ? modelBindingChoice.existing_secret_id : "__new__"}
                                onChange={(e) => {
                                  e.stopPropagation()
                                  if (e.target.value === "__new__") {
                                    setModelNewSecretExpanded(true)
                                    if (!("new_secret" in modelBindingChoice)) {
                                      setModelNewSecretDisplayName("")
                                      setModelNewSecretPlaintext("")
                                    }
                                  } else {
                                    setModelBindingChoice({ source: "shared", existing_secret_id: e.target.value })
                                    setModelNewSecretExpanded(false)
                                  }
                                }}
                                onClick={(e) => e.stopPropagation()}
                                className="h-7 w-full rounded border border-line bg-surface px-2 text-sm"
                              >
                                {sharedSecrets.map((s) => (
                                  <option key={s.id} value={s.id}>{s.name}</option>
                                ))}
                                <option value="__new__">{t("credentialCheck.createNewShared")}</option>
                              </select>
                            )}
                            {sharedSecrets.length === 0 && !modelNewSecretExpanded && !("new_secret" in modelBindingChoice) && (
                              <button
                                type="button"
                                className="inline-flex h-7 items-center gap-1 rounded border border-dashed border-line-strong px-2 text-sm text-fg-muted hover:bg-surface-subtle"
                                onClick={(e) => {
                                  e.preventDefault()
                                  e.stopPropagation()
                                  setModelNewSecretExpanded(true)
                                  setModelNewSecretDisplayName("")
                                  setModelNewSecretPlaintext("")
                                }}
                              >
                                <Check className="h-3 w-3" />
                                {t("credentialCheck.createNewShared")}
                              </button>
                            )}
                            {"new_secret" in modelBindingChoice && !modelNewSecretExpanded && (
                              <div className="flex items-center gap-2 rounded border border-success-border bg-success-subtle px-2 py-1 text-xs text-success-emphasis">
                                <Check className="h-3 w-3 shrink-0" />
                                <span className="flex-1 truncate">
                                  {t("credentialCheck.sharedNewQueued", { name: modelBindingChoice.new_secret.display_name || t("credentialCheck.modelBindingTitle") })}
                                </span>
                                <button
                                  type="button"
                                  className="text-fg-muted underline"
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
                              <div className="space-y-2 rounded border border-line bg-surface-subtle p-2" onClick={(e) => e.stopPropagation()}>
                                <div className="grid gap-1">
                                  <label className="text-xs font-medium text-fg-muted">{t("credentialCheck.form.displayName")}</label>
                                  <Input
                                    value={modelNewSecretDisplayName}
                                    onChange={(e) => setModelNewSecretDisplayName(e.target.value)}
                                    placeholder={selectedModel?.name ?? t("credentialCheck.modelBindingTitle")}
                                    className="h-7 text-sm"
                                  />
                                </div>
                                <div className="grid gap-1">
                                  <label className="text-xs font-medium text-fg-muted">
                                    {t("credentialCheck.form.value")}
                                    <span className="ml-0.5 text-danger">*</span>
                                  </label>
                                  <div className="relative">
                                    <Input
                                      type={modelNewSecretShowPlaintext ? "text" : "password"}
                                      value={modelNewSecretPlaintext}
                                      onChange={(e) => setModelNewSecretPlaintext(e.target.value)}
                                      placeholder="sk-..."
                                      className="h-7 pr-8 text-sm"
                                    />
                                    <button
                                      type="button"
                                      onClick={() => setModelNewSecretShowPlaintext(!modelNewSecretShowPlaintext)}
                                      className="absolute right-2 top-1/2 -translate-y-1/2 text-fg-faint hover:text-fg-muted"
                                    >
                                      {modelNewSecretShowPlaintext ? <EyeOff className="h-3 w-3" /> : <Eye className="h-3 w-3" />}
                                    </button>
                                  </div>
                                </div>
                                <div className="flex justify-end gap-2">
                                  <Button
                                    type="button"
                                    variant="ghost"
                                    size="sm"
                                    className="h-6 text-xs"
                                    onClick={() => {
                                      setModelNewSecretExpanded(false)
                                      // If the user cancels and no existing secret has been chosen yet,
                                      // fall the binding back to an existing one (if any) or to personal,
                                      // so we don't leave the shared radio "selected with nothing inside".
                                      if (!("new_secret" in modelBindingChoice)) {
                                        if (sharedSecrets[0]) {
                                          setModelBindingChoice({ source: "shared", existing_secret_id: sharedSecrets[0].id })
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
                                    className="h-6 text-xs"
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

          {step === 2 && (
            <>
              <section className="space-y-3">
                <Input value={capabilitySearch} onChange={(e) => setCapabilitySearch(e.target.value)} placeholder={t("agents.form.placeholders.capabilitySearch")} />
                {capabilityOptions.length === 0 ? (
                  <p className="rounded-md bg-surface-subtle px-3 py-2 text-sm text-fg-subtle">
                    {admin ? t("agents.form.noTagsAdmin") : t("agents.form.noTagsMember")}
                  </p>
                ) : (
                  <>
                    <Tabs value={capabilityTypeFilter} onValueChange={(v) => setCapabilityTypeFilter(v as typeof capabilityTypeFilter)}>
                      <TabsList>
                        <TabsTrigger value="all">{t("agents.form.capabilityTypeTabs.all")} ({capabilityTypeCounts.all})</TabsTrigger>
                        <TabsTrigger value="mcp">{t("agents.form.capabilityTypeTabs.mcp")} ({capabilityTypeCounts.mcp})</TabsTrigger>
                        <TabsTrigger value="skill">{t("agents.form.capabilityTypeTabs.skill")} ({capabilityTypeCounts.skill})</TabsTrigger>
                        <TabsTrigger value="plugin">{t("agents.form.capabilityTypeTabs.plugin")} ({capabilityTypeCounts.plugin})</TabsTrigger>
                        <TabsTrigger value="system_prompt">{t("agents.form.capabilityTypeTabs.systemPrompt")} ({capabilityTypeCounts.system_prompt})</TabsTrigger>
                      </TabsList>
                    </Tabs>
                    <div className="max-h-56 overflow-y-auto rounded-md border border-line bg-surface">
                      {visibleCapabilityOptions.length === 0 ? (
                        <p className="px-3 py-2 text-sm text-fg-subtle">{tc("states.noResults")}</p>
                      ) : (() => {
                        const sections = (["workspace", "marketplace"] as const)
                          .map((sec) => ({ sec, rows: visibleCapabilityOptions.filter((o) => o.section === sec) }))
                          .filter((g) => g.rows.length > 0)
                        let rowCounter = 0
                        return sections.map(({ sec, rows }, sectionIdx) => (
                          <Fragment key={sec}>
                            <div className={"px-3 py-1.5 text-xs font-semibold uppercase tracking-wider text-fg-subtle bg-surface-subtle" + (sectionIdx > 0 ? " border-t border-line" : "")}>
                              {t(`agents.form.capabilitySections.${sec}`)}
                            </div>
                            {rows.map((cap) => {
                              const index = rowCounter++
                              const checked = mode === "create" ? selectedCapabilityIDs.includes(cap.id) : capabilities.includes(cap.name)
                              const lockedNoVersion = mode === "create" && !cap.latestVersionID
                              const lockedDeprecatedAndUnchecked = cap.deprecated && !checked
                              const disabled = lockedNoVersion || lockedDeprecatedAndUnchecked
                              const ghostTitle = cap.deprecated ? t("agents.form.deprecatedCapabilityTooltip") : undefined
                              return (
                                <label key={`${sec}:${cap.id || cap.name}`} title={ghostTitle} className={"flex w-full min-w-0 items-start gap-3 px-3 py-2 text-left " + (disabled ? "cursor-not-allowed bg-surface-subtle text-fg-faint" : "cursor-pointer hover:bg-surface-subtle") + (index > 0 ? " border-t border-line-muted" : "")}>
                                  <input
                                    type="checkbox"
                                    className="mt-0.5 h-4 w-4 shrink-0"
                                    checked={checked}
                                    disabled={disabled}
                                    onChange={() => mode === "create" ? toggleInitialCapability(cap.id, cap.latestVersionID) : toggleCapability(cap.name, cap.id, cap.latestVersionID)}
                                  />
                                  <span className="min-w-0 flex-1">
                                    <span className="flex min-w-0 items-center gap-2">
                                      <span className={"min-w-0 flex-1 truncate text-sm font-medium leading-4 " + (cap.deprecated ? "text-fg-subtle" : "text-fg")}>{cap.name}</span>
                                      {cap.type && !cap.deprecated && <span className="shrink-0"><Badge variant="neutral">{cap.type}</Badge></span>}
                                      {sec === "marketplace" && !cap.deprecated && <span className="shrink-0"><Badge variant="neutral">{t("agents.form.capabilityBadges.marketplace")}</Badge></span>}
                                      {cap.deprecated && <span className="shrink-0"><Badge variant="warning">{t("agents.form.deprecatedCapabilityBadge")}</Badge></span>}
                                      {!cap.deprecated && !checked && cap.latestVersion && <span className="shrink-0"><Badge variant="primary">v{cap.latestVersion}</Badge></span>}
                                      {!cap.deprecated && !checked && !cap.latestVersion && <span className="shrink-0"><Badge variant="warning">{t("agents.form.noCapabilityVersion")}</Badge></span>}
                                      {!cap.deprecated && checked && cap.id && (
                                        <CapabilityVersionPicker
                                          capabilityID={cap.id}
                                          fromMarketplace={sec === "marketplace"}
                                          workspaceID={workspaceID}
                                          latestVersionID={cap.latestVersionID}
                                          latestVersion={cap.latestVersion}
                                          choice={capabilityVersionChoices[cap.id]}
                                          onChange={(next) => setCapabilityVersionChoice(cap.id, next)}
                                        />
                                      )}
                                    </span>
                                    {cap.description && !cap.deprecated && <span className="mt-0.5 block truncate text-sm leading-4 text-fg-subtle">{cap.description}</span>}
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

              {aggregatedRequiredKinds.length > 0 && (
                <section className="space-y-3">
                  <h3 className="text-base font-semibold text-fg">{t("agents.form.sections.credentials")}</h3>
                  <CredentialCheckPanel
                    requiredKinds={aggregatedRequiredKinds}
                    workspaceID={workspaceID}
                    sharedSecrets={sharedSecrets}
                    visibility={visibility}
                    initialBindings={mode === "edit" ? initialCredentialBindings : undefined}
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

          {errMsg && <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">{errMsg}</p>}
        </form>

        <DialogFooter className="shrink-0 border-t border-line-muted pt-4">
          {step > 1 ? (
            <Button type="button" variant="outline" size="sm" onClick={() => setStep((step - 1) as WizardStep)} disabled={pending}>
              {t("agents.form.wizard.actions.back")}
            </Button>
          ) : (
            <Button type="button" variant="outline" size="sm" onClick={() => onOpenChange(false)} disabled={pending}>
              {tc("actions.cancel")}
            </Button>
          )}
          {step < totalSteps ? (
            <Button
              type="button"
              size="sm"
              className="bg-success text-white hover:bg-success"
              onClick={() => tryAdvance((step + 1) as WizardStep)}
              disabled={pending || (step === 1 && !step1Valid)}
            >
              {t("agents.form.wizard.actions.next")}
            </Button>
          ) : (
            <Button
              type="button"
              size="sm"
              className="bg-success text-white hover:bg-success"
              onClick={() => submit()}
              disabled={!canSubmit}
            >
              {pending ? tc("states.loading") : mode === "edit" ? t("agents.form.submit.edit") : t("agents.form.submit.create")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
    </>
  )
}

interface WizardProgressProps {
  step: WizardStep
  totalSteps: number
  progressPercent: number
  title: string
  summary: string
  stepOfLabel: string
  completeLabel: string
}
function WizardProgress({
  step,
  totalSteps,
  progressPercent,
  title,
  summary,
  stepOfLabel,
  completeLabel,
}: WizardProgressProps) {
  return (
    <div className="shrink-0 space-y-1.5">
      <div className="flex items-baseline justify-between gap-2">
        <p className="text-xs font-semibold uppercase tracking-wider text-fg-subtle">{stepOfLabel}</p>
        <p className="text-xs text-fg-faint">{completeLabel}</p>
      </div>
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="shrink-0 text-lg font-semibold leading-none text-fg">{title}</h2>
        <p className="min-w-0 truncate text-sm text-fg-subtle">{summary}</p>
      </div>
      <div className="relative h-1 w-full overflow-hidden rounded-full bg-surface-muted">
        <div
          className="absolute inset-y-0 left-0 bg-success transition-all"
          style={{ width: `${Math.max(0, Math.min(100, progressPercent))}%` }}
          aria-hidden
        />
      </div>
      <div className="sr-only" role="progressbar" aria-valuemin={0} aria-valuemax={totalSteps} aria-valuenow={step} />
    </div>
  )
}

interface FieldProps {
  label: ReactNode
  children: ReactNode
  required?: boolean
  hint?: string
  error?: string
}
const Field = forwardRef<HTMLDivElement, FieldProps>(function Field(
  { label, children, required, hint, error },
  ref
) {
  return (
    <div ref={ref} className="grid gap-1.5">
      <label className="text-sm font-medium text-fg-muted">
        {label}{required && <span className="ml-0.5 text-danger">*</span>}
      </label>
      {children}
      {hint && <span className="text-xs text-fg-faint">{hint}</span>}
      {error && <span className="text-xs text-danger">{error}</span>}
    </div>
  )
})
function ChoiceCard({ icon, title, description, selected, onSelect, disabled = false }: { icon: ReactNode; title: string; description?: string; selected: boolean; onSelect: () => void; disabled?: boolean }) {
  // min-h on cards with descriptions keeps neighbors aligned when one wraps;
  // cards without one collapse to natural height to avoid an empty body block.
  const heightClass = description ? "min-h-[92px] items-start" : "items-center"
  const className = disabled
    ? `flex w-full ${heightClass} gap-2 rounded-md border border-line bg-surface-subtle px-3 py-2 text-left text-sm text-fg-faint`
    : `flex w-full ${heightClass} gap-2 rounded-md border px-3 py-2 text-left text-sm transition ` + (selected ? "border-line-strong bg-surface-subtle text-fg" : "border-line bg-surface text-fg-muted hover:bg-surface-subtle")
  return (
    <button
      type="button"
      onClick={disabled ? undefined : onSelect}
      disabled={disabled}
      className={className}
    >
      <span className={(description ? "mt-0.5 " : "") + "text-fg-subtle"}>{icon}</span>
      <span className="min-w-0">
        <span className="block font-medium">{title}</span>
        {description && (
          <span className="mt-0.5 block text-xs leading-4 text-fg-subtle">{description}</span>
        )}
      </span>
    </button>
  )
}

function DependencyCard({ title, description, href, cta }: { title: string; description: string; href: string; cta: string }) {
  return (
    <div className="rounded-lg border border-dashed border-line-strong bg-surface-subtle p-3">
      <p className="text-sm font-medium text-fg">{title}</p>
      <p className="mt-1 text-sm text-fg-subtle">{description}</p>
      <a href={href} className="mt-2 inline-flex items-center gap-1 text-sm font-medium text-fg underline">
        {cta} <ArrowUpRight className="h-3 w-3" />
      </a>
    </div>
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
  capabilityID,
  fromMarketplace,
  workspaceID,
  latestVersionID,
  latestVersion,
  choice,
  onChange,
}: {
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

  const handleChange = (event: ChangeEvent<HTMLSelectElement>) => {
    event.stopPropagation()
    event.preventDefault()
    const value = event.target.value
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
    <select
      value={selectValue}
      onChange={handleChange}
      onClick={(event) => event.stopPropagation()}
      // Marketplace bindings see the same picker but the cue (label
      // suffix) reminds the user that breaking changes can land
      // without warning. We don't disable "latest" outright — opting
      // into it is a deliberate user choice we surface explicitly.
      className="ml-1 h-7 shrink-0 rounded-md border border-line bg-surface px-2 text-sm text-fg focus:outline-none focus:ring-2 focus:ring-line-strong"
      title={fromMarketplace ? t("agents.form.versionPicker.marketplaceHint") : t("agents.form.versionPicker.localHint")}
    >
      <option value="__latest__">
        {t("agents.form.versionPicker.latest")}{latestVersion ? ` (v${latestVersion})` : ""}
      </option>
      {versions.length === 0 && choice?.pinningMode === "pinned" && choice.versionID && (
        // Versions list still loading — keep the current pin visible
        // so the dropdown doesn't appear to forget the user's choice
        // until the network round-trip lands. choice.pinnedVersion
        // (hydrated from binding.version on edit, set by handleChange
        // on user pick) carries the literal for the PINNED row — never
        // fall back to latestVersion here, which would mis-label the
        // pinned option with whatever the capability's newest version
        // happens to be.
        <option value={choice.versionID}>v{choice.pinnedVersion || "?"}</option>
      )}
      {versions.map((version) => (
        <option key={version.id} value={version.id}>
          v{version.version}
        </option>
      ))}
    </select>
  )
}
