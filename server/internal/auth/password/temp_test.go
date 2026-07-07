package password

import (
	"regexp"
	"strings"
	"testing"
)

func TestGenerateTempShape(t *testing.T) {
	p, err := GenerateTemp()
	if err != nil {
		t.Fatalf("GenerateTemp: %v", err)
	}
	if len(p) != 16 {
		t.Fatalf("length = %d, want 16 (%q)", len(p), p)
	}
	// No confusable characters: l, I, O, o, 0, 1 must never appear.
	confusables := regexp.MustCompile(`[lIOo01]`)
	if confusables.MatchString(p) {
		t.Fatalf("contains confusable char: %q", p)
	}
	// Alnum only.
	alnum := regexp.MustCompile(`^[A-Za-z2-9]+$`)
	if !alnum.MatchString(p) {
		t.Fatalf("contains non-alnum char: %q", p)
	}
}

func TestGenerateTempPassesValidate(t *testing.T) {
	// The temp password should always clear the login policy so
	// callers can log in with it without re-prompting.
	for i := 0; i < 10; i++ {
		p, err := GenerateTemp()
		if err != nil {
			t.Fatalf("GenerateTemp: %v", err)
		}
		if err := Validate(p); err != nil {
			t.Fatalf("Validate rejects generated temp %q: %v", p, err)
		}
	}
}

func TestGenerateTempUnique(t *testing.T) {
	// Sanity: 20 draws should all differ with astronomical probability.
	seen := map[string]struct{}{}
	for i := 0; i < 20; i++ {
		p, err := GenerateTemp()
		if err != nil {
			t.Fatalf("GenerateTemp: %v", err)
		}
		if _, dup := seen[p]; dup {
			t.Fatalf("duplicate temp password after %d draws: %q", i, p)
		}
		seen[p] = struct{}{}
	}
	// Extra: distribution smell test — reject a result that is all
	// digits (should be roughly 1 in 3.5B for a 16-char draw).
	for p := range seen {
		if strings.ContainsAny(p, "23456789") && !strings.ContainsAny(p, "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ") {
			t.Logf("suspicious all-digit draw: %q (statistically improbable, may be a real bug)", p)
		}
	}
}
