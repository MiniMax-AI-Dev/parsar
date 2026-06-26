# 飞书 Inflight Driver:重试 / 死信 / 排查

> 飞书出站的"一条 query 一张卡"驱动的运维说明。
> 任何线上"卡片不动了"、"重复卡片"、"红色错误卡"、"系统通知刷屏"类问题先看本文。

---

## 1. 路径概览

驱动入口:`server/internal/gateway/feishuoutbound/inflight_driver.go::InflightTickOnce`,
由 `Worker.Run` 以 ~2 s 心跳触发,也是 `PARSAR_FEISHU_OUTBOUND=true` 的服务起的同一个 worker(env 名沿用历史)。

每次 tick 做的事:

```
ClaimActiveFeishuInflightConversations    -- 批量抢 conversation
  └─ 对每个 claim 到的 conversation:
       └─ foldEventsIntoCardState         -- 回放 agent_run_events 推出 card state
       └─ POST / PATCH Feishu             -- 用 attemptSendWithRetry 包一层
       └─ MarkGatewayOutboundDelivered    -- 终态时给 messages.metadata.gateway_delivered_at 盖戳
       └─ ClearConversationInflightSlot   -- 终态时清 slot
       └─ asyncClearTypingReaction        -- 终态时异步 DELETE typing emoji
```

P1 outbound worker 已下线;driver 是唯一出站路径。

---

## 2. 重试与退避

`server/internal/gateway/feishuoutbound/retry.go`:

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

退避存在 `conversations.metadata.gateway_inflight.working` 的子字段里:

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

claim SQL 会同时检查 `next_retry_at <= now` 让到点的重试行重新被抢到。

成功 → `attempts/last_error/next_retry_at` 三件套清零(`zeroRetryWorking`)。
失败 5 次 → 进入死信路径。

---

## 3. 死信路径

第 6 次失败(`attempts == 5` 且本次还是失败)时:

1. 写一条 `sender_type='system'` 的 message 进 conversation:
   - `metadata.kind` = `feishu_outbound_dead_letter_working_<run-id>`
     (per-run discriminator;同一 run 重复触发只产生 1 条 notice,
     `SendSystemNoticeMessage` 用 `(conversation_id, metadata.kind)` 做 idempotent)。
   - `content` = 截断到 512 字符的最后错误。
2. 调 `audit.Ingester.Write` 写一条 `feishu_outbound.dead_letter` audit 行
   (`Worker.audit` 为 nil 时 fallback 成 warn log,不阻塞)。
3. `ClearConversationInflightSlot` 清掉 working slot 释放后续路径。
4. 异步 DELETE typing reaction(终态都会做)。

permission 卡的死信同样,key 是 `feishu_outbound_dead_letter_permission_<run-id>`。

### 3.1 Audit event 形状

死信路径在 `audit_records` 表写一条 system audit 行(`audit.SourceRuntime` / `audit.ActorTypeSystem`):

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

Ops 仪表盘按 `event_type` 过滤这两个值统计死信率;按 `payload.external_app_id` 切片可以分 Bot 看哪个 app 最容易触发死信(通常是 secret 被吊销 / scope 配错)。
Audit ingester 为 nil 或 buffer 满(`audit.ErrDropped`)时只 warn log,不阻塞 driver tick — 别在死信率 dashboard 上做 hard alert,因为 audit 链路本身的可用性独立于死信本身。

---

## 4. Diagnostics 字段读哪里

`GET /api/v1/agents/{id}/feishu-connector-diagnostics`(`GetFeishuConnectorDiagnostics`)的语义:

| 字段 | 含义 | 数据源 |
| --- | --- | --- |
| `pending_outbound_count` | 还没派送出去的 agent message | `messages.metadata.gateway_delivered_at = ''` |
| `delivered_outbound_count` | 已完成出站 | 同上但 `<> ''` |
| `retrying_outbound_count` | 正在重试中的 conversation 数 | `conversations.metadata.gateway_inflight.working.attempts > 0` |
| `dead_outbound_count` | 死信总数 | `sender_type='system' AND metadata.kind LIKE 'feishu_outbound_dead_letter_%'` |
| `last_error` / `last_error_at` | 最近一次错误 | 优先取最近的 dead-letter notice;否则取当前 inflight slot 的 `last_error` / `updated_at` |

---

## 5. 排查指南

### 5.1 "用户发的消息一直没回复"

```sql
-- 找 inbound row
select id, conversation_id, metadata
from messages
where external_message_id = '<飞书侧 message_id>'
  and sender_type = 'user';

-- 看对应 conversation 的 inflight slot
select metadata->'gateway_inflight'->'working'
from conversations
where id = '<conversation_id>';
```

