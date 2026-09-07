/**
 * Preview panel for a multi-file Skill zip. SKILL.md expanded; others
 * collapsed by default. Renders as syntax-highlighted source (not
 * rendered markdown) so packaging mistakes — broken frontmatter,
 * indentation — stay visible. shiki is loaded lazily; the highlighter
 * is created on first expand and reused.
 */
import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { ChevronRight, FileText, FolderOpen } from "lucide-react"
import {
  createHighlighterCore,
  type HighlighterCore,
} from "shiki/core"
import { createOnigurumaEngine } from "shiki/engine/oniguruma"

import { Badge } from "../../../components/ui/badge"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "../../../components/ui/collapsible"
import { cn } from "../../../lib/utils"
import type { CanonicalSkillSpec, SkillFile } from "./types"

interface Props {
  skill: CanonicalSkillSpec
}

/* 28px hairline rows; mono file names; chevron rotates when open. */
const TREE_ROW_CLASS = "flex h-7 w-full items-center gap-2 border-b border-line text-left text-sm transition-colors duration-150 ease-settle hover:app-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent/40"

export function SkillFileTree({ skill }: Props) {
  const { t } = useTranslation("admin")
  const files = useMemo(() => skill.files ?? [], [skill.files])

  const grouped = useMemo(() => groupFiles(files), [files])

  return (
    <section className="border-t border-line">
      <SkillMdRow skill={skill} />

      {grouped.references.length > 0 && (
        <GroupRows
          title={t("capabilities.import.skill.fileTree.references", "references/")}
          files={grouped.references}
        />
      )}
      {grouped.scripts.length > 0 && (
        <GroupRows
          title={t("capabilities.import.skill.fileTree.scripts", "scripts/")}
          files={grouped.scripts}
        />
      )}
      {grouped.other.length > 0 && (
        <GroupRows
          title={t("capabilities.import.skill.fileTree.other", "Other files")}
          files={grouped.other}
        />
      )}
    </section>
  )
}

function Chevron({ open }: { open: boolean }) {
  return (
    <ChevronRight
      className={cn("h-3.5 w-3.5 shrink-0 text-fg-muted transition-transform duration-200 ease-spring", open && "rotate-90")}
      strokeWidth={1.5}
      aria-hidden="true"
    />
  )
}

function SkillMdRow({ skill }: { skill: CanonicalSkillSpec }) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(true)

  // Parser drops the raw bytes; rebuild from canonical fields so what
  // the user sees matches what's been imported.
  const source = useMemo(() => buildSkillMdSource(skill), [skill])

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger className={TREE_ROW_CLASS}>
        <Chevron open={open} />
        <FileText className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
        <code className="font-mono text-xs text-fg">SKILL.md</code>
        <Badge variant="success" dot className="ml-auto">
          {t("capabilities.import.skill.fileTree.entry", "Entry")}
        </Badge>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <ShikiCode content={source} lang="markdown" />
      </CollapsibleContent>
    </Collapsible>
  )
}

/**
 * Rebuild SKILL.md from the parsed spec (YAML frontmatter + body) so the
 * preview is identical to what `splitSkillDoc` consumed.
 */
function buildSkillMdSource(skill: CanonicalSkillSpec): string {
  const lines: string[] = ["---"]
  lines.push(`name: ${skill.slug}`)
  if (skill.title && skill.title !== skill.slug) {
    lines.push(`title: ${skill.title}`)
  }
  if (skill.description) {
    // Use the block-scalar form so multi-line descriptions render
    // naturally and the user can tell they ARE multi-line.
    lines.push(`description: |`)
    for (const line of skill.description.split("\n")) {
      lines.push(`  ${line}`)
    }
  }
  if (skill.trigger) {
    lines.push(`trigger: ${skill.trigger}`)
  }
  lines.push("---", "")
  lines.push(skill.instruction.replace(/\n+$/, ""))
  return lines.join("\n")
}

function GroupRows({
  title,
  files,
}: {
  title: string
  files: SkillFile[]
}) {
  const [open, setOpen] = useState(false)
  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger className={TREE_ROW_CLASS}>
        <Chevron open={open} />
        <FolderOpen className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
        <span className="font-mono text-xs text-fg">{title}</span>
        <span className="ml-auto font-mono text-xs tabular-nums text-fg-muted">{files.length}</span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <ul className="m-0 list-none p-0">
          {files.map((f) => (
            <FileRow key={f.path} file={f} />
          ))}
        </ul>
      </CollapsibleContent>
    </Collapsible>
  )
}

function FileRow({ file }: { file: SkillFile }) {
  const [open, setOpen] = useState(false)
  return (
    <li>
      <Collapsible open={open} onOpenChange={setOpen}>
        <CollapsibleTrigger className={cn(TREE_ROW_CLASS, "pl-5")}>
          <Chevron open={open} />
          <FileText className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
          <code className="truncate font-mono text-xs text-fg">{file.path}</code>
          <span className="ml-auto text-xs text-fg-muted">{file.kind}</span>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <FileBody file={file} />
        </CollapsibleContent>
      </Collapsible>
    </li>
  )
}

