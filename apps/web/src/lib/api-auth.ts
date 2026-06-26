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
