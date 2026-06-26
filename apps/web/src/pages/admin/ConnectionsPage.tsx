import { useState } from "react"
import { useTranslation } from "react-i18next"
import { CheckCircle2, Github, KeyRound, Loader2, Plus } from "lucide-react"

import { AdminLayout } from "../../components/layout/AdminLayout"
import { PageHeader } from "../../components/layout/PageHeader"
import { ScopeRequiredState } from "../../components/admin/ScopeRequiredState"
import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import { Input } from "../../components/ui/input"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import {
  useCreateMyCredential,
  useDeleteMyCredential,
  useMyCredentials,
  usePatchMyCredential,
} from "../../lib/api-credentials"
import { useCredentialKindOptions } from "../../lib/credential-kind-ui"
import { useWorkspaceId } from "../../lib/workspace"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import { useCreateCredentialKindMutation } from "./capabilities/api"
import { CredentialDialog, DeleteCredentialDialog } from "./credentials/CredentialDialogs"
import type { UserCredentialCreateRequest, UserCredentialPatchRequest, UserCredential } from "../../lib/api-types"
import type { ApiError } from "../../lib/api-client"

const GITHUB_OAUTH_START_URL = "/api/v1/connections/github/start"

export function ConnectionsPage() {
  const { t } = useTranslation("admin")
  const wsId = useWorkspaceId()
  const workspacesQ = useMyWorkspaces()
  const role = workspacesQ.data?.workspaces.find((w) => w.id === wsId)?.role
  const canCreateKind = role === "owner" || role === "admin"

  const credentialsQ = useMyCredentials()
  const kindsQ = useCredentialKindOptions(wsId)
  const createKindMut = useCreateCredentialKindMutation(wsId)
  const createCredMut = useCreateMyCredential()
  const patchCredMut = usePatchMyCredential()
  const deleteCredMut = useDeleteMyCredential()

  const [createKindOpen, setCreateKindOpen] = useState(false)
  const [pendingKind, setPendingKind] = useState<string | null>(null)
  const [editingCredential, setEditingCredential] = useState<UserCredential | null>(null)
  const [deletingCredential, setDeletingCredential] = useState<UserCredential | null>(null)

  const credentials = credentialsQ.data?.credentials ?? []
  const credByKind = new Map(credentials.map((c) => [c.kind, c]))

  const oauthKinds = kindsQ.kinds.filter((k) => k.source === "platform_oauth")
  const modelKinds = kindsQ.kinds.filter((k) => k.source === "platform_model")

  return (
    <AdminLayout activeMenu="connections">
      <PageHeader title={t("connections.page.title")} />

      {!wsId ? (
        <ScopeRequiredState scope="workspace" resourceName={t("connections.page.title")} />
      ) : (
        <div className="space-y-8">
          <section>
            <h2 className="mb-3 text-[14px] font-semibold text-slate-900">{t("connections.oauth.title")}</h2>
            {oauthKinds.length === 0 ? (
              <p className="text-[12.5px] text-slate-500">{t("connections.oauth.empty")}</p>
            ) : (
              <div className="grid gap-3 lg:grid-cols-3">
                {oauthKinds.map((kind) => {
                  const cred = credByKind.get(kind.code)
                  return (
                    <OAuthConnectionCard
                      key={kind.code}
                      title={kind.display_name || kind.code}
                      description={oauthDescription(kind.code, t as (k: string) => string)}
                      connected={!!cred}
                      loading={credentialsQ.isLoading}
                      onAction={() => window.location.assign(oauthStartURL(kind.code))}
                    />
                  )
                })}
              </div>
            )}
          </section>

          <section>
            <div className="mb-3 flex items-center justify-between">
              <h2 className="text-[14px] font-semibold text-slate-900">{t("connections.model.title")}</h2>
              {canCreateKind && (
                <Button size="sm" variant="outline" onClick={() => setCreateKindOpen(true)}>
                  <Plus className="h-3.5 w-3.5" />
                  {t("connections.model.addAction")}
                </Button>
              )}
            </div>
            {modelKinds.length === 0 ? (
              <p className="text-[12.5px] text-slate-500">{t("connections.model.empty")}</p>
            ) : (
              <div className="grid gap-3 lg:grid-cols-3">
                {modelKinds.map((kind) => {
                  const cred = credByKind.get(kind.code)
                  return (
                    <ModelKeyCard
                      key={kind.code}
                      title={kind.display_name || kind.code}
                      description={kind.description}
                      configured={!!cred}
                      loading={credentialsQ.isLoading}
                      onLink={() => {
                        setPendingKind(kind.code)
                        setEditingCredential(null)
                      }}
                      onReconfigure={() => {
                        if (cred) {
                          setEditingCredential(cred)
                          setPendingKind(null)
                        }
                      }}
                      onRemove={() => {
                        if (cred) setDeletingCredential(cred)
                      }}
                    />
                  )
                })}
              </div>
            )}
          </section>
        </div>
      )}

      {createKindOpen && (
        <CreateModelKindDialog
          pending={createKindMut.isPending}
          error={createKindMut.error as ApiError | undefined}
          onClose={() => {
            setCreateKindOpen(false)
            createKindMut.reset()
          }}
          onSubmit={async ({ code, displayName, description }) => {
            await createKindMut.mutateAsync({
              code,
              display_name: displayName,
              description,
              source: "platform_model",
            })
            setCreateKindOpen(false)
            createKindMut.reset()
          }}
        />
      )}

      {pendingKind && (
        <CredentialDialog
          key={`create:${pendingKind}`}
          mode="create"
          initialKind={pendingKind}
          pending={createCredMut.isPending}
          error={createCredMut.error as ApiError | undefined}
          onClose={() => {
            setPendingKind(null)
            createCredMut.reset()
          }}
          onSubmit={async (body) => {
            await createCredMut.mutateAsync(body as UserCredentialCreateRequest)
            setPendingKind(null)
            createCredMut.reset()
          }}
        />
      )}

      {editingCredential && (
        <CredentialDialog
          key={`edit:${editingCredential.id}`}
          mode="edit"
          credential={editingCredential}
          pending={patchCredMut.isPending}
          error={patchCredMut.error as ApiError | undefined}
          onClose={() => {
            setEditingCredential(null)
            patchCredMut.reset()
          }}
          onSubmit={async (body) => {
            await patchCredMut.mutateAsync({
              id: editingCredential.id,
              body: body as UserCredentialPatchRequest,
            })
            setEditingCredential(null)
            patchCredMut.reset()
          }}
        />
      )}

      {deletingCredential && (
        <DeleteCredentialDialog
          target={deletingCredential}
          pending={deleteCredMut.isPending}
          error={deleteCredMut.error as ApiError | undefined}
          onCancel={() => {
            setDeletingCredential(null)
            deleteCredMut.reset()
          }}
          onConfirm={async () => {
            await deleteCredMut.mutateAsync(deletingCredential.id)
            setDeletingCredential(null)
            deleteCredMut.reset()
          }}
        />
      )}
    </AdminLayout>
  )
}

