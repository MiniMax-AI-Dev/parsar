
// The deployment gives this API Session its own native state directory.
export async function recoverSession(cwd: string): Promise<string | undefined> {
  const { getSessionInfo, getSessionMessages, listSessions } = await import("@anthropic-ai/claude-agent-sdk");
  const sessions = await listSessions({ dir: cwd, includeWorktrees: false, limit: 2 });
  if (sessions.length !== 1 || sessions[0].cwd !== cwd) return undefined;
  const id = sessions[0].sessionId;
  const info = await getSessionInfo(id, { dir: cwd });
  if (!info || info.cwd !== cwd || (await getSessionMessages(id, { dir: cwd, limit: 1 })).length === 0) return undefined;
  return id;
}
