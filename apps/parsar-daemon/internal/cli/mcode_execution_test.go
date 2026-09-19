package cli

import (
	"context"
	"io"
	"testing"
)

func TestMCodeExecutionOptInIsVersionBound(t *testing.T) {
	for _, tc := range []struct {
		enabled, version string
		qualified        bool
	}{{"", "0.4.12", false}, {"1", "0.3.11", false}, {"1", "0.4.12", true}} {
		t.Run(tc.enabled+"/"+tc.version, func(t *testing.T) {
			t.Setenv("PARSAR_MCODE_AGENTS_API", tc.enabled)
			rc := &runContext{stdout: io.Discard, stderr: io.Discard}
			info := discoverMCode(rc, func(context.Context, string) (string, error) { return tc.version, nil })
			if !info.Available || info.Capabilities.EnvironmentNone != tc.qualified || info.Capabilities.DurableInputReceipts != tc.qualified {
				t.Fatalf("capabilities=%+v", info.Capabilities)
			}
			if info.Capabilities.NativeSessionRecovery || info.Capabilities.LocalEnvironment || info.Capabilities.FunctionTools {
				t.Fatal("unqualified capability advertised")
			}
		})
	}
}
