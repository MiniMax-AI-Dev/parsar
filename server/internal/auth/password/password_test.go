package password

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHashCompareRoundTrip(t *testing.T) {
	h, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(h, "$2") {
		t.Fatalf("hash prefix: want $2*, got %q", h[:4])
	}
	if err := Compare(h, "correct horse battery staple"); err != nil {
		t.Fatalf("Compare same: %v", err)
	}
}

func TestCompareWrongPassword(t *testing.T) {
	h, _ := Hash("correct horse battery staple")
	if err := Compare(h, "wrong password nope"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials, got %v", err)
	}
}

// TestCompareEmptyHashBurnsTime asserts that Compare against an empty
// hash still spends real CPU (i.e. we did NOT short-circuit into a
// cheap string compare). The bar is generous — we only need to prove
// the dummy bcrypt path actually ran.
func TestCompareEmptyHashBurnsTime(t *testing.T) {
	start := time.Now()
	err := Compare("", "anything")
	elapsed := time.Since(start)
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials, got %v", err)
	}
	// cost=12 on modern CPUs is >30ms. Anything <5ms means we
	// short-circuited.
	if elapsed < 5*time.Millisecond {
		t.Fatalf("Compare(\"\") too fast: %s — did dummy bcrypt run?", elapsed)
	}
}

func TestValidateRejectsWeak(t *testing.T) {
	cases := []string{
		"",
		"password",
		"12345678",
		"qwertyui",
		"aaaaaaaaaaaa",
	}
	for _, in := range cases {
		if err := Validate(in); err == nil {
			t.Fatalf("Validate(%q): want error, got nil", in)
		}
	}
}

func TestValidateAcceptsStrong(t *testing.T) {
	cases := []string{
		"correct horse battery staple",
		"Tr0ub4dor&3-really-long-passphrase",
		"MyP@ssw0rd!ButLonger42",
	}
	for _, in := range cases {
		if err := Validate(in); err != nil {
			t.Fatalf("Validate(%q): unexpected err %v", in, err)
		}
	}
}

func TestValidateRejectsOver72Bytes(t *testing.T) {
	long := strings.Repeat("a", 73)
	if err := Validate(long); err == nil {
		t.Fatal("Validate(73 bytes): want error")
	}
}

func TestHashRejectsOver72Bytes(t *testing.T) {
	long := strings.Repeat("a", 73)
	if _, err := Hash(long); err == nil {
		t.Fatal("Hash(73 bytes): want error")
	}
}
