package deepseekharness

import (
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestServerIdleTimeoutDefaultsToTheDaemonContract(t *testing.T) {
	t.Setenv("PARSAR_DSH_SERVER_IDLE", "")
	if got := serverIdleTimeout(); got != time.Hour {
		t.Fatalf("idle timeout = %s, want 1h", got)
	}
	t.Setenv("PARSAR_DSH_SERVER_IDLE", "17m")
	if got := serverIdleTimeout(); got != 17*time.Minute {
		t.Fatalf("override = %s", got)
	}
	t.Setenv("PARSAR_DSH_SERVER_IDLE", "invalid")
	if got := serverIdleTimeout(); got != time.Hour {
		t.Fatalf("invalid fallback = %s", got)
	}
}

func sampleLaunch() serverLaunch {
	return serverLaunch{
		Home:     "/state/home",
		WorkDir:  "/work",
		Binary:   "dsh",
		StateKey: "conv:agent:engine",
		Provider: providerConfig{
			BaseURL:   "https://api.example/v1",
			API:       "openai-completions",
			APIKeyEnv: "PARSAR_DSH_API_KEY",
			Model:     "deepseek/deepseek-v4-flash",
		},
		HasProvider: true,
		Env:         []string{"PARSAR_DSH_API_KEY=secret-1", "DSH_HOME=/state/home"},
	}
}

func TestServerKeyIsStableForIdenticalLaunches(t *testing.T) {
	a := sampleLaunch().key()
	b := sampleLaunch().key()
	if a != b {
		t.Fatalf("identical launches produced different keys: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "conv:agent:engine:") {
		t.Errorf("key should be readable and start with the state key, got %q", a)
	}
}

func TestServerKeyChangesWhenTheBakedRouteChanges(t *testing.T) {
	base := sampleLaunch().key()

	cases := map[string]func(*serverLaunch){
		// Each of these is read once, at boot, into the generated profile.
		// Reusing a server across such a change would silently run the turn
		// on the old route.
		"model":     func(l *serverLaunch) { l.Provider.Model = "deepseek/other" },
		"base url":  func(l *serverLaunch) { l.Provider.BaseURL = "https://elsewhere/v1" },
		"api shape": func(l *serverLaunch) { l.Provider.API = "anthropic-messages" },
		"key env":   func(l *serverLaunch) { l.Provider.APIKeyEnv = "OTHER_KEY" },
		"headers":   func(l *serverLaunch) { l.Provider.Headers = map[string]string{"x-tenant": "b"} },
		// A rotated credential has to restart the server: the running one
		// captured the old value in its environment.
		"rotated key": func(l *serverLaunch) { l.Env = []string{"PARSAR_DSH_API_KEY=secret-2", "DSH_HOME=/state/home"} },
		"home":        func(l *serverLaunch) { l.Home = "/state/other" },
		"workdir":     func(l *serverLaunch) { l.WorkDir = "/other" },
		"binary":      func(l *serverLaunch) { l.Binary = "/opt/dsh" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			launch := sampleLaunch()
			mutate(&launch)
			if got := launch.key(); got == base {
				t.Errorf("changing the %s did not change the reuse key", name)
			}
		})
	}
}

func TestServerKeyIsIndependentOfEnvOrdering(t *testing.T) {
	a := sampleLaunch()
	b := sampleLaunch()
	b.Env = []string{"DSH_HOME=/state/home", "PARSAR_DSH_API_KEY=secret-1"}
	if a.key() != b.key() {
		t.Error("env ordering must not fork the reuse key")
	}
}

func TestServerKeyNeverLeaksTheCredential(t *testing.T) {
	key := sampleLaunch().key()
	if strings.Contains(key, "secret-1") {
		t.Fatalf("the reuse key exposed the API key: %q", key)
	}
}

func TestServerSpecPointsAtTheGeneratedProfile(t *testing.T) {
	spec := sampleLaunch().spec()
	args := spec.Args(51234)
	if len(args) != 2 || args[0] != "--profile" || args[1] != serverProfileName {
		t.Fatalf("args = %v, want the generated profile", args)
	}
	// dsh has no port flag: the port reaches it through the profile that
	// Prepare writes, so the argv must not carry one.
	for _, arg := range args {
		if strings.Contains(arg, "51234") {
			t.Errorf("argv carries the port, but dsh takes it from config: %v", args)
		}
	}
	if spec.Dir != "/work" {
		t.Errorf("spec dir = %q", spec.Dir)
	}
	if spec.StateKey != sampleLaunch().StateKey {
		t.Errorf("spec state key = %q, want %q", spec.StateKey, sampleLaunch().StateKey)
	}
	if spec.Ready == nil || spec.Prepare == nil || spec.Env == nil {
		t.Error("spec must supply a readiness probe, a prepare step and an environment")
	}
}

func TestSpecPrepareWritesABootableProfile(t *testing.T) {
	home := t.TempDir()
	launch := sampleLaunch()
	launch.Home = home
	spec := launch.spec()

	if err := spec.Prepare(t.Context(), 41000); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	body, err := renderServerPatch(serverProfileSpec{
		Home: home, Port: 41000, Provider: launch.Provider, HasProvider: true,
	})
	if err != nil {
		t.Fatalf("renderServerPatch: %v", err)
	}
	inserts, _ := rowsFromPatch(t, body)
	if got := inserts["@deepseek-ai/dsh-host-webserver"]["port"]; got != 41000 {
		t.Errorf("Prepare did not bake the assigned port: %v", got)
	}
}

func TestBuildServerLaunchPinsStateAndTelemetryEnv(t *testing.T) {
	req := proto.PromptRequestPayload{
		RunID:          "run-1",
		ConversationID: "conv-1",
		AgentStateKey:  "conv-1/agent-1/deepseek_harness",
		Prompt:         "hi",
		AgentOptions: map[string]any{
			// An adapter must not let agent_options redirect the state root,
			// widen the file-effect boundary, or re-enable telemetry.
			"env": map[string]any{
				dshHomeEnvVar:              "/tmp/attacker",
				dshPermissionModeEnvVar:    "danger-full-access",
				dshTelemetryDisabledEnvVar: "",
				"HARMLESS":                 "ok",
			},
		},
	}
	launch, err := buildServerLaunch(req)
	if err != nil {
		t.Fatalf("buildServerLaunch: %v", err)
	}

	found := map[string]string{}
	for _, entry := range launch.Env {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			found[k] = v
		}
	}
	if found[dshHomeEnvVar] == "/tmp/attacker" {
		t.Error("agent_options was able to redirect DSH_HOME")
	}
	if found[dshHomeEnvVar] != launch.Home {
		t.Errorf("DSH_HOME = %q, want the resolved home %q", found[dshHomeEnvVar], launch.Home)
	}
	if found[dshPermissionModeEnvVar] != sandboxPermissionMode {
		t.Errorf("permission mode = %q, want %q", found[dshPermissionModeEnvVar], sandboxPermissionMode)
	}
	if found[dshTelemetryDisabledEnvVar] != "1" {
		t.Errorf("telemetry opt-out = %q, want 1", found[dshTelemetryDisabledEnvVar])
	}
	if found["HARMLESS"] != "ok" {
		t.Error("an unrelated env entry was dropped")
	}
	if launch.StateKey != "conv-1/agent-1/deepseek_harness" {
		t.Errorf("state key = %q", launch.StateKey)
	}
}

func TestBuildServerLaunchRejectsAnIncompleteManagedRoute(t *testing.T) {
	req := proto.PromptRequestPayload{
		RunID:  "run-1",
		Prompt: "hi",
		AgentOptions: map[string]any{
			// dsh refuses the whole profile at boot when a non-shipped route
			// is missing a field, so this has to fail here with a readable
			// message rather than as an engine boot timeout.
			"dsh_provider": map[string]any{"base_url": "https://x/v1", "api": "openai-completions"},
		},
	}
	if _, err := buildServerLaunch(req); err == nil {
		t.Fatal("an incomplete dsh_provider must be rejected before launch")
	}
}

func TestBuildServerLaunchFallsBackToConversationIDForTheKey(t *testing.T) {
	req := proto.PromptRequestPayload{RunID: "run-1", ConversationID: "conv-9", Prompt: "hi"}
	launch, err := buildServerLaunch(req)
	if err != nil {
		t.Fatalf("buildServerLaunch: %v", err)
	}
	if launch.StateKey != "conv-9" {
		t.Errorf("state key = %q, want the conversation id fallback", launch.StateKey)
	}
}
