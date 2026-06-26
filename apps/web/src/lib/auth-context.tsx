import { createContext, useContext, type ReactNode } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { ApiError, noUnreachableRetry } from "./api-client"
import { getMe, logout as apiLogout, type MeResponse } from "./api-auth"

interface AuthContextValue {
  user: MeResponse | null
  isLoading: boolean
  isAuthenticated: boolean
  logout: () => Promise<void>
  refresh: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)
const meQueryKey = ["me"] as const

export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const meQuery = useQuery({
    queryKey: meQueryKey,
    queryFn: async () => {
      try {
        return await getMe()
      } catch (err) {
        if (err instanceof ApiError && err.envelope.status === 401) {
          return null
        }
        throw err
      }
    },
    retry: noUnreachableRetry,
  })

  const user = meQuery.data ?? null
  const value: AuthContextValue = {
    user,
    isLoading: meQuery.isLoading,
    isAuthenticated: !meQuery.isLoading && !!user,
    logout: async () => {
      await apiLogout()
      await queryClient.invalidateQueries({ queryKey: meQueryKey })
      window.location.assign("/login")
    },
    refresh: async () => {
      await queryClient.invalidateQueries({ queryKey: meQueryKey })
    },
  }

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext)
  if (!value) {
    throw new Error("useAuth must be used inside AuthProvider")
  }
  return value
}
