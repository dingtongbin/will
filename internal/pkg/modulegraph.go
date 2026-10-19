package pkg

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/mod"
)

// moduleNode is one module tree in the build: the root module, a local replace
// target, or the bundled standard library.
type moduleNode struct {
	Dir  string
	Path string
	Mod  *mod.Module
	// Keys are the requirement paths that selected this tree, sorted. A key
	// other than Path is another import prefix for the same packages, which
	// is how two replace keys pointing at one directory become two import
	// paths.
	Keys  []string
	Local bool
	Std   bool
}

// graph is the module set of one build and the requirement path that selected
// each module.
type graph struct {
	root  *moduleNode
	nodes map[string]*moduleNode
	order []string
	dirs  map[string]string
	// known holds every require of every module loaded so far. It survives a
	// restart, so a higher version discovered by a module already loaded is
	// selected in the first round of the next attempt.
	known map[string]requirement
	diags []diag.Diagnostic
	std   *moduleNode
	// stdErr is set when the bundled standard library is needed and its root
	// is not on disk.
	stdErr error
}

const (
	// moduleLoadAttempts bounds the restarts after a higher selected version
	// moves a module path to another local directory.
	moduleLoadAttempts = 3
	// moduleResolveRounds bounds the fixed point over the modules' requires.
	moduleResolveRounds = 64
	// replaceChainLimit bounds replace chains that point at another version.
	replaceChainLimit = 16
)

// requirement is the selected version of one module path. The highest
// required version wins; a bundled requirement only stands while no versioned
// requirement exists.
type requirement struct {
	version   mod.Version
	versioned bool
	bundled   bool
}

// resolveModules loads the root and every module reachable through requires
// and local replaces.
func resolveModules(root *moduleNode) *graph {
	known := map[string]requirement{}
	mergeRequires(known, root.Mod)
	var g *graph
	for attempt := 0; attempt < moduleLoadAttempts; attempt++ {
		g = &graph{
			root:  root,
			nodes: map[string]*moduleNode{},
			dirs:  map[string]string{},
			known: known,
		}
		g.addNode(root)
		if g.resolve() {
			break
		}
	}
	return g
}

func (g *graph) addNode(n *moduleNode) {
	key := canonicalDir(n.Dir)
	g.nodes[key] = n
	g.order = append(g.order, key)
	sort.Strings(n.Keys)
	mergeRequires(g.known, n.Mod)
}

// resolve reaches a fixed point where every selected module path has a
// directory and every loaded module's requires have been selected. It returns
// false when a path moved to a different directory, so the caller starts over
// with the higher version already known.
func (g *graph) resolve() bool {
	for round := 0; round < moduleResolveRounds; round++ {
		selected := g.known
		changed := false
		for _, path := range selectionPaths(selected) {
			dir, std, ok := g.target(path, selected[path])
			if !ok {
				continue
			}
			key := canonicalDir(dir)
			if prev, seen := g.dirs[path]; seen && prev != key {
				if _, loaded := g.nodes[prev]; loaded {
					// A module already loaded no longer supplies this path;
					// start over with the higher version in known.
					return false
				}
			}
			g.dirs[path] = key
			if std {
				changed = g.addStd(dir) || changed
				continue
			}
			if n, seen := g.nodes[key]; seen {
				if addKey(n, path) {
					changed = true
				}
				continue
			}
			n, ds := readLocalModule(dir, path)
			g.diags = append(g.diags, ds...)
			if n == nil {
				// A local replace target without will.mod is not a module;
				// its import paths stay unknown and the import reports B0008.
				continue
			}
			g.addNode(n)
			changed = true
		}
		if !changed {
			return true
		}
	}
	return true
}

// mergeRequires folds one module's requires into the selected set, keeping the
// highest version per path and letting a versioned requirement replace a
// bundled one.
func mergeRequires(out map[string]requirement, m *mod.Module) {
	if m == nil {
		return
	}
	for _, r := range m.Requires {
		cur, seen := out[r.Path]
		if !seen {
			cur = requirement{bundled: r.Bundled}
			if !r.Bundled {
				cur.version = r.Version
				cur.versioned = true
			}
			out[r.Path] = cur
			continue
		}
		switch {
		case r.Bundled && !cur.versioned:
			cur.bundled = true
		case !r.Bundled && (!cur.versioned || r.Version.Compare(cur.version) > 0):
			cur.version = r.Version
			cur.versioned = true
			cur.bundled = false
		}
		out[r.Path] = cur
	}
}

