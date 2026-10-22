package pkg

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/mod"
	"github.com/will-lang/will/internal/source"
)

// moduleNode is one module tree in the build: the root module or a local
// replace target.
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
	// moduleDirs maps a module path to the directory that declared it, so two
	// trees claiming one path are reported instead of silently picking one.
	moduleDirs map[string]string
	// replaceReported remembers the paths whose replace chain was already
	// reported, so a retry does not repeat the diagnostic.
	replaceReported map[string]bool
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
// required version wins.
type requirement struct {
	version mod.Version
}

// resolveModules loads the root and every module reachable through requires
// and local replaces.
func resolveModules(root *moduleNode) *graph {
	known := map[string]requirement{}
	mergeRequires(known, root.Mod)
	var g *graph
	for attempt := 0; attempt < moduleLoadAttempts; attempt++ {
		g = &graph{
			root:            root,
			nodes:           map[string]*moduleNode{},
			dirs:            map[string]string{},
			known:           known,
			moduleDirs:      map[string]string{},
			replaceReported: map[string]bool{},
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
	if n.Mod != nil {
		declared := n.Mod.Path
		if prev, seen := g.moduleDirs[declared]; seen && prev != key {
			g.diags = append(g.diags, diag.Diagnostic{
				Severity: diag.Error,
				Code:     diag.CodeInvalidWillMod,
				Pos:      source.Pos{File: filepath.Join(n.Dir, "will.mod"), Line: 1, Col: 1},
				Message:  "module " + declared + " is declared by " + prev + " and " + n.Dir,
			})
		} else if !seen {
			g.moduleDirs[declared] = key
		}
	}
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
			dir, ok := g.target(path, selected[path])
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
// highest version per path.
func mergeRequires(out map[string]requirement, m *mod.Module) {
	if m == nil {
		return
	}
	for _, r := range m.Requires {
		cur, seen := out[r.Path]
		if !seen || r.Version.Compare(cur.version) > 0 {
			out[r.Path] = requirement{version: r.Version}
		}
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
// continues the chain at the other version.
func (g *graph) target(path string, req requirement) (string, bool) {
	curPath, curVersion := path, req.version
	exhausted := false
	for i := 0; i < replaceChainLimit; i++ {
		rep, owner, ok := g.findReplace(curPath, curVersion)
		if !ok {
			exhausted = false
			break
		}
		if !rep.IsVersioned {
			target := filepath.FromSlash(rep.Target)
			if !filepath.IsAbs(target) {
				target = filepath.Join(owner.Dir, target)
			}
			return target, true
		}
		curPath, curVersion = rep.Target, rep.TargetVer
		exhausted = true
	}
	if exhausted && !g.replaceReported[path] {
		g.replaceReported[path] = true
		g.diags = append(g.diags, diag.Diagnostic{
			Severity: diag.Error,
			Code:     diag.CodeInvalidWillMod,
			Pos:      source.Pos{File: "will.mod", Line: 1, Col: 1},
			Message:  "replace chain for " + path + " is too long or cycles",
			Help:     "shorten the chain or remove the cycle",
		})
	}
	return "", false
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

// readLocalModule reads one local replace target. The module keeps the path its
// own will.mod declares; the replace key is an alias that the import table
// resolves to the same packages. A target without will.mod is not a module and
// returns nil with no diagnostics.
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
