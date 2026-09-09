"use client"

import Image from "next/image"
import Link from "next/link"
import { useParams, usePathname, useRouter } from "next/navigation"
import { FrameworkProvider, type Framework } from "fumadocs-core/framework"
import { RootProvider } from "fumadocs-ui/provider/base"
import { useCallback, type ReactNode } from "react"
import type { Lang } from "@/lib/i18n"

const locales = [
  { name: "English", locale: "en" },
  { name: "中文", locale: "zh" },
]

const translations = {
  en: {
    toc: "On this page",
    chooseLanguage: "Choose language",
    nextPage: "Next page",
    previousPage: "Previous page",
  },
  zh: {
    toc: "本页目录",
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
  const pathname = pathForLocale(usePathname(), lang)
  // Navigation must use the public path during both SSR and hydration.
  const usePublicPathname = useCallback(() => pathname, [pathname])

  return (
    <FrameworkProvider
      usePathname={usePublicPathname}
      useParams={useParams}
      useRouter={useRouter}
      Link={Link as Framework["Link"]}
      Image={Image as Framework["Image"]}
    >
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
    </FrameworkProvider>
  )
}
