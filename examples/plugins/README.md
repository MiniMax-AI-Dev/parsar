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

