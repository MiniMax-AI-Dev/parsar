/** Multi-file Skill viewer with an optional plain-text edit mode. */
import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { ChevronRight, FileText, FolderOpen } from "lucide-react"
import { createHighlighterCore, type HighlighterCore } from "shiki/core"
import { createOnigurumaEngine } from "shiki/engine/oniguruma"

import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "../../../components/ui/collapsible"
import type { CanonicalSkillSpec, SkillFile } from "./types"

export interface SkillFileTreeEntry {
  path: string
  content: string | null
  kind: SkillFile["kind"]
  size?: number
}

interface Props {
  skill?: CanonicalSkillSpec
  files?: SkillFileTreeEntry[]
  editable?: boolean
  onFileChange?: (path: string, content: string) => void
}

export function SkillFileTree({ skill, files, editable = false, onFileChange }: Props) {
  const { t } = useTranslation("admin")
  const entries = useMemo(() => files ?? (skill ? buildTreeEntries(skill) : []), [files, skill])
  const skillMd = entries.find((file) => file.path.toLowerCase() === "skill.md")
  const grouped = useMemo(
    () => groupFiles(entries.filter((file) => file !== skillMd)),
    [entries, skillMd],
  )

  return (
    <section className="grid gap-3">
      {skillMd && <SkillMdCard file={skillMd} editable={editable} onFileChange={onFileChange} />}

      {grouped.references.length > 0 && (
        <GroupCard
          icon={<FolderOpen className="h-4 w-4 text-fg-subtle" />}
          title={t("capabilities.import.skill.fileTree.references", "references/")}
          files={grouped.references}
          editable={editable}
          onFileChange={onFileChange}
        />
      )}
      {grouped.scripts.length > 0 && (
        <GroupCard
          icon={<FolderOpen className="h-4 w-4 text-fg-subtle" />}
          title={t("capabilities.import.skill.fileTree.scripts", "scripts/")}
          files={grouped.scripts}
          editable={editable}
          onFileChange={onFileChange}
        />
      )}
      {grouped.other.length > 0 && (
        <GroupCard
          icon={<FolderOpen className="h-4 w-4 text-fg-subtle" />}
          title={t("capabilities.import.skill.fileTree.other", "Other files")}
          files={grouped.other}
          editable={editable}
          onFileChange={onFileChange}
        />
      )}
    </section>
  )
}

function SkillMdCard({
  file,
  editable,
  onFileChange,
}: {
  file: SkillFileTreeEntry
  editable: boolean
  onFileChange?: (path: string, content: string) => void
}) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(true)

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <div className="overflow-hidden rounded-lg border border-success-border bg-surface">
        <CollapsibleTrigger className="flex w-full items-center gap-2 border-b border-line-muted bg-success-subtle/50 px-3 py-2 text-left">
          <ChevronRight
            className={`h-4 w-4 shrink-0 text-fg-subtle transition-transform ${open ? "rotate-90" : ""}`}
          />
          <FileText className="h-4 w-4 shrink-0 text-success" />
          <code className="font-mono text-sm text-fg">SKILL.md</code>
          <span className="ml-auto rounded-full bg-success-subtle px-2 py-0.5 text-xs font-medium uppercase tracking-wide text-success-emphasis">
            {t("capabilities.import.skill.fileTree.entry", "Entry")}
          </span>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <FileBody file={file} editable={editable} onFileChange={onFileChange} />
        </CollapsibleContent>
      </div>
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

function GroupCard({
  icon,
  title,
  files,
  editable,
  onFileChange,
}: {
  icon: React.ReactNode
  title: string
  files: SkillFileTreeEntry[]
  editable: boolean
  onFileChange?: (path: string, content: string) => void
}) {
  const [open, setOpen] = useState(false)
  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <div className="overflow-hidden rounded-lg border border-line bg-surface">
        <CollapsibleTrigger className="flex w-full items-center gap-2 border-b border-line-muted bg-surface-subtle px-3 py-2 text-left">
          <ChevronRight
            className={`h-4 w-4 shrink-0 text-fg-subtle transition-transform ${open ? "rotate-90" : ""}`}
          />
          {icon}
          <span className="font-mono text-sm text-fg-muted">{title}</span>
          <span className="ml-auto text-xs text-fg-subtle">{files.length}</span>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <ul className="grid gap-2 p-3">
            {files.map((f) => (
              <FileRow key={f.path} file={f} editable={editable} onFileChange={onFileChange} />
            ))}
          </ul>
        </CollapsibleContent>
      </div>
    </Collapsible>
  )
}

