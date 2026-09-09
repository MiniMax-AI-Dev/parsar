export function conversationRecoveryLinks(
  workspaceID: string | null | undefined,
  capabilityID: string,
  credentialKind: string,
  returnTo: string,
) {
  const capability = new URLSearchParams({ admin: "capabilities" })
  const credential = new URLSearchParams({ profile: "credentials", kind: credentialKind, returnTo })
  if (workspaceID) {
    capability.set("ws", workspaceID)
    credential.set("ws", workspaceID)
  }
  if (capabilityID) capability.set("id", capabilityID)
  return {
    capability: `/?${capability}`,
    credential: credentialKind ? `/?${credential}` : "",
  }
}
