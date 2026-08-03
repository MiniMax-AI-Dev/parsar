package skillcatalog

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	skillcatalogdata "github.com/MiniMax-AI-Dev/parsar/catalog/skills"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/parser"
)

var ErrItemNotFound = errors.New("skill catalog item not found")

type Options struct {
	BuiltinJSON []byte
	Files       fs.FS
}

type Loader struct {
	builtin    Catalog
	files      fs.FS
	builtinErr error
}

func New(options Options) *Loader {
	builtinJSON := options.BuiltinJSON
	if len(builtinJSON) == 0 {
		builtinJSON = skillcatalogdata.CatalogJSON
	}
	files := options.Files
	if files == nil {
		files = skillcatalogdata.FS
	}
	builtin, builtinErr := Decode(builtinJSON)
	return &Loader{builtin: builtin, files: files, builtinErr: builtinErr}
}

func (l *Loader) Load() (Catalog, error) {
	if l == nil {
		return Catalog{}, errors.New("skill catalog loader is nil")
	}
	if l.builtinErr != nil {
		return Catalog{}, fmt.Errorf("load builtin skill catalog: %w", l.builtinErr)
	}
	return l.builtin, nil
}

func (l *Loader) LoadItem(id string) (Item, Package, error) {
	catalog, err := l.Load()
	if err != nil {
		return Item{}, Package{}, err
	}
	item, found := catalog.Find(id)
	if !found {
		return Item{}, Package{}, fmt.Errorf("%w: %s", ErrItemNotFound, strings.TrimSpace(id))
	}
	if l.files == nil {
		return Item{}, Package{}, errors.New("skill catalog files are unavailable")
	}
	pkg, err := l.loadPackage(item)
	return item, pkg, err
}

func (c Catalog) Find(id string) (Item, bool) {
	id = strings.TrimSpace(id)
	for _, item := range c.Items {
		if item.ID == id {
			return item, true
		}
	}
	return Item{}, false
}

func (l *Loader) loadPackage(item Item) (Package, error) {
	entries, err := readEntries(l.files, item.ContentPath)
	if err != nil {
		return Package{}, fmt.Errorf("read skill %q package: %w", item.ID, err)
	}
	skillMD, ok := entries["SKILL.md"]
	if !ok {
		return Package{}, fmt.Errorf("skill %q package is missing SKILL.md", item.ID)
	}
	parsed, err := parser.ParseSkill(string(skillMD), parser.SourceFormatMarkdown)
	if err != nil {
		return Package{}, fmt.Errorf("parse skill %q SKILL.md: %w", item.ID, err)
	}
	if parsed.Spec.Skill == nil {
		return Package{}, fmt.Errorf("skill %q parser returned a nil skill spec", item.ID)
	}

	files := make([]canonical.SkillFile, 0, len(entries)-1)
	paths := make([]string, 0, len(entries))
	for rel := range entries {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		if rel == "SKILL.md" {
			continue
		}
		files = append(files, canonical.SkillFile{
			Path:    rel,
			Content: string(entries[rel]),
			Kind:    skillFileKind(rel),
		})
	}
	parsed.Spec.Skill.Files = files

	zipBytes, err := buildZip(entries, paths)
	if err != nil {
		return Package{}, fmt.Errorf("package skill %q: %w", item.ID, err)
	}
	if _, err := parser.ParseSkillZip(zipBytes); err != nil {
		return Package{}, fmt.Errorf("validate packaged skill %q: %w", item.ID, err)
	}
	sum := sha256.Sum256(zipBytes)
	return Package{
		Spec:   parsed.Spec,
		Files:  files,
		Zip:    zipBytes,
		SHA256: hex.EncodeToString(sum[:]),
	}, nil
}

func readEntries(files fs.FS, root string) (map[string][]byte, error) {
	entries := make(map[string][]byte)
	err := fs.WalkDir(files, root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(filePath, strings.TrimSuffix(root, "/")+"/")
		if rel == filePath || rel == "" || path.IsAbs(rel) || strings.HasPrefix(rel, "../") || rel == ".." {
			return fmt.Errorf("invalid embedded path %q", filePath)
		}
		content, err := fs.ReadFile(files, filePath)
		if err != nil {
			return err
		}
		entries[rel] = content
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func buildZip(entries map[string][]byte, paths []string) ([]byte, error) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, rel := range paths {
		header := &zip.FileHeader{Name: rel, Method: zip.Deflate}
		header.SetModTime(time.Unix(0, 0).UTC())
		header.SetMode(0o644)
		part, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(part, bytes.NewReader(entries[rel])); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func skillFileKind(rel string) canonical.SkillFileKind {
	switch strings.ToLower(path.Ext(rel)) {
	case ".md", ".markdown":
		return canonical.SkillFileKindMarkdown
	case ".py", ".sh", ".bash", ".js", ".ts", ".mjs", ".cjs":
		return canonical.SkillFileKindScript
	default:
		return canonical.SkillFileKindAsset
	}
}

func Decode(data []byte) (Catalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode skill catalog: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Catalog{}, fmt.Errorf("decode skill catalog: trailing JSON data")
	}
	if err := catalog.Validate(); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}
