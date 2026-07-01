/**
 * Workspace-dimension IM connector hooks.
 *
 * A bot binds at the WORKSPACE dimension (one bot per platform per workspace),
 * not per-agent: all agents reachable from one workspace `/list` share the bot
 * credential. These hooks back the Connections-tab ChannelConnectorPanel.
 *
 * Reads `GET /workspaces/{id}/connectors` (all platforms, decoded config jsonb)
 * and writes per-platform via `PATCH /workspaces/{id}/connector/{platform}`.
 * Secret plaintext never travels through these types — only the `*_ref` UUIDs
 * minted by `useCreateSecret`.
 */
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { apiRequest, noUnreachableRetry } from "./api-client"

/* --- Query keys --------------------------------------------------------- */

const KEY_CONNECTORS = (workspaceID: string) =>
  ["admin", "workspaceConnectors", workspaceID] as const

/* --- Wire types --------------------------------------------------------- */

export type ConnectorPlatform = "feishu" | "slack" | "discord"

/** One decoded row from workspace_im_connectors. `config` holds the *_ref
 *  UUIDs + non-secret fields; column fields (app_id/enabled) are hoisted. */
export interface WorkspaceConnector {
  id: string
  workspace_id: string
  workspace_name: string
  platform: string
  app_id: string
  enabled: boolean
  config: Record<string, unknown>
  created_at: string
  updated_at: string
}

interface ListWorkspaceConnectorsResponse {
  connectors: WorkspaceConnector[]
}

export interface WorkspaceConnectorChange {
  id: string
  workspace_id: string
  platform: string
  app_id: string
  enabled: boolean
  config: Record<string, unknown>
  updated_at: string
  noop?: boolean
}

interface UpdateWorkspaceConnectorResponse {
  connector: WorkspaceConnectorChange
}

/** Slack connector PATCH body — mirrors updateWorkspaceSlackConnectorBody. */
export interface SlackConnectorInput {
  enabled: boolean
  app_id: string
  bot_token_ref: string
  app_token_ref: string
  signing_secret_ref: string
  event_mode: "socket" | "events"
}

/** Discord connector PATCH body — mirrors updateWorkspaceDiscordConnectorBody. */
export interface DiscordConnectorInput {
  enabled: boolean
  app_id: string
  bot_token_ref: string
  public_key_ref: string
  intents: string
}

/** Feishu connector PATCH body — mirrors updateWorkspaceFeishuConnectorBody. */
export interface FeishuConnectorInput {
  enabled: boolean
  app_id: string
  app_secret_ref: string
  verification_token_ref: string
  encrypt_key_ref: string
  bot_open_id: string
  event_mode: "websocket" | "webhook"
}

/* --- Network ------------------------------------------------------------ */

async function listWorkspaceConnectorsRequest(
  workspaceID: string | null,
): Promise<ListWorkspaceConnectorsResponse> {
  if (!workspaceID) return { connectors: [] }
  return apiRequest<ListWorkspaceConnectorsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/connectors`,
    { method: "GET" },
  )
}

async function updateConnectorRequest<T>(
  workspaceID: string,
  platform: ConnectorPlatform,
  body: T,
): Promise<WorkspaceConnectorChange> {
  const res = await apiRequest<UpdateWorkspaceConnectorResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/connector/${platform}`,
    { method: "PATCH", body },
  )
  return res.connector
}

/* --- Hooks -------------------------------------------------------------- */

export function useWorkspaceIMConnectors(workspaceID: string | null) {
  return useQuery({
    queryKey: KEY_CONNECTORS(workspaceID ?? "_none"),
    queryFn: () => listWorkspaceConnectorsRequest(workspaceID),
    retry: noUnreachableRetry,
    staleTime: 15_000,
  })
}

function useUpdateConnector<T>(
  workspaceID: string | null,
  platform: ConnectorPlatform,
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (input: { config: T }) => {
      if (!workspaceID) {
        throw new Error("no workspace selected")
      }
      return updateConnectorRequest<T>(workspaceID, platform, input.config)
    },
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: KEY_CONNECTORS(workspaceID ?? "_none"),
      })
    },
  })
}

export function useUpdateWorkspaceSlackConnector(workspaceID: string | null) {
  return useUpdateConnector<SlackConnectorInput>(workspaceID, "slack")
}

export function useUpdateWorkspaceDiscordConnector(workspaceID: string | null) {
  return useUpdateConnector<DiscordConnectorInput>(workspaceID, "discord")
}

export function useUpdateWorkspaceFeishuConnector(workspaceID: string | null) {
  return useUpdateConnector<FeishuConnectorInput>(workspaceID, "feishu")
}

/* --- Config extractors -------------------------------------------------- */
//
// Pull a single platform's persisted config out of the connectors list. Each
// returns undefined when that platform was never configured for the workspace,
// so a field module can fall back to its EMPTY_CONFIG.

function pickConnector(
  connectors: WorkspaceConnector[] | undefined,
  platform: ConnectorPlatform,
): WorkspaceConnector | undefined {
  return connectors?.find((c) => c.platform === platform)
}

function configString(config: Record<string, unknown>, key: string): string {
  const v = config[key]
  return typeof v === "string" ? v : ""
}

export function readSlackConnector(
  connectors: WorkspaceConnector[] | undefined,
): SlackConnectorInput | undefined {
  const conn = pickConnector(connectors, "slack")
  if (!conn) return undefined
  const mode = configString(conn.config, "event_mode")
  return {
    enabled: conn.enabled,
    app_id: conn.app_id,
    bot_token_ref: configString(conn.config, "bot_token_ref"),
    app_token_ref: configString(conn.config, "app_token_ref"),
    signing_secret_ref: configString(conn.config, "signing_secret_ref"),
    event_mode: mode === "events" ? "events" : "socket",
  }
}

export function readDiscordConnector(
  connectors: WorkspaceConnector[] | undefined,
): DiscordConnectorInput | undefined {
  const conn = pickConnector(connectors, "discord")
  if (!conn) return undefined
  return {
    enabled: conn.enabled,
    app_id: conn.app_id,
    bot_token_ref: configString(conn.config, "bot_token_ref"),
    public_key_ref: configString(conn.config, "public_key_ref"),
    intents: configString(conn.config, "intents"),
  }
}

export function readFeishuConnector(
  connectors: WorkspaceConnector[] | undefined,
): FeishuConnectorInput | undefined {
  const conn = pickConnector(connectors, "feishu")
  if (!conn) return undefined
  const mode = configString(conn.config, "event_mode")
  return {
    enabled: conn.enabled,
    app_id: conn.app_id,
    app_secret_ref: configString(conn.config, "app_secret_ref"),
    verification_token_ref: configString(conn.config, "verification_token_ref"),
    encrypt_key_ref: configString(conn.config, "encrypt_key_ref"),
    bot_open_id: configString(conn.config, "bot_open_id"),
    event_mode: mode === "webhook" ? "webhook" : "websocket",
  }
}
