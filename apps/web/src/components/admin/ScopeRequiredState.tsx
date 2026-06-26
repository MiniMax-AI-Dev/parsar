import { FolderKanban, Layers, Plus } from "lucide-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import {
  useCreateProject,
  useCreateWorkspace,
  useMyWorkspaces,
  useWorkspaceProjects,
} from "../../lib/api-workspaces"
import { setProjectId, setWorkspaceId, useWorkspaceId } from "../../lib/workspace"
import { Button } from "../ui/button"
import { EmptyState } from "../ui/empty-state"
import {
  ProjectFormDialog,
  WorkspaceFormDialog,
} from "../layout/WorkspaceCrudDialogs"

interface ScopeRequiredStateProps {
  scope: "workspace" | "project"
  resourceName: string
}

export function ScopeRequiredState({ scope, resourceName }: ScopeRequiredStateProps) {
  const { t } = useTranslation("admin")
  const wsId = useWorkspaceId()
  const workspacesQ = useMyWorkspaces()
  const projectsQ = useWorkspaceProjects(wsId)
  const workspaces = workspacesQ.data?.workspaces ?? []
  const projects = projectsQ.data?.projects ?? []

  const [createWsOpen, setCreateWsOpen] = useState(false)
  const [createProjOpen, setCreateProjOpen] = useState(false)
  const createWorkspaceMut = useCreateWorkspace()
  const createProjectMut = useCreateProject()

  if (scope === "workspace") {
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
                  setProjectId(null)
                  setCreateWsOpen(false)
                },
              }
            )
          }}
        />
      </>
    )
  }

  const title = !wsId
    ? t("scopeRequired.project.noWorkspaceTitle", { resource: resourceName })
    : projectsQ.isLoading
      ? t("scopeRequired.project.loadingTitle")
      : projects.length === 0
        ? t("scopeRequired.project.noProjectsTitle")
        : t("scopeRequired.project.selectTitle", { resource: resourceName })

  const description = !wsId
    ? t("scopeRequired.project.noWorkspaceDescription")
    : projectsQ.isLoading
      ? t("scopeRequired.project.loadingDescription")
      : projects.length === 0
        ? undefined
        : t("scopeRequired.project.selectDescription")

  const action =
    wsId && projects.length > 0 ? (
      <div className="flex flex-wrap justify-center gap-2">
        {projects.map((project) => (
          <Button key={project.id} type="button" variant="outline" size="sm" onClick={() => setProjectId(project.id)}>
            {project.name}
          </Button>
        ))}
      </div>
    ) : wsId && !projectsQ.isLoading && projects.length === 0 ? (
      <Button type="button" size="sm" onClick={() => setCreateProjOpen(true)}>
        <Plus className="h-3.5 w-3.5" />
        {t("workspaceCrud.project.createAction", { ns: "common" })}
      </Button>
    ) : null

  return (
    <>
      <EmptyState
        icon={FolderKanban}
        title={title}
        description={description}
        action={action}
      />

      <ProjectFormDialog
        open={createProjOpen}
        onOpenChange={(open) => {
          if (!open) createProjectMut.reset()
          setCreateProjOpen(open)
        }}
        mode="create"
        pending={createProjectMut.isPending}
        error={createProjectMut.error}
        onSubmit={({ name, description }) => {
          if (!wsId) return
          createProjectMut.mutate(
            {
              wsId,
              body: {
                name,
                description: description || undefined,
              },
            },
            {
              onSuccess: (data) => {
                setProjectId(data.project.id)
                setCreateProjOpen(false)
              },
            }
          )
        }}
      />
    </>
  )
}
