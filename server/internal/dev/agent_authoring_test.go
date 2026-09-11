package dev

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/parser"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type authoringTestStore struct {
	skillUploadTestStore
	agent      store.AgentSummary
	updates    []store.UpdateAgentInput
	capability store.CapabilityRead
	version    store.CapabilityVersionRead
	versions   []store.ImportCapabilityVersionInput
}

func (s *authoringTestStore) GetAgent(_ context.Context, id string) (store.AgentSummary, error) {
	if id != s.agent.ID {
		return store.AgentSummary{}, store.ErrUnknownAgent
	}
	return s.agent, nil
}
func (s *authoringTestStore) UpdateAgent(_ context.Context, input store.UpdateAgentInput) (store.AgentSummary, []string, error) {
	s.updates = append(s.updates, input)
	return s.agent, nil, nil
}
func (s *authoringTestStore) GetCapability(_ context.Context, _ string) (store.CapabilityRead, error) {
	return s.capability, nil
}
func (s *authoringTestStore) GetCapabilityVersion(_ context.Context, _ string) (store.CapabilityVersionRead, error) {
	return s.version, nil
}
func (s *authoringTestStore) ImportCapabilityVersion(_ context.Context, input store.ImportCapabilityVersionInput) (store.ImportCapabilityResult, error) {
	s.versions = append(s.versions, input)
	return store.ImportCapabilityResult{Capability: s.capability, CapabilityVersion: s.version}, nil
}

func newAuthoringTestStore() *authoringTestStore {
	return &authoringTestStore{
		skillUploadTestStore: skillUploadTestStore{role: "owner", run: store.AgentRunInvocation{RunID: uploadTestRun, WorkspaceID: "workspace", AgentID: "agent", RequestedByType: "user", RequestedByID: "requester", Status: "running"}},
		agent:                store.AgentSummary{ID: "agent", WorkspaceID: "workspace", Config: map[string]any{"system_prompt": "old", "default_model_id": "model", "env": map[string]any{"secret": "do-not-return"}}},
	}
}

func TestAgentAuthoringUsesCurrentRequesterPermissions(t *testing.T) {
	for _, tc := range []struct {
		name, role, state, actor string
		roleErr                  error
		allowed                  bool
	}{
		{"owner", "owner", "running", "user", nil, true},
		{"admin", "admin", "running", "user", nil, true},
		{"member", "member", "running", "user", nil, false},
		{"viewer", "viewer", "running", "user", nil, false},
		{"removed", "", "running", "user", store.ErrNotMember, false},
		{"failed permission lookup", "owner", "running", "user", errors.New("unavailable"), false},
		{"completed", "owner", "completed", "user", nil, false},
		{"queued", "owner", "queued", "user", nil, false},
		{"system", "owner", "running", "system", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newAuthoringTestStore()
			s.role, s.roleErr, s.run.Status, s.run.RequestedByType = tc.role, tc.roleErr, tc.state, tc.actor
			handler := NewAgentAuthoringHandler(s, nil)
			ctx := auth.WithUserID(t.Context(), "spoofed")
			_, err := handler(ctx, uploadTestRun, proto.AuthoringRequestPayload{Operation: proto.AuthoringPromptWrite, Content: "new"})
			if (err == nil) != tc.allowed {
				t.Fatalf("unexpected result: %v", err)
			}
			if !tc.allowed && len(s.updates) != 0 {
				t.Fatal("unauthorized write")
			}
			if tc.allowed {
				input := s.updates[0]
				if input.AgentID != "agent" || input.ActorID != "requester" || *input.SystemPrompt != "new" || input.ConfigSet || input.DefaultModelID != nil || input.CapabilitiesSet {
					t.Fatalf("write exceeded scope: %+v", input)
				}
				if s.checkedActor != "requester" || s.checkedWorkspace != "workspace" {
					t.Fatal("supplied identity was trusted")
				}
			}
		})
	}
}