- `working` 字段 NULL → driver 还没 claim 过(看 worker pod 日志:
  `feishu inflight driver starting`、`feishu inflight: ...`)。
- `working.attempts > 0` 且 `next_retry_at` 远未到 → 卡在退避,看 `last_error` 是什么。
- 没 working slot 且 `agent_run_events` 已有 `run.completed` → 看 messages.metadata.gateway_delivered_at 是否盖了戳,
  如果没盖戳但 conversation.updated_at 已经动过,说明终态 patch 跑了但 MarkDelivered 失败
  (worker 日志会 warn `feishu inflight: mark delivered failed`)。

### 5.2 "一条 query 来了两张卡"

driver-only 重构(2026-06-12)修了这个症状的两个 race:

1. **claim 过滤漏掉 run.completed/run.failed** → driver 不会再 wake 做终态 patch。
   Phase 1 fix:`store.sql:ClaimActiveFeishuInflightConversations` 的 event_kind set 加上这俩。
2. **driver 每 tick 重发终态卡片** → main 的 a46453d 修;
   `MarkGatewayOutboundDelivered` 给 messages 盖 `gateway_delivered_at` 戳,
   claim SQL 用 LEFT JOIN 过滤掉这种 conversation。

如果新版本又出现两张卡,先确认 conversation 上有 working slot 但 messages 上没盖 delivered_at —
通常是 MarkDelivered call 沉默失败了(老 P1 worker 已下线;Phase 5 起,driver 沉默失败会
warn `feishu inflight: mark delivered failed`)。

### 5.3 "系统通知不停刷"

不可能,`SendSystemNoticeMessage` 用 `(conversation_id, metadata.kind)` 做 idempotent,
且 dedup key 是 per-run 的(`feishu_outbound_dead_letter_working_<run-id>`)所以下一次 run
不会被吞。如果真出现,grep `metadata.kind` 看是否有人手工塞了同名 kind。

### 5.4 "卡死在 retrying 但 last_error 是 nil"

#### 症状

- `conversations.metadata.gateway_inflight.working` 存在,attempts=0/1/2,但 `last_error=''`。
- Driver 日志看不到 `feishu inflight: ...` warning。
- 用户角度:发了消息,卡片始终是"执行中",过了几分钟还没变。
- 旧日志(2026-06-12 Phase 6 之前)可能能看到 `working` slot 的 jsonb 写入应当成功(无 conflict 报错)但下一次 tick 读不到。

#### 根因 1:Phase 6 修了的 prod bug(2026-06-12 之前的版本)

PG 的 `jsonb_set(metadata, '{gateway_inflight,working}', ...)` 当 conversation 还没有 `gateway_inflight` 这个 top-level key 时**静默 no-op** — 即使 `create_missing=true` 也不创建中间路径的 key。
对应症状是:driver 第一次 first-send 给 Feishu 发卡片成功,但回写 slot 时 SQL 报告成功,实际什么都没写;下一次 tick 又走 first-send 路径,又新发一张卡。

修复(Phase 6, MR `fix/feishu-driver-cleanup-docs`):`UpsertConversationInflightWorkingCard` 改成 `jsonb_build_object || jsonb_build_object` concat 形式,中间路径不存在时显式创建。

#### 根因 2:手工塞数据时复刻了同样的坑

有人手工 `update conversations set metadata = jsonb_set(metadata, '{gateway_inflight,working}', '{...}'::jsonb)` — 同样 silent no-op。

#### 排查步骤

```sql
-- 看实际写入了什么
select jsonb_pretty(metadata->'gateway_inflight') from conversations where id = '<conversation_id>';

-- 看 worker 是否真 reach 过这条 conversation
select metadata->'gateway_inflight_claim' from conversations where id = '<conversation_id>';
-- claimed_at 在最近 1-2 分钟内说明 driver tick 跑了;太老说明 worker 没 wake 起来
```

### 5.5 操作:手工解锁一个卡死的 slot

```sql
-- 清掉 working slot 让 driver 下一轮重新评估
update conversations
set metadata = metadata #- '{gateway_inflight,working}'
where id = '<conversation_id>';
```

只清 slot,不动 messages.gateway_delivered_at;
下一轮 tick 看 agent_run_events 决定 send 还是 patch 终态。

---

## 6. 相关代码

- 驱动入口:`server/internal/gateway/feishuoutbound/inflight_driver.go`
- 重试 / 死信封装:`server/internal/gateway/feishuoutbound/retry.go`
- claim SQL:`server/internal/db/queries/store.sql::ClaimActiveFeishuInflightConversations`
- system notice 写入:`server/internal/store/system_messages.go::SendSystemNoticeMessage`
- diagnostics 聚合 SQL:`server/internal/db/queries/store.sql::GetFeishuConnectorDiagnostics`

