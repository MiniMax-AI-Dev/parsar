// MCP stdio transport — implements the Model Context Protocol (JSON-RPC 2.0)
// over stdin/stdout for tool invocation and hook interception.
//
// Protocol reference: https://modelcontextprotocol.io/specification
//
// Supported methods:
//   initialize           → server info + capabilities
//   tools/list           → list all registered tools
//   tools/call           → invoke a tool handler
//   hooks/list           → list all registered hook events
//   hooks/invoke         → invoke hook handlers for an event (Phase 3)
//   notifications/initialized → client ack (no response)
//   ping                 → pong

import { createInterface } from 'node:readline';

const JSONRPC_VERSION = '2.0';

const SERVER_INFO = {
  name: 'parsar-plugin-host',
  version: '0.1.0',
};

const SERVER_CAPABILITIES = {
  tools: {},
  hooks: {},
};

/**
 * Start the MCP stdio transport loop.
 * @param {import('./host.js').PluginHost} host
 */
export function startMCPStdio(host) {
  const rl = createInterface({ input: process.stdin, terminal: false });

  rl.on('line', async (line) => {
    if (!line.trim()) return;

    let request;
    try {
      request = JSON.parse(line);
    } catch {
      writeLine(makeError(null, -32700, 'Parse error'));
      return;
    }

    // Notifications have no id — no response needed.
    if (request.id === undefined || request.id === null) {
      // Accept notifications silently.
      return;
    }

    try {
      const result = await handleRequest(host, request);
      writeLine({ jsonrpc: JSONRPC_VERSION, id: request.id, result });
    } catch (err) {
      if (err.code) {
        writeLine(makeError(request.id, err.code, err.message));
      } else {
        writeLine(makeError(request.id, -32603, err.message ?? 'Internal error'));
      }
    }
  });

  rl.on('close', () => {
    process.exit(0);
  });
}

/**
 * Route a JSON-RPC request to the appropriate handler.
 */
async function handleRequest(host, request) {
  const { method, params } = request;

  switch (method) {
    case 'initialize':
      return handleInitialize();
    case 'ping':
      return {};
    case 'tools/list':
      return handleToolsList(host);
    case 'tools/call':
      return handleToolsCall(host, params);
    case 'hooks/list':
      return handleHooksList(host);
    case 'hooks/invoke':
      return handleHooksInvoke(host, params);
    default: {
      const err = new Error(`Method not found: ${method}`);
      err.code = -32601;
      throw err;
    }
  }
}

function handleInitialize() {
  return {
    protocolVersion: '2024-11-05',
    serverInfo: SERVER_INFO,
    capabilities: SERVER_CAPABILITIES,
  };
}

function handleToolsList(host) {
  const tools = [];
  for (const [, def] of host.tools) {
    tools.push({
      name: def.name,
      description: def.description,
      inputSchema: def.parameters,
    });
  }
  return { tools };
}

async function handleToolsCall(host, params) {
  if (!params?.name) {
    const err = new Error('tools/call: missing params.name');
    err.code = -32602;
    throw err;
  }

  const def = host.tools.get(params.name);
  if (!def) {
    const err = new Error(`tools/call: unknown tool "${params.name}"`);
    err.code = -32602;
    throw err;
  }

  const args = params.arguments ?? {};
  const timeoutMs = 30_000;

  let result;
  try {
    result = await Promise.race([
      def.handler(args),
      timeout(timeoutMs, `Tool "${params.name}" timed out after ${timeoutMs}ms`),
    ]);
  } catch (handlerErr) {
    // Handler errors are returned as tool-level errors (isError: true),
    // not JSON-RPC errors, per MCP spec.
    return {
      content: [
        {
          type: 'text',
          text: `Error: ${handlerErr.message ?? String(handlerErr)}`,
        },
      ],
      isError: true,
    };
  }

  // Normalize handler return value into MCP content format.
  return normalizeToolResult(result);
}

/**
 * Normalize a plugin handler's return value into MCP-compatible content array.
 *
 * Accepted return shapes:
 *   { value: any, presentation?: any }  → canonical plugin result
 *   string                              → text content
 *   any other                           → JSON serialized text content
 */
