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

/** Anything shaped like a translate call. Aliases are declared as `t: tc`. */
const T_NAMES = /^(t|tc|td|tCommon|tAdmin|translate|translateDetail|i18nT)$/

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
    if (head) prefixes.add(head)
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

for (const file of walkFiles(SRC)) {
  const sf = ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true)
  const visit = (node) => {
    if (ts.isCallExpression(node) && node.arguments.length > 0) {
      const callee = node.expression
      const name = ts.isIdentifier(callee)
        ? callee.text
        : ts.isPropertyAccessExpression(callee) && ts.isIdentifier(callee.name)
          ? callee.name.text
          : ""
      if (T_NAMES.test(name)) collectFromArg(node.arguments[0])
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
  for (const p of prefixes) if (key === p || key.startsWith(p + ".")) return true
  // A parent path being referenced (returnObjects, or a whole subtree passed on)
  // keeps its children.
  for (const l of allLiterals) if (l && key.startsWith(l + ".")) return true
  return false
}

const report = {}
for (const ns of readdirSync(path.join(LOCALES, "zh-CN")).map((f) => f.replace(/\.json$/, ""))) {
  const keys = flatten(JSON.parse(readFileSync(path.join(LOCALES, "zh-CN", `${ns}.json`), "utf8")))
  report[ns] = [...keys].filter((k) => !isReached(k)).sort()
}

if (process.argv.includes("--json")) {
  console.log(JSON.stringify(report, null, 2))
} else {
  for (const [ns, dead] of Object.entries(report)) {
    const total = flatten(JSON.parse(readFileSync(path.join(LOCALES, "zh-CN", `${ns}.json`), "utf8"))).size
    console.log(`\n${ns}: ${dead.length} of ${total} keys unreachable`)
    for (const k of dead) console.log(`  ${k}`)
  }
}
