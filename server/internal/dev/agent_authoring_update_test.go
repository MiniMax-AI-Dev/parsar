package dev

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/parser"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestAgentAuthoringUpdateProtectsPublicAndArchivedContent(t *testing.T) {
	const markdown = "---\nname: guide\ndescription: Workspace guide\n---\nOriginal instructions.\n"
	for _, tc := range []struct {
		name, visibility string
		asset            bool
	}{
		{name: "public", visibility: "public"},
		{name: "asset omitted from canonical files", visibility: "workspace", asset: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := []struct{ name, content string }{{"guide/SKILL.md", markdown}}
			if tc.asset {
				entries = append(entries, struct{ name, content string }{"guide/assets/large.txt", strings.Repeat("x", (1<<20)+1)})
			}
			archive := makeSkillZip(t, entries)
			parsed, err := parser.ParseSkillZip(archive)
			if err != nil || len(parsed.Spec.Skill.Files) != 0 {
				t.Fatalf("expected valid archive with empty canonical files: %v", err)
			}
			s := newAuthoringTestStore()
			const id = "00000000-0000-0000-0000-000000000777"
			s.capability = store.CapabilityRead{ID: id, WorkspaceID: "workspace", Visibility: tc.visibility, Type: "skill", LatestVersionID: "version"}
			s.version = store.CapabilityVersionRead{CapabilityID: id, OssKey: "pg:archive", CanonicalSpec: json.RawMessage(mustJSON(t, parsed.Spec))}
			blobs := blob.NewMemoryStore("https://api.test")
			if err := blobs.PutBytes(t.Context(), s.version.OssKey, "workspace", archive); err != nil {
				t.Fatal(err)
			}
			handler := NewAgentAuthoringHandler(s, blobs)
			_, err = handler(t.Context(), uploadTestRun, proto.AuthoringRequestPayload{Operation: proto.AuthoringSkillUpdate, CapabilityID: id, Content: markdown})
			if err == nil || len(s.versions) != 0 || len(s.imports) != 0 {
				t.Fatalf("unsafe replacement was accepted: %v", err)
			}
			read, err := handler(t.Context(), uploadTestRun, proto.AuthoringRequestPayload{Operation: proto.AuthoringSkillRead, CapabilityID: id})
			if err != nil || read.(map[string]any)["markdown"] != markdown {
				t.Fatalf("existing Skill source was not readable: %v", err)
			}
		})
	}
}

func TestAgentAuthoringReadsLegacyMarkdownSource(t *testing.T) {
	s := newAuthoringTestStore()
	s.version.SourcePayload = json.RawMessage(`{"format":"markdown","body":"Original source","run_id":"private-provenance"}`)
	service := agentAuthoringService{store: s}
	markdown, err := service.readSkillMarkdown(t.Context(), "version")
	if err != nil || markdown != "Original source" {
		t.Fatalf("legacy source unavailable: %v", err)
	}
}
