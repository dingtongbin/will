package types

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/parser"
	"github.com/will-lang/will/internal/resolve"
)

func checkedExpr(t *testing.T, src, name string) (*Result, *ast.NameExpr) {
	t.Helper()
	f, ds := parser.Parse("test.will", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("parse: %v", ds)
	}
	res, rds := resolve.Resolve(f)
	if len(rds) != 0 {
		t.Fatalf("resolve: %v", rds)
	}
	got, tds := CheckFull(f, res)
	if len(tds) != 0 {
		t.Fatalf("check: %v", tds)
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, st := range fn.Body.Stmts {
			vs, ok := st.(*ast.VarStmt)
			if !ok {
				continue
			}
			decl, ok := vs.Decl.(*ast.VarDecl)
			if ok && decl.Name == name {
				return got, decl.Value.(*ast.NameExpr)
			}
		}
	}
	t.Fatalf("no local %s", name)
	return nil, nil
}

func TestSendSyncIsStructural(t *testing.T) {
	src := `struct Plain { n: i64 }
struct Local { n: rc<Plain> }
struct Shared { n: arc<Plain> }
struct Mixed { n: arc<rc<Plain>> }
struct Open { f: func() -> i32 }

def f(a: Plain, b: Local, c: Shared, d: Mixed, e: Open, task: Task<Plain>, ch: chan<Plain>) {
    var plain = a
    var local = b
    var shared = c
    var mixed = d
    var open = e
    var taskCopy = task
    var chCopy = ch
    _ = plain
    _ = local
    _ = shared
    _ = mixed
    _ = open
    _ = taskCopy
    _ = chCopy
}
`
	got, plain := checkedExpr(t, src, "plain")
	if !got.Send(got.TypeOf(plain)) || !got.Sync(got.TypeOf(plain)) {
		t.Fatalf("Plain must be Send and Sync")
	}
	got, local := checkedExpr(t, src, "local")
	if got.Send(got.TypeOf(local)) || got.Sync(got.TypeOf(local)) {
		t.Fatalf("Local holds rc and must not cross a task")
	}
	got, shared := checkedExpr(t, src, "shared")
	if !got.Send(got.TypeOf(shared)) || !got.Sync(got.TypeOf(shared)) {
		t.Fatalf("Shared holds arc<Plain> and must cross a task")
	}
	got, mixed := checkedExpr(t, src, "mixed")
	if got.Send(got.TypeOf(mixed)) || got.Sync(got.TypeOf(mixed)) {
		t.Fatalf("Mixed holds arc<rc<Plain>> and must not cross a task")
	}
	got, task := checkedExpr(t, src, "taskCopy")
	if !got.Send(got.TypeOf(task)) || got.Sync(got.TypeOf(task)) {
		t.Fatalf("Task<Plain> must be Send but not Sync")
	}
	got, ch := checkedExpr(t, src, "chCopy")
	if !got.Send(got.TypeOf(ch)) || !got.Sync(got.TypeOf(ch)) {
		t.Fatalf("chan<Plain> must be Send and Sync")
	}
}
