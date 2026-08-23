// Compliance Approval Plugin — server hooks + tools
// Demonstrates Phase 3 plugin hook capabilities: ctx.hooks.on
//
// This plugin implements enterprise compliance rules:
// 1. Auto-deny dangerous commands (rm -rf, DROP TABLE, etc.)
// 2. Forward sensitive operations to internal OA approval system
// 3. Auto-allow known-safe operations
// 4. Everything else → ask_human (normal approval flow)

// ─── Configuration ───────────────────────────────────────────────────────────

// In production, these would be loaded from env or an admin config API.
const OA_WEBHOOK_URL = process.env.COMPLIANCE_OA_WEBHOOK || 'http://oa.internal/api/approval/create';

// Patterns that trigger immediate denial — no human override by default.
const DENY_PATTERNS = [
  { pattern: /rm\s+(-[a-zA-Z]*f[a-zA-Z]*\s+|--force\s+)*-[a-zA-Z]*r[a-zA-Z]*|rm\s+(-[a-zA-Z]*r[a-zA-Z]*\s+|--recursive\s+)*-[a-zA-Z]*f/, reason: '危险命令：递归强制删除 (rm -rf)' },
  { pattern: /rm\s+(-\w+\s+)*\/(\s|$)/, reason: '危险命令：删除根目录' },
  { pattern: /DROP\s+(DATABASE|TABLE|SCHEMA)/i, reason: '危险 SQL：DROP 语句' },
  { pattern: /TRUNCATE\s+TABLE/i, reason: '危险 SQL：TRUNCATE TABLE' },
  { pattern: />\s*\/dev\/sd[a-z]/, reason: '危险操作：直接写入磁盘设备' },
  { pattern: /mkfs\s/, reason: '危险操作：格式化文件系统' },
  { pattern: /:(){ :\|:& };:/, reason: '危险操作：fork bomb' },
  { pattern: /dd\s+.*of=\/dev\//, reason: '危险操作：dd 写入设备' },
];

// Patterns that trigger OA escalation (ask_human + notify OA system).
const SENSITIVE_PATTERNS = [
  { pattern: /kubectl\s+(delete|drain|cordon)/, category: 'k8s_destructive' },
  { pattern: /docker\s+(rm|rmi|system\s+prune)/, category: 'docker_cleanup' },
  { pattern: /ALTER\s+TABLE.*DROP/i, category: 'schema_change' },
  { pattern: /DELETE\s+FROM\s+\w+\s*(WHERE\s+1\s*=\s*1|$)/i, category: 'mass_delete' },
  { pattern: /curl.*\|\s*(bash|sh|zsh)/, category: 'remote_exec' },
  { pattern: /chmod\s+777/, category: 'unsafe_permissions' },
  { pattern: /iptables\s+-F/, category: 'firewall_flush' },
];

// Known-safe operations that can be auto-allowed (bypass human approval).
const SAFE_PATTERNS = [
  /^ls\s/,
  /^cat\s/,
  /^echo\s/,
  /^pwd$/,
  /^whoami$/,
  /^date$/,
  /^git\s+(status|log|diff|branch)/,
  /^npm\s+(list|outdated|audit)/,
  /^go\s+(version|env|list)/,
];

// ─── Audit Log (in-memory for demo; production would use a DB) ───────────────

const auditLog = [];

function logAudit(entry) {
  auditLog.push({
    ...entry,
    timestamp: new Date().toISOString(),
  });
  // Keep last 1000 entries in memory.
  if (auditLog.length > 1000) auditLog.shift();
}

// ─── OA Integration (mock for demo) ─────────────────────────────────────────

async function notifyOASystem(request, category) {
  const payload = {
    type: 'agent_permission_escalation',
    category,
    tool: request.tool,
    title: request.title || '',
    detail: request.detail || '',
    args: request.args || '',
    request_id: request.request_id,
    timestamp: new Date().toISOString(),
  };

  try {
    const res = await fetch(OA_WEBHOOK_URL, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
      signal: AbortSignal.timeout(3000),
    });
    logAudit({ action: 'oa_notify', category, request_id: request.request_id, status: res.ok ? 'sent' : 'failed' });
  } catch (err) {
    // OA notification is best-effort — don't block the approval flow.
    logAudit({ action: 'oa_notify', category, request_id: request.request_id, status: 'error', error: err.message });
  }
}

