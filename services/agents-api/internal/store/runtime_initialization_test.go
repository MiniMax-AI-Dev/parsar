package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type initializingProvider struct {
	lifecycleProvider
	writes int
	fail   bool
	check  func()
}

func (p *initializingProvider) RunCommand(_ context.Context, _ sandbox.Reference, c sandbox.Command) (sandbox.CommandResult, error) {
	p.writes++
	if p.check != nil {
		p.check()
	}
	if p.fail {
		return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
	}
	if c.Args[len(c.Args)-1] == "/usr/local/bin/agents-api-runtime-initialize" {
		var operation struct {
			Version int    `json:"version"`
			Action  string `json:"action"`
		}
		if json.Unmarshal(c.Stdin, &operation) != nil || operation.Version != 1 || operation.Action == "" {
			return sandbox.CommandResult{}, sandbox.ErrInvalid
		}
		return sandbox.CommandResult{Stdout: `{"version":1,"outcome":"completed"}`}, nil
	}
	size, err := strconv.Atoi(c.Args[len(c.Args)-1])
	if err != nil || len(c.Stdin) != size+32 {
		return sandbox.CommandResult{}, sandbox.ErrInvalid
	}
	digest := sha256.Sum256(c.Stdin[:size])
	if !bytes.Equal(digest[:], c.Stdin[size:]) {
		return sandbox.CommandResult{}, sandbox.ErrInvalid
	}
	return sandbox.CommandResult{Stdout: fmt.Sprintf(`{"version":1,"outcome":"completed","size_bytes":%d}`, size)}, nil
}

func TestManagedInitialFilesGateFairnessCompletionAndRestart(t *testing.T) {
	for _, mode := range []string{"complete", "restart", "uncertain", "setup-complete", "setup-restart", "setup-uncertain"} {
		t.Run(mode, func(t *testing.T) {
			setupOnly := strings.HasPrefix(mode, "setup-")
			mode = strings.TrimPrefix(mode, "setup-")
			expectedSteps := 2
			_, pool := store.NewTestStore(t)
			cipher, err := credentialcrypto.New(bytes.Repeat([]byte{9}, 32))
			if err != nil {
				t.Fatal(err)
			}
			s := store.NewWithCredentialCipher(pool, cipher)
			tenant := uuid.NewString()
			input := store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), InitialFiles: []store.InitialFile{{Type: "inline", Path: "/workspace/a", Data: []byte("first")}, {Type: "inline", Path: "/workspace/b", Data: []byte("second")}}}
			if setupOnly {
				input.InitialFiles = nil
				input.Initialization = store.EnvironmentSetup{Env: map[string]string{"VALUE": "private"}, Packages: v1.EnvironmentPackages{NPM: []string{"is-number@7.0.0"}}, Commands: []store.SetupCommand{{Command: "touch first"}, {Command: "test -f first"}}}
				expectedSteps = 4
			}
			session, err := s.CreateSession(t.Context(), tenant, input)
			if err != nil {
				t.Fatal(err)
			}
			env, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			p := &initializingProvider{lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}}, fail: mode == "uncertain"}
			key := uuid.NewString()
			w, stop := managedWorker(t, s, key, p)
			owner, err := w.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
			if err != nil || owner.Initialization != "pending" {
				t.Fatal("initialization ownership", owner, err)
			}
			credential, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID)
			if err != nil || !ok || credential.ID != owner.DeviceID {
				t.Fatal("pending initialization blocks daemon authentication")
			}
			p.check = func() {
				if _, err := s.GetSessionDevice(t.Context(), tenant, session.ID); !errors.Is(err, store.ErrNotFound) {
					t.Fatal("pending file access", err)
				}
				if _, err := s.GetSessionExecutionBinding(t.Context(), tenant, session.ID); !errors.Is(err, store.ErrNotFound) {
					t.Fatal("premature native preparation", err)
				}
			}
			// Another allocation is observed between file steps, rather than after the full batch.
			otherTenant, _, otherEnv := managedSession(t, s)
			if _, err := w.ProvisionEnvironment(t.Context(), otherTenant, otherEnv.ID, key); err != nil {
				t.Fatal(err)
			}
			for n := 0; p.writes == 0 && n < 100; n++ {
				if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if p.writes != 1 {
				t.Fatal("initialization did not perform one bounded file step", p.writes)
			}
			afterFirst := p.gets
			if mode == "restart" {
				stop()
				w, _ = managedWorker(t, s, key, p)
			}
			if mode == "complete" {
				for n := 0; p.writes < expectedSteps && n < 100; n++ {
					if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				got, err := s.GetRuntimeAllocation(t.Context(), tenant, env.ID)
				if err != nil || got.Initialization != "complete" || p.writes != expectedSteps || p.gets <= afterFirst {
					t.Fatal("completion or maintenance", got, err, p.writes)
				}
				if _, err := s.GetSessionExecutionBinding(t.Context(), tenant, session.ID); err != nil {
					t.Fatal("ready execution still blocked", err)
				}
				stop()
				w, _ = managedWorker(t, s, key, p)
				for range 4 {
					if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				if p.writes != expectedSteps {
					t.Fatal("completed initialization replayed")
				}
			} else {
				reconcileManagedState(t, w, s, tenant, env.ID, "released")
				if p.writes != 1 || p.kills != 1 {
					t.Fatal("uncertain initialization replayed or released twice", p.writes, p.kills)
				}
				failed, err := s.GetEnvironment(t.Context(), tenant, env.ID)
				if err != nil || failed.Status != "failed" {
					t.Fatal("failed initialization exposed", failed, err)
				}
			}
		})
	}
}
