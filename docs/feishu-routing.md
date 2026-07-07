# Feishu IM routing and Agent visibility design

> This document defines the product semantics and implementation contract for
> the Parsar Feishu IM entry point. Any implementation or refactor of the
> Feishu webhook, Agent visibility, gateway routing, or related RBAC must
> read this first.
> Decisions are aligned with the product owner (2026-05-29); 2026-06-09
> update: keep per-Agent direct Bots and add an env-backed default shared Bot
> command-routing mode.

---

## 1. One-line summary

The Feishu IM entry point is an Agent's public entry inside Feishu. Two Bot
routing modes are supported today:

- `direct`: default mode; one Feishu Bot binds to one Agent, and messages that Bot receives enter that Agent directly.
- `shared`: unified entry mode; one instance-level default Feishu Bot acts as the host/router. Users list available Agents via `/list`, switch the current target via `/select <index or slug>`, and regular messages route to the currently selected Agent.

In either mode, messages must pass the target Agent's visibility gate
before we reply; conversation content always lands in the target Agent's
workspace, and outbound replies always use the Bot App ID from the
inbound path.

It is NOT:

- The "Feishu login entry" for Parsar users (that is the OAuth platform app's job, see §2).
- A router that free-parses `@<agent-name>` text (shared mode only accepts explicit commands and stored selections).
- A user's personal-workspace notification inbox (conversations belong to the Agent, not the user).

---

## 2. Two categories of Feishu custom app

A Feishu "custom app" is a container that can host both OAuth and Bot
capability. In Parsar these are **semantically different products** and
must be modeled separately.

| Type | Purpose | Count | Configuration |
| --- | --- | --- | --- |
| **OAuth platform app** | Feishu-based login for Parsar users | Fixed at **1** per Parsar instance | env: `PARSAR_FEISHU_APP_ID/SECRET` (already shipped, see D.2 / `docs/deploy/feishu-prod.md`) |
| **Bot app** (default shared entry point or dedicated Agent avatar) | shared: instance-level default command entry; direct: send/receive entry for a single Agent | Default shared Bot is one per instance; dedicated Bots can be configured N per Agent | Default shared Bot reads env `PARSAR_FEISHU_DEFAULT_BOT_*` (falls back to `PARSAR_FEISHU_APP_ID/SECRET` when not explicitly set); dedicated Bots live in `agents.config.connectors.feishu` (see the §6 schema) |

**Why not merge them:**

- The OAuth platform app is a "Parsar-platform"-level resource with fixed credentials that every login uses.
- Bot apps are "Feishu entry"-level resources: in default shared mode the instance env holds unified Bot credentials while the target Agent handles execution + permission semantics; in direct mode N Agents typically use N credential sets.
- Feishu also treats them separately: leaking the OAuth credential compromises platform login; leaking the default shared Bot credential compromises the unified entry; leaking a dedicated Bot credential only compromises that Agent.

**OSS lazy mode** (open-source deployments where a small team has only 1
Agent connected to Feishu on a single Parsar instance):

- We allow you to configure the OAuth platform app and the Bot app as the same Feishu custom app (filling the same value for both App ID fields).
- This is only "the configuration layer allowing two independent fields to hold the same value"; **at the architecture layer these two categories must remain separate**.
- In internal deployments with multiple Agents, they must be created separately.

---

## 3. The three tiers of Agent visibility

An Agent is a workspace asset (`agents.workspace_id` is the ownership,
already shipped), but "who can invoke this Agent" is an independent
dimension, controlled by `agents.visibility`.

### 3.1 Tier definitions

| visibility | Who can invoke it in the web UI | Who can @Agent-bot in Feishu | Typical scenario |
| --- | --- | --- | --- |
| **workspace** (default) | Only members of this workspace | Only the Feishu accounts of workspace members | Department-internal tools (team-only code reviewer / internal data query Agent) |
| **tenant** | All registered Parsar users | Feishu accounts of all registered Parsar users | Cross-department services (HR Q&A / IT ticketing / internal knowledge bot) |
| **public** | All registered Parsar users | Any Feishu user (including ones not yet registered on Parsar) | External services (customer support / sales / public Q&A bot) |

`tenant` differs from `public` **only on the Feishu side**: `tenant`
requires the Feishu sender to already be a Parsar user (Feishu `open_id`
hits `oauth_users`); unregistered Feishu users are guided by the bot to
register. `public` admits unregistered users directly.

On the web UI side, `tenant` and `public` behave the same ("all
registered users can see and use it") because the web forces login.

### 3.2 Defaults and constraints

- **Default value:** `workspace` (strictest, guards the safety floor).
- **Who can change visibility:** workspace owner / admin only (members cannot, to prevent members from privately setting an Agent to public and leaking internal capability).
- **`public` back to `workspace` / `tenant`:** allowed, but the UI must double-confirm and warn "This will immediately cut off external / cross-tenant users currently using the Agent; historical conversations are preserved".
- **Enum is closed:** exactly three tiers — no `restricted` / `team-internal` / `partner` subdivisions until we have a real customer scenario.

### 3.3 Relationship between visibility and conversation ownership

**Visibility decides "who can start the conversation"; conversations
always land in the Agent's workspace.**

This is important; example:

- Agent X (workspace=W1, visibility=tenant)
- Alice is a Parsar user in W2, not W1
- Alice @ the Bot of X in Feishu and asks a question → the Bot replies (visibility=tenant permits it) → a conversation is created in **W1** (not W2), with Alice recorded as a participant
- Whether Alice can see this conversation on the web is a separate permission layer (participants can see conversations they took part in — see §7 RBAC)

---

## 4. Inbound routing (Feishu → Parsar)

Processing order when a webhook / websocket delivers a Feishu message:

```text
1. Signature verification / websocket tenant token check
2. challenge response         (only on URL registration)
3. By inbound app_id          → hits the env default shared Bot app_id: use the virtual host route
                                → else look up the dedicated-Bot Agent in agents.config.connectors.feishu.app_id
                                → if neither matches: 400 Unknown app / websocket drop
4. Decide routing mode
   - env default Bot: fixed shared; handle /list /select first; for plain messages, pull selected_agent_id from gateway_sessions(platform=feishu, external_id=chat_id, session_type=chat)
   - dedicated Bot direct: target Agent = host Agent
5. From sender union_id/open_id → look up the Parsar user_id (may miss)
6. Gate against target Agent.visibility
7. If the gate passes → create / continue conversation inside target Agent.workspace_id
                        conversation.source_app_id = inbound app_id
                        trigger the target Agent run
8. If the gate rejects → host Bot posts a guidance message; no conversation is created
```

### 4.1 Visibility gate in detail

```text
visibility = workspace:
  sender_user_id ∈ workspace_members(agent.workspace_id) → allow
  else → deny (bot replies: "This Agent is available to <workspace name> members only")

visibility = tenant:
  sender_user_id hits oauth_users (i.e. already registered on Parsar) → allow
  else → deny (bot replies: "Please register at <Parsar URL> first")

visibility = public:
  unconditionally allow
  if sender_user_id missed → conversation.participants records open_id, user_id=null
                             first bot reply appends "Register at <URL> to manage conversation history"
```

### 4.2 Group-chat scenario

When @Bot happens in a group, routing keys off the **sender's open_id**
(not the group ID); everything else is identical to DM.

- Groups do not introduce a "group → workspace" binding concept (avoids admin-side pre-configuration overhead).
- Multiple people @-ing the same Agent in one group → one conversation per sender, mutually invisible.
- If a customer ever asks for "group as one conversation" semantics, open a follow-up design; not in MVP.

### 4.3 First-login reconcile for unregistered users

Under `visibility=public`, an unregistered Feishu user can chat with the
bot directly. When that user later actually registers on Parsar (through
any channel):

- The Feishu `open_id` from registration matches historical `participants.open_id`.
- Auto-reconcile: back-fill historical `participants.user_id` from null to the newly created `user_id`.
- The user, after logging in on the web, can see their past Feishu conversations.

### 4.4 Shared Bot command routing

The default shared Bot does not execute business requests directly; it
takes Feishu credentials from the instance env
`PARSAR_FEISHU_DEFAULT_BOT_APP_ID` / `PARSAR_FEISHU_DEFAULT_BOT_APP_SECRET`
(falling back to `PARSAR_FEISHU_APP_ID` / `PARSAR_FEISHU_APP_SECRET` when
not explicitly set) and maintains the current target Agent for each
Feishu conversation.

Commands:

- `/list`: list Agents visible to the sender, excluding Agents that have enabled a dedicated Bot. Logged-in Parsar senders see workspace / tenant / public Agents; unregistered senders see only public Agents.
- `/select <index|agent-slug|workspace-slug/agent-slug>`: store `selected_agent_id` on the current `gateway_sessions(platform=feishu, external_id=chat_id, session_type=chat)`.
- `/help`: show command help.

Plain messages:

1. Without a selection, the host Bot replies "Please /list and /select first"; no conversation is created.
2. If the selected Agent has been deleted / disabled, the host Bot asks the user to re-select.
3. If the selected Agent still exists, re-run the visibility gate; only when it passes do we call `CreateInboundIMMessage(TargetAgentID=selected_agent_id, SourceAppID=host_app_id)`.
4. When the outbound worker sees `SourceAppID` equals the default Bot app_id, it uses env credentials directly, so the reply is sent from the unified Bot; audit / AgentRun / usage still attribute to the target Agent.

---

## 5. Outbound routing (Parsar → Feishu)

After an Agent run produces a message, the destination is chosen by
conversation source:

```text
conversation.source_gateway = feishu
conversation.source_app_id  = <Bot app's App ID>
conversation.source_thread  = <Feishu reply anchor message_id, used for reply_in_thread>
```

Historically (B Phase, now retired) there were two outbound paths running side by side:
- **P1 outbound worker**: polled `messages.metadata.gateway_delivered_at=''` rows for agent messages, called Feishu SendMessage, with its own backoff / dead letters.
- **P2 inflight driver**: polled `agent_run_events`, sent a "running" card on run.started, PATCHed the same card on event stream deltas, and PATCHed Done/Error on run completion.

When both loops raced the same conversation, the "one query, two cards"
symptom appeared (P1 finished and sent the Done card, P2 also PATCHed
independently). So the 2026-06-12 refactor deleted P1 entirely, and the
**driver is now the sole entry for Feishu outbound** (see the "Driver-only
architecture" section in `docs/feishu-driver-retry.md`).

### 5.1 Current architecture (driver-only)

The driver goroutine does:

1. Every ~2s, call `ClaimActiveFeishuInflightConversations` to claim a batch of active conversations
   (filter: `agent_run_events.sequence > seq_emitted` OR `next_retry_at <= now` OR `gateway_delivered_at = ''`).
2. For each claimed conversation, replay `agent_run_events` to fold the current card state (steps / streaming text / thinking).
3. No working slot → POST a new "running" card, write message_id back to `gateway_inflight.working`; existing slot → PATCH the same card.
4. On receiving `run.completed` / `run.failed`, PATCH once more to the Done / Error card, call
   `MarkGatewayOutboundDelivered` to stamp `messages.metadata.gateway_delivered_at`,
   `ClearConversationInflightSlot` to clear the working slot, and async DELETE the typing reaction.
5. Any send / patch failure backs off `1s → 5s → 30s → 5m → 5m`; the 6th failure writes a `sender_type='system'` dead-letter notice + audit row and clears the slot.

Credentials resolve by `conversation.source_app_id`: equal to the default
shared Bot's env app_id → use the env secret; otherwise read
`app_secret_ref` from the dedicated Bot's
`agents.config.connectors.feishu` and decrypt via the secret vault. On
inbound, `source_thread` picks `root_id → parent_id → message_id` so
replies stay inside the original thread in group chats and topics.

Do not cache dedicated-Bot credentials in the worker's process memory —
you cannot detect a secret rotation live. Fetch from the vault on demand
each send (the vault has its own LRU cache). Default shared Bot
credentials come from process env, so a rotation requires restarting or
a rolling deploy.

### 5.2 Diagnostics field semantics (from Phase 6)

`GetFeishuConnectorDiagnostics` outbound counts now aggregate from two
sources (no longer reading `messages.metadata.gateway_retry_*` fields):

| Field | Source |
| --- | --- |
| `pending_outbound_count` | Agent rows with `messages.metadata.gateway_delivered_at = ''` |
| `delivered_outbound_count` | Same, `<> ''` |
| `retrying_outbound_count` | `conversations.metadata.gateway_inflight.working.attempts > 0` |
| `dead_outbound_count` | Messages with `sender_type='system'` and `metadata.kind LIKE 'feishu_outbound_dead_letter_%'` |
| `last_error` / `last_error_at` | content/created_at of the most recent dead-letter notice, otherwise the current inflight slot's `last_error` |

---

## 6. Schema changes

The following schema / config contracts back Feishu IM routing. Where
migrations already exist, the database is authoritative; the jsonb
connector substructure is an application-layer convention.

### 6.1 New `visibility` column on `agents`

```sql
alter table agents
  add column visibility text not null default 'workspace';

alter table agents
  add constraint agents_visibility_check
  check (visibility in ('workspace', 'tenant', 'public'));
```

**Why a column, not config jsonb:**

- Every RBAC middleware + inbound routing gate reads it; it is a hot path.
- We need a check constraint on the enum; jsonb cannot enforce it strongly.
- Audit events must carry the visibility change verbatim (an owner flipping an Agent to public is high-sensitivity).

**Why no index:** `agents` is filtered by workspace_id naturally (the
workspace table is small), and a standalone index on visibility has poor
ROI.

### 6.2 `agents.config.connectors.feishu` substructure (convention, not schema)

The default shared Bot writes nothing to any Agent's
`agents.config.connectors.feishu`; it is an instance-level env-backed
entry. This jsonb substructure applies only to dedicated Bots: an Agent
that picks "Dedicated Bot" carries its own Feishu App ID and vault secret
refs, and is excluded from the default Bot's `/list`.

`agents.config` is jsonb, but the feishu-connector portion for dedicated
Bots follows this shape:

```jsonc
{
  "connectors": {
    "feishu": {
      "enabled": true,
      "event_mode": "websocket",                 // websocket=default when created via QR; webhook=manual fallback
      "routing_mode": "direct",                  // direct=one Bot into one Agent; shared=/list+/select unified entry
      "app_id": "cli_xxx",
      "app_secret_ref": "secret_id_in_vault",    // never store plaintext
      "verification_token_ref": "secret_id_in_vault", // required in webhook mode; may be empty in websocket mode
      "encrypt_key_ref": "secret_id_in_vault",   // optional; fill when Feishu event encryption is on
      "bot_open_id": "ou_xxx"                    // optional; dedup for self-sent messages
    }
  }
}
```

`*_ref` fields are references to entries in the secret vault; plaintext
credentials always go through the vault (D.6 + the Phase 4 secret system).

QR-provisioned Bots default to `event_mode="websocket"` and only need
`app_secret_ref`; the manual HTTP webhook fallback uses
`event_mode="webhook"` and requires `verification_token_ref` for event
validation.

`routing_mode` defaults to `direct`. Historically an Agent connector
could set `shared` as host; today the default shared Bot uses instance
env, and we do not require or recommend creating a host Agent for it.
A dedicated Bot, once saved, is removed from the default shared Bot's
`/list`.

Runtime requirements: with `PARSAR_FEISHU_WEBSOCKET=true`, the websocket
inbound manager opens the env-backed default shared Bot long connection
(when the default Bot env is complete) and continues to periodically scan
enabled dedicated-Bot Agents to open their corresponding long
connections; outbound replies are delivered by the inflight driver
goroutine when `PARSAR_FEISHU_OUTBOUND=true` (the env name is inherited
from the P1 era and kept as an alias).

### 6.3 New `source_app_id` field on conversations

```sql
alter table conversations
  add column source_app_id text;
```

Filled on inbound; used on outbound to look up the corresponding Bot
credentials. Pairs with the existing `source_gateway` (`'feishu'` /
`'web'` / ...) field.

### 6.4 Feishu inbound app_id → agent_id lookup

New store method (no new table needed; a GIN index on
`agents.config.connectors.feishu.app_id` is enough):

```sql
create index idx_agents_feishu_app_id
  on agents ((config->'connectors'->'feishu'->>'app_id'))
  where deleted_at is null
    and (config->'connectors'->'feishu'->>'enabled')::boolean = true;
```

Query:

```sql
select id, workspace_id, visibility
from agents
where config->'connectors'->'feishu'->>'app_id' = $1
  and deleted_at is null
  and (config->'connectors'->'feishu'->>'enabled')::boolean = true
limit 1;
```

### 6.5 `gateway_sessions` selection table

`routing_mode=shared` reuses the generic gateway-session state to store
the Agent currently selected in the same external-platform conversation.
The Feishu shared-Bot MVP uses chat-level selection state: one Feishu
group / DM conversation shares a single selected Agent.

```sql
create table gateway_sessions (
  id uuid primary key,
  platform text not null,
  external_id text not null,
  external_thread_id text not null default '',
  session_type text not null default 'chat',
  selected_agent_id uuid references agents(id) on delete set null,
  metadata jsonb not null default '{}'::jsonb,
  created_at timestamptz not null,
  updated_at timestamptz not null,
  unique (platform, external_id, external_thread_id, session_type)
);
```

For Feishu: `platform='feishu'`, `external_id=<chat_id>`,
`external_thread_id=''`, `session_type='chat'`. `selected_agent_id` is
set null on deletion; the next plain message prompts the user to `/list`
+ `/select` again.

---

## 7. RBAC boundaries

### 7.1 Web permission to view Agents / conversations

- Agent list / detail visibility:
  - Workspace member → sees every Agent in that workspace (regardless of visibility).
  - Non-member but in the tenant: only sees Agents with `visibility in ('tenant','public')`.
  - Not logged in: only sees `visibility='public'` (in an open-source default instance this can be disabled).
- Conversation detail visibility:
  - Workspace member → sees every conversation in that workspace.
  - Conversation participant (matched by `participants.user_id`) → sees the conversations they took part in.
  - Everyone else → cannot see.

### 7.2 Who can change Agent visibility

- Only `workspace_members.role in ('owner','admin')`.
- Any switch to or from `public` writes an audit event `agent.visibility.changed`.

### 7.3 Relationship with the D.5 RBAC middleware

The existing `requireWorkspaceMember` / `requireWorkspaceOwnerOrAdmin`
middleware from D.5 stays as is. **We add a new
`requireAgentVisibilityAccess` middleware** dedicated to Agent-read paths
and gating per §7.1.

---

## 8. Out of scope for MVP

Explicitly not doing:

- **Explicit "group → workspace" binding**: workspace is always determined by the target Agent; in shared mode the target Agent is decided by chat-level `/select`.
- **Finer visibility tiers** (`restricted` / `partner`, etc.): wait for real customer scenarios.
- **Cross-Agent routing that parses @ text**: shared mode must go through `/list` + `/select`; guessing targets from natural language or `@<agent-name>` is not supported.
- **Complex bot-command platform**: the MVP supports only `/list`, `/select`, `/help`; any other slash command enters the currently selected Agent as a plain prompt.
- **Multi-language bot copy i18n**: the bot's reply copy is one Chinese (default) + one English (fallback) in the MVP; full i18n follows Parsar's overall i18n effort.

---

## 9. History: B Phase rollout order

B Phase (initial Feishu IM inbound / outbound) shipped in five ordered
segments: schema → inbound routing → outbound worker → visibility
API/UI → OSS lazy mode. The outbound portion (the former P1 outbound
worker) was completely replaced in the 2026-06-12 driver-only refactor;
new outbound changes should read §5.1 + `docs/feishu-driver-retry.md`.

---

## 10. Decision record (why we picked this)

| Decision | Chosen | Not chosen | Reason |
| --- | --- | --- | --- |
| Number of Bots | Default 1 Bot per Agent; also supports explicit shared Bot command routing | Instance-wide implicit shared Bot + @ text parsing | direct preserves a clear Agent avatar; shared satisfies the "unified entry" ask, but with `/list` + `/select` + visibility gate the governance surface stays sane |
| Visibility tiers | Three (workspace / tenant / public) | Two (private / public) | Internal deployments always have "used across the company but not external" scenarios; modeling it up front is cheaper than a later schema change |
| Conversation ownership | Agent's workspace | Sender's personal workspace | Enterprise governance / audit / memory must center on the Agent-owner workspace |
| Unregistered Feishu users | Reply + guide to register | Silent drop | Silent no-reply on Feishu is a UX disaster; `public` Agents are exactly this use case |
| OAuth vs Bot app | Two separate tracks | One combined app | Credential granularity / blast radius / multi-Agent scenarios all require separation; OSS single-Agent lazy path is a config-layer alias only |
| Location of the visibility field | `agents` column | `agents.config` jsonb | Hot-path query + check constraint + audit needs plaintext events |
| Who can change visibility | owner / admin | Any member | Prevents members from making an Agent public and leaking capability |
| Group-chat routing | By sender open_id | Group ↔ workspace binding | Simpler; avoids admin-side pre-configuration; add later if a real customer needs it |

---

## 11. Related docs

- Feishu inflight driver retries / dead letters / triage: `docs/feishu-driver-retry.md`
- Feishu production deployment: `docs/deploy/feishu-prod.md`
- Webhook signature verification implementation: `server/internal/auth/feishu/webhook.go`
- Data model source of truth: `server/migrations/` (agents / conversations table definitions)