function FileRow({
  file,
  editable,
  onFileChange,
}: {
  file: SkillFileTreeEntry
  editable: boolean
  onFileChange?: (path: string, content: string) => void
}) {
  const [open, setOpen] = useState(false)
  return (
    <li>
      <Collapsible open={open} onOpenChange={setOpen}>
        <CollapsibleTrigger className="flex w-full items-center gap-2 rounded-md border border-line bg-surface px-2.5 py-1.5 text-left hover:bg-surface-subtle">
          <ChevronRight
            className={`h-3.5 w-3.5 shrink-0 text-fg-faint transition-transform ${open ? "rotate-90" : ""}`}
          />
          <FileText className="h-3.5 w-3.5 shrink-0 text-fg-subtle" />
          <code className="truncate font-mono text-xs text-fg-emphasis">{file.path}</code>
          <span className="ml-auto text-xs uppercase tracking-wide text-fg-faint">{file.kind}</span>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <FileBody file={file} editable={editable} onFileChange={onFileChange} />
        </CollapsibleContent>
      </Collapsible>
    </li>
  )
}

function FileBody({
  file,
  editable,
  onFileChange,
}: {
  file: SkillFileTreeEntry
  editable: boolean
  onFileChange?: (path: string, content: string) => void
}) {
  const { t } = useTranslation("admin")
  if (file.content === null) {
    return (
      <div className="bg-surface-subtle px-3 py-4 text-xs text-fg-subtle">
        {t("capabilities.versions.add.binaryPreserved", {
          size: file.size ?? 0,
          defaultValue: "Binary file ({{size}} bytes). It will be preserved unchanged.",
        })}
      </div>
    )
  }
  if (editable) {
    return (
      <textarea
        value={file.content}
        onChange={(event) => onFileChange?.(file.path, event.target.value)}
        rows={16}
        spellCheck={false}
        autoCorrect="off"
        autoCapitalize="off"
        className="block min-h-64 w-full resize-y border-0 bg-surface-subtle px-3 py-2 font-mono text-xs leading-relaxed text-fg focus:outline-none focus:ring-2 focus:ring-inset focus:ring-line-strong"
      />
    )
  }
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
      <pre className="max-h-[420px] overflow-y-auto whitespace-pre-wrap break-all bg-surface-subtle px-3 py-2 font-mono text-xs leading-relaxed text-fg-muted">
        {content}
      </pre>
    )
  }
  return (
    <div
      // Force shiki's <pre> to wrap — default overflow-x: auto pushes
      // long URLs / minified JSON into horizontal dialog scroll.
      className="max-h-[420px] overflow-y-auto text-xs leading-relaxed [&_pre]:!m-0 [&_pre]:!whitespace-pre-wrap [&_pre]:!break-all [&_pre]:!bg-surface-subtle [&_pre]:!px-3 [&_pre]:!py-2"
      // shiki output is sanitized server-controlled markup.
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}

/* ---------- helpers ---------------------------------------------------- */

interface Grouped {
  references: SkillFileTreeEntry[]
  scripts: SkillFileTreeEntry[]
  other: SkillFileTreeEntry[]
}

function groupFiles(files: SkillFileTreeEntry[]): Grouped {
  const references: SkillFileTreeEntry[] = []
  const scripts: SkillFileTreeEntry[] = []
  const other: SkillFileTreeEntry[] = []
  // Stable sort makes the list reproducible across re-uploads.
  const sorted = [...files].sort((a, b) => a.path.localeCompare(b.path))
  for (const f of sorted) {
    if (f.path.startsWith("references/")) references.push(f)
    else if (f.path.startsWith("scripts/")) scripts.push(f)
    else other.push(f)
  }
  return { references, scripts, other }
}

function buildTreeEntries(skill: CanonicalSkillSpec): SkillFileTreeEntry[] {
  return [
    {
      path: "SKILL.md",
      content: buildSkillMdSource(skill),
      kind: "markdown",
    },
    ...(skill.files ?? []).map((file) => ({ ...file })),
  ]
}

function inferLang(path: string): string {
  const lower = path.toLowerCase()
  if (lower.endsWith(".py")) return "python"
  if (lower.endsWith(".sh") || lower.endsWith(".bash")) return "bash"
  if (lower.endsWith(".ts") || lower.endsWith(".tsx")) return "typescript"
  if (lower.endsWith(".js") || lower.endsWith(".jsx") || lower.endsWith(".mjs")) return "javascript"
  if (lower.endsWith(".json")) return "json"
  if (lower.endsWith(".yaml") || lower.endsWith(".yml")) return "yaml"
  if (lower.endsWith(".toml")) return "toml"
  if (lower.endsWith(".md") || lower.endsWith(".markdown")) return "markdown"
  return "text"
}

// Singleton: first expand pays the cost, subsequent reuse it. Languages
// are hand-picked so the chunk only loads what's rendered.
let highlighterPromise: Promise<HighlighterCore> | null = null
function getHighlighter(): Promise<HighlighterCore> {
  if (!highlighterPromise) {
    highlighterPromise = createHighlighterCore({
      themes: [import("shiki/themes/github-light.mjs")],
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
  return h.codeToHtml(code, { lang: useLang, theme: "github-light" })
}

export { SkillFileTree as default }