func TestAgentAuthoringSkillArchiveAndVersionScope(t *testing.T) {
	s := newAuthoringTestStore()
	blobs := blob.NewMemoryStore("https://api.test")
	handler := NewAgentAuthoringHandler(s, blobs)
	const markdown = "---\nname: generated-guide\ndescription: Workspace guide\nallowed-tools: Read\ndisable-model-invocation: true\nlicense: MIT\n---\nReturn AUTHORING-OK.\n"
	_, err := handler(t.Context(), uploadTestRun, proto.AuthoringRequestPayload{Operation: proto.AuthoringSkillCreate, Content: markdown})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.imports) != 1 {
		t.Fatal("Skill not imported")
	}
	input := s.imports[0]
	if input.WorkspaceID != "workspace" || input.CreatorID != "requester" || input.Type != "skill" || input.Visibility != "workspace" {
		t.Fatalf("wrong scope: %+v", input)
	}
	assertMarkdownArchive(t, blobs, "workspace", input.OssKey, input.SHA256, input.Spec)
	const id = "00000000-0000-0000-0000-000000000777"
	s.capability = store.CapabilityRead{ID: id, WorkspaceID: "foreign", Visibility: "workspace", Type: "skill", LatestVersionID: "version"}
	s.version = store.CapabilityVersionRead{CapabilityID: id, OssKey: input.OssKey, CanonicalSpec: json.RawMessage(mustJSON(t, input.Spec))}
	for _, op := range []string{proto.AuthoringSkillRead, proto.AuthoringSkillUpdate} {
		if _, err := handler(t.Context(), uploadTestRun, proto.AuthoringRequestPayload{Operation: op, CapabilityID: id, Content: markdown}); !errors.Is(err, store.ErrUnknownCapability) {
			t.Fatalf("cross-workspace access: %v", err)
		}
	}
	if len(s.versions) != 0 {
		t.Fatal("foreign Skill updated")
	}
	s.capability.WorkspaceID = "workspace"
	_, err = handler(t.Context(), uploadTestRun, proto.AuthoringRequestPayload{Operation: proto.AuthoringSkillUpdate, CapabilityID: id, Content: markdown})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.versions) != 1 || s.versions[0].Version != "" || s.versions[0].CreatorID != "requester" || s.versions[0].OssKey == input.OssKey || s.versions[0].ExpectedSkillVersionID != "version" {
		t.Fatal("version did not reuse automatic version/archive contract")
	}
	for _, ref := range []string{input.OssKey, s.versions[0].OssKey} {
		data, err := blobs.Download(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseSkillZip(data)
		if err != nil || parsed.EntryMarkdown != markdown {
			t.Fatalf("archive lost original Skill source: %v", err)
		}
		s.version.OssKey = ref
		read, err := handler(t.Context(), uploadTestRun, proto.AuthoringRequestPayload{Operation: proto.AuthoringSkillRead, CapabilityID: id})
		if err != nil || read.(map[string]any)["markdown"] != markdown {
			t.Fatalf("read lost original Skill source: %v", err)
		}
	}
	input.Spec.Skill.Files = []canonical.SkillFile{{Path: "references/policy.md", Content: "Preserve"}}
	s.version.CanonicalSpec = json.RawMessage(mustJSON(t, input.Spec))
	if _, err := handler(t.Context(), uploadTestRun, proto.AuthoringRequestPayload{Operation: proto.AuthoringSkillUpdate, CapabilityID: id, Content: markdown}); err == nil {
		t.Fatal("Markdown update discarded supporting files")
	}
	if len(s.versions) != 1 {
		t.Fatal("unsupported update persisted")
	}
}

func TestAgentAuthoringReadOnlyContextOmitsSecrets(t *testing.T) {
	s := newAuthoringTestStore()
	s.role = "member"
	handler := NewAgentAuthoringHandler(s, nil)
	result, err := handler(t.Context(), uploadTestRun, proto.AuthoringRequestPayload{Operation: proto.AuthoringPromptRead})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if string(data) != `{"agent_id":"agent","instructions":"old"}` {
		t.Fatalf("returned unrelated config: %s", data)
	}
	if _, err := handler(t.Context(), uploadTestRun, proto.AuthoringRequestPayload{Operation: "credentials.read"}); err == nil {
		t.Fatal("unknown operation accepted")
	}
}
