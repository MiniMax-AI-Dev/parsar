package store

import "testing"

func TestNormalizeEmail(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"admin@example.com", "admin@example.com"},
		{"Admin@Example.COM", "admin@example.com"},
		{"  spaced@x.io  ", "spaced@x.io"},
		{"\tTAB@x.io\n", "tab@x.io"},
		{"MiXeD@Case.Local", "mixed@case.local"},
	}
	for _, tc := range cases {
		if got := normalizeEmail(tc.in); got != tc.want {
			t.Fatalf("normalizeEmail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
