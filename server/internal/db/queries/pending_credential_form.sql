-- Queries for the pending credential-form stash, hosted inside
-- conversations.metadata.gateway_inflight.pending_credential_form.
-- Slot shape is documented on PendingCredentialFormSlot in
-- server/internal/store/pending_credential_form.go — keep it in sync
-- there, not duplicated here.
--
-- Mirrors the working / permission slot patterns in store.sql:
--   * `||` jsonb concat (not jsonb_set) materialises the
--     gateway_inflight parent on demand; jsonb_set silently no-ops on
--     missing intermediate keys and would drop the very first write.
--   * `#-` for clear paths — no-op safe when the key is absent.

-- name: WritePendingCredentialFormSlot :one
-- Insert-or-noop: write @payload only when no pending slot exists yet.
-- If one is already there (qkey still active), it wins and is returned
-- unchanged so the caller can reuse the same qkey + external_msg_id on
-- subsequent inflight ticks instead of churning new cards.
--
-- coalesce on the slot expression: when the existing slot is non-null
-- (jsonb) it is returned as-is; when it is null (jsonb null sentinel or
-- key absent), the new @payload is written. The outer || on
-- gateway_inflight materialises the parent if it does not exist yet,
-- same as the working / permission writers.
--
-- The submit-handler claim path (ClaimPendingCredentialFormSlotByQkey)
-- atomically clears the slot, so once the user fills the form, the
-- next stash on the same conversation starts fresh.
update conversations
set metadata = coalesce(metadata, '{}'::jsonb) || jsonb_build_object(
      'gateway_inflight',
      coalesce(metadata->'gateway_inflight', '{}'::jsonb) || jsonb_build_object(
        'pending_credential_form',
        coalesce(metadata->'gateway_inflight'->'pending_credential_form', @payload::jsonb)
      )
    ),
    updated_at = @now
where id = @conversation_id::uuid
  and deleted_at is null
returning id::text, (metadata->'gateway_inflight'->'pending_credential_form') as slot;

-- name: UpdatePendingCredentialFormSlotMessageID :execrows
-- Stamp external_msg_id onto an existing slot after the first
-- SendMessage / ReplyMessage returns. Gated on @qkey so a slot that
-- was already claimed (qkey gone) or replaced by a later stash is left
-- alone — exec returns 0 rows in that case and the caller logs+moves on.
--
-- jsonb_set with `true` for create_missing so the field appears even on
-- slots that predate this column (legacy slots have no external_msg_id
-- key at all). The path is rooted at gateway_inflight.pending_credential_form
-- which we know exists because the qkey predicate matched.
update conversations
set metadata = jsonb_set(
      metadata,
      '{gateway_inflight,pending_credential_form,external_msg_id}',
      to_jsonb(@external_msg_id::text),
      true
    ),
    updated_at = @now
where id = @conversation_id::uuid
  and deleted_at is null
  and metadata->'gateway_inflight'->'pending_credential_form'->>'qkey' = @qkey::text;

-- name: ClaimPendingCredentialFormSlotByQkey :one
-- Atomic "I won this submit" primitive for the credential-form callback.
-- A single statement returns the slot to exactly one caller; siblings
-- racing on the same qkey see zero rows and short-circuit to an "already handled"
-- toast without writing credentials.
--
-- WHY a CTE: a naive UPDATE … RETURNING reads the post-UPDATE state, so
-- after the slot is cleared by `#-` the RETURNING would yield an empty
-- payload to the caller. The CTE captures the pre-image slot under FOR
-- UPDATE row lock, the UPDATE then unconditionally clears it, and the
-- final SELECT joins both — caller gets the original slot, while two
-- pods serialise on the lock so only one sees the row.
--
-- The `expires_at > now()` predicate in the pre-image SELECT keeps the
-- claim consistent with the sweep job: a "morally expired" slot whose
-- sweep hasn't yet arrived returns no row, surfacing a stable "expired"
-- toast to the user instead of behaviour that depends on cron timing.
--
-- The reverse-lookup uses the partial expression index from migration
-- 000008 (idx_conversations_pending_credential_form_qkey) — O(log n).
--
-- Returns the conversation's external-facing identifiers alongside the
-- slot so the submit handler doesn't need a second round-trip to load
-- workspace_id / source_app_id / external_chat_id / external_thread_id
-- for the post-claim work (patch reject card, enqueue re-fired inbound).
with claimed as (
    select c.id,
           c.workspace_id,
           c.external_id            as external_chat_id,
           c.external_thread_id,
           c.source_app_id,
           c.metadata->'gateway_inflight'->'pending_credential_form' as slot
    from conversations c
    where c.deleted_at is null
      and c.metadata->'gateway_inflight'->'pending_credential_form'->>'qkey' = @qkey::text
      and (c.metadata->'gateway_inflight'->'pending_credential_form'->>'expires_at')::timestamptz > @now::timestamptz
    for update
),
cleared as (
    update conversations
    set metadata = conversations.metadata #- array['gateway_inflight', 'pending_credential_form'],
        updated_at = @now::timestamptz
    where conversations.id in (select id from claimed)
    returning conversations.id
)
select claimed.id::text                  as conversation_id,
       claimed.workspace_id::text        as workspace_id,
       claimed.external_chat_id          as external_chat_id,
       claimed.external_thread_id        as external_thread_id,
       claimed.source_app_id             as source_app_id,
       claimed.slot                      as slot
from claimed
inner join cleared on cleared.id = claimed.id;

-- name: ClearPendingCredentialFormSlotByConversation :exec
-- Drop any pending form slot on a conversation. Called by the inbound
-- path when a fresh user message arrives without going through the
-- form submit — without this we'd auto-resume an abandoned draft when
-- a future submit somehow fires.
--
-- `#-` is no-op safe when the key is absent.
update conversations
set metadata = metadata #- array['gateway_inflight', 'pending_credential_form'],
    updated_at = @now
where id = @conversation_id::uuid
  and deleted_at is null;

-- name: SweepExpiredPendingCredentialFormSlots :execrows
-- Periodic cleanup. expires_at < @cutoff lets the test suite inject a
-- deterministic cutoff (production passes now()). Returns the number
-- of slots cleared so the cron logger can surface drift.
--
-- No expires_at index (see 000008 migration doc); the partial WHERE
-- on gateway_inflight presence bounds the candidate set to "currently
-- pending forms" — at our row counts (~tens), the in-place
-- ::timestamptz cast is cheap.
update conversations
set metadata = metadata #- array['gateway_inflight', 'pending_credential_form'],
    updated_at = @cutoff
where deleted_at is null
  and metadata->'gateway_inflight' ? 'pending_credential_form'
  and (metadata->'gateway_inflight'->'pending_credential_form'->>'expires_at')::timestamptz < @cutoff;

-- name: CountStalePendingCredentialFormSlots :one
-- Monitoring hook: how many slots are past @stale_cutoff (typically
-- now() - 1h, giving the 10-minute sweep plenty of cushion before
-- false-positive alerts)? Healthy state is 0. Sustained non-zero →
-- sweep cron is stalled / failing; alert.
select count(*) as stale_count
from conversations
where deleted_at is null
  and metadata->'gateway_inflight' ? 'pending_credential_form'
  and (metadata->'gateway_inflight'->'pending_credential_form'->>'expires_at')::timestamptz < @stale_cutoff::timestamptz;
