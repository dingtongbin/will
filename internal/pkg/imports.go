package pkg

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

// importEdge is one import that resolved to a package.
type importEdge struct {
	to  *Package
	pos source.Pos
}

// canonicalDir is the key that decides whether two packages live in the same
// directory. Windows compares directory names without case, so two spellings
// of one directory do not become two packages there.
func canonicalDir(dir string) string {
	cleaned := filepath.Clean(dir)
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}

// importEdges lists the packages every package imports, in file order and
// import order. Duplicate targets keep their first position.
func importEdges(pkgs []*Package, table map[string]*Package) map[*Package][]importEdge {
	out := map[*Package][]importEdge{}
	for _, p := range pkgs {
		seen := map[*Package]bool{}
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, imp := range f.AST.Imports {
				if imp.C {
					continue
				}
				target := table[imp.Path]
				if target == nil || seen[target] {
					continue
				}
				seen[target] = true
				out[p] = append(out[p], importEdge{to: target, pos: imp.Span.Start})
			}
		}
	}
	return out
}

// checkTwoPaths reports B0009 when two import paths reach one package
// directory. The diagnostic lands on the import that writes the second path.
func checkTwoPaths(pkgs []*Package, table map[string]*Package) []diag.Diagnostic {
	var bag diag.Bag
	first := map[string]string{}
	for _, p := range pathOrder(pkgs) {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			for _, imp := range f.AST.Imports {
				if imp.C {
					continue
				}
				target := table[imp.Path]
				if target == nil {
					continue
				}
				dir := canonicalDir(target.Dir)
				if prev, seen := first[dir]; seen {
					if prev != imp.Path {
						bag.Error(imp.Span.Start, diag.CodePackageTwoPaths,
							"package "+imp.Path+" is the same as "+prev,
							"import one of them; fix replace or the module layout")
					}
					continue
				}
				first[dir] = imp.Path
			}
		}
	}
	return bag.Diagnostics()
}

// checkImportCycles runs a depth-first search over the package import graph
// and reports B0010 on the import that closes each back edge. The message
// names the whole cycle, otherwise the reader has to find it.
func checkImportCycles(pkgs []*Package, edges map[*Package][]importEdge) []diag.Diagnostic {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	var bag diag.Bag
	state := map[*Package]int{}
	var stack []*Package
	var visit func(p *Package)
	visit = func(p *Package) {
		state[p] = gray
		stack = append(stack, p)
		for _, e := range edges[p] {
			switch state[e.to] {
			case white:
				visit(e.to)
			case gray:
				bag.Error(e.pos, diag.CodeImportCycle,
					"import cycle: "+cycleText(stack, e.to),
					"break the cycle; move the shared part into a lower package")
			}
		}
		stack = stack[:len(stack)-1]
		state[p] = black
	}
	for _, p := range pathOrder(pkgs) {
		if state[p] == white {
			visit(p)
		}
	}
	return bag.Diagnostics()
}

// cycleText renders the cycle from the first occurrence of start to the end of
// the stack and back to start.
func cycleText(stack []*Package, start *Package) string {
	begin := 0
	for i, p := range stack {
		if p == start {
			begin = i
			break
		}
	}
	parts := make([]string, 0, len(stack)-begin+1)
	for _, p := range stack[begin:] {
		parts = append(parts, p.Path)
	}
	parts = append(parts, start.Path)
	return strings.Join(parts, " -> ")
}

// orderPackages returns the packages dependencies first. Every package sits
// one level above its deepest dependency, and one level holds paths in byte
// order, so the result is a topological order with a fixed tie break.
func orderPackages(pkgs []*Package, edges map[*Package][]importEdge) []*Package {
	level := map[*Package]int{}
	active := map[*Package]bool{}
	var depth func(p *Package) int
	depth = func(p *Package) int {
		if l, ok := level[p]; ok {
			return l
		}
		active[p] = true
		best := 0
		for _, e := range edges[p] {
			if active[e.to] {
				// A back edge stays inside a cycle; it cannot raise the
				// level without making the order impossible.
				continue
			}
			if l := depth(e.to) + 1; l > best {
				best = l
			}
		}
		delete(active, p)
		level[p] = best
		return best
	}
	out := append([]*Package(nil), pkgs...)
	for _, p := range out {
		depth(p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if level[out[i]] != level[out[j]] {
			return level[out[i]] < level[out[j]]
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// pathOrder returns the packages sorted by import path, which is the order the
// whole-graph checks read them in.
func pathOrder(pkgs []*Package) []*Package {
	out := append([]*Package(nil), pkgs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
