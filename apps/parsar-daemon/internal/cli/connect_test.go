package cli

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestScrubInlineConnectArgsRemovesTokenURLAndDeviceName(t *testing.T) {
	got := scrubInlineConnectArgs([]string{
		"parsar-daemon", "connect",
		"--url", "https://parsar.example.com",
		"--token=rtk_secret",
		"--device-name", "dev-1",
		"-b",
		"--profile", "sandbox",
	})
	want := []string{"parsar-daemon", "connect", "-b", "--profile", "sandbox"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scrubInlineConnectArgs() = %#v, want %#v", got, want)
	}
}

func TestLoadInlineConnectEnvFillsMissingValuesAndUnsets(t *testing.T) {
	t.Setenv(connectInlineURLEnv, "https://parsar.example.com")
	t.Setenv(connectInlineTokenEnv, "rtk_secret")
	t.Setenv(connectInlineDeviceNameEnv, "dev-1")

	serverURL, token, deviceName := "", "", ""
	loadInlineConnectEnv(&serverURL, &token, &deviceName)

	if serverURL != "https://parsar.example.com" || token != "rtk_secret" || deviceName != "dev-1" {
		t.Fatalf("loaded values = (%q, %q, %q)", serverURL, token, deviceName)
	}
	if got := inlineConnectEnvValue(connectInlineTokenEnv); got != "" {
		t.Fatalf("%s still set to %q", connectInlineTokenEnv, got)
	}
}

func inlineConnectEnvValue(key string) string { return os.Getenv(key) }

// Regression: pre-fork auth.json check used to run BEFORE env-to-flag
// hydration, so sandboxes passing the token via env bailed with
// "not paired". loadInlineConnectEnv now runs first.
func TestLoadInlineConnectEnvHydratesParentProcessFlags(t *testing.T) {
	t.Setenv(connectInlineURLEnv, "https://parsar.example.com")
	t.Setenv(connectInlineTokenEnv, "rtk_secret")

	serverURL, token, deviceName := "", "", ""

	loadInlineConnectEnv(&serverURL, &token, &deviceName)

	// Same predicate runConnect uses to decide whether to skip the
	// pre-fork auth.json check.
	inlinePair := strings.TrimSpace(serverURL) != "" || strings.TrimSpace(token) != ""
	if !inlinePair {
		t.Fatalf("inlinePair=false after env hydration; serverURL=%q token=%q", serverURL, token)
	}
}
