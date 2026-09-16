import assert from "node:assert/strict";
import test from "node:test";
import { parseStart } from "../dist/adapter.js";
import { MCPProfile } from "../dist/mcp.js";

const reference = "PARSAR_MCP_BEARER_" + "A".repeat(26);
const server = { server_label: "private", server_url: "https://example.invalid/mcp", allowed_tools: ["echo.v1"], bearer_token_env_var: reference };
const start = servers => ({ type: "start", prompt: "hello", model: "fixture", system_prompt: "", cwd: "/tmp", mcp_http_servers: servers });

test("bearer references remain literal in native configuration and private status is not retained", t => {
  const token = "fixture-private-bearer+/==";
  process.env[reference] = token;
  t.after(() => { delete process.env[reference]; });
  const declarations = [server, { server_label: "anonymous", server_url: "http://example.invalid/mcp", allowed_tools: [] }];
  const parsed = parseStart(JSON.stringify(start(declarations)));
  const profile = new MCPProfile(parsed.mcp_http_servers, []);
  assert.equal(profile.servers.private.headers.Authorization, "Bearer ${" + reference + "}");
  assert.equal(profile.servers.anonymous.headers.Authorization, "");
  assert.equal(JSON.stringify(profile.servers).includes(token), false);
  profile.verify(["mcp__private__echo_v1"], [
    { name: "private", status: "connected", tools: [{ name: "echo.v1" }], config: { ...profile.servers.private, headers: { Authorization: "Bearer " + token } } },
    { name: "anonymous", status: "connected", tools: [] },
  ], "session");
  assert.deepEqual([...profile.identities.values()], [{ server: "private", name: "echo.v1" }]);
  assert.equal(JSON.stringify(profile).includes(token), false);
  assert.deepEqual(parsed.mcp_http_servers, declarations);
});

test("untrusted header expressions, raw tokens and non-HTTPS authenticated declarations are rejected", () => {
  for (const invalid of [
    { ...server, bearer_token: "fixture-secret" },
    { ...server, headers: { Authorization: "Bearer fixture-secret" } },
    { ...server, server_url: "http://example.invalid/mcp" },
    ...[null, "", "ANTHROPIC_AUTH_TOKEN", "PARSAR_MCP_BEARER_", "${OPERATOR_TOKEN}", reference + ":-fallback"].map(bearer_token_env_var => ({ ...server, bearer_token_env_var })),
  ]) assert.throws(() => parseStart(JSON.stringify(start([invalid]))));
  assert.throws(() => parseStart(JSON.stringify(start([server, { ...server, server_label: "other" }]))));
});

test("missing credential environment fails before the native query", () => {
  const missing = "PARSAR_MCP_BEARER_" + "B".repeat(26);
  delete process.env[missing];
  assert.throws(() => new MCPProfile([{ ...server, bearer_token_env_var: missing }], []), /missing MCP credential environment/);
});
