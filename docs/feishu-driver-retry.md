# Feishu Inflight Driver: retries / dead letters / triage

> Operational reference for the "one query, one card" Feishu outbound driver.
> Any production issue like "card stopped updating", "duplicate cards", "red
> error card", or "system notice spam" should start here.

---

## 1. Path overview

Entry point: `server/internal/gateway/inflight/inflight_driver.go::InflightTickOnce`,
invoked by `Worker.Run` on a ~2s heartbeat. It is the same worker that
services get spawned by `PARSAR_FEISHU_OUTBOUND=true` (env name kept for
historical reasons).

Each tick does:

```
ClaimActiveFeishuInflightConversations    -- batch-claim conversations
  └─ For each claimed conversation:
       └─ foldEventsIntoCardState         -- replay agent_run_events into card state
       └─ POST / PATCH Feishu             -- wrapped in attemptSendWithRetry
       └─ MarkGatewayOutboundDelivered    -- on terminal state, stamp messages.metadata.gateway_delivered_at
       └─ ClearConversationInflightSlot   -- on terminal state, clear the slot
       └─ asyncClearTypingReaction        -- on terminal state, async DELETE typing emoji
```

The P1 outbound worker is retired; the driver is the sole outbound path.

---

## 2. Retry and backoff

`server/internal/gateway/inflight/retry.go`:

```go
var driverBackoffSchedule = []time.Duration{
    1 * time.Second,
    5 * time.Second,
    30 * time.Second,
    5 * time.Minute,
    5 * time.Minute,
}
var maxDriverAttempts = len(driverBackoffSchedule)  // = 5
```

Backoff state lives in the `working` subfield of
`conversations.metadata.gateway_inflight`:

```json
{
  "external_msg_id": "om_abc",
  "agent_run_id":    "<uuid>",
  "seq_emitted":     17,
  "attempts":        2,
  "last_error":      "feishu 502 upstream timeout",
  "next_retry_at":   "2026-06-12T10:23:48Z"
}
```

The claim SQL also checks `next_retry_at <= now` so due retries get
re-claimed.

Success → `attempts / last_error / next_retry_at` are cleared
(`zeroRetryWorking`). Five failures → dead-letter path.

---

## 3. Dead-letter path

On the 6th failure (`attempts == 5` and this one still fails):

1. Insert a `sender_type='system'` message into the conversation:
   - `metadata.kind` = `feishu_outbound_dead_letter_working_<run-id>`
     (per-run discriminator; repeated triggers for the same run only produce
     one notice — `SendSystemNoticeMessage` is idempotent on
     `(conversation_id, metadata.kind)`).
   - `content` = the last error, truncated to 512 chars.
2. Call `audit.Ingester.Write` to record a `feishu_outbound.dead_letter`
   audit row (`Worker.audit` being nil falls back to a warn log — never
   blocks).
3. `ClearConversationInflightSlot` releases the working slot for downstream paths.
4. Async DELETE the typing reaction (done on every terminal transition).

Permission cards share the same shape, keyed as
`feishu_outbound_dead_letter_permission_<run-id>`.

### 3.1 Audit event shape

The dead-letter path writes a system audit row (`audit.SourceRuntime` /
`audit.ActorTypeSystem`) to `audit_records`:

```
event_type   = feishu_outbound.dead_letter             (working slot)
              feishu_outbound.dead_letter_permission   (permission slot)
target_type  = conversation
target_id    = <conversation_id>
workspace_id = <ws-id>
project_id   = <project-id>
payload      = {
  "agent_run_id":     "<uuid>",
  "attempts":         5,
  "last_error":       "<truncated to 512 chars>",
  "external_chat_id": "<feishu chat id>",
  "external_app_id":  "<bot app_id>"
}
```

Ops dashboards filter on `event_type` for those two values to count
dead-letter rate; slicing by `payload.external_app_id` reveals which Bot
app triggers the most dead letters (usually a revoked secret or misconfigured
scope). If the audit ingester is nil or the buffer is full
(`audit.ErrDropped`), it only warn-logs — it does not block the driver tick.
Do not hard-alert on the dead-letter dashboard, since the audit path's
availability is independent from the dead letter itself.

---

## 4. Where to read the diagnostics fields

Semantics of `GET /api/v1/agents/{id}/feishu-connector-diagnostics`
(`GetFeishuConnectorDiagnostics`):

| Field | Meaning | Source |
| --- | --- | --- |
| `pending_outbound_count` | Agent messages not yet delivered | `messages.metadata.gateway_delivered_at = ''` |
| `delivered_outbound_count` | Outbound completed | Same, `<> ''` |
| `retrying_outbound_count` | Conversations currently retrying | `conversations.metadata.gateway_inflight.working.attempts > 0` |
| `dead_outbound_count` | Total dead-letter count | `sender_type='system' AND metadata.kind LIKE 'feishu_outbound_dead_letter_%'` |
| `last_error` / `last_error_at` | Most recent error | Prefer the newest dead-letter notice; otherwise the current inflight slot's `last_error` / `updated_at` |

