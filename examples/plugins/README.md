# Example Plugins

Demo plugins for validating the Parsar Plugin (KindBundle) system.

## customer-service-skin

A "skin change" plugin that transforms an Agent's personality into a
professional customer service representative. Contains only a skill
(no server/client components) — the simplest possible plugin.

### Install

```bash
export PARSAR_SERVER_URL=http://localhost:8080
export PARSAR_RUNNER_TOKEN=<your-token>
export PARSAR_WORKSPACE_ID=<your-workspace-id>

parsar plugin add ./examples/plugins/customer-service-skin
```

### What It Does

When bound to an Agent, the skill markdown is injected as a system prompt
addition (append mode). The Agent will adopt a warm, solution-oriented
customer service communication style in all responses.

### Verify

1. Install the plugin
2. Bind it to an Agent via the admin UI (Capability page → enable on Agent)
3. Start a new conversation with that Agent
4. The Agent should respond with customer-service-style phrasing:
   - Warm greeting
   - Acknowledge → Respond → Follow-up structure
   - "Is there anything else I can help with?"

## hotel-ops (Phase 1)

A server-tools plugin that demonstrates Phase 1 capabilities. The Agent
gets two tools (`check_room_status`, `suggest_pricing`) backed by a
Node.js handler running inside the plugin-host MCP server.

### Prerequisites

- Node.js >= 20 available on the server
- `PARSAR_PLUGIN_HOST_PATH` set to the absolute path of
  `server/plugin-host/index.js`

### Install

```bash
export PARSAR_SERVER_URL=http://localhost:8080
export PARSAR_RUNNER_TOKEN=<your-token>
export PARSAR_WORKSPACE_ID=<your-workspace-id>

parsar plugin add ./examples/plugins/hotel-ops
```

### What It Does

- **check_room_status**: Returns mock PMS room data (occupied/vacant/
  maintenance). Supports querying a specific room or filtering by status.
- **suggest_pricing**: Calculates a price suggestion based on current
  occupancy rate for a room type (standard/deluxe/suite).
- **hotel-workflow skill**: Instructs the Agent on how to use the tools
  and present results.

### Verify

1. Install the plugin
2. Set `PARSAR_PLUGIN_HOST_PATH` and restart the server
3. Bind it to an Agent via the admin UI
4. Start a new conversation and ask "What's the room status?"
5. The Agent should call `check_room_status` and return occupancy data
6. Ask "What price should I set for deluxe rooms?"
7. The Agent should call `suggest_pricing` with `room_type=deluxe`

### End-to-End Flow

```
parsar plugin add → API creates KindBundle capability
                  → CLI copies plugin to ~/.parsar/plugins/hotel-ops/
Server restart    → resolveBundleCapability sees server_entry
                  → emits MCP server entry: node plugin-host.js --plugin @internal/hotel-ops
Daemon prompt     → spawns plugin-host → loads server/index.js → tools registered
Agent calls tool  → MCP tools/call → handler runs → result returned
```


## compliance-approval (Phase 3)

A hooks plugin that demonstrates Phase 3 event interception capabilities.
The plugin registers a `before_permission_forward` hook that intercepts
permission requests before they reach the human approval UI.

### Prerequisites

- Node.js >= 20 available on the server
- `PARSAR_PLUGIN_HOST_PATH` set to the absolute path of
  `server/plugin-host/index.js`
- (Optional) `COMPLIANCE_OA_WEBHOOK` set to your internal OA system's
  approval API endpoint for sensitive operation escalation

### Install

```bash
export PARSAR_SERVER_URL=http://localhost:8080
export PARSAR_RUNNER_TOKEN=<your-token>
export PARSAR_WORKSPACE_ID=<your-workspace-id>

parsar plugin add ./examples/plugins/compliance-approval
```

### What It Does

**Hook: before_permission_forward**

When the Agent requests approval for a command, the hook evaluates it:

| Pattern | Decision | Example |
|---------|----------|---------|
| `rm -rf`, `DROP TABLE`, `mkfs` | Auto-deny | `rm -rf /tmp/data` → blocked |
| `kubectl delete`, `curl\|bash`, `chmod 777` | Escalate to OA + ask human | Sent to internal OA webhook |
| `ls`, `cat`, `git status` | Auto-allow | No approval needed |
| Everything else | Ask human | Normal approval flow |

**Tools:**

- **compliance_check_status**: Shows active policy rules and recent audit entries
- **compliance_audit_log**: Query the audit log by action type

**Skill:**

Tells the Agent about the compliance policy so it doesn't try to work
around denied commands.

### Verify

1. Install the plugin and restart the server
2. Bind it to an Agent via the admin UI
3. Start a new conversation and ask the Agent to run `rm -rf /tmp/scratch`
4. The command should be **auto-denied** — no approval card appears, the
   run timeline shows "Auto-denied by plugin" with the reason
5. Ask the Agent to run `kubectl delete pod nginx`
6. This should trigger **OA escalation** — the approval card appears
   AND the OA system is notified
7. Ask the Agent to run `ls -la`
8. This should be **auto-allowed** — no approval card, command executes

### End-to-End Flow

```
Agent requests bash "rm -rf /"
  → Daemon emits permission_request
  → Go server receives EventPermissionRequest
  → pluginHookInvoker calls hooks/invoke { event: "before_permission_forward", payload: {...} }
  → Plugin-host runs compliance handler
  → Handler matches DENY_PATTERNS → returns { deny: true, reason: "危险命令：递归强制删除" }
  → Go server auto-submits denial to daemon (no human interaction created)
  → Event persisted as "permission.auto_denied" with hook_reason
  → SSE stream sends permission event with hook_decision
  → Frontend skips approval card, shows "Auto-denied by plugin" in timeline
```
