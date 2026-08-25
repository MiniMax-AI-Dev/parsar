#!/usr/bin/env node
// Plugin client bundle builder.
// Called by the CLI: node build-client.js <entry> <outfile>
//
// Bundles the entry TSX/JS file into a single ESM file with React externalized.
// The output is a self-registering bundle that calls window.__PARSAR_PLUGIN_API__.

import { build } from 'esbuild'
import { dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const [entry, outfile] = process.argv.slice(2)

if (!entry || !outfile) {
  process.stderr.write('Usage: node build-client.js <entry> <outfile>\n')
  process.exit(1)
}

try {
  await build({
    entryPoints: [entry],
    outfile,
    bundle: true,
    format: 'iife',
    platform: 'browser',
    target: 'es2020',
    // Plugin authors access React via window.__PARSAR_PLUGIN_API__.React.
    // We inject a banner that exposes it as a top-level var and use a
    // plugin to rewrite any `require("react")` calls to the global.
    external: [],
    banner: {
      js: [
        `var React = window.__PARSAR_PLUGIN_API__.React;`,
        `var require = function(m) { if (m === "react" || m === "react-dom") return React; throw new Error("require(" + m + ") is not available in plugin bundles"); };`,
      ].join('\n'),
    },
    plugins: [{
      name: 'externalize-react',
      setup(build) {
        // Resolve react/react-dom to an empty module — the banner shim handles it.
        build.onResolve({ filter: /^react(-dom)?$/ }, () => ({
          path: 'react',
          namespace: 'react-shim',
        }))
        build.onLoad({ filter: /.*/, namespace: 'react-shim' }, () => ({
          contents: 'module.exports = window.__PARSAR_PLUGIN_API__.React;',
          loader: 'js',
        }))
      },
    }],
    define: {
      'process.env.NODE_ENV': '"production"',
    },
    minify: false, // Keep readable for debugging during development.
    sourcemap: false,
  })
  process.stderr.write(`[build-client] built ${outfile}\n`)
} catch (err) {
  process.stderr.write(`[build-client] error: ${err.message}\n`)
  process.exit(1)
}
