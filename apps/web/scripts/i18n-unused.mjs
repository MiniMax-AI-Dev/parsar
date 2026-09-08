/**
 * Reports locale keys no source file can reach.
 *
 * A grep cannot do this job: the codebase calls `t()` with ternaries inside the
 * argument (`t(isSwitch ? "a.title" : "b.title")`), through aliases (`tc`,
 * `translateDetail`), with template literals whose tail is a variable
 * (`t(\`runStatus.${run.status}\`)`), and it passes bare key strings as props
 * that some other component later hands to `t()` (`subtitleFor="audit.page.title"`).
 * A naive scan calls all of those dead and deleting them breaks the app.
 *
 * So this parses with the TypeScript compiler and is deliberately generous:
 *   · every string literal inside any `t(...)`-shaped call, however nested
 *   · the static head of any template literal in that position, as a prefix
 *     that keeps everything beneath it
 *   · and, as a safety net, EVERY dotted string literal anywhere in the tree,
 *     because a key can travel as a prop, a map value or a constant
 *
 * Anything still unreached is reported. Usage:
 *   node scripts/i18n-unused.mjs           # report
 *   node scripts/i18n-unused.mjs --json    # machine-readable
 */
import { readFileSync, readdirSync, statSync } from "node:fs"
import path from "node:path"
import ts from "typescript"

const ROOT = path.resolve(import.meta.dirname, "..")
const SRC = path.join(ROOT, "src")
const LOCALES = path.join(SRC, "i18n/locales")

/**
 * Names that mean "translate". The fixed ones, plus every alias the tree
 * actually declares — `const { t: ta } = useTranslation("admin")` renames it,
 * and a hand-maintained list silently goes stale the next time someone does.
 */
const T_NAMES = new Set(["t", "i18nT", "translate", "translateDetail"])

function walkFiles(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const full = path.join(dir, entry)
    if (statSync(full).isDirectory()) walkFiles(full, out)
    else if (/\.(ts|tsx)$/.test(entry)) out.push(full)
  }
  return out
}

function flatten(obj, prefix = "", out = new Set()) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === "object") flatten(v, key, out)
    else out.add(key)
  }
  return out
}

const literals = new Set()   // every dotted string in the tree
const prefixes = new Set()   // static heads of template literals in t() position

function collectFromArg(node) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
    literals.add(node.text)
  } else if (ts.isTemplateExpression(node)) {
    const head = node.head.text.replace(/\.$/, "")
    // `t(`${section}.title`)` has nothing static to anchor on; the tool cannot
    // see which keys it reaches, so it says so instead of staying quiet.
    if (head) prefixes.add(head)
    else emptyHeads.push(node.getText().slice(0, 60))
  } else if (ts.isConditionalExpression(node)) {
    collectFromArg(node.whenTrue)
    collectFromArg(node.whenFalse)
  } else if (ts.isBinaryExpression(node)) {
    collectFromArg(node.left)
    collectFromArg(node.right)
  } else if (ts.isParenthesizedExpression(node)) {
    collectFromArg(node.expression)
  } else if (ts.isArrayLiteralExpression(node)) {
    node.elements.forEach(collectFromArg)
  } else if (ts.isAsExpression(node) || ts.isSatisfiesExpression?.(node)) {
    collectFromArg(node.expression)
  }
}

const sources = walkFiles(SRC).map((file) => ({
  file,
  sf: ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true),
}))

