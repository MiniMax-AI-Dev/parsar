package parser

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// MaxSkillZipBytes caps a single Skill upload. 8 MiB carries a Skill with
// several thick references docs plus scripts while staying well under
// presign's 32 MiB ceiling.
const MaxSkillZipBytes = 8 * 1024 * 1024

// maxSkillEntryBytes caps individual file decompression — zip-bomb defence:
// a 1 KiB compressed entry could otherwise expand to 8 MiB and consume the
// whole zip-level budget on one file.
const maxSkillEntryBytes int64 = 1 << 20

// maxSkillTotalDecompressedBytes caps the SUM of decompressed bytes; without
// it, 256 entries × 1 MiB each could stage 256 MiB into a single
// canonical_spec jsonb.
const maxSkillTotalDecompressedBytes int64 = 32 * 1024 * 1024

// maxSkillEntryCount: thousands of entries means a packaged node_modules,
// not a Skill.
const maxSkillEntryCount = 256

// ErrInvalidSkillZip wraps hard failures (not a zip, missing SKILL.md,
// frontmatter broken, oversize). The import handler maps it to 4xx.
var ErrInvalidSkillZip = errors.New("parser: invalid skill zip")

// skillRootName is matched case-insensitively (packaging quirks) but we
// always emit "SKILL.md" downstream so storage stays predictable.
const skillRootName = "SKILL.md"

// allowedSkillSubdirs is the closed ingest set. Anything else is dropped
// with a warning — silent inclusion of arbitrary blobs would inflate
// canonical_spec for no consumer benefit.
var allowedSkillSubdirs = []string{"references/", "scripts/"}

// ParseSkillZip extracts a multi-file skill. Layout:
//
//	SKILL.md (or one-level wrapping: my-skill/SKILL.md)
//	references/*.md, references/*.txt, ...
//	scripts/*.{py,sh,js,ts,json,...}
//
// SKILL.md goes through ParseSkill so the frontmatter contract is identical
// between paste and zip imports. Pure: no I/O beyond buf.
func ParseSkillZip(buf []byte) (SkillParseResult, error) {
	if len(buf) == 0 {
		return SkillParseResult{}, fmt.Errorf("%w: empty upload (0 bytes)", ErrInvalidSkillZip)
	}
	if int64(len(buf)) > MaxSkillZipBytes {
		return SkillParseResult{}, fmt.Errorf("%w: skill zip exceeds %d byte cap (got %d)", ErrInvalidSkillZip, MaxSkillZipBytes, len(buf))
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return SkillParseResult{}, fmt.Errorf("%w: not a valid zip file: %v", ErrInvalidSkillZip, err)
	}

	if len(zr.File) > maxSkillEntryCount {
		return SkillParseResult{}, fmt.Errorf("%w: skill zip contains %d entries, max %d", ErrInvalidSkillZip, len(zr.File), maxSkillEntryCount)
	}

	// detectSingleRoot handles "user packaged with `zip -r foo/`".
	rawPaths := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		rawPaths = append(rawPaths, normalizeZipPath(f.Name))
	}
	root := detectSingleRoot(rawPaths)

	type strippedEntry struct {
		rel   string
		entry *zip.File
	}
	stripped := make([]strippedEntry, 0, len(zr.File))
	for i, p := range rawPaths {
		if isMacOSMetadata(p) {
			continue
		}
		if root != "" {
			p = strings.TrimPrefix(p, root)
		}
		if p == "" {
			continue
		}
		// Reject the whole zip on traversal: a `..` hint means it was crafted
		// in a way we don't understand; sanitising could mask intent.
		if hasParentTraversal(p) {
			return SkillParseResult{}, fmt.Errorf("%w: zip entry %q contains parent-directory reference (..)", ErrInvalidSkillZip, zr.File[i].Name)
		}
		stripped = append(stripped, strippedEntry{rel: p, entry: zr.File[i]})
	}

	var skillMdEntry *zip.File
	for _, se := range stripped {
		if strings.EqualFold(se.rel, skillRootName) {
			skillMdEntry = se.entry
			break
		}
	}
	if skillMdEntry == nil {
		return SkillParseResult{}, fmt.Errorf("%w: missing SKILL.md at the zip root", ErrInvalidSkillZip)
	}

	skillMdBytes, err := readZipEntryDirect(skillMdEntry)
	if err != nil {
		return SkillParseResult{}, fmt.Errorf("%w: cannot read SKILL.md: %v", ErrInvalidSkillZip, err)
	}

	skillRes, err := ParseSkill(string(skillMdBytes), SourceFormatMarkdown)
	if err != nil {
		return SkillParseResult{}, fmt.Errorf("%w: SKILL.md: %v", ErrInvalidSkillZip, err)
	}

	warnings := append([]string(nil), skillRes.Warnings...)
	files := make([]canonical.SkillFile, 0)
	ignored := make([]string, 0)

	// SKILL.md counts against the cumulative budget too — otherwise a near-
	// 1 MiB SKILL.md plus 32 MiB of references could slip past since the
	// loop below only sees non-SKILL.md entries.
	cumulative := int64(len(skillMdBytes))
	if cumulative > maxSkillTotalDecompressedBytes {
		return SkillParseResult{}, fmt.Errorf("%w: SKILL.md alone exceeds the %d byte total decompressed cap", ErrInvalidSkillZip, maxSkillTotalDecompressedBytes)
	}

	for _, se := range stripped {
		if strings.EqualFold(se.rel, skillRootName) {
			continue
		}
		// Filter directory markers BEFORE the allowlist check — otherwise
		// legitimate `references/` / `scripts/` markers (no trailing slash
		// after normalize) get reported as "ignored files outside SKILL.md
		// / references/ / scripts/", which is exactly where they live.
		if se.entry.FileInfo().IsDir() {
			continue
		}
		if !pathHasAllowedSkillPrefix(se.rel) {
			ignored = append(ignored, se.rel)
			continue
		}
		content, err := readZipEntryDirect(se.entry)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped %s: %s", se.rel, err.Error()))
			continue
		}
		cumulative += int64(len(content))
		if cumulative > maxSkillTotalDecompressedBytes {
			// Hard-fail rather than truncate: silently dropping the tail
			// would produce a half-imported skill whose references break.
			return SkillParseResult{}, fmt.Errorf("%w: total decompressed size exceeds %d bytes (running total at %s = %d)", ErrInvalidSkillZip, maxSkillTotalDecompressedBytes, se.rel, cumulative)
		}
		files = append(files, canonical.SkillFile{
			Path:    se.rel,
			Content: string(content),
			Kind:    inferSkillFileKind(se.rel),
		})
	}

	// Stable order so re-importing the same zip produces byte-for-byte
	// identical canonical_spec → capability_version rows.
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	if len(ignored) > 0 {
		sort.Strings(ignored)
		preview := ignored
		more := ""
		if len(preview) > 8 {
			more = fmt.Sprintf(" 等 %d 个", len(ignored)-8)
			preview = preview[:8]
		}
		warnings = append(warnings, fmt.Sprintf("忽略了 %d 个文件: %s%s", len(ignored), strings.Join(preview, ", "), more))
	}

	if skillRes.Spec.Skill == nil {
		// Defensive — ParseSkill always populates Skill on nil error.
		return SkillParseResult{}, fmt.Errorf("%w: parser invariant violated (skill spec nil after ParseSkill)", ErrInvalidSkillZip)
	}
	skillRes.Spec.Skill.Files = files
	skillRes.Warnings = warnings
	return skillRes, nil
}