// ─── Plugin Setup ────────────────────────────────────────────────────────────

export default (ctx) => {

  // ═══ Hook: before_permission_forward ═══
  // This is the core of the compliance plugin — intercepts every
  // permission request before it reaches the human approval UI.

  ctx.hooks.on('before_permission_forward', async (request) => {
    const command = extractCommand(request);

    // 1. Check deny patterns (immediate rejection, no human override).
    for (const rule of DENY_PATTERNS) {
      if (rule.pattern.test(command)) {
        logAudit({
          action: 'auto_deny',
          tool: request.tool,
          command,
          reason: rule.reason,
          request_id: request.request_id,
        });
        return { deny: true, reason: rule.reason };
      }
    }

    // 2. Check sensitive patterns (escalate to OA + ask human).
    for (const rule of SENSITIVE_PATTERNS) {
      if (rule.pattern.test(command)) {
        logAudit({
          action: 'oa_escalation',
          tool: request.tool,
          command,
          category: rule.category,
          request_id: request.request_id,
        });
        // Fire-and-forget: notify OA system in background.
        notifyOASystem(request, rule.category);
        return { ask_human: true, reason: `敏感操作 (${rule.category})，已同步到 OA 审批` };
      }
    }

    // 3. Check safe patterns (auto-allow, no approval needed).
    for (const pattern of SAFE_PATTERNS) {
      if (pattern.test(command)) {
        logAudit({
          action: 'auto_allow',
          tool: request.tool,
          command,
          request_id: request.request_id,
        });
        return { allow: true, reason: '安全命令，自动放行' };
      }
    }

    // 4. Default: ask human (normal approval flow).
    return { ask_human: true };
  });

  // ═══ Tool: compliance_check_status ═══
  // Lets the agent (or admin) check what compliance rules are active.

  ctx.tools.define('compliance_check_status', {
    description: 'Check the current compliance policy status — shows active deny rules, sensitive patterns, and recent audit log entries.',
    parameters: {
      type: 'object',
      properties: {
        last_n: {
          type: 'number',
          description: 'Number of recent audit log entries to show (default: 10).',
        },
      },
    },
    handler: async (args) => {
      const lastN = args.last_n || 10;
      return {
        value: {
          policy_version: '1.0.0',
          deny_rules_count: DENY_PATTERNS.length,
          sensitive_rules_count: SENSITIVE_PATTERNS.length,
          safe_patterns_count: SAFE_PATTERNS.length,
          recent_audit: auditLog.slice(-lastN),
          oa_webhook_configured: !!process.env.COMPLIANCE_OA_WEBHOOK,
        },
      };
    },
  });

  // ═══ Tool: compliance_audit_log ═══
  // Query the in-memory audit log (for demo purposes).

  ctx.tools.define('compliance_audit_log', {
    description: 'Query compliance audit log entries. Filter by action type or get all recent entries.',
    parameters: {
      type: 'object',
      properties: {
        action: {
          type: 'string',
          enum: ['auto_deny', 'auto_allow', 'oa_escalation', 'oa_notify'],
          description: 'Filter by action type. Omit to get all entries.',
        },
        limit: {
          type: 'number',
          description: 'Max entries to return (default: 20).',
        },
      },
    },
    handler: async (args) => {
      let entries = [...auditLog];
      if (args.action) {
        entries = entries.filter((e) => e.action === args.action);
      }
      const limit = args.limit || 20;
      return {
        value: {
          total: entries.length,
          entries: entries.slice(-limit),
        },
      };
    },
  });
};

// ─── Helpers ─────────────────────────────────────────────────────────────────

/**
 * Extract the command string from a permission request.
 * Handles different payload shapes from different connectors.
 */
function extractCommand(request) {
  // Direct command field.
  if (request.args) return request.args;
  // Nested in payload.
  if (request.payload?.command) return request.payload.command;
  // Some connectors put it in detail.
  if (request.detail) return request.detail;
  // Title as last resort.
  return request.title || '';
}
