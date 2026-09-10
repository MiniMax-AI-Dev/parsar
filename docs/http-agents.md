# Connect an external HTTP Agent

Use **Agents → New Agent → External Agent** to connect a service you operate.
Enter its HTTP(S) endpoint, choose an optional workspace bearer credential,
and save. No Parsar model or paired runtime is required. The service must be
reachable from the Parsar server, which may have a different network address
from your browser.

The API server dispatches HTTP Agents automatically, including in `make dev-all`.
Do not run the legacy `http-runner-once` or `http-runner-loop` against the same
database; they bypass the server's credential, queue, and Stop handling.

The service manages its models, tools, approvals, and conversation history.
Parsar provides the existing Web, IM, and Agent MCP entry points, persists
results, and records usage supplied by the service. Parsar Skill, MCP, and
knowledge bindings are not injected into external services.

## Request and response

Parsar sends one `POST` with `Content-Type: application/json`. With bearer
authentication enabled, it also sends `Authorization: Bearer <token>`.
Redirects are rejected. Tokens are encrypted in the credential vault and
must be active HTTP Agent credentials managed by the Agent's workspace.

```json
{
  "run_id": "run UUID",
  "workspace_id": "workspace UUID",
  "conversation_id": "conversation UUID",
  "agent_id": "agent UUID",
  "agent_name": "Service desk",
  "agent_slug": "agent-example",
  "trigger_message_content": "What is the ticket status?",
  "agent_config": {"system_prompt": "Answer using approved ticket data."}
}
```

Return a 2xx response with a nonempty `content` string and optional usage:

```json
{
  "content": "Your ticket is ready for pickup.",
  "usage": {
    "provider": "your-provider",
    "model": "your-model",
    "input_tokens": 120,
    "output_tokens": 12,
    "cost_usd": 0.001
  }
}
```

Use actual per-run usage; omit values you cannot report. Parsar does not infer
prices. Response JSON is limited to 4 MiB. The current protocol returns a final
reply rather than streaming tokens or tool events. It sends text and optional
instructions; attachments and Parsar credential/configuration data are not sent.

Key your service's conversation history by `conversation_id`, and deduplicate
requests by `run_id`. Failed runs can be retried through the existing run UI;
a retry receives a new run ID. The execution deadline is 30 minutes. **Stop**
cancels Parsar's HTTP request when handled by the server instance executing it;
use a single Parsar server instance for this initial connector. Cross-instance
request cancellation is not supported. The service should honor request
cancellation to stop its own work. Parsar cannot force a remote process to terminate.

See [the contributor contract](../CONTRIBUTING.md#external-http-agents) for
implementation ownership and compatibility requirements.
