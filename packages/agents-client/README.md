# Agents API Go client

`v1` configures the [official openai-go SDK](https://github.com/openai/openai-go/tree/v3.61.0),
pinned in the root `go.mod`. It returns the SDK's Session service directly. Request
types, response parsing, cursor pagination, events and errors remain SDK-owned.
The external protocol baseline remains the pinned Python SDK in
[`contracts/agents-api/upstream.json`](../../contracts/agents-api/upstream.json).

```go
import (
    agentsclient "github.com/MiniMax-AI-Dev/parsar/packages/agents-client/v1"
    "github.com/openai/openai-go/v3"
    "github.com/openai/openai-go/v3/option"
)

sessions, err := agentsclient.New(agentsclient.Config{
    BaseURL: serviceBaseURL, // Includes /v1; use TLS for remote connections.
    APIKey: serviceKey,     // Execution tenant identity, not a product login token.
})
if err != nil {
    return err
}
session, err := sessions.New(ctx, openai.BetaAgentSessionNewParams{
    Agent: openai.BetaAgentSessionNewParamsAgent{
        Model: openai.String("requested-model"),
        Instructions: openai.String("Follow the supplied instructions."),
    },
    Environment: openai.EnvironmentParamUnion{
        OfParamNone: &openai.EnvironmentParamNone{},
    },
}, option.WithHeader("Idempotency-Key", operationID))
```

Use a stable, non-secret operation ID for a creation retry, with the same request.
Omitting the key creates a new Session on each call. SDK retries are disabled by
default. Requests honor the caller's context; the default HTTP timeout is 30 seconds.
An optional trusted HTTP client can configure the transport/timeout. Its cookie jar
is ignored and redirects are rejected. No OpenAI environment credentials or product
session cookies are inherited. Per-request SDK options are trusted application code;
do not accept them from end users.

Use `sessions.Get`, `sessions.List` and the returned page's `GetNextPage` directly.
Errors can be inspected using `errors.As(err, &apiErr)` with `*openai.Error`.
SDK errors retain the request and response: log selected status/code fields, not
raw errors, request dumps or credentials. Constructing this client does not switch
Parsar's current execution flow or grant workspace/user permissions.

The SDK includes more methods than the server currently supports. Only the
[documented Session subset](../../contracts/agents-api/README.md) is implemented;
other methods receive explicit service errors. Team orchestration belongs in Parsar
and depends on `openai-agents-python`, not this client package.

`make check-go` includes configuration tests. The real-service harness in
`services/agents-api/tests/official_client.py` runs `TestService` with fresh tenants
and a dedicated PostgreSQL database. It validates Go-created Sessions through the
official Python SDK as well. No product database or model calls are involved.
