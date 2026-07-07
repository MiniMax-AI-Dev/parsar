import { useMutation, useQueryClient } from "@tanstack/react-query"

import { apiRequest } from "./api-client"

export interface MeResponse {
  user_id: string
  email: string
  name: string
  avatar_url: string
}

export function getMe(): Promise<MeResponse> {
  return apiRequest<MeResponse>("/api/v1/me")
}

export function logout(): Promise<void> {
  return apiRequest<void>("/api/v1/auth/logout", { method: "POST" })
}

export function feishuLoginUrl(): string {
  return "/api/v1/auth/feishu/start"
}

/* --- Email/password login ---------------------------------------------
 *
 * Mirrors server/internal/auth/password/handler.go. The server issues
 * a session cookie on 200 and returns { user_id, email, name }; the
 * failure envelope collapses "wrong password" and "unknown email"
 * into a single opaque 401 with code "invalid_credentials".
 */

export interface LoginRequest {
  email: string
  password: string
}

export interface LoginResponse {
  user_id: string
  email: string
  name: string
}

export async function loginWithPasswordRequest(
  req: LoginRequest,
): Promise<LoginResponse> {
  return apiRequest<LoginResponse>("/api/v1/auth/login", {
    method: "POST",
    body: req,
  })
}

export function useLoginWithPassword() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: loginWithPasswordRequest,
    onSuccess: async () => {
      // Cookie is set by the 200 response; refresh me-query so AuthProvider
      // flips isAuthenticated -> true and the router mounts AuthedRoot.
      await qc.invalidateQueries({ queryKey: ["me"] })
    },
  })
}
