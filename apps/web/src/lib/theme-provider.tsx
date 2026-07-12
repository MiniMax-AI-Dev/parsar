import {
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"
import {
  ThemeContext,
  defaultThemePreference,
  themeStorageKey,
  type ResolvedTheme,
  type ThemeContextValue,
  type ThemePreference,
} from "./theme"

function readStoredPreference(): ThemePreference {
  if (typeof window === "undefined") return defaultThemePreference
  const stored = window.localStorage.getItem(themeStorageKey)
  return stored === "light" || stored === "dark" || stored === "system"
    ? stored
    : defaultThemePreference
}

function systemTheme(): ResolvedTheme {
  if (typeof window === "undefined") return "light"
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"
}

function applyTheme(resolvedTheme: ResolvedTheme) {
  if (typeof document === "undefined") return
  document.documentElement.dataset.theme = resolvedTheme
  document.documentElement.style.colorScheme = resolvedTheme
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setPreference] = useState<ThemePreference>(() => readStoredPreference())
  const [systemPreference, setSystemPreference] = useState<ResolvedTheme>(() => systemTheme())
  const resolvedTheme = preference === "system" ? systemPreference : preference

  useEffect(() => {
    applyTheme(resolvedTheme)
    window.localStorage.setItem(themeStorageKey, preference)
  }, [preference, resolvedTheme])

  useEffect(() => {
    if (preference !== "system") return
    const media = window.matchMedia("(prefers-color-scheme: dark)")
    const onChange = () => setSystemPreference(systemTheme())
    media.addEventListener("change", onChange)
    return () => media.removeEventListener("change", onChange)
  }, [preference])

  const value = useMemo<ThemeContextValue>(
    () => ({
      preference,
      resolvedTheme,
      setPreference,
    }),
    [preference, resolvedTheme]
  )

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

