#!/usr/bin/env node
// Parsar Plugin Host
// Loads plugin server modules from --plugins-dir and exposes their tools
// via MCP stdio protocol (JSON-RPC 2.0 over stdin/stdout).
//
// Usage:
//   node index.js --plugins-dir /path/to/plugins [--plugin <name>]
//
// If --plugin is provided, only that specific plugin is loaded.
// Otherwise all plugins in the directory with a server entry are loaded.

import { parseArgs } from 'node:util';
import { createPluginHost } from './lib/host.js';
import { startMCPStdio } from './lib/mcp-stdio.js';

const { values } = parseArgs({
  options: {
    'plugins-dir': { type: 'string' },
    'plugin': { type: 'string' },
  },
  strict: false,
});

const pluginsDir = values['plugins-dir'];
const pluginFilter = values['plugin'];

if (!pluginsDir) {
  process.stderr.write('plugin-host: --plugins-dir is required\n');
  process.exit(1);
}

const host = await createPluginHost(pluginsDir, { pluginFilter });
startMCPStdio(host);
