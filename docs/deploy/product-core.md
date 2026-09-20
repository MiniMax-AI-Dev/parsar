# Connect the Parsar product to Core

Parsar executes only through the public Agents API. Deploy Core independently
using its [operator guide](../../services/agents-api/README.md), including its
model provider, Docker or E2B sandbox provider, database and credentials.
The product no longer runs or pairs native daemons, HTTP workers or sandboxes.

## Object and operation mapping

Parsar has its own database. Core is the only Agent execution substrate, not the
only source of product data or business behavior. Preserve Parsar's sidebar,
member pages and conversations; the upstream dashboard is not a UI specification.

| Product object / operation | Protocol carrier and ownership | Current integration |
| --- | --- | --- |
| Agent as a working member | Parsar identity, description, permissions and SP; execution fields become Session `agent` | Connected through inline configuration. Description is display-only; SP maps to additional `instructions`, not replacement of Core's base instructions. |
| Reusable execution definition | Core `/agents` CRUD | Implemented by Core; product does not currently use saved Agents. No additional product Agent-template object is required. |
| Environment selection | `POST /agents/sessions` with `environment` | Saved once in the Agent defaults and passed on the first message. Explicit conversation selectors remain overrides; no running container is bound to the Agent. |
| Environment template | `/agents/environments/templates` CRUD | Name and enabled/disabled network are implemented. Installation fields and restricted domains await Core support. |
| Actual environment | Created with Session; `/agents/environments/{id}` reads | Owned by Core. Multiple conversations for a member use separate contexts; the product does not manage provider allocations. |
| Skill / MCP asset management | Parsar assets, versions, configuration and authorization | Retained. Import, preview, upload, publication and credential authorization do not mean runtime activation. |
| Skill installation/loading | Hosted Session/template `skills` and `capability_directories`; skill references or inline archives | Not connected. Core rejects populated installation fields. Do not inject product bundles directly into a daemon or use live Files writes as a substitute for startup installation. |
| MCP tools at execution | Official Agent `tools` and supported credentials mechanisms | Inline protocol tools may be submitted to Core, which validates support. Product catalog/OAuth bindings are not automatically translated or loaded. |
| Conversation and one execution | Session; Events submission and Turn/Items reads | Connected. One product conversation/Agent binding freezes one Session; each input creates a Turn, while a user retry creates a new input. |
| Cancellation, recovery, usage | Session cancel event; durable Turns/Items and Turn usage | Connected, including measured failed/cancelled usage. Product event projection/accounting are stored in Parsar. |

There is no official persisted Agent-to-Environment binding in the pinned contract.
An environment is combined with an inline or saved Agent when creating a Session.
Existing Session snapshots do not change when product member configuration or an
environment template changes. The product deliberately does not add Runtime or
Run as new first-class concepts for this integration.