---

## 5. Triage guide

### 5.1 "The user's message never got a reply"

```sql
-- Locate the inbound row
select id, conversation_id, metadata
from messages
where external_message_id = '<feishu-side message_id>'
  and sender_type = 'user';

-- Inspect the corresponding conversation's inflight slot
select metadata->'gateway_inflight'->'working'
from conversations
where id = '<conversation_id>';
```

- `working` field NULL → driver has not claimed it yet (check the worker
  pod logs: `feishu inflight driver starting`, `feishu inflight: ...`).
- `working.attempts > 0` and `next_retry_at` far in the future → stuck in
  backoff; see what `last_error` says.
- No working slot and `agent_run_events` already has `run.completed` →
  check whether `messages.metadata.gateway_delivered_at` is stamped. If it
  is not stamped but `conversation.updated_at` has moved, the terminal
  patch happened but MarkDelivered failed (worker logs will warn
  `feishu inflight: mark delivered failed`).

### 5.2 "One query, two cards"

The driver-only refactor (2026-06-12) fixed the two races behind this
symptom:

1. **claim filter missed run.completed/run.failed** → driver stopped waking
   up for the terminal patch. Phase 1 fix:
   `store.sql:ClaimActiveFeishuInflightConversations` adds those two event
   kinds to the set.
2. **driver re-sent the terminal card every tick** → fixed on main in
   a46453d; `MarkGatewayOutboundDelivered` stamps `gateway_delivered_at`
   on the message and the claim SQL LEFT JOINs those conversations out.

If two-cards resurfaces in a newer build, first check: does the
conversation have a working slot but the message lacks `delivered_at`?
That usually means the MarkDelivered call silently failed (the old P1
worker is gone; from Phase 5, driver silent failures warn `feishu
inflight: mark delivered failed`).

### 5.3 "System notices spam"

Impossible: `SendSystemNoticeMessage` is idempotent on
`(conversation_id, metadata.kind)`, and the dedup key is per-run
(`feishu_outbound_dead_letter_working_<run-id>`), so the next run is not
swallowed. If it happens, grep `metadata.kind` — someone probably wrote a
same-named kind by hand.

### 5.4 "Stuck retrying but last_error is nil"

#### Symptoms

- `conversations.metadata.gateway_inflight.working` exists, `attempts=0/1/2`, but `last_error=''`.
- Driver logs show no `feishu inflight: ...` warning.
- From the user's angle: the message was sent but the card stays on "running" for several minutes with no update.
- On old logs (before Phase 6, 2026-06-12) the `working` slot jsonb write would appear to succeed (no conflict error) but the next tick would not see it.

#### Root cause 1: prod bug fixed in Phase 6 (before 2026-06-12)

PG's `jsonb_set(metadata, '{gateway_inflight,working}', ...)` **silently
no-ops** when the conversation has no top-level `gateway_inflight` key —
`create_missing=true` does not create intermediate path keys either.
Symptomatically: the driver sent the initial card to Feishu successfully,
but writing back the slot's SQL "reported success" while actually writing
nothing; the next tick took the first-send path again and produced a new
card.

Fix (Phase 6, MR `fix/feishu-driver-cleanup-docs`):
`UpsertConversationInflightWorkingCard` now uses `jsonb_build_object ||
jsonb_build_object` concat, explicitly creating the intermediate path when
it does not exist.

#### Root cause 2: hand-written data reproducing the same pitfall

Someone runs `update conversations set metadata = jsonb_set(metadata,
'{gateway_inflight,working}', '{...}'::jsonb)` — same silent no-op.

#### Diagnosis steps

```sql
-- What was actually written
select jsonb_pretty(metadata->'gateway_inflight') from conversations where id = '<conversation_id>';

-- Did the worker actually reach this conversation
select metadata->'gateway_inflight_claim' from conversations where id = '<conversation_id>';
-- A recent (last 1-2 minutes) claimed_at means driver ticks did run; older means the worker never woke up
```

### 5.5 Ops: manually unstick a wedged slot

```sql
-- Clear the working slot so the next tick re-evaluates
update conversations
set metadata = metadata #- '{gateway_inflight,working}'
where id = '<conversation_id>';
```

Only clear the slot, do not touch `messages.gateway_delivered_at`; the
next tick reads `agent_run_events` to decide whether to send or patch a
terminal state.

---

## 6. Related code

- Driver entry point: `server/internal/gateway/inflight/inflight_driver.go`
- Retry / dead-letter wrapper: `server/internal/gateway/inflight/retry.go`
- Claim SQL: `server/internal/db/queries/store.sql::ClaimActiveFeishuInflightConversations`
- System notice writer: `server/internal/store/system_messages.go::SendSystemNoticeMessage`
- Diagnostics aggregation SQL: `server/internal/db/queries/store.sql::GetFeishuConnectorDiagnostics`

