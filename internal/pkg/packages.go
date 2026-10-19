package pkg

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/source"
)

// packages walks every module of the graph into packages and returns the
// import table: every package path plus every local replace key that names the
// same package under another path.
func (g *graph) packages() ([]*Package, map[string]*Package, []diag.Diagnostic) {
	var bag diag.Bag
	groups := make([]moduleGroup, 0, len(g.order))
	var pkgs []*Package
	for _, key := range g.order {
		n := g.nodes[key]
		ps, ds := walkModule(n)
		bag.AddAll(ds)
		groups = append(groups, moduleGroup{node: n, pkgs: ps})
		pkgs = append(pkgs, ps...)
	}
	// Features are compile-time: prune their conditions before the import
	// check, because the std/meta import is dropped once it is used.
	declared := g.declaredFeatures()
	bag.AddAll(enabledUndeclared(declared))
	bag.AddAll(prunePackages(pkgs, declared))
	// A module that imports std without requiring it still needs the
	// bundled root, because the root's absence is a toolchain fault and not
	// an unknown import path.
	if g.std == nil && importsStd(pkgs) {
		dir, ok := bundledStdRoot()
		if !ok {
			g.stdErr = &MissingBundledStd{Path: dir}
			return nil, nil, bag.Diagnostics()
		}
		g.addStd(dir)
		ps, ds := walkModule(g.std)
		bag.AddAll(ds)
		groups = append(groups, moduleGroup{node: g.std, pkgs: ps})
		pkgs = append(pkgs, ps...)
	}
	return pkgs, buildImportTable(groups), bag.Diagnostics()
}

// moduleGroup is the package set of one module.
type moduleGroup struct {
	node *moduleNode
	pkgs []*Package
}

// buildImportTable registers the module paths first and the replace keys
// second, so a real package path always wins over an alias.
func buildImportTable(groups []moduleGroup) map[string]*Package {
	table := map[string]*Package{}
	for _, group := range groups {
		for _, p := range group.pkgs {
			if _, ok := table[p.Path]; !ok {
				table[p.Path] = p
			}
		}
	}
	for _, group := range groups {
		for _, key := range group.node.Keys {
			if key == group.node.Path {
				continue
			}
			for _, p := range group.pkgs {
				alias := importPath(key, group.node.Dir, p.Dir)
				if _, ok := table[alias]; !ok {
					table[alias] = p
				}
			}
		}
	}
	return table
}

// importsStd reports whether any loaded package imports the bundled standard
// library.
func importsStd(pkgs []*Package) bool {
	for _, p := range pkgs {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, imp := range f.AST.Imports {
				if imp.C {
					continue
				}
				if imp.Path == "std" || strings.HasPrefix(imp.Path, "std/") {
					return true
				}
			}
		}
	}
	return false
}

// walkModule reads every .will file under one module root and groups them by
// directory.
func walkModule(n *moduleNode) ([]*Package, []diag.Diagnostic) {
	var bag diag.Bag
	if !n.Std {
		if base := filepath.Base(n.Dir); base != lastSegment(n.Path) {
			bag.Error(source.Pos{File: base, Line: 1, Col: 1}, diag.CodePackageNameMismatch,
				"package name does not match the directory name", "rename one of them")
		}
	}

	dirs := map[string][]string{}
	filepath.WalkDir(n.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == n.Dir {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "build" || name == "desc" {
				return fs.SkipDir
			}
			// A directory with its own will.mod is another module, even
			// inside this tree; it is loaded only when a require selects it.
			if _, err := os.Stat(filepath.Join(path, "will.mod")); err == nil {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".will" {
			dir := filepath.Dir(path)
			dirs[dir] = append(dirs[dir], path)
		}
		return nil
	})
	if _, ok := dirs[n.Dir]; !ok {
		dirs[n.Dir] = nil
	}

	var pkgs []*Package
	for dir, files := range dirs {
		name := filepath.Base(dir)
		if !validPackageName(name) {
			bag.Error(source.Pos{File: name, Line: 1, Col: 1}, diag.CodePackageNameMismatch,
				"package name does not match the directory name", "rename one of them")
			continue
		}
		sort.Strings(files)
		p := &Package{Dir: dir, Root: n.Dir, Name: name, Path: importPath(n.Path, n.Dir, dir)}
		for _, path := range files {
			rel, err := filepath.Rel(n.Dir, path)
			if err != nil {
				continue
			}
			src, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			f, fds := parser.Parse(filepath.ToSlash(rel), src)
			bag.AddAll(fds)
			p.Files = append(p.Files, &File{Path: filepath.ToSlash(rel), AST: f})
		}
		p.Names, p.Diags = buildNames(p)
		p.Exports = buildExports(p)
		bag.AddAll(p.Diags)
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Path < pkgs[j].Path })
	return pkgs, bag.Diagnostics()
}
