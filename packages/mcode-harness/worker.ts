import { LocalReadTool, LocalWriteTool, LocalEditTool, LocalBashTool } from '@native/local-pi-tools';
import { LocalGrepTool } from '@native/local-grep';
import { LocalGlobTool } from '@native/local-glob';
import { toRuntimeTool, isRuntimeToolInputValid } from '@mavis/agent-core/tools';
import { readFileSync } from 'node:fs';
import { isAbsolute } from 'node:path';

const root = process.argv[2];
if (!root || !isAbsolute(root)) throw new Error('absolute workspace argument required');
const tools = [new LocalReadTool(root), new LocalWriteTool(root), new LocalEditTool(root),
  new LocalBashTool(root, undefined, { mode: 'off' }), new LocalGrepTool(root), new LocalGlobTool(root)]
  .map(toRuntimeTool);
if (process.argv[3] === '--describe') {
  process.stdout.write(JSON.stringify(tools.map(t => ({ name: t.def.name,
    description: t.def.description, inputSchema: t.def.schema }))) + '\n');
  process.exit(0);
}
const request = JSON.parse(readFileSync(0, 'utf8'));
const tool = tools.find(value => value.def.name === request.tool);
if (!tool || !request.input || typeof request.input !== 'object' || Array.isArray(request.input) ||
    !isRuntimeToolInputValid(tools, request.tool, request.input))
  throw new Error('invalid tool request');
const context = { sessionId: 'worker', turnId: 'call', allowBashAutoPromotion: false,
  canConsumeBackgroundBashOutput: false };
try {
  const result = await tool.impl.execute(context, request.input);
  process.stdout.write(JSON.stringify(result) + '\n');
} catch (error) {
  process.stdout.write(JSON.stringify({ tool_name: request.tool, isError: true,
    text: error instanceof Error ? error.message : String(error), content: [] }) + '\n');
  process.exitCode = 1;
}
