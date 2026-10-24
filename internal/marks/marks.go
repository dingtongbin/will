// Package marks records the concurrency-relevant shapes of a source file:
// which functions contain suspension points, where the regions, spawns, and
// wait bridges are. It is data for the check package and the generator; it
// decides nothing by itself.
package marks

import (
	"github.com/will-lang/will/internal/ast"
)

// Func holds the marks of one function body.
type Func struct {
	Name     string
	Async    bool
	Suspends []ast.Span
	Waits    []ast.Span
	Spawns   []*ast.SpawnExpr
	Regions  []*ast.RegionStmt
}

// File holds the marks of one source file.
type File struct {
	Funcs []*Func
}

// Analyze walks one file and collects the marks. A suspension point is a
// Join, Send, or Recv call, a select, a deadline block, a wait bridge, a
// time.Sleep, or a call whose method name starts with Async, which is the
// language's marker for the async half of a paired IO operation.
func Analyze(f *ast.File) *File {
	m := &File{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		mf := &Func{Name: fn.Name, Async: fn.Async}
		walkBlock(fn.Body, mf)
		if len(mf.Suspends) != 0 || len(mf.Waits) != 0 ||
			len(mf.Spawns) != 0 || len(mf.Regions) != 0 {
			m.Funcs = append(m.Funcs, mf)
		}
	}
	return m
}

func walkBlock(b *ast.Block, mf *Func) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		walkStmt(st, mf)
	}
}

func walkStmt(st ast.Stmt, mf *Func) {
	switch s := st.(type) {
	case *ast.VarStmt:
		walkDecl(s.Decl, mf)
	case *ast.AssignStmt:
		walkExpr(s.Target, mf)
		walkExpr(s.Value, mf)
	case *ast.ExprStmt:
		walkExpr(s.Expr, mf)
	case *ast.DeferStmt:
		walkExpr(s.Call, mf)
	case *ast.ReturnStmt:
		walkExpr(s.Value, mf)
	case *ast.ForStmt:
		walkExpr(s.Iter, mf)
		walkBlock(s.Body, mf)
	case *ast.RegionStmt:
		mf.Regions = append(mf.Regions, s)
		walkBlock(s.Body, mf)
	case *ast.ParallelStmt:
		walkBlock(s.Body, mf)
	case *ast.SelectStmt:
		mf.Suspends = append(mf.Suspends, s.Span)
		for _, arm := range s.Arms {
			walkExpr(arm.Chan, mf)
			walkExpr(arm.Timeout, mf)
			walkStmt(arm.Body, mf)
		}
	case *ast.IfExpr:
		walkExpr(s.Cond, mf)
		walkBlock(s.Then, mf)
		walkStmt(s.Else, mf)
	case *ast.MatchExpr:
		walkExpr(s.Subject, mf)
		for _, arm := range s.Arms {
			walkExpr(arm.Guard, mf)
			walkStmt(arm.Body, mf)
		}
	case *ast.DeadlineExpr:
		mf.Suspends = append(mf.Suspends, s.Span)
		walkExpr(s.Duration, mf)
		walkBlock(s.Body, mf)
	}
}

func walkDecl(d ast.Decl, mf *Func) {
	switch v := d.(type) {
	case *ast.VarDecl:
		walkExpr(v.Value, mf)
	case *ast.ConstDecl:
		walkExpr(v.Value, mf)
	}
}

func walkExpr(e ast.Expr, mf *Func) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.CallExpr:
		if name, ok := x.Callee.(*ast.NameExpr); ok && name.Name == "wait" {
			mf.Waits = append(mf.Waits, x.Span)
			mf.Suspends = append(mf.Suspends, x.Span)
		}
		if name, ok := x.Callee.(*ast.FieldExpr); ok && isQualifiedName(name, "time", "Sleep") {
			mf.Suspends = append(mf.Suspends, x.Span)
		}
		walkExpr(x.Callee, mf)
		walkArgs(x.Args, mf)
	case *ast.MethodCallExpr:
		if IsSuspendMethod(x.Name) {
			mf.Suspends = append(mf.Suspends, x.Span)
		}
		walkExpr(x.Recv, mf)
		walkArgs(x.Args, mf)
	case *ast.FieldExpr:
		walkExpr(x.Recv, mf)
	case *ast.IndexExpr:
		walkExpr(x.Recv, mf)
		walkExpr(x.Index, mf)
	case *ast.SliceExpr:
		walkExpr(x.Recv, mf)
		walkExpr(x.Lo, mf)
		walkExpr(x.Hi, mf)
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			walkExpr(f.Value, mf)
		}
	case *ast.NewExpr:
		walkArgs(x.Args, mf)
	case *ast.UnaryExpr:
		walkExpr(x.X, mf)
	case *ast.BinaryExpr:
		walkExpr(x.L, mf)
		walkExpr(x.R, mf)
	case *ast.RangeExpr:
		walkExpr(x.Lo, mf)
		walkExpr(x.Hi, mf)
	case *ast.LambdaExpr:
		walkBlock(x.Body, mf)
	case *ast.TryExpr:
		walkExpr(x.X, mf)
	case *ast.AwaitExpr:
		mf.Suspends = append(mf.Suspends, x.Span)
		walkExpr(x.X, mf)
	case *ast.UnwrapExpr:
		walkExpr(x.X, mf)
	case *ast.SpawnExpr:
		mf.Spawns = append(mf.Spawns, x)
		walkExpr(x.Call, mf)
	case *ast.DeadlineExpr:
		mf.Suspends = append(mf.Suspends, x.Span)
		walkExpr(x.Duration, mf)
		walkBlock(x.Body, mf)
	case *ast.IfExpr:
		walkExpr(x.Cond, mf)
		walkBlock(x.Then, mf)
		walkStmt(x.Else, mf)
	case *ast.MatchExpr:
		walkExpr(x.Subject, mf)
		for _, arm := range x.Arms {
			walkExpr(arm.Guard, mf)
			walkStmt(arm.Body, mf)
		}
	case *ast.InterpExpr:
		for _, part := range x.Parts {
			walkExpr(part.Expr, mf)
		}
	}
}

func walkArgs(args []*ast.CallArg, mf *Func) {
	for _, a := range args {
		walkExpr(a.Expr, mf)
	}
}

func isQualifiedName(f *ast.FieldExpr, pkg, name string) bool {
	n, ok := f.Recv.(*ast.NameExpr)
	return ok && n.Name == pkg && f.Name == name
}

// IsSuspendMethod reports whether a method call of this name suspends. The
// Async prefix is the language's marker for the async half of a paired IO
// operation per the io volume of the design docs; Join, Send, and Recv are
// the wait primitives themselves.
func IsSuspendMethod(name string) bool {
	switch name {
	case "Join", "Recv", "Send", "Sleep", "AsyncRead", "AsyncWrite",
		"AsyncFlush", "AsyncSeek", "AsyncSync", "AsyncStat", "AsyncNext":
		return true
	}
	return len(name) > 5 && name[:5] == "Async"
}
