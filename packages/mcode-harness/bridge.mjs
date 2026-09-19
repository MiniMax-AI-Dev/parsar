import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';
import { CallToolRequestSchema, ListToolsRequestSchema } from '@modelcontextprotocol/sdk/types.js';
import { readFileSync } from 'node:fs';
import { isAbsolute } from 'node:path';
import { ToolExecutor } from './tool-executor.mjs';

const profile = process.argv[2];
if (!profile || !isAbsolute(profile)) throw new Error('Private workspace profile is required');
const definitions = JSON.parse(readFileSync(new URL('./dist/tools.json', import.meta.url), 'utf8'));
const tools = new Map(definitions.map(tool => ['workspace_' + tool.name, tool]));
const executor = new ToolExecutor(profile);
const server = new Server({ name: 'parsar-workspace', version: '1' }, { capabilities: { tools: {} } });
server.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: [...tools].map(([name, tool]) => ({ ...tool, name,
    description: 'Bound working directory: /workspace. ' + tool.description })),
}));
server.setRequestHandler(CallToolRequestSchema, async (request, extra) => {
  const tool = tools.get(request.params.name);
  if (!tool) throw new Error('Unknown workspace tool');
  const result = await executor.execute(tool.name, request.params.arguments, extra.signal);
  if (result.content.some(item => item.type !== 'text' && item.type !== 'image'))
    return { isError: true, content: [{ type: 'text', text: 'This workspace transport supports text and image results only.' }] };
  return { isError: result.isError === true,
    content: result.content.length ? result.content : [{ type: 'text', text: result.text }] };
});

let shutdown;
const close = () => shutdown ??= executor.close().then(() => server.close());
server.onclose = () => { void close(); };
process.stdin.on('end', () => { void close(); });
process.on('SIGTERM', () => { void close(); });
process.on('SIGINT', () => { void close(); });
await server.connect(new StdioServerTransport());
