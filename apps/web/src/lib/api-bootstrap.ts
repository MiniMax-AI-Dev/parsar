import { useQuery } from "@tanstack/react-query"

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
  http_enabled: boolean
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

/* --- Hooks -------------------------------------------------------------- */

export function useBootstrapStatus() {
  return useQuery({
    queryKey: ["bootstrap", "status"],
    queryFn: getBootstrapStatusRequest,
    staleTime: 60_000,
    retry: noUnreachableRetry,
  })
}