## 7. 历史背景

- 2026-06 之前:并存 P1(`gateway_outbound_messages` queue)和 P2(inflight driver),
  典型症状"一条 query 两张卡"。
- 2026-06-12 driver-only 重构(分 7 个 Phase):
  - Phase 1-4:修 claim 过滤、driver 自带重试 / 死信、`run.failed` event 化、driver 接管 reaction DELETE。
  - Phase 5:删 P1 worker / dispatcher / SQL / store wrapper(`refactor(gateway/feishu): delete P1 outbound worker, driver owns sends`)。
  - Phase 6:清 metadata 残留字段、把 diagnostics 聚合换到 inflight slot + 死信 notice、修 `jsonb_set` Upsert silent no-op latent bug。
  - Phase 7:本文档。
- 2026-06-19 `pollEvery` 默认 10s → 2s(降卡片更新延迟,DB 负载提高 ~5x 可接受;
  更彻底的方案见 §8)。

---

## 8. Future work:PG NOTIFY 推送 + polling 兜底

### 背景

当前 driver 100% 靠 `pollEvery` 定时器醒来扫 `ClaimActiveFeishuInflightConversations`。
症状:用户感知延迟 = pollEvery(2s);DB 即使空闲也按 `0.5 QPS × pod 数` 持续扫表。

`agent_run_events` 写入端(connector/runtime)和读取端(driver)在同一 PG 实例,
天然适合用 PG 自带 LISTEN/NOTIFY 做事件推送,polling 降级为"漏消息兜底"。

### 设计

**触发**:`agent_run_events` 上加 `AFTER INSERT` trigger,按 conversation_id 发 NOTIFY:

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

(payload 最长 8000 字节,conversation_id UUID 远在限内;
filter 在订阅端做,trigger 不应该 SELECT conversations 判断 platform——
每次 INSERT 都查会拖慢写入,白发的 NOTIFY 订阅端忽略即可。)

**订阅**:Worker 启动时单独起一个 pgx Conn 调用 `LISTEN feishu_card_dirty`,
收到 payload 后:
1. 单 conv 节流(同一 conversation_id 距上次 tick < 1s → 标记 dirty 等下个窗口,
   ≥ 1s → 立刻 `handleInflightConversation`),防止 21 个 tool.call 触发 21 次飞书
   PATCH 撞 rate limit。
2. 不抢 `ClaimActive...` 的全量扫表路径,跑一个单 conv 版的 claim+handle。

**Polling 兜底**:`pollEvery` 拉长到 60s,职责剩四件事:
- pod 启动 race 期间漏掉的 NOTIFY
- DB 连接断重连期间漏掉的 NOTIFY
- 退避到点的重试(`next_retry_at <= now`,无新事件不会触发 NOTIFY)
- `permissionStaleWindow = 5min` 的自动 deny(时间触发,无事件)

### 预期收益

| 维度 | 当前(2s poll) | NOTIFY + 60s 兜底 |
|---|---|---|
| 平均事件延迟 | ~1s | <1s(立刻) |
| 最坏事件延迟 | 2s | 60s(仅 pod 重启/断连场景) |
| 单 pod 空闲 tick QPS | 0.5 | 0.017(30x↓) |

### 风险与前提

- **trigger 必须覆盖所有 INSERT 路径**:漏一个写入点该事件只能等 60s 兜底。
  用 `AFTER INSERT` trigger 而不是在 Go 代码到处 `pg_notify` 手写。
- **pod 启动 race**:连 DB → `LISTEN` 之间 100ms-1s 窗口的 NOTIFY 收不到。
  保留现有 `worker.go:268` 的 100ms 首次 tick 兜底。
- **NOTIFY 是广播**:所有 LISTEN 的 pod 都收到,N 个 pod 一起来抢同一 conv;
  现有 `ClaimActiveFeishuInflightConversations` 的 `claimed_by` 乐观锁已经处理,
  改造单 conv claim 时沿用同一把锁即可。
- **节流 state 是进程内的**:某 pod 节流定时器待发时进程挂了,事件落到 polling
  兜底(最坏 60s)。可接受。
- **退避重试感知变慢**:退避到点不发 NOTIFY,只能等下次 polling(最坏 60s)。
  退避节奏本来是 1s/5s/30s/5m/5m,60s 兜底只影响第一二档,实际偏差很小。

### 不做的事

- 不上 Redis/Kafka:PG NOTIFY 已经够用,引入外部依赖徒增运维面。
- 不做"per-event PATCH":节流是产品意图(用户不需要 1 秒看到卡片刷 4 次),
  不是 rate-limit workaround。
