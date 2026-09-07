import { Layers, Plus } from "lucide-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import {
  useCreateWorkspace,
  useMyWorkspaces,
} from "../../lib/api-workspaces"
import { useAuth } from "../../lib/auth-context"
import { setWorkspaceId } from "../../lib/workspace"
import { workspaceOwnerName } from "../../lib/workspace-defaults"
import { Button } from "../ui/button"
import { EmptyState } from "../ui/empty-state"
import { Select } from "../ui/select"
import { WorkspaceFormDialog } from "../layout/WorkspaceCrudDialogs"

interface ScopeRequiredStateProps {
  scope: "workspace"
  resourceName: string
}

/**
 * Flat "pick a workspace first" state: muted icon, 13px/500 title and one
 * action — a single workspace button, a workspace select, or the create
 * button when the user has no workspace yet.
 */
export function ScopeRequiredState({ resourceName }: ScopeRequiredStateProps) {
  const { t } = useTranslation("admin")
  const { user } = useAuth()
  const workspacesQ = useMyWorkspaces()
  const workspaces = workspacesQ.data?.workspaces ?? []
  const workspaceOwner = workspaceOwnerName(user)
  const defaultWorkspaceName = workspaceOwner
    ? t("workspaceDefaults.personal", { ns: "common", name: workspaceOwner })
    : t("workspaceDefaults.generic", { ns: "common" })

  const [createWsOpen, setCreateWsOpen] = useState(false)
  const createWorkspaceMut = useCreateWorkspace()

  const pickLabel = t("scopeRequired.workspace.pick")
  const action =
    workspaces.length === 0 ? (
      <Button type="button" size="sm" onClick={() => setCreateWsOpen(true)}>
        <Plus strokeWidth={1.5} aria-hidden="true" />
        {t("workspaceCrud.workspace.createAction", { ns: "common" })}
      </Button>
    ) : workspaces.length === 1 ? (
      <Button type="button" variant="outline" size="sm" onClick={() => setWorkspaceId(workspaces[0].id)}>
        {workspaces[0].name}
      </Button>
    ) : (
      <Select
        aria-label={pickLabel}
        defaultValue=""
        wrapperClassName="w-60"
        onChange={(event) => {
          if (event.target.value) setWorkspaceId(event.target.value)
        }}
      >
        <option value="" disabled>
          {pickLabel}
        </option>
        {workspaces.map((workspace) => (
          <option key={workspace.id} value={workspace.id}>
            {workspace.name}
          </option>
        ))}
      </Select>
    )

  return (
    <>
      <EmptyState
        icon={Layers}
        title={t("scopeRequired.workspace.title", { resource: resourceName })}
        action={action}
      />

      <WorkspaceFormDialog
        open={createWsOpen}
        onOpenChange={(open) => {
          if (!open) createWorkspaceMut.reset()
          setCreateWsOpen(open)
        }}
        mode="create"
        initialName={defaultWorkspaceName}
        pending={createWorkspaceMut.isPending}
        error={createWorkspaceMut.error}
        onSubmit={({ name }) => {
          createWorkspaceMut.mutate(
            { name },
            {
              onSuccess: (data) => {
                setWorkspaceId(data.workspace.id)
                setCreateWsOpen(false)
              },
            }
          )
        }}
      />
    </>
  )
}
