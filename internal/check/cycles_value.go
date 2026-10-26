package check

import (
	"sort"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

// ValueCycles finds rc objects that are actually linked into a cycle by field
// assignments in one function. The type pass can only warn that an edge may
// close; this pass reports the concrete a.next = b / b.next = a shape. The graph
// is deliberately local: handles stored through calls or returned objects need
// a whole-program points-to result and stay a type-level warning.
func ValueCycles(f *ast.File) []diag.Diagnostic {
	own := NewOwnership([]*ast.File{f})
	var out []diag.Diagnostic
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		out = append(out, valueCyclesInFunc(fn, own)...)
	}
	return out
}

type valueEdge struct {
	from  string
	to    string
	field string
	pos   source.Pos
}

func valueCyclesInFunc(fn *ast.FuncDecl, own *Ownership) []diag.Diagnostic {
	types := map[string]*ast.Type{}
	for _, p := range fn.Params {
		types[p.Name] = p.Type
	}
	if fn.Recv != nil {
		types[fn.Recv.Name] = fn.Recv.Type
	}
	var edges []valueEdge
	var walkStmt func(ast.Stmt)
	var walkExpr func(ast.Expr)
	walkExpr = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
		case *ast.CallExpr:
			walkExpr(x.Callee)
			for _, a := range x.Args {
				walkExpr(a.Expr)
			}
		case *ast.MethodCallExpr:
			walkExpr(x.Recv)
			for _, a := range x.Args {
				walkExpr(a.Expr)
			}
		case *ast.FieldExpr:
			walkExpr(x.Recv)
		case *ast.IndexExpr:
			walkExpr(x.Recv)
			walkExpr(x.Index)
		case *ast.SliceExpr:
			walkExpr(x.Recv)
			walkExpr(x.Lo)
			walkExpr(x.Hi)
		case *ast.UnaryExpr:
			walkExpr(x.X)
		case *ast.BinaryExpr:
			walkExpr(x.L)
			walkExpr(x.R)
		case *ast.NewExpr:
			for _, a := range x.Args {
				walkExpr(a.Expr)
			}
		case *ast.AggregateExpr:
			for _, f := range x.Fields {
				walkExpr(f.Value)
			}
		case *ast.RangeExpr:
			walkExpr(x.Lo)
			walkExpr(x.Hi)
		case *ast.TryExpr:
			walkExpr(x.X)
		case *ast.AwaitExpr:
			walkExpr(x.X)
		case *ast.UnwrapExpr:
			walkExpr(x.X)
		case *ast.SpawnExpr:
			walkExpr(x.Call)
		case *ast.LambdaExpr:
			for _, st := range x.Body.Stmts {
				walkStmt(st)
			}
		case *ast.InterpExpr:
			for _, p := range x.Parts {
				walkExpr(p.Expr)
			}
		case *ast.IfExpr:
			walkStmt(x)
		case *ast.MatchExpr:
			walkStmt(x)
		case *ast.DeadlineExpr:
			walkStmt(x)
		}
	}
	walkStmt = func(st ast.Stmt) {
		switch s := st.(type) {
		case nil:
		case *ast.Block:
			for _, inner := range s.Stmts {
				walkStmt(inner)
			}
		case *ast.VarStmt:
			switch d := s.Decl.(type) {
			case *ast.VarDecl:
				walkExpr(d.Value)
				if d.Name != "" && d.Name != "_" {
					types[d.Name] = d.Type
				}
			case *ast.ConstDecl:
				walkExpr(d.Value)
			}
		case *ast.AssignStmt:
			if edge, ok := valueEdgeOf(s.Target, s.Value, types, own); ok {
				edges = append(edges, edge)
			}
			walkExpr(s.Target)
			walkExpr(s.Value)
		case *ast.ExprStmt:
			walkExpr(s.Expr)
		case *ast.DeferStmt:
			walkExpr(s.Call)
		case *ast.ReturnStmt:
			walkExpr(s.Value)
		case *ast.ForStmt:
			walkExpr(s.Iter)
			walkStmt(s.Body)
		case *ast.RegionStmt:
			walkStmt(s.Body)
		case *ast.ParallelStmt:
			walkStmt(s.Body)
		case *ast.SelectStmt:
			for _, arm := range s.Arms {
				walkExpr(arm.Chan)
				walkExpr(arm.Timeout)
				walkStmt(arm.Body)
			}
		case *ast.IfExpr:
			walkExpr(s.Cond)
			walkStmt(s.Then)
			if s.Else != nil {
				walkStmt(s.Else)
			}
		case *ast.MatchExpr:
			walkExpr(s.Subject)
			for _, arm := range s.Arms {
				walkExpr(arm.Guard)
				walkStmt(arm.Body)
			}
		case *ast.DeadlineExpr:
			walkExpr(s.Duration)
			walkStmt(s.Body)
		}
	}
	walkStmt(fn.Body)

	graph := map[string][]valueEdge{}
	for _, edge := range edges {
		graph[edge.from] = append(graph[edge.from], edge)
	}
	var out []diag.Diagnostic
	for _, comp := range stronglyConnectedValue(graph) {
		var cycle []valueEdge
		for _, name := range comp {
			for _, edge := range graph[name] {
				if containsName(comp, edge.to) {
					cycle = append(cycle, edge)
				}
			}
		}
		if len(cycle) == 0 || (len(comp) == 1 && cycle[0].to != cycle[0].from) {
			continue
		}
		sort.Slice(cycle, func(i, j int) bool {
			if cycle[i].from != cycle[j].from {
				return cycle[i].from < cycle[j].from
			}
			return cycle[i].field < cycle[j].field
		})
		var names []string
		for _, e := range cycle {
			names = append(names, e.from+"."+e.field)
		}
		out = append(out, diag.Diagnostic{
			Pos: cycle[0].pos, End: cycle[0].pos, Severity: diag.Error,
			Code:    diag.CodeUnreleasableCycle,
			Message: "this cycle cannot be released",
			Help:    "mark one of these edges as weak: " + strings.Join(names, ", "),
		})
	}
	return out
}