function FileBody({ file }: { file: SkillFile }) {
  const lang = inferLang(file.path)
  return <ShikiCode content={file.content} lang={lang} />
}

/**
 * ShikiCode — shared "render this string as syntax-highlighted code"
 * surface used by SKILL.md, references/*, scripts/*. Falls back to a
 * plain <pre> while the highlighter is loading or if the language is
 * unknown so we never hide content behind a loading state.
 */
function ShikiCode({ content, lang }: { content: string; lang: string }) {
  const [html, setHtml] = useState<string | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    void highlight(content, lang)
      .then((rendered) => {
        if (!cancelled) setHtml(rendered)
      })
      .catch((e: unknown) => {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [content, lang])

  if (err || html === null) {
    return (
      <pre className="m-0 my-2 max-h-[420px] overflow-y-auto whitespace-pre-wrap break-all rounded-md bg-surface-muted p-2 font-mono text-xs leading-relaxed text-fg">
        {content}
      </pre>
    )
  }
  return (
    <div
      // Force shiki's <pre> to wrap — default overflow-x: auto pushes
      // long URLs / minified JSON into horizontal dialog scroll. Token
      // colours come from the light / dark CSS variables shiki emits.
      className="my-2 max-h-[420px] overflow-y-auto text-xs leading-relaxed [&_pre]:!m-0 [&_pre]:!whitespace-pre-wrap [&_pre]:!break-all [&_pre]:rounded-md [&_pre]:!bg-surface-muted [&_pre]:!p-2 [&_span]:[color:var(--shiki-light)] [html[data-theme=dark]_&_span]:[color:var(--shiki-dark)]"
      // shiki output is sanitized server-controlled markup.
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}

/* ---------- helpers ---------------------------------------------------- */

interface Grouped {
  references: SkillFile[]
  scripts: SkillFile[]
  other: SkillFile[]
}

function groupFiles(files: SkillFile[]): Grouped {
  const references: SkillFile[] = []
  const scripts: SkillFile[] = []
  const other: SkillFile[] = []
  // Stable sort makes the list reproducible across re-uploads.
  const sorted = [...files].sort((a, b) => a.path.localeCompare(b.path))
  for (const f of sorted) {
    if (f.path.startsWith("references/")) references.push(f)
    else if (f.path.startsWith("scripts/")) scripts.push(f)
    else other.push(f)
  }
  return { references, scripts, other }
}

function inferLang(path: string): string {
  const lower = path.toLowerCase()
  if (lower.endsWith(".py")) return "python"
  if (lower.endsWith(".sh") || lower.endsWith(".bash")) return "bash"
  if (lower.endsWith(".ts") || lower.endsWith(".tsx")) return "typescript"
  if (lower.endsWith(".js") || lower.endsWith(".jsx") || lower.endsWith(".mjs"))
    return "javascript"
  if (lower.endsWith(".json")) return "json"
  if (lower.endsWith(".yaml") || lower.endsWith(".yml")) return "yaml"
  if (lower.endsWith(".toml")) return "toml"
  if (lower.endsWith(".md") || lower.endsWith(".markdown")) return "markdown"
  return "text"
}

// Singleton: first expand pays the cost, subsequent reuse it. Languages
// are hand-picked so the chunk only loads what's rendered. Both themes
// load so the block follows the app theme without a re-render.
let highlighterPromise: Promise<HighlighterCore> | null = null
function getHighlighter(): Promise<HighlighterCore> {
  if (!highlighterPromise) {
    highlighterPromise = createHighlighterCore({
      themes: [import("shiki/themes/github-light.mjs"), import("shiki/themes/github-dark.mjs")],
      langs: [
        import("shiki/langs/python.mjs"),
        import("shiki/langs/bash.mjs"),
        import("shiki/langs/typescript.mjs"),
        import("shiki/langs/javascript.mjs"),
        import("shiki/langs/json.mjs"),
        import("shiki/langs/yaml.mjs"),
        import("shiki/langs/toml.mjs"),
        import("shiki/langs/markdown.mjs"),
      ],
      engine: createOnigurumaEngine(import("shiki/wasm")),
    })
  }
  return highlighterPromise
}

async function highlight(code: string, lang: string): Promise<string> {
  const h = await getHighlighter()
  const known = new Set(h.getLoadedLanguages())
  const useLang = known.has(lang as never) ? lang : "text"
  return h.codeToHtml(code, {
    lang: useLang,
    themes: { light: "github-light", dark: "github-dark" },
    defaultColor: false,
  })
}

export { SkillFileTree as default }
