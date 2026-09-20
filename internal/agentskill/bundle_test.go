package agentskill

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"testing"
)

func TestBundle(t *testing.T) {
	metadata := Metadata{Type: "inline", Name: "proof-skill", Description: "Run the proof."}
	manifest := []byte("---\nname: proof-skill\ndescription: Run the proof.\n---\nRun scripts/check.py.\n")
	makeArchive := func(paths []string, bodies [][]byte, mode fs.FileMode) []byte {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		for i, p := range paths {
			h := &zip.FileHeader{Name: p, Method: zip.Deflate}
			h.SetMode(mode)
			f, err := w.CreateHeader(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.Write(bodies[i]); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	data := makeArchive([]string{"folder/SKILL.md", "folder/scripts/check.py"}, [][]byte{manifest, {0, 1, 2}}, 0755)
	files, err := Read(data, metadata)
	if err != nil || len(files) != 2 || !bytes.Equal(files[1].Data, []byte{0, 1, 2}) || !files[1].Executable {
		t.Fatalf("files=%+v err=%v", files, err)
	}
	for _, tc := range []struct {
		name   string
		paths  []string
		bodies [][]byte
		mode   fs.FileMode
	}{
		{"escape", []string{"folder/SKILL.md", "folder/../../private"}, [][]byte{manifest, {}}, 0600},
		{"duplicate", []string{"folder/SKILL.md", "folder/SKILL.md"}, [][]byte{manifest, manifest}, 0600},
		{"multiple roots", []string{"folder/SKILL.md", "other/file"}, [][]byte{manifest, {}}, 0600},
		{"symlink", []string{"folder/SKILL.md"}, [][]byte{manifest}, fs.ModeSymlink | 0600},
		{"file parent", []string{"folder/SKILL.md", "folder/a", "folder/a/b"}, [][]byte{manifest, {}, {}}, 0600},
		{"expanded limit", []string{"folder/SKILL.md", "folder/large"}, [][]byte{manifest, bytes.Repeat([]byte{0}, MaxExpandedBytes)}, 0600},
		{"missing manifest", []string{"folder/file"}, [][]byte{{}}, 0600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Read(makeArchive(tc.paths, tc.bodies, tc.mode), metadata); err == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
	if _, err := Read(make([]byte, MaxArchiveBytes+1), metadata); err == nil {
		t.Fatal("archive byte limit ignored")
	}
	for _, front := range []string{
		"name: other\ndescription: Run the proof.",
		"name: proof-skill\nname: proof-skill\ndescription: Run the proof.",
		"name: proof-skill\ndescription: Run the proof.\nhooks: {}",
		"name: proof-skill\ndescription: Run the proof.\ncontext: fork",
		"name: proof-skill\ndescription: Run the proof.\nallowed-tools: Bash",
	} {
		if ValidateManifest([]byte("---\n"+front+"\n---\nText"), metadata) == nil {
			t.Fatal("unsupported manifest accepted")
		}
	}
}
