package specmemory

import (
	"fmt"
	"strings"
)

// memoryWriteGuide is the static <memory-write-guide> block injected
// at SessionStart. Indentation is significant — the agent sees this
// exactly as written.
const memoryWriteGuide = "<memory-write-guide>\n" +
	"You have a persistent memory you can write to via the `parsar` CLI:\n" +
	"\n" +
	"  parsar memory add --type <type> --body \"…\" [--title \"…\"] [--why \"…\"] [--tag a,b]\n" +
	"\n" +
	"Memory types:\n" +
	"  - user      : facts about the user's role, preferences, responsibilities, knowledge.\n" +
	"  - feedback  : guidance the user gave about how to work — corrections AND validated\n" +
	"                non-obvious decisions. Always set --why (the reason makes the rule\n" +
	"                applicable to edge cases later).\n" +
	"  - workspace : ongoing workspace state (initiatives, constraints, deadlines, decisions)\n" +
	"                that isn't derivable from code or git history. Always set --why.\n" +
	"  - reference : pointers to external systems (dashboards, docs, Slack channels) so\n" +
	"                future-you knows where to look.\n" +
	"\n" +
	"When to save:\n" +
	"  - Any time the user reveals stable information that will help future conversations.\n" +
	"  - Save quietly — do NOT announce \"I'll remember that\" in the chat.\n" +
	"  - Save from successes too, not just corrections. If the user accepts a non-obvious\n" +
	"    approach without pushback, that confirmation is worth a feedback memory.\n" +
	"\n" +
	"When NOT to save:\n" +
	"  - Anything derivable from the current code or git history.\n" +
	"  - One-off bug-fix recipes (the fix is in the code; the commit message has context).\n" +
	"  - Ephemeral in-progress task state — that belongs in a plan, not memory.\n" +
	"  - Content already covered by the <spec> block above.\n" +
	"</memory-write-guide>"

// MemoryWriteGuide returns the static guide block. SessionStart only;
// per-turn skips it (the agent already learned the rules).
func MemoryWriteGuide() string { return memoryWriteGuide }

// memoryTypeRenderOrder fixes the order memory types appear under the
// <memory> block so the agent can rely on positional cues and the
// prompt-cache key doesn't churn from map iteration randomness.
var memoryTypeRenderOrder = []MemoryType{
	MemoryTypeUser,
	MemoryTypeFeedback,
	MemoryTypeWorkspace,
	MemoryTypeReference,
}

// RenderSpecBlock renders the workspace's active spec fragments into
// the <spec workspace="..."> block. Fragments are emitted in the order
// received — caller controls ordering. Empty input returns "".
func RenderSpecBlock(workspaceName string, fragments []Fragment) string {
	if len(fragments) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<spec workspace=%q>\n", workspaceName)
	for i, f := range fragments {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "### %s\n", strings.TrimSpace(f.Title))
		body := strings.TrimRight(f.Body, "\n")
		b.WriteString(body)
		b.WriteString("\n")
		if len(f.Tags) > 0 {
			fmt.Fprintf(&b, "[tags: %s]\n", strings.Join(f.Tags, ", "))
		}
	}
	b.WriteString("</spec>")
	return b.String()
}

// RenderMemoryBlock renders the SessionStart <memory> snapshot grouped
// by MemoryType. Only feedback and workspace entries print the
// "(Why: ...)" suffix; user/reference rows stay terse.
func RenderMemoryBlock(memories []Memory) string {
	if len(memories) == 0 {
		return ""
	}
	grouped := groupMemoriesByType(memories)
	if len(grouped) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<memory>\n")
	first := true
	for _, mt := range memoryTypeRenderOrder {
		bucket, ok := grouped[mt]
		if !ok || len(bucket) == 0 {
			continue
		}
		if !first {
			b.WriteString("\n")
		}
		first = false
		fmt.Fprintf(&b, "## %s\n", mt.String())
		for _, m := range bucket {
			writeMemoryLine(&b, m)
		}
	}
	b.WriteString("</memory>")
	return b.String()
}

// RenderIncrementalMemory renders the per-turn delta in the same
// format as the snapshot block, wrapped in <memory-incremental> so the
// hook + agent can distinguish "fresh" from "snapshot".
func RenderIncrementalMemory(memories []Memory) string {
	if len(memories) == 0 {
		return ""
	}
	grouped := groupMemoriesByType(memories)
	if len(grouped) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<memory-incremental>\n")
	first := true
	for _, mt := range memoryTypeRenderOrder {
		bucket, ok := grouped[mt]
		if !ok || len(bucket) == 0 {
			continue
		}
		if !first {
			b.WriteString("\n")
		}
		first = false
		fmt.Fprintf(&b, "## %s\n", mt.String())
		for _, m := range bucket {
			writeMemoryLine(&b, m)
		}
	}
	b.WriteString("</memory-incremental>")
	return b.String()
}

// writeMemoryLine emits one bullet — title + body, with "(Why: ...)"
// appended for the types that document a reason.
func writeMemoryLine(b *strings.Builder, m Memory) {
	body := strings.TrimSpace(m.Body)
	if body == "" {
		// Store layer requires non-empty body; if a row slips through
		// drop it rather than emitting "- " and confusing the agent.
		return
	}
	title := strings.TrimSpace(m.Title)
	b.WriteString("- ")
	if title != "" {
		fmt.Fprintf(b, "**%s** — ", title)
	}
	b.WriteString(body)
	if why := strings.TrimSpace(m.Why); why != "" && memoryTypeWantsWhy(m.MemoryType) {
		fmt.Fprintf(b, " (Why: %s)", why)
	}
	b.WriteString("\n")
}

// memoryTypeWantsWhy reports whether a memory type's render line
// should suffix the (Why: ...) clause.
func memoryTypeWantsWhy(mt MemoryType) bool {
	switch mt {
	case MemoryTypeFeedback, MemoryTypeWorkspace:
		return true
	}
	return false
}

// groupMemoriesByType buckets the input by MemoryType. Unknown types
// are dropped so a poisoned row doesn't render under a bogus heading.
func groupMemoriesByType(memories []Memory) map[MemoryType][]Memory {
	out := make(map[MemoryType][]Memory, 4)
	for _, m := range memories {
		if !m.MemoryType.Valid() {
			continue
		}
		out[m.MemoryType] = append(out[m.MemoryType], m)
	}
	return out
}
