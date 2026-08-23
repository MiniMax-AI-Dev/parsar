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
