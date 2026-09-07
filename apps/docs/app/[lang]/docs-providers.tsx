"use client"

import { usePathname, useRouter } from "next/navigation"
import { RootProvider } from "fumadocs-ui/provider"
import type { ReactNode } from "react"
import type { Lang } from "@/lib/i18n"

const locales = [
  { name: "English", locale: "en" },
  { name: "中文", locale: "zh" },
]

const translations = {
  en: {
    chooseLanguage: "Choose language",
    nextPage: "Next page",
    previousPage: "Previous page",
  },
  zh: {
    chooseLanguage: "选择语言",
    nextPage: "下一页",
    previousPage: "上一页",
  },
} as const

function pathForLocale(pathname: string, locale: string) {
  const segments = pathname.split("/").filter(Boolean)
  if (segments[0] === "zh" || segments[0] === "en") segments.shift()
  const rest = segments.join("/")
  if (locale === "en") return rest ? `/${rest}` : "/"
  return rest ? `/${locale}/${rest}` : `/${locale}`
}

export function DocsProviders({ lang, children }: { lang: Lang; children: ReactNode }) {
  const router = useRouter()
  const pathname = usePathname()

  return (
    <RootProvider
      search={{ enabled: false }}
      i18n={{
        locale: lang,
        locales,
        translations: translations[lang],
        onLocaleChange: (locale) => router.push(pathForLocale(pathname, locale)),
      }}
    >
      {children}
    </RootProvider>
  )
}
