// Plugin Server SDK — provides the PluginServerContext (ctx) passed to each
// plugin's server/index.js default export.

/**
 * Creates a PluginServerContext for one plugin.
 *
 * @param {string} pluginName - the plugin's name from manifest.json
 * @param {Map} toolRegistry - shared registry (name → ToolDefinition)
 * @param {Map} hookRegistry - shared registry (eventName → HookHandler[])
 * @returns {Object} ctx - the plugin server context
 */
export function createPluginContext(pluginName, toolRegistry, hookRegistry) {
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

  const hooks = createHooksContext(pluginName, hookRegistry);

  return {
    tools,
    hooks,
    // Placeholder for future phases — calling these throws NotImplemented.
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
 * Supported hook event names.
 */
export const HOOK_EVENTS = [
  'before_permission_forward',
  'after_tool_result',
  'on_run_complete',
  'on_run_error',
];

/**
 * Creates the ctx.hooks namespace for a plugin.
 *
 * @param {string} pluginName - owning plugin name
 * @param {Map<string, Array>} hookRegistry - shared registry (eventName → handler[])
 * @returns {Object} hooks API
 */
function createHooksContext(pluginName, hookRegistry) {
  return {
    /**
     * Register a hook handler for an event.
     *
     * @param {string} eventName - one of HOOK_EVENTS
     * @param {Function} handler - async (payload) => { deny?, allow?, ask_human?, reason? }
     */
    on(eventName, handler) {
      if (!eventName || typeof eventName !== 'string') {
        throw new Error(`[${pluginName}] hooks.on: eventName must be a non-empty string`);
      }
      if (!HOOK_EVENTS.includes(eventName)) {
        throw new Error(
          `[${pluginName}] hooks.on: unknown event "${eventName}". Supported: ${HOOK_EVENTS.join(', ')}`
        );
      }
      if (typeof handler !== 'function') {
        throw new Error(
          `[${pluginName}] hooks.on("${eventName}"): handler must be a function`
        );
      }

      if (!hookRegistry.has(eventName)) {
        hookRegistry.set(eventName, []);
      }
      hookRegistry.get(eventName).push({
        pluginName,
        handler,
      });
    },

    /**
     * List registered events for this plugin (introspection).
     * @returns {string[]}
     */
    registered() {
      const events = [];
      for (const [eventName, handlers] of hookRegistry) {
        if (handlers.some((h) => h.pluginName === pluginName)) {
          events.push(eventName);
        }
      }
      return events;
    },
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
