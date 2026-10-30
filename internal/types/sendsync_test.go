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
class SharedBox { n: Plain }
struct Shared { n: SharedBox }
struct Open { f: func() -> i32 }

def f(a: Plain, c: Shared, e: Open, task: Task<Plain>, ch: chan<Plain>) {
    var plain = a
    var shared = c
    var open = e
    var taskCopy = task
    var chCopy = ch
    _ = plain
    _ = shared
    _ = open
    _ = taskCopy
    _ = chCopy
}
`
	got, plain := checkedExpr(t, src, "plain")
	if !got.Send(got.TypeOf(plain)) || !got.Sync(got.TypeOf(plain)) {
		t.Fatalf("Plain must be Send and Sync")
	}
	got, shared := checkedExpr(t, src, "shared")
	if !got.Send(got.TypeOf(shared)) || !got.Sync(got.TypeOf(shared)) {
		t.Fatalf("Shared holds a class reference and must cross a task")
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
