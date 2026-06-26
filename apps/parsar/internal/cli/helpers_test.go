package cli

import (
	"strings"
	"testing"
)

func TestSplitTags(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{",,,", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , ,c ", []string{"a", "b", "c"}},
	}
	for _, tc := range cases {
		got := splitTags(tc.in)
		if !stringSliceEqual(got, tc.want) {
			t.Errorf("splitTags(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hell…"},
		// truncate is rune-aware, not byte-aware.
		{"工程师", 2, "工…"},
		{"abc", 1, "a"},
		{"abc", 0, "abc"},
	}
	for _, tc := range cases {
		got := truncate(tc.in, tc.n)
		if got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestEmitJSON(t *testing.T) {
	var buf strings.Builder
	if err := emitJSON(&buf, map[string]string{"k": "v"}); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}
	got := buf.String()
	want := "{\n  \"k\": \"v\"\n}\n"
	if got != want {
		t.Errorf("emitJSON output = %q, want %q", got, want)
	}
}

func TestResolveBodyPlain(t *testing.T) {
	got, err := resolveBody("plain body")
	if err != nil {
		t.Fatalf("resolveBody: %v", err)
	}
	if got != "plain body" {
		t.Errorf("got %q, want %q", got, "plain body")
	}
}

// The stdin branch is covered indirectly via spec_test / memory_test
// through the runContext seam.

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
