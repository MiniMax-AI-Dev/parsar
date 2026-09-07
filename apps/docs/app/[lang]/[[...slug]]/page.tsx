import type { Metadata } from "next"
import { notFound } from "next/navigation"
import defaultMdxComponents from "fumadocs-ui/mdx"
import { DocsBody, DocsDescription, DocsPage, DocsTitle } from "fumadocs-ui/page"
import { i18n, type Lang } from "@/lib/i18n"
import { source } from "@/lib/source"

function asLang(value: string): Lang {
  return (i18n.languages as readonly string[]).includes(value)
    ? (value as Lang)
    : i18n.defaultLanguage
}

function pageSlug(slug?: string[]) {
  return slug ?? []
}

export default async function DocsPageRoute({
  params,
}: {
  params: Promise<{ lang: string; slug?: string[] }>
}) {
  const { lang: rawLang, slug } = await params
  const lang = asLang(rawLang)
  const page = source.getPage(pageSlug(slug), lang)
  if (!page) notFound()

  const MDX = page.data.body
  return (
    <DocsPage toc={page.data.toc}>
      <DocsTitle>{page.data.title}</DocsTitle>
      <DocsDescription>{page.data.description}</DocsDescription>
      <DocsBody>
        <MDX components={defaultMdxComponents} />
      </DocsBody>
    </DocsPage>
  )
}

export function generateStaticParams() {
  return source.generateParams()
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ lang: string; slug?: string[] }>
}): Promise<Metadata> {
  const { lang: rawLang, slug } = await params
  const page = source.getPage(pageSlug(slug), asLang(rawLang))
  if (!page) notFound()
  return { title: page.data.title, description: page.data.description }
}
