package specmemory

import (
	"strings"
	"testing"
)

func TestRenderSpecBlockEmpty(t *testing.T) {
	if got := RenderSpecBlock("any", nil); got != "" {
		t.Errorf("RenderSpecBlock(nil) = %q, want empty", got)
	}
	if got := RenderSpecBlock("any", []Fragment{}); got != "" {
		t.Errorf("RenderSpecBlock([]) = %q, want empty", got)
	}
}

func TestRenderSpecBlockSingle(t *testing.T) {
	got := RenderSpecBlock("acme", []Fragment{
		{Title: "Stack", Body: "Go + Postgres + pgx"},
	})
	want := `<spec workspace="acme">
### Stack
Go + Postgres + pgx
</spec>`
	if got != want {
		t.Errorf("RenderSpecBlock single =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderSpecBlockMultipleWithTags(t *testing.T) {
	got := RenderSpecBlock("acme", []Fragment{
		{Title: "Stack", Body: "Go + Postgres", Tags: []string{"backend"}},
		{Title: "Style", Body: "tabs not spaces"},
	})
	want := `<spec workspace="acme">
### Stack
Go + Postgres
[tags: backend]

### Style
tabs not spaces
</spec>`
	if got != want {
		t.Errorf("RenderSpecBlock multi =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderSpecBlockTrimsTitleAndTrailingNewlines(t *testing.T) {
	got := RenderSpecBlock("acme", []Fragment{
		{Title: "  Padded  ", Body: "body\n\n"},
	})
	want := `<spec workspace="acme">
### Padded
body
</spec>`
	if got != want {
		t.Errorf("RenderSpecBlock trim =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderMemoryBlockEmpty(t *testing.T) {
	if got := RenderMemoryBlock(nil); got != "" {
		t.Errorf("RenderMemoryBlock(nil) = %q, want empty", got)
	}
}

func TestRenderMemoryBlockGroupsByType(t *testing.T) {
	got := RenderMemoryBlock([]Memory{
		{MemoryType: MemoryTypeReference, Body: "dashboards.example.com"},
		{MemoryType: MemoryTypeUser, Body: "user is a senior backend dev"},
		{MemoryType: MemoryTypeFeedback, Body: "no defer in hot loop", Why: "got a 30% regression last year"},
		{MemoryType: MemoryTypeWorkspace, Body: "migrating to grpc", Why: "REST timeout SLO violations"},
	})
	want := `<memory>
## user
- user is a senior backend dev

## feedback
- no defer in hot loop (Why: got a 30% regression last year)

## workspace
- migrating to grpc (Why: REST timeout SLO violations)

## reference
- dashboards.example.com
</memory>`
	if got != want {
		t.Errorf("RenderMemoryBlock grouping =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderMemoryBlockOmitsWhyForUserAndReference(t *testing.T) {
	got := RenderMemoryBlock([]Memory{
		{MemoryType: MemoryTypeUser, Body: "prefers vim", Why: "habit"},
		{MemoryType: MemoryTypeReference, Body: "wiki.internal", Why: "main kb"},
	})
	if strings.Contains(got, "Why:") {
		t.Errorf("RenderMemoryBlock leaked Why for user/reference:\n%s", got)
	}
}

func TestRenderMemoryBlockRendersTitleAsBold(t *testing.T) {
	got := RenderMemoryBlock([]Memory{
		{MemoryType: MemoryTypeReference, Title: "Grafana", Body: "grafana.internal/d/api"},
	})
	want := `<memory>
## reference
- **Grafana** — grafana.internal/d/api
</memory>`
	if got != want {
		t.Errorf("RenderMemoryBlock title =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderMemoryBlockSkipsEmptyBody(t *testing.T) {
	got := RenderMemoryBlock([]Memory{
		{MemoryType: MemoryTypeUser, Body: "   "},
		{MemoryType: MemoryTypeUser, Body: "real one"},
	})
	want := `<memory>
## user
- real one
</memory>`
	if got != want {
		t.Errorf("RenderMemoryBlock empty body =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderMemoryBlockSkipsUnknownType(t *testing.T) {
	got := RenderMemoryBlock([]Memory{
		{MemoryType: MemoryType("blah"), Body: "should be dropped"},
		{MemoryType: MemoryTypeUser, Body: "kept"},
	})
	if strings.Contains(got, "blah") || strings.Contains(got, "dropped") {
		t.Errorf("RenderMemoryBlock leaked unknown type:\n%s", got)
	}
	if !strings.Contains(got, "kept") {
		t.Errorf("RenderMemoryBlock lost valid row:\n%s", got)
	}
}

func TestRenderIncrementalMemoryWrapsInIncrementalTag(t *testing.T) {
	got := RenderIncrementalMemory([]Memory{
		{MemoryType: MemoryTypeUser, Body: "just learned"},
	})
	want := `<memory-incremental>
## user
- just learned
</memory-incremental>`
	if got != want {
		t.Errorf("RenderIncrementalMemory =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderIncrementalMemoryEmpty(t *testing.T) {
	if got := RenderIncrementalMemory(nil); got != "" {
		t.Errorf("RenderIncrementalMemory(nil) = %q, want empty", got)
	}
}

func TestMemoryWriteGuideHasAllFourTypes(t *testing.T) {
	guide := MemoryWriteGuide()
	for _, want := range []string{"user", "feedback", "workspace", "reference"} {
		if !strings.Contains(guide, want) {
			t.Errorf("MemoryWriteGuide missing %q", want)
		}
	}
	if !strings.Contains(guide, "parsar memory add") {
		t.Error("MemoryWriteGuide should mention `parsar memory add`")
	}
}