// Pass one: whatever `useTranslation()` was destructured into.
for (const { sf } of sources) {
  const visit = (node) => {
    if (
      ts.isVariableDeclaration(node) &&
      node.initializer &&
      ts.isCallExpression(node.initializer) &&
      ts.isIdentifier(node.initializer.expression) &&
      node.initializer.expression.text === "useTranslation" &&
      ts.isObjectBindingPattern(node.name)
    ) {
      for (const el of node.name.elements) {
        const from = el.propertyName && ts.isIdentifier(el.propertyName) ? el.propertyName.text : null
        if ((from ?? (ts.isIdentifier(el.name) ? el.name.text : "")) === "t" && ts.isIdentifier(el.name)) {
          T_NAMES.add(el.name.text)
        }
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
}

const emptyHeads = []
for (const { file, sf } of sources) {
  const visit = (node) => {
    if (ts.isCallExpression(node) && node.arguments.length > 0) {
      const callee = node.expression
      const name = ts.isIdentifier(callee)
        ? callee.text
        : ts.isPropertyAccessExpression(callee) && ts.isIdentifier(callee.name)
          ? callee.name.text
          : ""
      if (T_NAMES.has(name)) collectFromArg(node.arguments[0])
    }
    // The safety net: any dotted literal at all, wherever it sits.
    if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) && node.text.includes(".")) {
      literals.add(node.text)
    }
    if (ts.isTemplateExpression(node) && node.head.text.includes(".")) {
      prefixes.add(node.head.text.replace(/\.$/, ""))
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
}

/** A namespace prefix in the literal (`common:actions.save`) is not part of the key. */
const bare = new Set([...literals].map((l) => (l.includes(":") ? l.slice(l.indexOf(":") + 1) : l)))
const allLiterals = new Set([...literals, ...bare])

// `t("agents.execution." + mode + ".title")` leaves a literal ending in a dot;
// that is a prefix, not a key.
for (const l of allLiterals) if (l.endsWith(".")) prefixes.add(l.slice(0, -1))

function isReached(key) {
  if (allLiterals.has(key)) return true
  // i18next resolves `t("x", { count })` to `x_one` / `x_other`; the base key
  // is what appears in source, so a plural set stands or falls with it.
  const base = key.replace(/_(zero|one|two|few|many|other|male|female)$/, "")
  if (base !== key && allLiterals.has(base)) return true
  for (const p of prefixes) if (key === p || key.startsWith(p + ".")) return true
  // A parent path being referenced (returnObjects, or a whole subtree passed on)
  // keeps its children.
  for (const l of allLiterals) if (l && key.startsWith(l + ".")) return true
  return false
}

/**
 * The other half of the method, and the half that used to live in someone's
 * shell history: read the whole repo as raw text and keep any candidate whose
 * key appears anywhere at all — a Go constant, a test assertion, a doc, the
 * compiled bundle. A static reader of one directory is not evidence on its own.
 */
const REPO = path.resolve(ROOT, "../..")
const SCAN_EXT = new Set([".ts", ".tsx", ".js", ".mjs", ".cjs", ".go", ".json", ".md", ".py", ".yaml", ".yml", ".sh"])
function repoText() {
  const chunks = []
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      if (entry.name === "node_modules" || entry.name === ".git") continue
      const full = path.join(dir, entry.name)
      if (entry.isDirectory()) walk(full)
      else if (SCAN_EXT.has(path.extname(entry.name)) && !full.includes(path.join("i18n", "locales"))) {
        try { chunks.push(readFileSync(full, "utf8")) } catch { /* unreadable, skip */ }
      }
    }
  }
  walk(REPO)
  return chunks.join("\n")
}
const corpus = repoText()

const report = {}
const namespaces = new Set([
  ...readdirSync(path.join(LOCALES, "zh-CN")),
  ...readdirSync(path.join(LOCALES, "en-US")),
].map((f) => f.replace(/\.json$/, "")))
for (const ns of namespaces) {
  const keys = new Set()
  for (const loc of ["zh-CN", "en-US"]) {
    const f = path.join(LOCALES, loc, `${ns}.json`)
    try { flatten(JSON.parse(readFileSync(f, "utf8")), "", keys) } catch { /* one-sided namespace */ }
  }
  report[ns] = [...keys].filter((k) => !isReached(k) && !corpus.includes(k)).sort()
}

if (process.argv.includes("--json")) {
  console.log(JSON.stringify(report, null, 2))
} else {
  for (const [ns, dead] of Object.entries(report)) {
    const total = flatten(JSON.parse(readFileSync(path.join(LOCALES, "zh-CN", `${ns}.json`), "utf8"))).size
    console.log(`\n${ns}: ${dead.length} of ${total} keys unreachable`)
    for (const k of dead) console.log(`  ${k}`)
  }
  if (emptyHeads.length) {
    console.log(`\n${emptyHeads.length} template key(s) with no static head — this tool cannot see what they reach:`)
    for (const e of [...new Set(emptyHeads)]) console.log(`  ${e}`)
  }
  console.log("\nA key here is unreachable by static reading AND absent from the whole repo as text.")
  console.log("It is still not proof: run .impeccable/review/probe-missing-keys.mjs after deleting,")
  console.log("which renders every surface in both locales and looks for raw keys on screen.")
}
