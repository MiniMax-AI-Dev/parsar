package specmemory

import (
	"reflect"
	"strings"
	"testing"
)

func TestImportFragmentsFromMarkdownEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n\t"} {
		if got := ImportFragmentsFromMarkdown(in); got != nil {
			t.Errorf("ImportFragmentsFromMarkdown(%q) = %+v, want nil", in, got)
		}
	}
}

func TestImportFragmentsFromMarkdownSingleSection(t *testing.T) {
	in := "## Stack\nGo + Postgres + pgx\n"
	got := ImportFragmentsFromMarkdown(in)
	want := []ImportedFragment{{Title: "Stack", Body: "Go + Postgres + pgx"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("single section = %+v, want %+v", got, want)
	}
}

func TestImportFragmentsFromMarkdownMultipleSections(t *testing.T) {
	in := `## Stack
Go + Postgres
pgx for DB access

## Style
tabs not spaces

## Testing
table-driven tests preferred
`
	got := ImportFragmentsFromMarkdown(in)
	want := []ImportedFragment{
		{Title: "Stack", Body: "Go + Postgres\npgx for DB access"},
		{Title: "Style", Body: "tabs not spaces"},
		{Title: "Testing", Body: "table-driven tests preferred"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("multi section = %+v, want %+v", got, want)
	}
}

func TestImportFragmentsFromMarkdownDiscardsPreamble(t *testing.T) {
	in := `This is intro text that gets thrown away.
And another preamble line.

## Real Section
content
`
	got := ImportFragmentsFromMarkdown(in)
	want := []ImportedFragment{{Title: "Real Section", Body: "content"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("preamble discard = %+v, want %+v", got, want)
	}
}

func TestImportFragmentsFromMarkdownSkipsEmptySections(t *testing.T) {
	in := `## Empty One

## Empty Two

## Has Content
real
`
	got := ImportFragmentsFromMarkdown(in)
	want := []ImportedFragment{{Title: "Has Content", Body: "real"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("skip empty = %+v, want %+v", got, want)
	}
}

func TestImportFragmentsFromMarkdownIgnoresLevel3Heading(t *testing.T) {
	in := `## Outer
top body

### Inner
inner body

## Next
next body
`
	got := ImportFragmentsFromMarkdown(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 fragments, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Body, "### Inner") {
		t.Errorf("Outer.Body should include the level-3 heading, got %q", got[0].Body)
	}
	if got[1].Title != "Next" {
		t.Errorf("second fragment title = %q, want %q", got[1].Title, "Next")
	}
}

func TestImportFragmentsFromMarkdownHandlesNoTrailingNewline(t *testing.T) {
	in := "## Lone\nbody without trailing newline"
	got := ImportFragmentsFromMarkdown(in)
	want := []ImportedFragment{{Title: "Lone", Body: "body without trailing newline"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("no trailing newline = %+v, want %+v", got, want)
	}
}

func TestImportFragmentsFromMarkdownTrimsTitleWhitespace(t *testing.T) {
	in := "##    Padded Title   \nbody\n"
	got := ImportFragmentsFromMarkdown(in)
	if len(got) != 1 || got[0].Title != "Padded Title" {
		t.Errorf("title trim = %+v, want Title=%q", got, "Padded Title")
	}
}

func TestImportFragmentsFromMarkdownRejectsHeadingWithoutTitle(t *testing.T) {
	in := "##\nshould not be a section\n## Real\nreal body\n"
	got := ImportFragmentsFromMarkdown(in)
	// "##" with no title is not a heading; both lines are discarded
	// as preamble.
	want := []ImportedFragment{{Title: "Real", Body: "real body"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("no-title heading = %+v, want %+v", got, want)
	}
}

func TestImportFragmentsFromMarkdownPreservesInternalBlankLines(t *testing.T) {
	in := `## Block
line 1

line 3

line 5

## Next
next
`
	got := ImportFragmentsFromMarkdown(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 fragments, got %d", len(got))
	}
	if got[0].Body != "line 1\n\nline 3\n\nline 5" {
		t.Errorf("internal blanks not preserved, got %q", got[0].Body)
	}
}
