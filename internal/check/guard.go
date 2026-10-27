// This file owns K0002: a guard must not live across a suspension point.
// The walk follows the block structure, so a guard declared in an inner block
// stops being active when that block ends; a suspension in a later block does
// not count against it.
package check

import (
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/marks"
	"github.com/will-lang/will/internal/source"
)

type guardBinding struct {
	start source.Pos
}

type guardWalker struct {
	checker *concurrencyChecker
	active  []guardBinding
}

// guardTypeText mirrors the type-name test in the types package; the check
// package works on the syntax tree, before a type is built.
func guardTypeText(t *ast.Type) bool {
	if t == nil {
		return false
	}
	if t.Kind == ast.NamedType {
		name := t.Name
		if name == "Guard" || name == "ReadGuard" ||
			strings.HasSuffix(name, "sync.Guard") || strings.HasSuffix(name, "sync.ReadGuard") {
			return true
		}
	}
	for _, arg := range t.Args {
		if guardTypeText(arg) {
			return true
		}
	}
	return guardTypeText(t.Elem) || guardTypeText(t.Result)
}

// guardAcrossSuspend reports one K0002 for every suspension point reached
// while a guard is in scope.
func (c *concurrencyChecker) guardAcrossSuspend(f *ast.File) {
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		g := &guardWalker{checker: c}
		g.block(fn.Body)
	}
}

func (g *guardWalker) block(b *ast.Block) {
	if b == nil {
		return
	}
	mark := len(g.active)
	for _, st := range b.Stmts {
		g.stmt(st)
	}
	g.active = g.active[:mark]
}

func (g *guardWalker) stmt(st ast.Stmt) {
	switch s := st.(type) {
	case *ast.Block:
		g.block(s)
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			g.expr(d.Value)
			if guardTypeText(d.Type) {
				g.active = append(g.active, guardBinding{start: d.Pos()})
			}
		case *ast.ConstDecl:
			g.expr(d.Value)
		}
	case *ast.AssignStmt:
		g.expr(s.Target)
		g.expr(s.Value)
	case *ast.ExprStmt:
		g.expr(s.Expr)
	case *ast.ReturnStmt:
		g.expr(s.Value)
	case *ast.DeferStmt:
		g.expr(s.Call)
	case *ast.ForStmt:
		g.expr(s.Iter)
		g.block(s.Body)
	case *ast.IfExpr:
		g.expr(s.Cond)
		g.block(s.Then)
		g.stmt(s.Else)
	case *ast.MatchExpr:
		g.expr(s.Subject)
		for _, arm := range s.Arms {
			g.expr(arm.Guard)
			g.stmt(arm.Body)
		}
	case *ast.DeadlineExpr:
		g.suspend(s.Pos())
		g.expr(s.Duration)
		g.block(s.Body)
	case *ast.SelectStmt:
		g.suspend(s.Pos())
		for _, arm := range s.Arms {
			g.expr(arm.Chan)
			g.expr(arm.Timeout)
			g.stmt(arm.Body)
		}
	case *ast.RegionStmt:
		g.block(s.Body)
	case *ast.ParallelStmt:
		g.block(s.Body)
	}
}

func (g *guardWalker) expr(e ast.Expr) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.CallExpr:
		if field, ok := x.Callee.(*ast.FieldExpr); ok && field.Name == "sleep" {
			g.suspend(x.Pos())
		}
		g.expr(x.Callee)
		for _, arg := range x.Args {
			g.expr(arg.Expr)
		}
	case *ast.MethodCallExpr:
		if marks.IsSuspendMethod(x.Name) || x.Name == "lock" ||
			x.Name == "r_lock" || x.Name == "w_lock" {
			g.suspend(x.Pos())
		}
		g.expr(x.Recv)
		for _, arg := range x.Args {
			g.expr(arg.Expr)
		}
	case *ast.FieldExpr:
		g.expr(x.Recv)
	case *ast.IndexExpr:
		g.expr(x.Recv)
		g.expr(x.Index)
	case *ast.SliceExpr:
		g.expr(x.Recv)
		g.expr(x.Lo)
		g.expr(x.Hi)
	case *ast.BorrowExpr:
		g.expr(x.X)
	case *ast.AggregateExpr:
		for _, field := range x.Fields {
			g.expr(field.Value)
		}
	case *ast.ArrayLitExpr:
		for _, elem := range x.Elems {
			g.expr(elem)
		}
	case *ast.NewExpr:
		for _, arg := range x.Args {
			g.expr(arg.Expr)
		}
	case *ast.UnaryExpr:
		g.expr(x.X)
	case *ast.BinaryExpr:
		g.expr(x.L)
		g.expr(x.R)
	case *ast.RangeExpr:
		g.expr(x.Lo)
		g.expr(x.Hi)
	case *ast.LambdaExpr:
		g.block(x.Body)
	case *ast.TryExpr:
		g.expr(x.X)
	case *ast.AwaitExpr:
		g.expr(x.X)
	case *ast.UnwrapExpr:
		g.expr(x.X)
	case *ast.SpawnExpr:
		// A spawn starts a task; it does not wait, so a guard in the caller
		// may stay active while the child runs.
		g.expr(x.Call)
	case *ast.DeadlineExpr:
		g.suspend(x.Pos())
		g.expr(x.Duration)
		g.block(x.Body)
	case *ast.IfExpr:
		g.expr(x.Cond)
		g.block(x.Then)
		g.stmt(x.Else)
	case *ast.MatchExpr:
		g.expr(x.Subject)
		for _, arm := range x.Arms {
			g.expr(arm.Guard)
			g.stmt(arm.Body)
		}
	}
}

// suspend reports K0002 when a guard created earlier is still in scope. The
// guard is released at the end of its block, so only a suspension after the
// declaration and inside that block counts.
func (g *guardWalker) suspend(pos source.Pos) {
	for _, b := range g.active {
		if b.start.Offset >= pos.Offset {
			continue
		}
		g.checker.bag.Error(pos, diag.CodeGuardAcrossSuspend,
			"the guard is held across a suspension point",
			"release it before suspending")
		return
	}
}
