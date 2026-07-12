import { createContext, useContext } from "react"

export type ThemePreference = "light" | "dark" | "system"
export type ResolvedTheme = "light" | "dark"

export interface ThemeContextValue {
  preference: ThemePreference
  resolvedTheme: ResolvedTheme
  setPreference: (preference: ThemePreference) => void
}

export const themeStorageKey = "parsar.theme"
export const defaultThemePreference: ThemePreference = "system"

export const ThemeContext = createContext<ThemeContextValue | null>(null)

export function useTheme() {
  const value = useContext(ThemeContext)
  if (!value) {
    throw new Error("useTheme must be used inside ThemeProvider")
  }
  return value
}

