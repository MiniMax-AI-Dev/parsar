import { Layers, Plus } from "lucide-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import {
  useCreateWorkspace,
  useMyWorkspaces,
} from "../../lib/api-workspaces"
import { setWorkspaceId } from "../../lib/workspace"
import { Button } from "../ui/button"
import { EmptyState } from "../ui/empty-state"
import { WorkspaceFormDialog } from "../layout/WorkspaceCrudDialogs"

interface ScopeRequiredStateProps {
  scope: "workspace"
  resourceName: string
}

export function ScopeRequiredState({ resourceName }: ScopeRequiredStateProps) {
  const { t } = useTranslation("admin")
  const workspacesQ = useMyWorkspaces()
  const workspaces = workspacesQ.data?.workspaces ?? []

  const [createWsOpen, setCreateWsOpen] = useState(false)
  const createWorkspaceMut = useCreateWorkspace()

  return (
    <>
      <EmptyState
        icon={Layers}
        title={t("scopeRequired.workspace.title", { resource: resourceName })}
        description={t("scopeRequired.workspace.description")}
        action={
          workspaces.length > 0 ? (
            <div className="flex flex-wrap justify-center gap-2">
              {workspaces.map((workspace) => (
                <Button key={workspace.id} type="button" variant="outline" size="sm" onClick={() => setWorkspaceId(workspace.id)}>
                  {workspace.name}
                </Button>
              ))}
            </div>
          ) : (
            <Button type="button" size="sm" onClick={() => setCreateWsOpen(true)}>
              <Plus className="h-3.5 w-3.5" />
              {t("workspaceCrud.workspace.createAction", { ns: "common" })}
            </Button>
          )
        }
      />

      <WorkspaceFormDialog
        open={createWsOpen}
        onOpenChange={(open) => {
          if (!open) createWorkspaceMut.reset()
          setCreateWsOpen(open)
        }}
        mode="create"
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
