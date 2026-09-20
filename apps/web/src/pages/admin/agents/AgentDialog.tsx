import { CreateAgentDialog as CoreAgentDialog, type CreateAgentDialogProps } from "../CreateAgentDialog"

export function CreateAgentDialog(props: CreateAgentDialogProps) {
  return props.open ? <CoreAgentDialog key={`${props.workspaceID}:${props.mode}:${props.agent?.id ?? "new"}`} {...props} /> : null
}
