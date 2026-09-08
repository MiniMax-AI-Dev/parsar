package dev

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/parser"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestMarkdownSkillArchiveRoundTrip(t *testing.T) {
	spec := canonical.Spec{SchemaVersion: 1, Kind: canonical.KindSkill, Skill: &canonical.SkillSpec{
		Slug: "onboarding", Title: "Employee onboarding", Description: "Policy: laptop pickup\nFor new employees",
		Instruction: "Collect the laptop on working day 3.", Trigger: "onboarding questions",
	}}
	blobs := blob.NewMemoryStore("https://api.test")
	ref, digest, httpErr := ensureSkillImportArchive(t.Context(), "workspace", spec, "", "", blobs)
	if httpErr != nil {
		t.Fatalf("package failed: %s", httpErr.message)
	}
	assertMarkdownArchive(t, blobs, "workspace", ref, digest, spec)

	for _, test := range []struct {
		name   string
		spec   canonical.Spec
		blobs  blob.Store
		status int
	}{
		{"unavailable storage", spec, nil, http.StatusServiceUnavailable},
		{"missing instruction", canonical.Spec{SchemaVersion: 1, Kind: canonical.KindSkill, Skill: &canonical.SkillSpec{Slug: "empty"}}, blobs, http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			ref, digest, err := ensureSkillImportArchive(t.Context(), "workspace", test.spec, "", "", test.blobs)
			if err == nil || err.status != test.status || ref != "" || digest != "" {
				t.Fatalf("failed import returned archive: ref=%q digest=%q error=%v", ref, digest, err)
			}
		})
	}
}

func TestCapabilityImportMarkdownStoresEachVersionArchive(t *testing.T) {
	r, _, blobs := skillsInstallTestRouter(t, &fakeSkillInstallRunner{t: t})
	ids := store.DefaultDevFixtureIDs()
	base := "/api/v1/workspaces/" + ids.WorkspaceID + "/capabilities"
	preview := serveCapabilityRoute(t, r, http.MethodPost, base+"/import/preview", mustJSON(t, map[string]any{
		"kind": "skill", "source_format": "markdown", "raw_text": goodSkillMd,
	}), ids.UserID)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	var parsed previewCapabilityImportResponse
	if err := json.Unmarshal(preview.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	endpoint := base + "/import/commit"
	var firstRef string
	for _, version := range []string{"1.0.0", "1.0.1"} {
		parsed.CanonicalSpec.Skill.Instruction = "Read policy version " + version
		res := serveCapabilityRoute(t, r, http.MethodPost, endpoint, mustJSON(t, map[string]any{
			"kind": "skill", "name": "Markdown policy", "version": version, "canonical_spec": parsed.CanonicalSpec,
		}), ids.UserID)
		if res.Code != http.StatusCreated {
			t.Fatalf("commit %s: %d %s", version, res.Code, res.Body.String())
		}
		var result commitCapabilityImportResponse
		if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		v := result.CapabilityVersion
		assertMarkdownArchive(t, blobs, ids.WorkspaceID, v.OssKey, v.SHA256, parsed.CanonicalSpec)
		if v.OssKey == firstRef {
			t.Fatal("new version reused the old Markdown archive")
		}
		firstRef = v.OssKey
		endpoint = base + "/" + result.Capability.ID + "/versions/import/commit"
	}
}

func assertMarkdownArchive(t *testing.T, blobs blob.Store, workspaceID, ref, digest string, want canonical.Spec) {
	t.Helper()
	ctx := context.Background()
	owned, err := blobs.BelongsToWorkspace(ctx, ref, workspaceID)
	if err != nil || !owned {
		t.Fatalf("archive ownership: %v %v", owned, err)
	}
	data, err := blobs.Download(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatal("archive checksum does not match stored metadata")
	}
	parsed, err := parser.ParseSkillZip(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Spec.Skill.Files) != 0 {
		t.Fatal("Markdown archive unexpectedly contains additional files")
	}
	parsed.Spec.Skill.Files = nil
	if !reflect.DeepEqual(parsed.Spec.Skill, want.Skill) {
		t.Fatalf("archive content differs from committed Skill: %#v, %v", parsed.Spec.Skill, err)
	}
}
