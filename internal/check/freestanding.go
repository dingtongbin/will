// Package check reports the diagnostics that need more than one declaration:
// today the --freestanding rule of the build volume. Ownership, borrows, and
// release points will live here too.
package check

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

// runtimeMethodNames are the built-in methods that reach the runtime: channels,
// tasks, and the calls that release them.
var runtimeMethodNames = map[string]bool{
	"Join": true, "Send": true, "TrySend": true,
	"Recv": true, "TryRecv": true, "Close": true,
}

// runtimeBuiltinNames are the predefined functions that reach the runtime.
var runtimeBuiltinNames = map[string]bool{"panic": true, "assert": true, "print": true}

const (
	runtimeMessage = "the runtime is not available with --freestanding"
	runtimeHelp    = "remove this call, or drop --freestanding"
)

// Freestanding reports B0007 for every use of a runtime capability in f and
// for every import outside std/math. The build volume fixes the judgement: a
// call reaches willrt_* when it allocates or releases, starts a task or touches
// a channel, reads the clock or a poller, reports, or contains a panic. Every
// site is reported where the user wrote it, so calling a function that uses one
// is not reported a second time.
func Freestanding(f *ast.File) []diag.Diagnostic {
	c := &checker{file: f}
	c.imports()
	c.decls(f.Decls)
	return c.bag.Diagnostics()
}

type checker struct {
	file *ast.File
	bag  diag.Bag
}

func (c *checker) use(pos source.Pos) {
	c.bag.Error(pos, diag.CodeFreestanding, runtimeMessage, runtimeHelp)
}

// imports reports an import outside std/math.
func (c *checker) imports() {
	for _, imp := range c.file.Imports {
		if imp.C || imp.Path == "std/math" {
			continue
		}
		c.bag.Error(imp.Span.Start, diag.CodeFreestanding,
			"package "+imp.Path+" is not available with --freestanding",
			"only std/math is allowed; drop --freestanding to use it")
	}
}

func (c *checker) decls(decls []ast.Decl) {
	for _, d := range decls {
		switch decl := d.(type) {
		case *ast.ConstDecl:
			c.expr(decl.Value)
		case *ast.VarDecl:
			c.expr(decl.Value)
		case *ast.FuncDecl:
			c.block(decl.Body)
		case *ast.StructDecl:
			for _, field := range decl.Fields {
				c.expr(field.Default)
			}
		case *ast.LaneDecl:
			if decl.Decl != nil {
				c.decls([]ast.Decl{decl.Decl})
			}
		}
	}
}

func (c *checker) block(b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		c.stmt(s)
	}
}

func (c *checker) stmt(s ast.Stmt) {
	switch st := s.(type) {
	case *ast.Block:
		c.block(st)
	case *ast.VarStmt:
		switch d := st.Decl.(type) {
		case *ast.ConstDecl:
			c.expr(d.Value)
		case *ast.VarDecl:
			c.expr(d.Value)
		}
	case *ast.AssignStmt:
		c.expr(st.Target)
		c.expr(st.Value)
	case *ast.ExprStmt:
		c.expr(st.Expr)
	case *ast.DeferStmt:
		c.expr(st.Call)
	case *ast.ReturnStmt:
		c.expr(st.Value)
	case *ast.ForStmt:
		c.expr(st.Iter)
		c.block(st.Body)
	case *ast.RegionStmt:
		c.use(st.Pos())
		c.block(st.Body)
	case *ast.ParallelStmt:
		c.use(st.Pos())
		c.block(st.Body)
	case *ast.SelectStmt:
		c.use(st.Pos())
		for _, arm := range st.Arms {
			c.expr(arm.Chan)
			c.expr(arm.Timeout)
			c.stmt(arm.Body)
		}
	case *ast.IfExpr:
		c.expr(st.Cond)
		c.block(st.Then)
		if st.Else != nil {
			c.stmt(st.Else)
		}
	case *ast.MatchExpr:
		c.expr(st.Subject)
		for _, arm := range st.Arms {
			c.expr(arm.Guard)
			c.stmt(arm.Body)
		}
	case *ast.DeadlineExpr:
		c.use(st.Pos())
		c.expr(st.Duration)
		c.block(st.Body)
	case *ast.BreakStmt, *ast.ContinueStmt:
	}
}

func (c *checker) expr(e ast.Expr) {
	switch x := e.(type) {
	case nil, *ast.LiteralExpr, *ast.NameExpr, *ast.UnitExpr, *ast.LaneHereExpr:
	case *ast.NewExpr:
		c.use(x.Pos())
		for _, a := range x.Args {
			c.expr(a.Expr)
		}
	case *ast.CallExpr:
		if name, ok := x.Callee.(*ast.NameExpr); ok && runtimeBuiltinNames[name.Name] {
			c.use(x.Pos())
		}
		c.expr(x.Callee)
		for _, a := range x.Args {
			c.expr(a.Expr)
		}
	case *ast.MethodCallExpr:
		if runtimeMethodNames[x.Name] {
			c.use(x.NamePos)
		}
		c.expr(x.Recv)
		for _, a := range x.Args {
			c.expr(a.Expr)
		}
	case *ast.FieldExpr:
		c.expr(x.Recv)
	case *ast.IndexExpr:
		c.expr(x.Recv)
		c.expr(x.Index)
	case *ast.SliceExpr:
		c.expr(x.Recv)
		c.expr(x.Lo)
		c.expr(x.Hi)
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			c.expr(f.Value)
		}
	case *ast.UnaryExpr:
		c.expr(x.X)
	case *ast.BinaryExpr:
		c.expr(x.L)
		c.expr(x.R)
	case *ast.RangeExpr:
		c.expr(x.Lo)
		c.expr(x.Hi)
	case *ast.LambdaExpr:
		c.block(x.Body)
	case *ast.TryExpr:
		c.expr(x.X)
	case *ast.AwaitExpr:
		c.expr(x.X)
	case *ast.UnwrapExpr:
		c.expr(x.X)
	case *ast.SpawnExpr:
		c.use(x.Pos())
		c.expr(x.Call)
	case *ast.DeadlineExpr:
		c.stmt(x)
	case *ast.IfExpr:
		c.stmt(x)
	case *ast.MatchExpr:
		c.stmt(x)
	case *ast.InterpExpr:
		for _, part := range x.Parts {
			c.expr(part.Expr)
		}
	}
}