## 7. Historical background

- Before 2026-06: P1 (the `gateway_outbound_messages` queue) coexisted with P2 (the inflight driver); the classic symptom was "one query, two cards".
- 2026-06-12 driver-only refactor (7 phases):
  - Phases 1-4: fix the claim filter, driver-owned retries / dead letters, `run.failed` event-ification, driver takes over reaction DELETE.
  - Phase 5: delete the P1 worker / dispatcher / SQL / store wrapper (`refactor(gateway/feishu): delete P1 outbound worker, driver owns sends`).
  - Phase 6: clean up leftover metadata fields, move diagnostics aggregation to the inflight slot + dead-letter notices, fix the `jsonb_set` Upsert silent-no-op latent bug.
  - Phase 7: this document.
- 2026-06-19: `pollEvery` default from 10s → 2s (cuts card-update latency; DB load grows ~5x, acceptable — a more thorough fix is in §8).

---

## 8. Future work: PG NOTIFY push + polling fallback

### Background

The driver currently relies 100% on a `pollEvery` timer to wake up and scan
`ClaimActiveFeishuInflightConversations`. Symptoms: user-perceived latency
= `pollEvery` (2s); DB is scanned at `0.5 QPS × pod count` even when idle.

`agent_run_events` producers (connector / runtime) and its consumer (the
driver) share a PG instance, so PG's LISTEN/NOTIFY is a natural push
channel; polling degrades to a "missed message" safety net.

### Design

**Trigger:** add an `AFTER INSERT` trigger on `agent_run_events` that
emits a NOTIFY by conversation_id:

```sql
create or replace function notify_feishu_card_dirty() returns trigger as $$
begin
  perform pg_notify(
    'feishu_card_dirty',
    (select conversation_id::text from agent_runs where id = new.agent_run_id)
  );
  return null;
end;
$$ language plpgsql;

create trigger trg_agent_run_events_notify_feishu
  after insert on agent_run_events
  for each row execute function notify_feishu_card_dirty();
```

(Payload is capped at 8000 bytes; a UUID conversation_id fits easily.
Filtering happens on the subscriber side — the trigger must not SELECT
conversations to check the platform; that would slow every INSERT.
Extra NOTIFYs are cheap to ignore on the subscriber side.)

**Subscribe:** on Worker startup, take a dedicated pgx `Conn` and call
`LISTEN feishu_card_dirty`; on payload arrival:
1. Per-conversation throttling: if the same conversation_id was ticked
   less than 1s ago → mark dirty and let the next window handle it; ≥ 1s →
   run `handleInflightConversation` immediately. This stops 21 tool.calls
   from producing 21 Feishu PATCHes and hitting a rate limit.
2. Do not fall through to the full-table `ClaimActive...` scan; run a
   single-conv version of claim+handle.

**Polling fallback:** stretch `pollEvery` to 60s; it retains four
responsibilities:
- NOTIFYs missed during pod startup race.
- NOTIFYs missed during DB reconnects.
- Due retries (`next_retry_at <= now`, no new event triggers NOTIFY).
- The `permissionStaleWindow = 5min` auto-deny (time-triggered, not event-triggered).

### Expected wins

| Dimension | Current (2s poll) | NOTIFY + 60s fallback |
|---|---|---|
| Average event latency | ~1s | <1s (immediate) |
| Worst-case event latency | 2s | 60s (only during pod restart / reconnect) |
| Idle-tick QPS per pod | 0.5 | 0.017 (30x ↓) |

### Risks and prerequisites

- **The trigger must cover every INSERT path**: any missed write means that event only reaches the driver via the 60s fallback. Use an `AFTER INSERT` trigger rather than sprinkling manual `pg_notify` calls in Go code.
- **Pod-startup race**: the 100ms-1s window between connecting to the DB and issuing `LISTEN` will miss NOTIFYs. Keep the existing 100ms first-tick safety net at `worker.go:268`.
- **NOTIFY is broadcast**: every listening pod receives it, so N pods race the same conv; the existing `claimed_by` optimistic lock in `ClaimActiveFeishuInflightConversations` handles it. Reuse the same lock for the single-conv claim.
- **Throttling state is in-process**: if a pod dies with a pending throttle timer, that event falls to polling (worst-case 60s). Acceptable.
- **Slower backoff-retry perception**: a due retry does not emit a NOTIFY; only the next poll picks it up (worst-case 60s). Backoff is 1s/5s/30s/5m/5m, so the 60s fallback only affects the first two tiers with tiny practical drift.

### Out of scope

- Do not introduce Redis / Kafka: PG NOTIFY suffices and external dependencies balloon the ops surface.
- Do not do "PATCH per event": throttling is a product decision (users do not need the card refreshing four times per second), not a rate-limit workaround.