function oauthStartURL(kindCode: string): string {
  switch (kindCode) {
    case "github_pat":
      return GITHUB_OAUTH_START_URL
    default:
      return "#"
  }
}

function oauthDescription(kindCode: string, t: (k: string) => string): string {
  switch (kindCode) {
    case "github_pat":
      return t("connections.providers.github")
    case "slack_bot_token":
      return t("connections.providers.slack")
    default:
      return ""
  }
}

function OAuthConnectionCard({
  title,
  description,
  connected,
  loading,
  onAction,
}: {
  title: string
  description: string
  connected: boolean
  loading: boolean
  onAction: () => void
}) {
  const { t } = useTranslation("admin")
  return (
    <div className="flex min-h-[168px] flex-col rounded-lg border border-slate-200 bg-white p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-2">
          <div className="rounded-md bg-slate-100 p-2 text-slate-600">
            <Github className="h-4 w-4" />
          </div>
          <div>
            <h3 className="text-[14px] font-semibold text-slate-900">{title}</h3>
            <p className="mt-0.5 min-h-10 text-[12px] leading-5 text-slate-500">{description}</p>
          </div>
        </div>
        {loading ? (
          <Badge variant="neutral">
            <Loader2 className="h-3 w-3 animate-spin" />
            {t("connections.loading")}
          </Badge>
        ) : connected ? (
          <Badge variant="success">
            <CheckCircle2 className="h-3 w-3" />
            {t("connections.connected")}
          </Badge>
        ) : null}
      </div>
      <Button
        className="mt-auto w-full"
        size="sm"
        variant={connected ? "outline" : "default"}
        disabled={loading}
        onClick={onAction}
      >
        {connected ? t("connections.reconnect") : t("connections.connect")}
      </Button>
    </div>
  )
}

