package cli

import (
	"bytes"
	"strings"
	"testing"
)

// newCapturedCtx returns a runContext with buffer-backed streams.
// cfg=nil exercises the "config error" paths; a non-nil cfg stubs the
// loader to skip env reads.
func newCapturedCtx(cfg *Config) (*runContext, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ctx := &runContext{stdout: stdout, stderr: stderr}
	if cfg != nil {
		c := *cfg
		ctx.configLoader = func() (Config, error) { return c, nil }
	}
	return ctx, stdout, stderr
}

func TestExecuteVersion(t *testing.T) {
	ctx, stdout, _ := newCapturedCtx(nil)
	if err := execute(ctx, []string{"version"}); err != nil {
		t.Fatalf("version: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != Version {
		t.Errorf("version output = %q, want %q", stdout.String(), Version)
	}
}

func TestExecuteVersionFlag(t *testing.T) {
	ctx, stdout, _ := newCapturedCtx(nil)
	if err := execute(ctx, []string{"--version"}); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != Version {
		t.Errorf("--version output = %q, want %q", stdout.String(), Version)
	}
}

func TestExecuteHelp(t *testing.T) {
	ctx, stdout, _ := newCapturedCtx(nil)
	if err := execute(ctx, []string{"--help"}); err != nil {
		t.Fatalf("--help: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"spec", "memory", "inject", "sync", "version", "PARSAR_SERVER_URL"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q in:\n%s", want, out)
		}
	}
}

func TestExecuteMissingSubcommand(t *testing.T) {
	ctx, _, _ := newCapturedCtx(nil)
	err := execute(ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "missing subcommand") {
		t.Fatalf("expected missing-subcommand, got %v", err)
	}
}

func TestExecuteUnknownSubcommand(t *testing.T) {
	ctx, _, _ := newCapturedCtx(nil)
	err := execute(ctx, []string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("expected unknown-subcommand, got %v", err)
	}
}
