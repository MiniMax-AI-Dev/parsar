package coreaccess

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/v3"
)

func TestWorkspaceCredentialsCannotFallBackOrShareProjects(t *testing.T) {
	const first = "00000000-0000-0000-0000-000000000001"
	const second = "00000000-0000-0000-0000-000000000002"
	var received []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = append(received, r.Header.Get("Authorization")+":"+r.Header.Get("OpenAI-Project"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"has_more":false}`))
	}))
	defer upstream.Close()
	bindings := []Binding{{WorkspaceID: first, ProjectID: "project-one", BaseURL: upstream.URL + "/v1", APIKeyFile: "key-one"}, {WorkspaceID: second, ProjectID: "project-two", BaseURL: upstream.URL + "/v1", APIKeyFile: "key-two"}}
	read := func(path string) ([]byte, error) { return []byte(path), nil }
	resolve, err := Build(bindings, read)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first, second} {
		client, err := resolve(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = client.Environments.Templates.List(context.Background(), openai.BetaAgentEnvironmentTemplateListParams{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(received) != 2 || received[0] != "Bearer key-one:project-one" || received[1] != "Bearer key-two:project-two" {
		t.Fatalf("workspace credentials crossed: %v", received)
	}
	if _, err := resolve("unconfigured"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("global fallback: %v", err)
	}
	bindings[1].ProjectID = "project-one"
	if _, err := Build(bindings, read); err == nil {
		t.Fatal("shared project accepted")
	}
	bindings[1].ProjectID = "project-two"
	bindings[1].APIKeyFile = "key-one"
	if _, err := Build(bindings, read); err == nil {
		t.Fatal("shared caller key accepted")
	}
}