function ModelKeyCard({
  title,
  description,
  configured,
  loading,
  onLink,
  onReconfigure,
  onRemove,
}: {
  title: string
  description: string
  configured: boolean
  loading: boolean
  onLink: () => void
  onReconfigure: () => void
  onRemove: () => void
}) {
  const { t } = useTranslation("admin")
  return (
    <div className="flex min-h-[168px] flex-col rounded-lg border border-slate-200 bg-white p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-2">
          <div className="rounded-md bg-slate-100 p-2 text-slate-600">
            <KeyRound className="h-4 w-4" />
          </div>
          <div>
            <h3 className="text-[14px] font-semibold text-slate-900">{title}</h3>
            {description && (
              <p className="mt-0.5 min-h-10 text-[12px] leading-5 text-slate-500">{description}</p>
            )}
          </div>
        </div>
        {loading ? (
          <Badge variant="neutral">
            <Loader2 className="h-3 w-3 animate-spin" />
            {t("connections.loading")}
          </Badge>
        ) : configured ? (
          <Badge variant="success">
            <CheckCircle2 className="h-3 w-3" />
            {t("connections.model.configured")}
          </Badge>
        ) : null}
      </div>
      {configured ? (
        <div className="mt-auto flex gap-2">
          <Button size="sm" variant="outline" className="flex-1" disabled={loading} onClick={onReconfigure}>
            {t("connections.model.reconfigure")}
          </Button>
          <Button size="sm" variant="outline" disabled={loading} onClick={onRemove}>
            {t("connections.model.remove")}
          </Button>
        </div>
      ) : (
        <Button className="mt-auto w-full" size="sm" disabled={loading} onClick={onLink}>
          {t("connections.model.linkAction")}
        </Button>
      )}
    </div>
  )
}

function CreateModelKindDialog({
  pending,
  error,
  onClose,
  onSubmit,
}: {
  pending: boolean
  error?: ApiError
  onClose: () => void
  onSubmit: (input: { code: string; displayName: string; description: string }) => Promise<void>
}) {
  const { t } = useTranslation("admin")
  const [code, setCode] = useState("")
  const [displayName, setDisplayName] = useState("")
  const [description, setDescription] = useState("")

  const codeValid = /^[a-z][a-z0-9_]*$/.test(code)
  const disabled = pending || !codeValid || !displayName.trim()

  return (
    <Dialog open onOpenChange={(v) => { if (!v) onClose() }}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t("connections.createKind.title")}</DialogTitle>
        </DialogHeader>
        <form
          className="space-y-4"
          onSubmit={async (e) => {
            e.preventDefault()
            if (disabled) return
            await onSubmit({
              code: code.trim().toLowerCase(),
              displayName: displayName.trim(),
              description: description.trim(),
            })
          }}
        >
          <div className="space-y-1.5">
            <label htmlFor="create-kind-code" className="text-[12px] font-medium text-slate-700">
              {t("connections.createKind.code")}
            </label>
            <Input
              id="create-kind-code"
              value={code}
              onChange={(e) => setCode(e.target.value.toLowerCase())}
              placeholder={t("connections.createKind.codePlaceholder")}
              autoFocus
              autoComplete="off"
              spellCheck={false}
            />
            <p className="text-[11px] text-slate-500">{t("connections.createKind.codeHint")}</p>
          </div>

          <div className="space-y-1.5">
            <label htmlFor="create-kind-name" className="text-[12px] font-medium text-slate-700">
              {t("connections.createKind.displayName")}
            </label>
            <Input
              id="create-kind-name"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              placeholder={t("connections.createKind.displayNamePlaceholder")}
            />
          </div>

          <div className="space-y-1.5">
            <label htmlFor="create-kind-desc" className="text-[12px] font-medium text-slate-700">
              {t("connections.createKind.descriptionField")}
            </label>
            <Input
              id="create-kind-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={t("connections.createKind.descriptionPlaceholder")}
            />
          </div>

          {error && (
            <p className="text-[12px] text-red-600">
              {error.envelope.message || t("connections.createKind.errorGeneric")}
            </p>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t("connections.createKind.cancel")}
            </Button>
            <Button type="submit" disabled={disabled}>
              {t("connections.createKind.submit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