func selectionPaths(selected map[string]requirement) []string {
	out := make([]string, 0, len(selected))
	for path := range selected {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// target resolves one selected module path to a directory. A replace wins
// over everything: a local target names the directory, a versioned target
// continues the chain at the other version. A bundled std requirement lands on
// the toolchain's standard library root.
func (g *graph) target(path string, req requirement) (string, bool, bool) {
	curPath, curVersion := path, req.version
	for i := 0; i < replaceChainLimit; i++ {
		rep, owner, ok := g.findReplace(curPath, curVersion)
		if !ok {
			break
		}
		if !rep.IsVersioned {
			return filepath.Join(owner.Dir, filepath.FromSlash(rep.Target)), false, true
		}
		curPath, curVersion = rep.Target, rep.TargetVer
	}
	if req.bundled && curPath == "std" {
		dir, ok := bundledStdRoot()
		if !ok {
			g.stdErr = &MissingBundledStd{Path: dir}
			return "", false, false
		}
		return dir, true, true
	}
	return "", false, false
}

// findReplace looks a replacement up in the root module first and then in the
// other modules in path order, so load order never decides which one wins.
func (g *graph) findReplace(path string, version mod.Version) (mod.Replace, *moduleNode, bool) {
	nodes := g.sortedNodes()
	for _, n := range nodes {
		if n.Mod == nil {
			continue
		}
		for _, r := range n.Mod.Replaces {
			if r.Path == path && r.Version.Compare(version) == 0 {
				return r, n, true
			}
		}
	}
	return mod.Replace{}, nil, false
}

func (g *graph) sortedNodes() []*moduleNode {
	rootKey := canonicalDir(g.root.Dir)
	out := make([]*moduleNode, 0, len(g.nodes))
	if root, ok := g.nodes[rootKey]; ok {
		out = append(out, root)
	}
	var rest []*moduleNode
	for key, n := range g.nodes {
		if key == rootKey {
			continue
		}
		rest = append(rest, n)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Path < rest[j].Path })
	return append(out, rest...)
}

// readLocalModule reads one local replace target. A target without will.mod is
// not a module and returns nil with no diagnostics.
func readLocalModule(dir, key string) (*moduleNode, []diag.Diagnostic) {
	data, err := os.ReadFile(filepath.Join(dir, "will.mod"))
	if err != nil {
		return nil, nil
	}
	m, ds := mod.Parse("will.mod", data)
	if m == nil {
		return nil, ds
	}
	return &moduleNode{
		Dir:   filepath.Clean(dir),
		Path:  m.Path,
		Mod:   m,
		Keys:  []string{key},
		Local: true,
	}, ds
}

// addStd loads the bundled standard library root. It reports whether the root
// became a new node of this graph.
func (g *graph) addStd(dir string) bool {
	key := canonicalDir(dir)
	if n, ok := g.nodes[key]; ok {
		g.std = n
		return false
	}
	path := "std"
	var m *mod.Module
	var ds []diag.Diagnostic
	if data, err := os.ReadFile(filepath.Join(dir, "will.mod")); err == nil {
		m, ds = mod.Parse("will.mod", data)
		if m != nil && m.Path != "" {
			path = m.Path
		}
	}
	g.diags = append(g.diags, ds...)
	n := &moduleNode{
		Dir:  filepath.Clean(dir),
		Path: path,
		Mod:  m,
		Keys: []string{"std"},
		Std:  true,
	}
	g.addNode(n)
	g.std = n
	return true
}

func addKey(n *moduleNode, key string) bool {
	for _, k := range n.Keys {
		if k == key {
			return false
		}
	}
	n.Keys = append(n.Keys, key)
	sort.Strings(n.Keys)
	return true
}
