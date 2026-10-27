package resolve

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/parser"
)

func mustResolve(t *testing.T, src string) (*Result, []diag.Diagnostic) {
	t.Helper()
	return mustResolveWith(t, src, nil)
}

// mustResolveWith resolves src with the given packages available, as a module
// run would; a path outside them is an unknown import path. tables maps an
// import path to the member names other packages export from it.
func mustResolveWith(t *testing.T, src string, tables map[string][]string) (*Result, []diag.Diagnostic) {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse diagnostics:\n%s", diag.FormatAll(ds))
	}
	res, rds := ResolveWith(f, importTables(tables))
	for _, d := range rds {
		if d.Severity == diag.Error {
			t.Fatalf("resolve errors:\n%s", diag.FormatAll(rds))
		}
	}
	return res, rds
}

// importTables builds the exported-name tables a run knows.
func importTables(tables map[string][]string) Imports {
	imports := Imports{}
	for path, members := range tables {
		exp := Exported{}
		for _, m := range members {
			exp[m] = &Symbol{Name: m, Kind: FuncKind, Exported: true}
		}
		imports[path] = exp
	}
	return imports
}

func TestResolveHappyPath(t *testing.T) {
	src := `import "std/net/http"
import _ "std/bytes"

const Limit: i32 = 100

struct Point {
    x: i32
}

def (mut p: Point) Move(dx: i32) {
    p.x += dx
}

def Run(n: i32) -> i32 {
    var xs: Array<i32> = new Array<i32>(0)
    var total: i32 = 0
    for i, x in xs {
        total += i + x
    }
    var q: Point = Point { x: 1 }
    q.Move(n)
    if n > 0 {
        total += Limit
    }
    return http.Status(n) + total
}
`
	res, ds := mustResolveWith(t, src, map[string][]string{
		"std/net/http": {"Status"},
		"std/bytes":    {},
	})
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want 0:\n%s", len(ds), diag.FormatAll(ds))
	}
	names := map[string]Kind{}
	for _, s := range res.Symbols {
		names[s.Name] = s.Kind
	}
	if names["Run"] != FuncKind || names["Move"] != MethodKind || names["Point"] != StructKind {
		t.Fatalf("symbols = %v", names)
	}
	if names["http"] != ImportKind || names["Limit"] != ConstKind {
		t.Fatalf("symbols = %v", names)
	}
}

func TestReferencesAreRecorded(t *testing.T) {
	src := `def F(n: i32) {
    var x: i32 = n
    _ = x
}
`
	res, _ := mustResolve(t, src)
	kinds := map[ast.Node]Kind{}
	for node, sym := range res.Refs {
		kinds[node] = sym.Kind
	}
	foundParam := false
	foundLocal := false
	foundType := false
	for _, kind := range kinds {
		switch kind {
		case ParamKind:
			foundParam = true
		case LocalKind:
			foundLocal = true
		case PredeclaredTypeKind:
			foundType = true
		}
	}
	if !foundParam || !foundLocal || !foundType {
		t.Fatalf("refs kinds = %v, want param, local, and predeclared type", kinds)
	}
}

func TestUnexportedTopLevelVarIsNotReportedAsUnused(t *testing.T) {
	src := `var counter: i32 = 0
def F() {}
`
	_, ds := mustResolve(t, src)
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want 0:\n%s", len(ds), diag.FormatAll(ds))
	}
}

func TestQualifiedTypeAndAggregate(t *testing.T) {
	src := `import "std/net/http"
def F() -> http.Ctx {
    var c: http.Ctx = http.Ctx { host: "x" }
    return c
}
`
	res, ds := mustResolveWith(t, src, map[string][]string{"std/net/http": {"Ctx"}})
	if len(ds) != 0 {
		t.Fatalf("diagnostics = %d, want 0:\n%s", len(ds), diag.FormatAll(ds))
	}
	qualified := 0
	for node, sym := range res.Refs {
		if sym.Kind != ImportKind {
			continue
		}
		switch node.(type) {
		case *ast.Type, *ast.AggregateExpr:
			qualified++
		}
	}
	if qualified < 2 {
		t.Fatalf("qualified refs = %d, want at least 2", qualified)
	}
}
