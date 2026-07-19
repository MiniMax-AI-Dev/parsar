export interface WorkspaceOwnerIdentity {
  name?: string | null
  email?: string | null
}

export function workspaceOwnerName(identity?: WorkspaceOwnerIdentity | null): string {
  const name = identity?.name?.trim()
  if (name) return name

  const email = identity?.email?.trim()
  if (!email) return ""

  return email.split("@", 1)[0]?.trim() ?? ""
}
