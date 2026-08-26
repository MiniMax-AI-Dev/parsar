# Compliance Policy

This workspace has an active compliance plugin that automatically enforces security policies on tool executions.

## What happens when you run a command:

1. **Dangerous commands are blocked** — `rm -rf`, `DROP TABLE`, `mkfs`, and similar destructive operations are automatically denied. Do not attempt to work around these blocks.

2. **Sensitive operations are escalated** — Kubernetes destructive commands, mass deletions, remote code execution via curl|bash, and firewall changes are forwarded to the internal OA approval system. A human must approve these.

3. **Safe commands pass through** — `ls`, `cat`, `git status`, and other read-only operations are automatically approved without human intervention.

4. **Everything else** — Goes through the normal human approval flow.

## When a command is denied:

- Do NOT attempt to bypass the denial (e.g., encoding the command differently or using alternatives).
- Explain to the user why the command was blocked.
- Suggest a safer alternative if one exists.
- If the user insists, tell them to contact the workspace admin for a manual override.

## Tools available:

- `compliance_check_status` — Check active policy rules and recent audit log
- `compliance_audit_log` — Query compliance events (denials, escalations, approvals)
