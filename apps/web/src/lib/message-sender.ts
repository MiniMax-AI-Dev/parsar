export function isUserMessageSender(senderType: string): boolean {
  return senderType === "user" || senderType === "external"
}
