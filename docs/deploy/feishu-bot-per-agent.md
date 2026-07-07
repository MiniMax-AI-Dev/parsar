# Feishu Bot connection guide (per-Agent / shared)

For operators and project admins who want to expose a Parsar Agent as a Feishu
bot. Covers both connection modes:

- `direct` (default): one Feishu Bot bound to one Agent.
- `shared`: a single unified Feishu Bot acts as the entry point; users switch
  the target Agent inside Feishu via `/list` and `/select`.

The end-to-end flow is always: **create a Bot app on the Feishu Open
Platform → inject credentials into the Parsar secret vault → bind on the
Agent detail page → add the Bot to a Feishu group → @ the Bot in the group to
verify**.

Applies to both open-source and internal deployments — no fork-only code is
involved.

> **Architectural boundary (read first)**
>
> Parsar splits Feishu "custom apps" into two categories that correspond to
> different Parsar resources:
>
> | App type | Purpose | Configuration | Count |
> | --- | --- | --- | --- |
> | **OAuth platform app** | Parsar users log in to the web UI with Feishu | env `PARSAR_FEISHU_APP_ID/SECRET` | Fixed at 1 per Parsar instance |
> | **Bot app (subject of this doc)** | direct: Feishu entry point of one Agent; shared: unified command entry point | `agents.config.connectors.feishu` | Default one per Feishu-connected Agent; can also be configured as a shared host Bot |
>
> These two are semantically different products and must be created
> separately (different credential exposure surfaces, scopes, and
> lifecycles). See `docs/feishu-routing.md §2` for the deeper rationale.
>
> **How to create the OAuth app** → see `docs/deploy/feishu-prod.md`; **not in
> the scope of this document**. This document only covers Bot apps.

---

## 0. Prerequisites

Before you start, verify:

- [ ] Parsar server is deployed and passes `smoke.sh --core` (see `deploy-runbook.md`).
- [ ] You have logged in to the Parsar web UI with your Feishu account and are the **owner / admin** of the target workspace (the Bot config is RBAC-gated to owner / admin; regular members do not see the form).
- [ ] You have created at least one Agent (connector_type = agent_daemon or http).
- [ ] You have permission in the Feishu tenant to create custom apps (usually granted by the IT admin; personal lark.com accounts also work).
- [ ] Parsar server has a publicly reachable HTTPS domain (Feishu event subscriptions require HTTPS).

If any of the above is missing, go back to `deploy-runbook.md` /
`feishu-prod.md` and complete them first.

---

## 1. Feishu Open Platform: create the Bot custom app

### 1.1 Create the app

