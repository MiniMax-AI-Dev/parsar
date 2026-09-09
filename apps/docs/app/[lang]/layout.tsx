import "../global.css"
import type { Metadata } from "next"
import type { ReactNode } from "react"
import { DocsLayout } from "fumadocs-ui/layouts/docs"
import { baseOptions } from "../layout.config"
import { source } from "@/lib/source"
import { i18n, type Lang } from "@/lib/i18n"
import { DocsProviders } from "./docs-providers"

export const metadata: Metadata = {
  title: {
    template: "%s | Parsar Docs",
    default: "Parsar Docs",
  },
  description: "Documentation for Parsar, the self-hosted control plane for AI coding agents.",
}

export function generateStaticParams() {
  return i18n.languages.map((lang) => ({ lang }))
}

export default async function LangLayout({
  params,
  children,
}: {
  params: Promise<{ lang: string }>
  children: ReactNode
}) {
  const { lang: rawLang } = await params
  const lang = (i18n.languages as readonly string[]).includes(rawLang)
    ? (rawLang as Lang)
    : i18n.defaultLanguage

  return (
    <html lang={lang} suppressHydrationWarning>
      {/* Browser extensions may add attributes before React hydrates the body. */}
      <body suppressHydrationWarning>
        <DocsProviders lang={lang}>
          <DocsLayout
            tree={source.getPageTree(lang)}
            {...baseOptions}
            nav={{
              ...baseOptions.nav,
              url: lang === "zh" ? "/zh" : "/",
            }}
          >
            {children}
          </DocsLayout>
        </DocsProviders>
      </body>
    </html>
  )
}