// valueEdgeOf recognizes a strong rc field assignment a.next = b where both
// sides are local handles of the same object type.
func valueEdgeOf(target, value ast.Expr, types map[string]*ast.Type, own *Ownership) (valueEdge, bool) {
	field, ok := target.(*ast.FieldExpr)
	if !ok {
		return valueEdge{}, false
	}
	root := rootName(field.Recv)
	if root == "" {
		return valueEdge{}, false
	}
	recvType := types[root]
	if recvType == nil {
		return valueEdge{}, false
	}
	if _, ok := strongRcElem(recvType); !ok {
		return valueEdge{}, false
	}
	fieldType := own.FieldType(recvType, field.Name)
	fieldElem, ok := strongRcElem(fieldType)
	if !ok {
		return valueEdge{}, false
	}
	if call, ok := value.(*ast.CallExpr); ok {
		if callee, ok := call.Callee.(*ast.NameExpr); ok && callee.Name == "Some" && len(call.Args) == 1 {
			value = call.Args[0].Expr
		}
	}
	name := rootName(value)
	if name == "" {
		return valueEdge{}, false
	}
	valueType := types[name]
	if valueType == nil {
		return valueEdge{}, false
	}
	valueElem, ok := strongRcElem(valueType)
	if !ok || shortTypeName(fieldElem) != shortTypeName(valueElem) {
		return valueEdge{}, false
	}
	return valueEdge{from: root, to: name, field: field.Name, pos: field.NamePos}, true
}

// strongRcElem returns X for a direct rc<X> field and nothing for weak or any
// wrapper. A direct field edge is the shape whose concrete link this pass can
// prove; arrays and options stay type-level warnings.
func strongRcElem(t *ast.Type) (string, bool) {
	if t == nil || t.Kind != ast.NamedType {
		return "", false
	}
	switch t.Name {
	case "rc":
		if len(t.Args) != 1 || t.Args[0].Kind != ast.NamedType {
			return "", false
		}
		return t.Args[0].Name, true
	case "Option", "Array", "Set":
		if len(t.Args) != 0 {
			return strongRcElem(t.Args[0])
		}
	case "Map":
		if len(t.Args) == 2 {
			return strongRcElem(t.Args[1])
		}
	}
	return "", false
}

func stronglyConnectedValue(graph map[string][]valueEdge) [][]string {
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
