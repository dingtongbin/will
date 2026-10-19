package mod

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/cache"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

// SourceFaceHash returns the sha256 of one module tree's source face. The
// build volume fixes what that is: will.mod and every .will file, plus the .c
// and .h files each package holds. Entries are sorted by their POSIX relative
// path bytes, and every entry feeds its path length, path, content length, and
// content, so a changed source or header always changes the sum.
func SourceFaceHash(root string) (string, error) {
	entries, err := sourceFace(root)
	if err != nil {
		return "", err
	}
	h := cache.NewHasher()
	for _, rel := range entries {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		h.Add(rel, data)
	}
	return h.Key(), nil
}

// sourceFace lists the relative POSIX paths of one tree's source face, sorted
// by their bytes. Files outside the source face, the sum file itself, build
// output, and version control directories are left out.
func sourceFace(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root {
				switch d.Name() {
				case ".git", ".hg", ".svn", "build", ".will-cache":
					return fs.SkipDir
				}
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		switch {
		case rel == "will.mod":
		case filepath.Ext(rel) == ".will":
		case filepath.Ext(rel) == ".c", filepath.Ext(rel) == ".h":
		default:
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// SumLine is one line of will.sum: an import path and its checksum.
type SumLine struct {
	Path string
	Sum  string
	Line int
}

// ParseSum reads a will.sum file. Every line is "<import path> sha256:<hex>";
// the only algorithm is sha256.
func ParseSum(file string, data []byte) ([]SumLine, []diag.Diagnostic) {
	var bag diag.Bag
	var out []SumLine
	for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pos := source.Pos{File: file, Line: i + 1, Col: 1}
		if len(fields) != 2 {
			bag.Error(pos, diag.CodeChecksumMismatch, "checksum mismatch for "+fields[0],
				"run will mod tidy")
			continue
		}
		algo, hex, ok := strings.Cut(fields[1], ":")
		if !ok || algo != "sha256" || hex == "" {
			bag.Error(pos, diag.CodeChecksumMismatch, "checksum mismatch for "+fields[0],
				"run will mod tidy")
			continue
		}
		out = append(out, SumLine{Path: fields[0], Sum: fields[1], Line: i + 1})
	}
	return out, bag.Diagnostics()
}

// FormatSum writes the sum lines in the one order the file uses: by import
// path bytes.
func FormatSum(lines []SumLine) []byte {
	sorted := append([]SumLine(nil), lines...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	var b strings.Builder
	for _, l := range sorted {
		b.WriteString(l.Path)
		b.WriteByte(' ')
		b.WriteString(l.Sum)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// LocalDir returns the directory a dependency resolves to when a replace
// points at a local path. The second result is false when the dependency is
// not local, so its checksum cannot be computed here.
func LocalDir(root string, m *Module, dep string) (string, bool) {
	for _, r := range m.Replaces {
		if r.Path != dep || r.IsVersioned || r.Target == "" {
			continue
		}
		return filepath.Join(root, filepath.FromSlash(r.Target)), true
	}
	return "", false
}
