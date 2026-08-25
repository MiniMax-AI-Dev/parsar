// Plugin Host — discovers, loads, and manages plugin server modules.

import { readdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createPluginContext } from './sdk.js';

/**
 * @typedef {Object} ToolDefinition
 * @property {string} name
 * @property {string} description
 * @property {Object} parameters - JSON Schema object
 * @property {Function} handler - async (args) => result
 * @property {string} pluginName - owning plugin
 */

/**
 * @typedef {Object} PluginHost
 * @property {Map<string, ToolDefinition>} tools
 * @property {string[]} loadedPlugins
 */

/**
 * Creates a plugin host by scanning the plugins directory and loading
 * server modules.
 *
 * @param {string} pluginsDir - path to the plugins directory
 * @param {Object} [opts]
 * @param {string} [opts.pluginFilter] - load only this plugin name
 * @returns {Promise<PluginHost>}
 */
export async function createPluginHost(pluginsDir, opts = {}) {
  const tools = new Map();
  const loadedPlugins = [];
  const { pluginFilter } = opts;

  let entries;
  try {
    entries = await readdir(pluginsDir, { withFileTypes: true });
  } catch (err) {
    process.stderr.write(`plugin-host: cannot read plugins-dir: ${err.message}\n`);
    return { tools, loadedPlugins };
  }

  const dirs = entries.filter((e) => e.isDirectory());

  for (const dir of dirs) {
    const pluginDir = join(pluginsDir, dir.name);
    const manifestPath = join(pluginDir, 'manifest.json');

    let manifest;
    try {
      const raw = await readFile(manifestPath, 'utf8');
      manifest = JSON.parse(raw);
    } catch {
      // No manifest or invalid JSON — skip silently.
      continue;
    }

    // Skip plugins without a server entry.
    if (!manifest.server?.entry) {
      continue;
    }

    // If filtering to a specific plugin, skip non-matching.
    if (pluginFilter && manifest.name !== pluginFilter) {
      continue;
    }

    const serverEntry = join(pluginDir, manifest.server.entry);

    try {
      const ctx = createPluginContext(manifest.name, tools);
      const moduleURL = pathToFileURL(serverEntry).href;
      const mod = await import(moduleURL);
      const setup = mod.default ?? mod;

      if (typeof setup === 'function') {
        await setup(ctx);
      }

      loadedPlugins.push(manifest.name);
      process.stderr.write(
        `plugin-host: loaded "${manifest.name}" (${ctx.tools._count()} tools)\n`
      );
    } catch (err) {
      process.stderr.write(
        `plugin-host: error loading "${manifest.name}": ${err.message}\n`
      );
      // Continue loading other plugins — one failure doesn't block others.
    }
  }

  process.stderr.write(
    `plugin-host: ready (${loadedPlugins.length} plugins, ${tools.size} tools)\n`
  );

  return { tools, loadedPlugins };
}
