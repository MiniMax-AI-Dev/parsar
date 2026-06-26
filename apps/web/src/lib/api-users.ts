/**
 * Platform user search — backs AddMember dialog's combobox.
 * GET /api/v1/users/search?q=...&exclude_workspace=...
 * returns up to 20 active users; already-member users are excluded server-side.
 */
import { useQuery, keepPreviousData } from "@tanstack/react-query"
import { apiRequest, noUnreachableRetry } from "./api-client"
import type { SearchUsersResponse } from "./api-types"

interface UserSearchParams {
  q: string
  excludeWorkspace?: string
}

const KEY_USER_SEARCH = (p: UserSearchParams) =>
  [
    "platform",
    "userSearch",
    p.q.trim(),
    p.excludeWorkspace ?? "",
  ] as const

async function searchUsersRequest(
  params: UserSearchParams
): Promise<SearchUsersResponse> {
  return apiRequest<SearchUsersResponse>("/api/v1/users/search", {
    method: "GET",
    query: {
      q: params.q,
      exclude_workspace: params.excludeWorkspace,
    },
  })
}

/** Server-side user search. Disabled when the trimmed query is empty. */
export function useUserSearchQuery(params: UserSearchParams) {
  const trimmedQ = params.q.trim()
  return useQuery({
    queryKey: KEY_USER_SEARCH({ ...params, q: trimmedQ }),
    queryFn: () => searchUsersRequest({ ...params, q: trimmedQ }),
    enabled: trimmedQ.length > 0,
    retry: noUnreachableRetry,
    staleTime: 30_000,
    placeholderData: keepPreviousData,
  })
}
