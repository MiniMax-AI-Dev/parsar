import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"

import { apiRequest, noUnreachableRetry } from "./api-client"

/* --- Wire types ---------------------------------------------------------
 *
 * Mirror server/internal/bootstrap/bootstrap.go::StatusResult, served at
 * GET /api/v1/bootstrap/status (unauthenticated — boolean posture only).
 */

export interface BootstrapStatus {
  needed: boolean
  has_owners: boolean
  owner_count: number
  dev_auth_enabled: boolean
  /**
   * Operator-configured public base URL (PARSAR_PUBLIC_URL). The daemon
   * one-line connect command is minted from this trusted value instead of
   * the request Host header (which a client can spoof). Empty in dev/mock;
   * callers fall back to window.location.origin.
   */
  public_url: string
}

export async function getBootstrapStatusRequest(): Promise<BootstrapStatus> {
  return apiRequest<BootstrapStatus>("/api/v1/bootstrap/status")
}

export function useBootstrapStatus() {
  return useQuery({
    queryKey: ["bootstrap", "status"],
    queryFn: getBootstrapStatusRequest,
    staleTime: 60_000,
    retry: noUnreachableRetry,
  })
}

/* --- First-owner registration ------------------------------------------
 *
 * Mirror server/internal/bootstrap/handlers.go::CreateRequest/Response.
 * The server upserts the user, creates the workspace, binds the
 * email/password identity, and sets the session cookie in one shot.
 * On success we just invalidate ["me"] so AuthProvider re-reads the
 * cookie and drops the caller into AuthedRoot.
 */

export interface RegisterFirstOwnerRequest {
  email: string
  name: string
  workspace_name: string
  password: string
}

export interface RegisterFirstOwnerResponse {
  user_id: string
  user_created: boolean
  workspace_id: string
  workspace_slug: string
  workspace_name: string
  member_id: string
  setup_complete: boolean
}

export async function registerFirstOwnerRequest(
  req: RegisterFirstOwnerRequest,
): Promise<RegisterFirstOwnerResponse> {
  return apiRequest<RegisterFirstOwnerResponse>("/api/v1/bootstrap", {
    method: "POST",
    body: req,
  })
}

export function useRegisterFirstOwner() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: registerFirstOwnerRequest,
    onSuccess: async () => {
      // Server set parsar_session cookie on the 201 response — refresh
      // the me-query and the bootstrap status so gating flips over.
      await qc.invalidateQueries({ queryKey: ["me"] })
      await qc.invalidateQueries({ queryKey: ["bootstrap", "status"] })
    },
  })
}