function normalizeToolResult(result) {
  if (result === null || result === undefined) {
    return { content: [{ type: 'text', text: '' }] };
  }

  if (typeof result === 'string') {
    return { content: [{ type: 'text', text: result }] };
  }

  // Canonical plugin result: { value, presentation? }
  if (typeof result === 'object' && 'value' in result) {
    const text =
      typeof result.value === 'string'
        ? result.value
        : JSON.stringify(result.value, null, 2);

    const content = [{ type: 'text', text }];

    // Stash presentation metadata in a second content block so the
    // Parsar server can extract it for client-side rendering (Phase 2).
    if (result.presentation) {
      content.push({
        type: 'text',
        text: JSON.stringify({ __parsar_presentation: result.presentation }),
      });
    }

    return { content };
  }

  // Fallback: serialize as JSON.
  return { content: [{ type: 'text', text: JSON.stringify(result, null, 2) }] };
}

// ─── Hooks Methods ───────────────────────────────────────────────────────────

/**
 * Return the list of registered hook events and their plugin sources.
 */
function handleHooksList(host) {
  const hooks = [];
  for (const [eventName, handlers] of host.hooks) {
    hooks.push({
      event: eventName,
      plugins: handlers.map((h) => h.pluginName),
    });
  }
  return { hooks };
}

/**
 * Invoke all registered handlers for a hook event.
 *
 * Params:
 *   { event: string, payload: object }
 *
 * Returns:
 *   { decision: "deny" | "allow" | "ask_human", reason?: string, plugin?: string }
 *
 * Semantics:
 *   - Handlers run sequentially in registration order.
 *   - First handler that returns { deny: true } or { allow: true } wins.
 *   - If all return { ask_human: true } or no handlers exist, returns ask_human.
 *   - Per-handler timeout: 5s. Timeout → skip that handler (log warning).
 *   - Total invocation timeout: handled by the caller (Go side).
 */
async function handleHooksInvoke(host, params) {
  if (!params?.event) {
    const err = new Error('hooks/invoke: missing params.event');
    err.code = -32602;
    throw err;
  }

  const handlers = host.hooks.get(params.event);
  if (!handlers || handlers.length === 0) {
    // No handlers registered — signal the caller to use default behavior.
    return { decision: 'no_handler' };
  }

  const payload = params.payload ?? {};
  const perHandlerTimeout = 5_000;

  for (const entry of handlers) {
    let result;
    try {
      result = await Promise.race([
        entry.handler(payload),
        timeout(perHandlerTimeout, `Hook handler from "${entry.pluginName}" timed out (${perHandlerTimeout}ms)`),
      ]);
    } catch (handlerErr) {
      process.stderr.write(
        `plugin-host: hook "${params.event}" handler from "${entry.pluginName}" failed: ${handlerErr.message}\n`
      );
      // Handler error → skip, continue to next handler.
      continue;
    }

    if (!result || typeof result !== 'object') {
      continue;
    }

    // First decisive answer wins.
    if (result.deny) {
      return {
        decision: 'deny',
        reason: result.reason ?? '',
        plugin: entry.pluginName,
      };
    }
    if (result.allow) {
      return {
        decision: 'allow',
        reason: result.reason ?? '',
        plugin: entry.pluginName,
      };
    }
    if (result.ask_human) {
      return {
        decision: 'ask_human',
        reason: result.reason ?? '',
        plugin: entry.pluginName,
      };
    }
    // Handler returned something else — treat as no-op, continue.
  }

  // All handlers either skipped or returned no decision → fallback.
  return { decision: 'ask_human', reason: 'no decisive handler' };
}

// ─── Utilities ───────────────────────────────────────────────────────────────

function timeout(ms, message) {
  return new Promise((_, reject) => {
    setTimeout(() => reject(new Error(message)), ms);
  });
}

function makeError(id, code, message) {
  return {
    jsonrpc: JSONRPC_VERSION,
    id,
    error: { code, message },
  };
}

function writeLine(obj) {
  process.stdout.write(JSON.stringify(obj) + '\n');
}
