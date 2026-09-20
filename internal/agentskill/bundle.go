// Package agentskill validates inert Skill bundles without native loading rules.
package agentskill

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	MaxArchiveBytes  = 5 << 20
	MaxExpandedBytes = 20 << 20
	MaxFiles         = 1000
)

var ErrInvalid = errors.New("invalid or unsupported Skill bundle")
var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

type Metadata struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type File struct {
	Path       string `json:"path"`
	Data       []byte `json:"data"`
	Executable bool   `json:"executable,omitempty"`
}

// Read validates the full archive before exposing any files for installation.
func Read(archive []byte, expected Metadata) ([]File, error) {
	if expected.Type != "inline" || !namePattern.MatchString(expected.Name) || len(expected.Name) > 64 || expected.Description == "" || !utf8.ValidString(expected.Description) || len(archive) > MaxArchiveBytes {
		return nil, ErrInvalid
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(reader.File) == 0 || len(reader.File) > MaxFiles {
		return nil, ErrInvalid
	}
	root := ""
	seen := map[string]bool{}
	files := []File{}
	total := 0
	manifest := false
	for _, entry := range reader.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if !utf8.ValidString(name) || len(name) > 4096 || strings.ContainsAny(name, "\\\x00\r\n") || path.Clean(name) != name || path.IsAbs(name) {
			return nil, ErrInvalid
		}
		parts := strings.SplitN(name, "/", 2)
		if parts[0] == "." || parts[0] == ".." || parts[0] == "" {
			return nil, ErrInvalid
		}
		if root == "" {
			root = parts[0]
		}
		if root != parts[0] || seen[name] || entry.Flags&1 != 0 {
			return nil, ErrInvalid
		}
		seen[name] = true
		if entry.Mode().Type() == os.ModeDir {
			continue
		}
		if !entry.Mode().IsRegular() || len(parts) != 2 || entry.UncompressedSize64 > MaxExpandedBytes || total+int(entry.UncompressedSize64) > MaxExpandedBytes {
			return nil, ErrInvalid
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, ErrInvalid
		}
		body, readErr := io.ReadAll(io.LimitReader(stream, int64(MaxExpandedBytes-total)+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || len(body) > MaxExpandedBytes-total {
			return nil, ErrInvalid
		}
		total += len(body)
		if parts[1] == "SKILL.md" {
			if ValidateManifest(body, expected) != nil {
				return nil, ErrInvalid
			}
			manifest = true
		}
		files = append(files, File{Path: parts[1], Data: body, Executable: entry.Mode().Perm()&0111 != 0})
	}
	if !manifest {
		return nil, ErrInvalid
	}
	regular := map[string]bool{}
	for _, f := range files {
		regular[f.Path] = true
	}
	for _, f := range files {
		for parent := path.Dir(f.Path); parent != "."; parent = path.Dir(parent) {
			if regular[parent] {
				return nil, ErrInvalid
			}
		}
	}
	return files, nil
}

// ValidateManifest accepts portable descriptive metadata, not native activation controls.
func ValidateManifest(body []byte, expected Metadata) error {
	if len(body) > 256<<10 || !utf8.Valid(body) {
		return ErrInvalid
	}
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return ErrInvalid
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return ErrInvalid
	}
	end += 4
	tail := text[end+4:]
	if tail != "" && !strings.HasPrefix(tail, "\n") {
		return ErrInvalid
	}
	decoder := yaml.NewDecoder(strings.NewReader(text[4:end]))
	var document yaml.Node
	if decoder.Decode(&document) != nil || len(document.Content) != 1 {
		return ErrInvalid
	}
	var extra yaml.Node
	if decoder.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	node := document.Content[0]
	if node.Kind != yaml.MappingNode {
		return ErrInvalid
	}
	fields := map[string]*yaml.Node{}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || fields[key.Value] != nil {
			return ErrInvalid
		}
		fields[key.Value] = value
		switch key.Value {
		case "name", "description", "license", "compatibility":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return ErrInvalid
			}
		case "metadata":
			var metadata map[string]string
			if value.Kind != yaml.MappingNode || value.Decode(&metadata) != nil {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	if fields["name"] == nil || fields["description"] == nil || fields["name"].Value != expected.Name || fields["description"].Value != expected.Description {
		return ErrInvalid
	}
	return nil
}