1. Open the [Feishu Open Platform](https://open.feishu.cn/app) (Lark users go to `https://open.larksuite.com/app`).
2. Click "Create custom app" → fill in name (a good pattern is to include the Agent name, e.g. `Parsar · Product Q&A Bot`), description, icon.
3. **Do not** reuse the OAuth platform app. This must be a brand-new, independent app.

### 1.2 Record the base credentials

Once the app is created, go to "Credentials & Basic Info" and **record four values** (you will inject them into Parsar shortly):

| Feishu console name | Parsar field | Notes |
|---|---|---|
| App ID | `app_id` | Looks like `cli_a1b2c3d4e5f6g7h8` |
| App Secret | App Secret (auto-written into the secret vault on save, produces `app_secret_ref`) | **Displayed only once** — copy it immediately |
| Verification Token | Verification Token (auto-written into `verification_token_ref` on save) | Retrieved from §1.4 event subscription |
| Encrypt Key (optional) | Encrypt Key (auto-written into `encrypt_key_ref` on save) | Only present when event encryption is enabled in §1.4 |

> **Never commit these four values into any repo or docs default.** They flow
> through the Parsar secret vault (runtime encryption + audit); the plaintext
> only briefly exists on your local clipboard.

### 1.3 Request permissions (scopes)

Go to "Permission Management" and request at least:

| Scope | Purpose |
|---|---|
| `im:message` | Receive IM message events |
| `im:message.group_at_msg:readonly` | Receive @Bot messages in groups |
| `im:message.p2p_msg:readonly` | Receive p2p private messages (if you want 1:1 chat too) |
| `im:message:send_as_bot` | Send messages as the Bot |
| `im:chat:readonly` | Read chat info (outbound sends need chat_id) |
| `contact:user.id:readonly` | Map the sender's open_id to union_id (needed by the visibility gate) |

After requesting scopes you **must** publish a version and get tenant-admin
approval; scopes are inactive until then.

> **Enterprise admins usually do not approve immediately.** Ping IT to speed
> things along after submission. If the approval is rejected, this path is dead.

### 1.4 Enable the "Bot" capability

Go to "App Capabilities → Bot" and **click Enable**.

> By default a Feishu "custom app" is only an OAuth container; without the
> Bot capability enabled, the Bot cannot be added to a group and no @Bot
> events reach you. **This is the most commonly missed step.**

### 1.5 Configure event subscription

Go to "Event Subscription":

1. **Request URL**: `https://<your-parsar-domain>/api/v1/feishu/events/message`
   - Must be HTTPS; Feishu rejects HTTP.
   - The path is **exactly** `/api/v1/feishu/events/message`; do not rename.
2. **Verification Token**: Feishu auto-generates one. **Copy it** — the bind
   card in §3 below writes it into the secret vault automatically.
3. **Encryption (optional)**: if you enable "Event Encryption", an Encrypt
   Key is generated — **copy it too**.
4. When you click "Save", Feishu sends a `url_verification` challenge; the
   built-in Parsar handler auto-replies `{"challenge":"..."}`.
   - If this fails, see `feishu-prod.md §6.1 / §6.2` for troubleshooting.
5. **Subscribe to events**: at minimum enable
   - `im.message.receive_v1` (message-received event; the important one).

> After subscribing you **must publish a new version** and get tenant-admin
> approval, otherwise the subscription is inactive.

---

## 2. Parsar: secret vault storage rules

You do not have to manually pre-create these entries on the Secrets page.
On save, the Feishu Bot bind card on the Agent detail page automatically
writes App Secret / Verification Token / Encrypt Key into the secret vault,
and only stores `app_secret_ref` / `verification_token_ref` /
`encrypt_key_ref` into the Agent config.

> The secret vault encrypts with `PARSAR_MASTER_KEY`; only ciphertext lives
> in the vault, and the UI never re-displays plaintext (only a masked preview).

The auto-created secrets use these conventions (Encrypt Key is optional):

| kind | provider | payload field |
|---|---|---|
| `feishu_app_secret` | `feishu` | `app_secret` |
| `feishu_verification_token` | `feishu` | `verification_token` |
| `feishu_encrypt_key` *(only when event encryption is on)* | `feishu` | `encrypt_key` |

To rotate later, re-enter the new value in the bind card and save; leaving
the field blank means "keep using the currently stored Secret".

---

## 3. Parsar: bind the Bot on the Agent detail page

Go to the "Agents" page (`?admin=agents`), pick the Agent you want to attach
a Bot to → **Connector tab**.

You will see two blocks:

- Top: existing connector info (agent_daemon / http).
- Bottom: **the "Feishu Bot binding" card**.

Fill in the Feishu Bot binding card:

| Field | Source | Required |
|---|---|---|
| Bot connection | Pick "Default Bot" or "Dedicated Bot" | Default Bot requires no separate Feishu app details |
| App ID | App ID from §1.2 | Required when Dedicated Bot is enabled |
| App Secret | App Secret from §1.2; auto-saved as a Secret | Required when no stored value exists |
| Verification Token | Verification Token from §1.5; auto-saved as a Secret | Required in webhook mode when no stored value exists |
| Encrypt Key (optional) | Encrypt Key from §1.5; auto-saved as a Secret | Only when event encryption is enabled |
| Bot Open ID (optional) | Leave blank; fill after the first successful run (see §5) | Optional |

Click Save. Once you pick "Dedicated Bot", this Agent is removed from the
Default Bot's selectable list.

**Possible errors:**

| Error | Meaning | Fix |
|---|---|---|
| `feishu_connector_incomplete` (422) | Enabled but required field missing | Check App ID + App Secret + Verification Token |
| `feishu_app_id_in_use` (409) | The App ID is already bound to another enabled Agent on this instance | Use a different App ID, or disable the Feishu binding of the other Agent first |
| Red "No available Secret" | Current workspace has no active Secret | Go back to §2 and create a Secret |

On success, audit records `agent.feishu_connector.updated`.

---

## 4. Feishu tenant: add the Bot to a group (or 1:1)

### 4.1 Group chat

1. In any Feishu group: Settings → Group bots → Add bot.
2. Search for the Bot name you created in §1.1 → Add.
3. direct mode: a group member **@Bot** with a message → should be received by Parsar → triggers a run on the current Agent → Bot replies.
4. shared mode: the member first **@Bot /list**, then **@Bot /select <index or slug>**; subsequent plain messages route to the selected Agent → Bot replies.

### 4.2 1:1 private chat

1. Global-search the Bot name in Feishu → send a direct message (some tenant policies disable this; check with IT).

> **Agent visibility must be ≥ the sender's reachable scope:**
>
> - `visibility=workspace` (default): only accepts @Bot from members of that workspace.
> - `visibility=tenant`: all registered Parsar users can @Bot.
> - `visibility=public`: any Feishu user can @Bot (including those not yet registered on Parsar).
>
> Change visibility on the Agent detail page's Overview tab (owner/admin
> permission). See `docs/feishu-routing.md §3`.

### 4.3 Common reasons a group @Bot gets no reply

