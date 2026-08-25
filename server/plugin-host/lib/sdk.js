// Plugin Server SDK — provides the PluginServerContext (ctx) passed to each
// plugin's server/index.js default export.

/**
 * Creates a PluginServerContext for one plugin.
 *
 * @param {string} pluginName - the plugin's name from manifest.json
 * @param {Map} toolRegistry - shared registry (name → ToolDefinition)
 * @returns {Object} ctx - the plugin server context
 */
export function createPluginContext(pluginName, toolRegistry) {
  let toolCount = 0;

  const tools = {
    /**
     * Register a tool handler.
     *
     * @param {string} name - tool name (must be unique across all plugins)
     * @param {Object} definition
     * @param {string} definition.description - human-readable description
     * @param {Object} definition.parameters - JSON Schema for the tool's input
     * @param {Function} definition.handler - async (args) => { value, presentation? }
     */
    define(name, definition) {
      if (!name || typeof name !== 'string') {
        throw new Error(`[${pluginName}] tools.define: name must be a non-empty string`);
      }
      if (toolRegistry.has(name)) {
        throw new Error(
          `[${pluginName}] tools.define: tool "${name}" is already registered`
        );
      }
      if (typeof definition.handler !== 'function') {
        throw new Error(
          `[${pluginName}] tools.define("${name}"): handler must be a function`
        );
      }

      const params = normalizeParameters(definition.parameters ?? {});

      toolRegistry.set(name, {
        name,
        description: definition.description ?? '',
        parameters: params,
        handler: definition.handler,
        pluginName,
      });
      toolCount++;
    },

    /** @internal — used by host for logging */
    _count() {
      return toolCount;
    },
  };

  // Future phases will add ctx.hooks, ctx.credentials, ctx.api, etc.
  // For Phase 1, only ctx.tools is implemented.
  return {
    tools,
    // Placeholder for future phases — calling these throws NotImplemented.
    hooks: notImplementedProxy('hooks'),
    credentials: notImplementedProxy('credentials'),
    api: notImplementedProxy('api'),
  };
}

/**
 * Normalize the parameters definition into JSON Schema "object" format.
 * Supports shorthand like { title: 'string', project: 'string' }
 * as well as full JSON Schema objects.
 */
function normalizeParameters(params) {
  // Already valid JSON Schema object.
  if (params.type === 'object' && params.properties) {
    return params;
  }

  // Shorthand: key → type-string
  const properties = {};
  const required = [];
  for (const [key, value] of Object.entries(params)) {
    if (typeof value === 'string') {
      properties[key] = { type: value };
      required.push(key);
    } else if (typeof value === 'object' && value !== null) {
      properties[key] = value;
      if (!value.optional) {
        required.push(key);
      }
    }
  }

  return {
    type: 'object',
    properties,
    required: required.length > 0 ? required : undefined,
  };
}

/**
 * Returns a Proxy that throws NotImplementedError for any property access.
 */
function notImplementedProxy(namespace) {
  return new Proxy(
    {},
    {
      get(_, prop) {
        if (prop === Symbol.toPrimitive || prop === 'then') return undefined;
        return () => {
          throw new Error(
            `ctx.${namespace}.${String(prop)} is not implemented yet (planned for a future phase)`
          );
        };
      },
    }
  );
}
