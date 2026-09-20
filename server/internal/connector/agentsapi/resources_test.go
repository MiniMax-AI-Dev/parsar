package agentsapi

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestResourceProjectionPreservesArchiveAndRejectsUnsupportedCombinations(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	manifest, _ := writer.Create("proof/SKILL.md")
	_, _ = manifest.Write([]byte("---\nname: proof\ndescription: A proof Skill\n---\nRead data.bin.\n"))
	asset, _ := writer.Create("proof/data.bin")
	_, _ = asset.Write([]byte{0, 255, 1})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(archive.Bytes())
	blobs := blob.NewMemoryStore("http://unused")
	if err := blobs.PutBytes(t.Context(), "archive", "source", archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	spec := []byte(`{"schema_version":1,"kind":"skill","skill":{"slug":"proof","title":"Proof","description":"A proof Skill","instruction":"Read data.bin."}}`)
	resource := store.EnabledCapabilityRead{Type: "skill", Name: "Proof", WorkspaceID: "source", CanonicalSpec: spec, OssKey: "archive", SHA256: hex.EncodeToString(hash[:])}
	connector := &Connector{Blobs: blobs}
	config := map[string]any{"instructions": "Original"}
	environment := map[string]any{"type": "openai_hosted"}
	if err := connector.projectResources(t.Context(), []store.EnabledCapabilityRead{resource}, config, environment); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(environment)
	if !bytes.Contains(raw, []byte(`"media_type":"application/zip"`)) || config["instructions"] != "Original" {
		t.Fatal("invalid Skill projection")
	}
	latest := resource
	latest.PinningMode = "latest"
	latest.LatestCanonicalSpec, latest.LatestOssKey, latest.LatestSHA256 = resource.CanonicalSpec, resource.OssKey, resource.SHA256
	latest.LatestRequiredCredentials = []store.RequiredCredential{{Kind: "github_pat", Required: true}}
	if connector.projectResources(t.Context(), []store.EnabledCapabilityRead{latest}, config, map[string]any{"type": "openai_hosted"}) == nil {
		t.Fatal("latest credential requirement was ignored")
	}
	latest.RequiredCredentials, latest.LatestRequiredCredentials = latest.LatestRequiredCredentials, nil
	if err := connector.projectResources(t.Context(), []store.EnabledCapabilityRead{latest}, config, map[string]any{"type": "openai_hosted"}); err != nil {
		t.Fatalf("old credential requirement blocked latest: %v", err)
	}
	for _, change := range []string{"template", "foreign", "checksum", "duplicate"} {
		t.Run(change, func(t *testing.T) {
			copy := resource
			env := map[string]any{"type": "openai_hosted"}
			resources := []store.EnabledCapabilityRead{copy}
			switch change {
			case "template":
				env["environment_template_id"] = "template"
			case "foreign":
				resources[0].WorkspaceID = "foreign"
			case "checksum":
				resources[0].SHA256 = "bad"
			case "duplicate":
				resources = append(resources, copy)
			}
			if connector.projectResources(t.Context(), resources, config, env) == nil {
				t.Fatal("unsupported or invalid combination accepted")
			}
		})
	}
}

func TestKnowledgeSurvivesSystemPromptOverride(t *testing.T) {
	resources := []store.EnabledCapabilityRead{
		{Type: "system_prompt", CanonicalSpec: []byte(`{"schema_version":1,"kind":"system_prompt","system_prompt":{"mode":"override","prompt":"Replacement"}}`)},
		{Type: "knowledge", Name: "Reference", CanonicalSpec: []byte(`{"schema_version":1,"kind":"knowledge","knowledge":{"documents":[{"name":"Policy","content":"Reference content"}]}}`)},
	}
	config := map[string]any{"instructions": "Original"}
	if err := (&Connector{}).projectResources(t.Context(), resources, config, map[string]any{"type": "openai_hosted"}); err != nil {
		t.Fatal(err)
	}
	body := []byte(config["instructions"].(string))
	if bytes.Contains(body, []byte("Original")) || !bytes.Contains(body, []byte("Replacement")) || !bytes.Contains(body, []byte("Reference content")) {
		t.Fatal("instruction composition failed")
	}
}

func TestBuildRootlessSkillAdaptationPreservesBinaryAndMode(t *testing.T) {
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	file, _ := writer.Create("SKILL.md")
	_, _ = file.Write([]byte("---\nname: proof\nslug: proof\ntitle: Product title\ndescription: A proof Skill\n---\nUse the script.\n"))
	header := zip.FileHeader{Name: "scripts/check.bin"}
	header.SetMode(0755)
	file, _ = writer.CreateHeader(&header)
	_, _ = file.Write([]byte{0, 255, 1})
	_ = writer.Close()
	metadata := agentskill.Metadata{Type: "inline", Name: "proof", Description: "A proof Skill"}
	adapted, err := protocolSkillArchive(body.Bytes(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	files, err := agentskill.Read(adapted, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || !bytes.Equal(files[1].Data, []byte{0, 255, 1}) || !files[1].Executable {
		t.Fatal("supporting file changed")
	}
	if _, err := protocolSkillManifest([]byte("---\nname: proof\ndescription: A proof Skill\ntrigger: always\n---\nBody"), metadata); err == nil {
		t.Fatal("activation control silently dropped")
	}
}