| Symptom | Cause | Diagnosis |
|---|---|---|
| @Bot gets zero reaction; Parsar receives no webhook | Event subscription URL unreachable / scopes not approved | Click "Test connection" on the Feishu event-subscription page; check whether the tenant admin approved the `im:message` scope |
| Parsar receives the webhook but logs say `unknown app_id` | App ID missing from Agent config / Agent disabled / Agent deleted | Check the Agent detail page's `enabled` toggle; in the DB run `select config->'connectors'->'feishu' from agents where id=...` |
| Parsar receives the webhook and routes to the Agent, but visibility rejects | Sender is out of the visibility scope | Check Agent visibility + whether the sender is a registered Parsar user |
| The Agent ran but the Bot did not reply | Outbound worker not up / bad credentials / Feishu API error | Grep the server log for `inflight`; verify that `PARSAR_FEISHU_OUTBOUND=true` env is set |

---

## 4.4 AgentRun write-back verification

For real-Feishu E2E, beyond confirming that Parsar received the inbound
event, also confirm the AgentRun result is written back to the same Feishu
message thread:

| Scenario | Expectation |
|---|---|
| Agent produces normal text output | Bot replies with the Agent output in the original group / DM thread |
| Agent completes with no output | Bot replies `Runtime completed this run with no output.` |
| Agent execution fails | Bot posts a user-visible failure message; detailed errors remain in Parsar Run detail / lifecycle events for triage |

If Parsar shows the run as completed / failed but no reply arrives on
Feishu:

1. Verify `PARSAR_FEISHU_OUTBOUND=true` and check the server log for `inflight` send / retry entries.
2. Verify the conversation is Feishu-sourced: `conversations.platform='feishu'` and `external_id` is non-empty.
3. Verify the run's agent message metadata carries `run_id` and lacks `gateway_delivered_at`.
4. If the message already has `gateway_retry_next_at`, wait for the backoff window or inspect the last Feishu API error.
5. If the message shows `gateway_delivery_status='dead'`, the retry limit was hit; fix per the logged Feishu error and re-trigger.

## 5. Hardening: fill in Bot Open ID (recommended)

After the first successful run, go back to §3's Agent Connector tab and fill
in **Bot Open ID**:

1. Feishu console → App → "Credentials & Basic Info", find the **Bot's open_id** (like `ou_xxxxxxxx`).
2. Enter it in the Bot Open ID field on the Parsar Agent detail page → Save.

**Why**: prevents the Bot from treating its own outgoing messages as fresh
inbound events (message self-loop). It runs without this too, but under
edge cases the loop can amplify traffic.

---

## 6. Multiple Agents: Default Bot vs Dedicated Bot

### 6.1 Default Bot: team-wide entry point

The Default Bot is a team-wide entry point; you do not need to fill in
Feishu app details on individual Agents. Agents that have not bound a
Dedicated Bot can remain in the Default Bot's selectable list and be served
through the unified entry.

### 6.2 Dedicated Bot: one Bot per Agent

If an Agent needs its own Feishu app — its own group presence or its own
permission governance — pick "Dedicated Bot" and repeat §1 → §3. After
saving, the Agent is removed from the Default Bot's selectable list; both
inbound and outbound traffic use its own Bot credentials.

**Anti-patterns (do not do this):**

- ❌ Two enabled Dedicated-Bot Agents sharing the same Bot App ID → 409 `feishu_app_id_in_use`, hard-blocked.
- ❌ Enabling the Bot capability on the OAuth platform app → credential coupling; a future OAuth credential leak drags every Bot down with it.

---

## 7. Security notes

- App Secret / Verification Token / Encrypt Key **never enter the repo**; they all flow through the Secret vault.
- Any docs / `.env.example` template only ever contains `<placeholder>`; real values live only in the deploy env or vault.
- `bot_open_id` is not sensitive; it can sit in the jsonb config (it does, in `agents.config.connectors.feishu.bot_open_id`).
- Changing visibility is a high-sensitivity operation (especially switching to `public`); it is restricted to workspace owner/admin and writes an audit record `agent.visibility.changed`.
- Changing the Bot binding itself also writes audit record `agent.feishu_connector.updated`, including old/new app_id — but **not** the `*_ref` values.

---

## 8. Known limitations (follow-up)

| Item | Current state | Follow-up |
|---|---|---|
| Outbound worker token cache after Bot credential rotation | Old token continues in use up to the ≤7200s token TTL | Add a way for PATCH to actively invalidate the worker token cache |
| TOCTOU window when the same app_id is PATCHed concurrently to two Agents | Application-layer probe + row lock, without a UNIQUE index safety net | Add `UNIQUE PARTIAL INDEX WHERE enabled=true` |
| Feishu Secret kind is still free text | The UI writes `feishu_*` kind on auto-creation, but the server does not enforce an enum | Server-side registration of a `feishu_*` kind enum |
| Group-as-conversation semantics | Dedicated Bot mode still follows the current conversation-continuation rules | Watch real customer usage |
| OSS lazy mode (OAuth app = Bot app) | Not implemented | An env flag to allow reuse |

---

## 9. Related docs

- `docs/feishu-routing.md` — source-of-truth design for Feishu IM routing + Agent visibility.
- `docs/deploy/feishu-prod.md` — production config for the OAuth platform app (user login).
- `docs/deploy/deploy-runbook.md` — deploy cold-start order.
- `docs/deploy/health-and-smoke.md` — health checks + smoke script.