// readZipEntryDirect enforces the per-entry cap so a single oversized file
// can't blow past the skill-level budget.
func readZipEntryDirect(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	limited := io.LimitReader(rc, maxSkillEntryBytes+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > maxSkillEntryBytes {
		return nil, fmt.Errorf("entry exceeds %d byte read cap", maxSkillEntryBytes)
	}
	return buf, nil
}

// pathHasAllowedSkillPrefix is case-sensitive on the prefix segment because
// skill consumers resolve `[link](references/foo.md)` case-sensitively at
// read time — accepting `References/foo.md` here would land at a path
// runtime won't find.
func pathHasAllowedSkillPrefix(rel string) bool {
	return slices.ContainsFunc(allowedSkillSubdirs, func(prefix string) bool {
		return strings.HasPrefix(rel, prefix)
	})
}

// inferSkillFileKind: only known script/markdown extensions get a specific
// Kind; everything else (images, json, yaml, opaque blobs) is Asset.
func inferSkillFileKind(rel string) canonical.SkillFileKind {
	lower := strings.ToLower(path.Ext(rel))
	switch lower {
	case ".md", ".markdown":
		return canonical.SkillFileKindMarkdown
	case ".py", ".sh", ".bash", ".js", ".ts", ".mjs", ".cjs":
		return canonical.SkillFileKindScript
	default:
		return canonical.SkillFileKindAsset
	}
}

func hasParentTraversal(p string) bool {
	for seg := range strings.SplitSeq(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// isMacOSMetadata catches Finder's `__MACOSX/` and `.DS_Store` noise.
func isMacOSMetadata(p string) bool {
	if strings.HasPrefix(p, "__MACOSX/") || p == "__MACOSX" {
		return true
	}
	base := path.Base(p)
	return base == ".DS_Store"
}