The member model takes identity, responsibilities and reusable abilities as product
concepts ([Multica's explanation](https://multica.ai/docs/agents)); it does not import
Multica's runtime architecture. Protocol evidence is the
[pinned upstream source](../../contracts/agents-api/upstream.json),
[Agent schema](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agent_create_params.py),
[Session schema](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/types/beta/agents/session_create_params.py), and
[official reference](https://developers.openai.com/api/reference/python/resources/beta/subresources/agents).

## Workspace credentials

Bootstrap the product and create a workspace. For each product workspace,
provision a distinct Core project and a caller key bound to that project.
Never reuse a Core project across product workspaces. The product sends an
`OpenAI-Project` header which Core verifies against the key's binding.

For native development, set `PARSAR_CORE_WORKSPACES_FILE` to an absolute path
under `~/.parsar/`. Its JSON array contains server-owned bindings:

```json
[
  {
    "workspace_id": "00000000-0000-0000-0000-000000000001",
    "project_id": "your-core-project",
    "base_url": "http://127.0.0.1:8091/v1",
    "api_key_file": "/run/secrets/core/workspace-one.key"
  }
]
```

Paths are resolved by the product process. For native execution, replace the
container path above with the absolute host key-file path. Keep key files private
and readable by the product process user. The JSON stores paths, never raw keys.
Bindings are loaded at startup; restart after changes. A binding's Core project
must remain stable once it has sessions. Credential rotation within that project
is supported. Moving execution history to another project is not a config edit.

The installer creates `<home>/core/workspaces.json` containing `[]`. The primary
Compose and production example mount `PARSAR_CORE_CONFIG_DIR` read-only at
`/run/secrets/core`. Put the JSON and key files in that directory. Grant the
container's configured user read access to them; never make keys world-readable.
An unconfigured workspace can use product administration but execution and Core
resource operations report that Core is not configured.

For a single native workspace, the equivalent explicit configuration is
`PARSAR_CORE_WORKSPACE_ID`, `PARSAR_CORE_PROJECT_ID`, `PARSAR_CORE_URL` and
`PARSAR_CORE_API_KEY_FILE`. There is no global fallback for other workspaces.

## Product workflow and contract

1. In Build → Models, create a workspace Provider with its protocol, HTTPS endpoint
   and API key, then add model identifiers under it. Provider headers open settings;
   their model rows are read-only. Keys are write-only and separate from Credentials.
2. Create an Agent by selecting a catalog model, Harness, default hosted environment/template and instructions. Advanced configuration accepts
   `tools`, `service_tier`, `reasoning`, `text` and `multi_agent` from the pinned
   official inline Agent contract.
3. Optionally create a Core environment template. The UI enables name and basic
   network configuration, and marks packages, setup commands, environment variables
   and other initialization fields as awaiting Core. API requests preserve official
   fields and let Core explicitly reject unsupported values. The product does not cache their confidential payloads. Template updates
   preserve omitted fields; secret values are never prefilled from list responses.
4. Select an Agent to open an empty chat directly. The first
   message creates a Core execution session. Subsequent inputs reuse that session;
   Agent edits apply to new conversations. Existing sessions keep their snapshots.
5. Observe text, tool activity, reasoning summaries and usage; cancel when needed.
   Product restarts recover through durable Core Turns and Items. Deleting a
   conversation cancels pending/running work; internal cleanup continues until
   submitted Core work settles while product history stays hidden.

Catalog keys require the product `PARSAR_MASTER_KEY` and the Core credential key.
Both services freeze confidential execution configuration with their existing
encryption services. Provider edits, key rotation and deletion affect new Sessions;
existing Sessions keep the original model, endpoint and key. See the
[execution extension contract](../../contracts/agents-api/model-execution.md).

The UI distinguishes protocol configuration from current execution support. Core
may reject fields it has not implemented; the product must not drop those fields
or execute through a legacy route. Current Core template support is limited; see
[coverage](../../contracts/agents-api/environment-templates.md).

Docker and E2B are hosted providers selected by the Core operator. Official
`self_hosted` instead requires a separately connected executor using the returned
Environment ID and remote URL. The product exposes that distinction but its
self-hosted setup and environment-key management are not yet available. The pinned
public SDK does not define a dashboard environment-key CRUD API; do not invent one.
During environment preparation, the baseline public Core Events API rejects
immediate cancellation while an input reservation is pending. Product cancellation
records intent; the owning observer cancels and settles Core work when admission
permits it. Product cancelled status does not assert immediate native quiescence.

Interactive tool approvals and application function results are also unavailable
in this client; a waiting turn is cancelled with an explicit unsupported result.

## Asset management versus runtime installation

Skills directory import runs its download tool in disposable storage and archives
assets into Parsar; it never installs into an Agent's executing workspace. Signed
Skill upload is an authorized product asset callback, but Core-driven authoring is
not connected and no runtime callback token is injected by this client. MCP catalog
import and OAuth save product configuration/authorization, without starting an MCP
server or invoking its tools. Browser plugin clients extend Parsar UI, not the Agent
harness. Preserve these product operations while removing the old connector's bundle
delivery, credential injection, provider allocation and native execution paths.

Capability-library and member pages distinguish managed assets from execution
availability. Existing unsupported capability bindings fail explicitly rather than
being silently ignored or loaded through an old connector.

## Default execution configuration

Parsar stores `config.x_agents_core.harness` and `config.environment` alongside the
model. The former is the [Core extension](../../contracts/agents-api/harness-selection.md);
the latter is a product default mapped to the separate official Session environment.
Neither is stored in metadata. Existing explicit conversation environment overrides
remain readable. Agent create/read/edit requires no executing container. Opening
an empty chat requires no Core Session; the first message freezes one request and
starts execution through the existing durable Core binding. Two chats with the
same Agent have separate contexts and environments. Later messages reuse their
original environment even after Agent defaults change.

Configure the desired harnesses and their qualified Runtime providers in Core
before using them. The product offers the existing harness identifiers, with a
clear error for a deployment that has not enabled the selection. Protocol-valid
configuration may still exceed a harness's supported model/environment/tool profile;
Core and its selected adapter retain those validation boundaries.
