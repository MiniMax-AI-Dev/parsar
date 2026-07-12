package cli

import (
	"bytes"
	"strings"
	"testing"
)

func runArgv(t *testing.T, argv ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	ctx := &runContext{stdout: &out, stderr: &errBuf}
	err = execute(ctx, argv)
	return out.String(), errBuf.String(), err
}

func TestExecuteNoArgsPrintsHelpAndReturnsError(t *testing.T) {
	stdout, _, err := runArgv(t)
	if err == nil {
		t.Fatal("expected error when called with no subcommand")
	}
	if !strings.Contains(stdout, "Subcommands:") {
		t.Errorf("help output missing subcommand list:\n%s", stdout)
	}
}

func TestExecuteHelpFlagSucceeds(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		stdout, _, err := runArgv(t, arg)
		if err != nil {
			t.Errorf("%s returned error: %v", arg, err)
		}
		if !strings.Contains(stdout, "parsar-daemon") {
			t.Errorf("%s output missing parsar-daemon banner:\n%s", arg, stdout)
		}
	}
}

func TestExecuteUnknownSubcommand(t *testing.T) {
	_, _, err := runArgv(t, "definitely-not-a-command")
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error %q", err.Error())
	}
}

func TestVersionSubcommandPrintsVersion(t *testing.T) {
	stdout, _, err := runArgv(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(stdout, Version) {
		t.Errorf("version output = %q, missing %q", stdout, Version)
	}
}

func TestSubcommandsAreRegistered(t *testing.T) {
	// Guards against dropping a subcommand off the commands slice —
	// the public CLI surface is the shipped contract.
	want := map[string]bool{
		"connect":         false,
		"managed-connect": false,
		"install-tool":    false,
		"status":          false,
		"stop":            false,
		"logs":            false,
		"logout":          false,
		"version":         false,
	}
	for _, c := range commands {
		if _, ok := want[c.name]; !ok {
			t.Errorf("unexpected subcommand registered: %q", c.name)
			continue
		}
		want[c.name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("expected subcommand %q to be registered", name)
		}
	}
}
