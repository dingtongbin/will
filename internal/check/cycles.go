package check

import (
	"sort"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

// Cycles reports the type-level part of the ownership cycle rule. Strong rc
// fields form a graph; a self edge is a cycle that cannot be released and is
// M0001, and a larger strongly connected component is M0002 because the value
// pass has not shown that a particular object closes it. A weak field does not
// add a strong edge, which is how the program marks the cycle as intentional.
func Cycles(f *ast.File) []diag.Diagnostic {
	decls := map[string]*ast.StructDecl{}
	for _, d := range f.Decls {
		if s, ok := d.(*ast.StructDecl); ok {
			decls[s.Name] = s
		}
	}
	graph := map[string][]cycleEdge{}
	for name, s := range decls {
		for _, field := range s.Fields {
			target, pos, ok := strongOwner(field.Type)
			if !ok {
				continue
			}
			if _, exists := decls[target]; !exists {
				continue
			}
			graph[name] = append(graph[name], cycleEdge{
				from: name, to: target, field: field.Name, pos: pos,
			})
		}
	}
	var out []diag.Diagnostic
	for _, comp := range stronglyConnected(graph) {
		var edges []cycleEdge
		self := false
		for _, name := range comp {
			for _, edge := range graph[name] {
				if containsName(comp, edge.to) {
					edges = append(edges, edge)
					if edge.to == name {
						self = true
					}
				}
			}
		}
		if len(edges) == 0 {
			continue
		}
		sort.Slice(edges, func(i, j int) bool {
			if edges[i].from != edges[j].from {
				return edges[i].from < edges[j].from
			}
			return edges[i].field < edges[j].field
		})
		if self {
			names := make([]string, 0, len(edges))
			for _, edge := range edges {
				names = append(names, edge.from+"."+edge.field)
			}
			out = append(out, diag.Diagnostic{
				Pos: edges[0].pos, End: edges[0].pos, Severity: diag.Error,
				Code:    diag.CodeUnreleasableCycle,
				Message: "this cycle cannot be released",
				Help:    "mark one of these edges as weak: " + strings.Join(names, ", "),
			})
			continue
		}
		for _, edge := range edges {
			out = append(out, diag.Diagnostic{
				Pos: edge.pos, End: edge.pos, Severity: diag.Warning,
				Code:    diag.CodePossibleCycle,
				Message: "this field may form a cycle",
				Help:    "mark it weak if that is the intent",
			})
		}
	}
	return out
}

type cycleEdge struct {
	from  string
	to    string
	field string
	pos   source.Pos
}

// strongOwner returns the type a field keeps alive through one or more rc
// wrappers. An Array, Option, Map, or Set around rc keeps the same edge; a weak
// anywhere in the path breaks it.
func strongOwner(t *ast.Type) (string, source.Pos, bool) {
	if t == nil {
		return "", source.Pos{}, false
	}
	if t.Kind != ast.NamedType {
		return "", source.Pos{}, false
	}
	switch t.Name {
	case "rc":
		if len(t.Args) == 1 && t.Args[0].Kind == ast.NamedType {
			return t.Args[0].Name, t.Pos(), true
		}
		return "", source.Pos{}, false
	case "weak":
		return "", source.Pos{}, false
	case "Array", "Option", "Map", "Set":
		for _, arg := range t.Args {
			if name, pos, ok := strongOwner(arg); ok {
				return name, pos, true
			}
		}
	}
	return "", source.Pos{}, false
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// stronglyConnected is Tarjan over the type graph. Components are returned in
// discovery order, which is deterministic because the caller iterates decls in
// source order through graph slices.
func stronglyConnected(graph map[string][]cycleEdge) [][]string {
	index := 0
	indices := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var out [][]string
	var visit func(string)
	visit = func(name string) {
		indices[name] = index
		low[name] = index
		index++
		stack = append(stack, name)
		onStack[name] = true
		for _, edge := range graph[name] {
			next := edge.to
			if _, seen := indices[next]; !seen {
				visit(next)
				if low[next] < low[name] {
					low[name] = low[next]
				}
			} else if onStack[next] && indices[next] < low[name] {
				low[name] = indices[next]
			}
		}
		if low[name] != indices[name] {
			return
		}
		var comp []string
		for {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[last] = false
			comp = append(comp, last)
			if last == name {
				break
			}
		}
		out = append(out, comp)
	}
	names := make([]string, 0, len(graph))
	for name := range graph {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, seen := indices[name]; !seen {
			visit(name)
		}
	}
	return out
}
