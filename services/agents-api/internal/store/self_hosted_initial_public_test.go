package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSelfHostedInitialCreationOfficialClient(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, pool := store.NewTestStore(t)
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	token, peer, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "initial-creator", TokenSHA256: device.HashCredential(token), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "user", SubjectID: "different-creator", TokenSHA256: device.HashCredential(peer), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: foreignTenant, SubjectKind: "service_account", SubjectID: "initial-creator", TokenSHA256: device.HashCredential(foreign), TenantID: foreignTenant},
	})
	if err != nil {
		t.Fatal(err)
	}
	const origin = "https://offline-executor.example"
	serve := func(s *store.Store, worker *execution.Worker) *httptest.Server {
		t.Helper()
		options := []api.Option{api.WithEnvironmentRemoteURL(origin)}
		if worker != nil {
			options = append(options, api.WithExecution(worker))
		}
		handler, err := api.NewHandler(s, auth, "codex", options...)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return server
	}
	worker, stop := publicInitialWorker(t, s)
	server := serve(s, worker)
	settings := map[string]any{"base": server.URL, "token": token, "peer_token": peer, "foreign_token": foreign, "remote_url": origin}
	run := func(phase string) json.RawMessage {
		t.Helper()
		settings["phase"] = phase
		input, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, python, "../../tests/official_self_hosted_initial.py")
		command.Stdin = bytes.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("public self-hosted initial %s: %v %s", phase, err, output)
		}
		if !json.Valid(output) {
			t.Fatal("invalid public initial fixture result")
		}
		return output
	}
	accepted := run("create")
	var created struct {
		Cases []struct {
			ID            string   `json:"id"`
			EnvironmentID string   `json:"environment_id"`
			Texts         []string `json:"texts"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(accepted, &created); err != nil || len(created.Cases) != 4 {
		t.Fatal("missing public initial creation cases", err)
	}
	reservations := func(s *store.Store, pool *pgxpool.Pool) map[string]store.EnvironmentInputReservation {
		t.Helper()
		result := make(map[string]store.EnvironmentInputReservation)
		for _, item := range created.Cases {
			var id string
			if err := pool.QueryRow(t.Context(), "SELECT id FROM environment_input_reservations WHERE session_id=$1 AND is_initial", item.ID).Scan(&id); err != nil {
				t.Fatal("public creation did not reserve initial input", err)
			}
			reservation, err := s.GetEnvironmentInputReservation(t.Context(), tenant, item.ID, id)
			if err != nil || !reservation.IsInitial || len(reservation.Inputs) != 1 || len(reservation.Receipts) != 0 {
				t.Fatal("invalid public initial reservation", err)
			}
			var batch struct {
				Input []struct {
					Content []struct{ Text string } `json:"content"`
				} `json:"input"`
			}
			if err := json.Unmarshal(reservation.Inputs[0].Payload, &batch); err != nil {
				t.Fatal(err)
			}
			var texts []string
			for _, message := range batch.Input {
				var text strings.Builder
				for _, part := range message.Content {
					text.WriteString(part.Text)
				}
				texts = append(texts, text.String())
			}
			if !reflect.DeepEqual(texts, item.Texts) {
				t.Fatal("public initial text order changed")
			}
			environment, err := s.GetSessionEnvironment(t.Context(), tenant, item.ID)
			if err != nil || environment.ID != item.EnvironmentID || environment.Status != "pending" {
				t.Fatal("public initial Environment identity changed", err)
			}
			var history int
			if err := pool.QueryRow(t.Context(), `SELECT
				(SELECT count(*) FROM turns WHERE session_id=$1) +
				(SELECT count(*) FROM turn_inputs WHERE session_id=$1) +
				(SELECT count(*) FROM session_items WHERE session_id=$1)`, item.ID).Scan(&history); err != nil || history != 0 {
				t.Fatal("offline public creation manufactured Turn or input history", history, err)
			}
			result[item.ID] = reservation
		}
		return result
	}
	before := reservations(s, pool)
	for _, reservation := range before {
		if reservation.State != store.EnvironmentInputPending || reservation.Deadline.Sub(reservation.CreatedAt) != 5*time.Minute {
			t.Fatal("public initial creation did not retain its database deadline")
		}
	}
	stop(false)
	server.Close()
	pool.Close()
	reopened, reopenedPool := store.NewTestStore(t)
	worker, stop = publicInitialWorker(t, reopened)
	server = serve(reopened, worker)
	settings["base"], settings["accepted"] = server.URL, accepted
	run("reopen")
	if !reflect.DeepEqual(before, reservations(reopened, reopenedPool)) {
		t.Fatal("reopened public retry changed reservation identity or deadline")
	}
	failureID := created.Cases[0].ID
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Advance one known deadline; the running Worker still owns settlement and events.
		tag, err := reopenedPool.Exec(r.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE session_id=$1 AND is_initial AND state='pending'", failureID)
		if err != nil || tag.RowsAffected() != 1 {
			t.Error("controlled initial deadline update failed", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer control.Close()
	settings["expiry_control"] = control.URL
	run("expire")
	after := reservations(reopened, reopenedPool)
	for id, reservation := range after {
		if id == failureID {
			if reservation.ID != before[id].ID || reservation.State != store.EnvironmentInputExpired || reservation.SettledAt == nil {
				t.Fatal("Worker did not settle the original public initial reservation")
			}
		} else if !reflect.DeepEqual(reservation, before[id]) {
			t.Fatal("one initial expiry changed unrelated pending work")
		}
	}
	var pid uint32
	err = reopenedPool.QueryRow(t.Context(), `SELECT pid FROM pg_locks WHERE locktype='advisory'
		AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
		AND classid=(706172736172::bigint >> 32)::oid
		AND objid=(706172736172::bigint & 4294967295)::oid AND objsubid=1 AND granted`).Scan(&pid)
	if err != nil {
		t.Fatal(err)
	}
	var killed bool
	if err := reopenedPool.QueryRow(t.Context(), "SELECT pg_terminate_backend($1, 1000)", pid).Scan(&killed); err != nil || !killed {
		t.Fatal("could not end the fixture Worker's execution lease", err)
	}
	stop(true)
	settings["disabled_base"] = serve(reopened, nil).URL
	run("unavailable")
	if !reflect.DeepEqual(after, reservations(reopened, reopenedPool)) {
		t.Fatal("unavailable execution or recorded retry changed initial work")
	}
}

func publicInitialWorker(t *testing.T, s *store.Store) (*execution.Worker, func(bool)) {
	t.Helper()
	dispatcher := &execution.Dispatcher{Store: s, Registry: gateway.NewRegistry()}
	dispatcher.EnvironmentConnection = func(context.Context, store.Session, store.Environment) (execution.EnvironmentConnection, error) {
		t.Error("offline public creation attempted native preparation")
		return execution.EnvironmentConnection{}, errors.New("offline fixture has no native connection")
	}
	worker, err := execution.StartWorker(t.Context(), dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	var once sync.Once
	stop := func(expectFailure bool) {
		once.Do(func() {
			defer cancel()
			if !expectFailure {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil || (!expectFailure && !errors.Is(err, context.Canceled)) {
					t.Error("unexpected offline Worker completion", err)
				}
			case <-time.After(10 * time.Second):
				t.Error("offline Worker did not release execution ownership")
			}
		})
	}
	t.Cleanup(func() { stop(false) })
	return worker, stop
}
