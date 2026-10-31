package lsp

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/check"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/pkg"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/types"
)

// analyzeText runs the same front end as will check over the text held in
// memory. The file on disk is never read for its contents: a language server
// must report on what the editor shows, not on the last save.
func analyzeText(path, text string) ([]diag.Diagnostic, error) {
	if root, ok := findModuleRoot(filepath.Dir(path)); ok {
		return analyzeInModule(root, path, text)
	}
	return analyzeStandalone(path, text), nil
}

// findModuleRoot walks up from dir to the nearest directory with a will.mod.
func findModuleRoot(dir string) (string, bool) {
	for {
		info, err := os.Stat(filepath.Join(dir, "will.mod"))
		if err == nil && !info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// analyzeStandalone checks one file with no module around it. It mirrors
// will check <file>: resolve.Resolve has no package table, so an import of a
// package known only through will.mod is reported as an unknown path, exactly
// as the command line does for a file outside a module.
func analyzeStandalone(path, text string) []diag.Diagnostic {
	name := filepath.ToSlash(path)
	f, ds := parser.Parse(name, []byte(text))
	var bag diag.Bag
	bag.AddAll(ds)
	if f != nil {
		res, rds := resolve.Resolve(f)
		bag.AddAll(rds)
		checked, ds := types.CheckWithFull(f, res, nil)
		bag.AddAll(ds)
		if !bag.HasErrors() {
			ownership := check.NewOwnership([]*ast.File{f})
			bag.AddAll(check.MovesWithOwnership(f, ownership))
			bag.AddAll(check.TopLevelWrites(f))
			bag.AddAll(check.Leaks(f))
			bag.AddAll(check.CrossTask(f, checked))
		}
	}
	return onlyFile(bag.Diagnostics(), name)
}

// analyzeInModule checks one file as part of its module. pkg.Load reads the
// tree from disk, so the module is copied to a temporary directory with the
// in-memory text of the open file written over its copy. Paths inside the
// module change, but positions are relative to each file and therefore stay
// valid. Other open buffers are not written into the copy; only the document
// that was opened or changed is analyzed from memory.
func analyzeInModule(root, path, text string) ([]diag.Diagnostic, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("lsp: %s is outside module %s", path, root)
	}
	rel = filepath.ToSlash(rel)

	parent, err := os.MkdirTemp("", "will-lsp-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(parent)

	// The module root name is part of the package-name check in pkg.Load, so
	// the mirror must keep the same final directory name.
	mirror := filepath.Join(parent, filepath.Base(root))
	if err := copyModule(root, mirror); err != nil {
		return nil, err
	}
	if err := rewriteReplaceTargets(root, mirror); err != nil {
		return nil, err
	}
	target := filepath.Join(mirror, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(target, []byte(text), 0o644); err != nil {
		return nil, err
	}

	b := pkg.Load(mirror)
	if b.Err != nil {
		return nil, b.Err
	}
	if b.Mod == nil {
		return nil, fmt.Errorf("lsp: %s has no valid will.mod", root)
	}
	return onlyFile(b.CheckAll(), rel), nil
}

// copyModule copies will.mod and every .will file the front end would read.
// The directory names pkg.Load skips are skipped here as well.
func copyModule(root, mirror string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "build" || name == "desc") {
				return fs.SkipDir
			}
			return os.MkdirAll(filepath.Join(mirror, rel), 0o755)
		}
		if d.Name() != "will.mod" && d.Name() != "will.sum" && filepath.Ext(path) != ".will" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(mirror, rel), data, 0o644)
	})
}

// rewriteReplaceTargets points every local replace of the mirror at the real
// directory the original will.mod names. The copy lives elsewhere, so a
// relative target such as ../dep would otherwise name a directory that does not
// exist beside the mirror, and every import of that dependency would be
// reported as unknown.
func rewriteReplaceTargets(root, mirror string) error {
	data, err := os.ReadFile(filepath.Join(root, "will.mod"))
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "replace ") {
			continue
		}
		arrow := strings.Index(trimmed, "=>")
		if arrow < 0 {
			continue
		}
		target := strings.TrimSpace(trimmed[arrow+2:])
		if target == "" || strings.Contains(target, "@") {
			continue
		}
		real := target
		if !filepath.IsAbs(real) {
			real = filepath.Join(root, filepath.FromSlash(real))
		}
		real, err := filepath.Abs(real)
		if err != nil {
			continue
		}
		head := line[:strings.Index(line, "=>")+2]
		lines[i] = head + " " + filepath.ToSlash(real)
	}
	return os.WriteFile(filepath.Join(mirror, "will.mod"), []byte(strings.Join(lines, "\n")), 0o644)
}

// isLocalTarget is not needed: a replace target without a version names a
// directory, and one with a version names a module.

// onlyFile keeps the diagnostics that belong to one file of the analyzed tree.
// Module checking covers every package, but a document only owns its own
// messages.
func onlyFile(ds []diag.Diagnostic, name string) []diag.Diagnostic {
	out := make([]diag.Diagnostic, 0, len(ds))
	for _, d := range ds {
		if d.Pos.File != name {
			continue
		}
		out = append(out, d)
	}
	return out
}
