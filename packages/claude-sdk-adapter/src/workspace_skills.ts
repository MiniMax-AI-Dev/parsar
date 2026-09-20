import { lstatSync, mkdirSync, readFileSync, readlinkSync, symlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";

export type WorkspaceSkill = { type: "inline"; name: string; description: string };
export const skillRoot = "/environment/initialization/capabilities/skills";
const pluginName = "environment-skills";

export function parseSkills(value: unknown): WorkspaceSkill[] {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length > 50) throw new Error("invalid_request");
  const seen = new Set<string>();
  for (const skill of value) {
    if (!skill || typeof skill !== "object" || Array.isArray(skill) ||
        Object.keys(skill).some(key => !["type", "name", "description"].includes(key)) ||
        skill.type !== "inline" || typeof skill.name !== "string" || !/^[a-z0-9]+(?:[-_][a-z0-9]+)*$/.test(skill.name) ||
        skill.name.length > 64 || typeof skill.description !== "string" || !skill.description || seen.has(skill.name)) throw new Error("invalid_request");
    seen.add(skill.name);
  }
  return value as WorkspaceSkill[];
}

// The adapter generates the native plugin envelope; public Skill archives never
// supply a plugin manifest, settings, hooks or an MCP installation.
export function workspaceSkills(state: string, skills: readonly WorkspaceSkill[]): { path: string; names: string[] } | undefined {
  if (!skills.length) return undefined;
  for (const skill of skills) {
    const root = join(skillRoot, skill.name);
    const body = readFileSync(join(root, "SKILL.md"), "utf8");
    if (/(?<=^|\s)!`[^`]+`/m.test(body) || /```!\s*\n?[\s\S]*?\n?```/.test(body)) {
      throw new Error("unsupported native Skill activation");
    }
  }
  const path = join(state, "environment-skills");
  mkdirSync(join(path, ".claude-plugin"), { recursive: true, mode: 0o700 });
  writeFileSync(join(path, ".claude-plugin", "plugin.json"), JSON.stringify({ name: pluginName, description: "Environment Skills" }), { mode: 0o600 });
  const link = join(path, "skills");
  try {
    if (!lstatSync(link).isSymbolicLink() || readlinkSync(link) !== skillRoot) throw new Error("invalid native Skill root");
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    symlinkSync(skillRoot, link);
  }
  return { path, names: skills.map(skill => `${pluginName}:${skill.name}`) };
}
