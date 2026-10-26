// Package check reports the diagnostics that need more than one declaration.
// This file owns the rule that a parallel block may not suspend.
package check

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

// SuspendInParallel reports K0001 for a wait inside a parallel block. A spawn
// is the only operation that may run concurrently inside that block; waiting
// for a channel, a task, a sleep, or an Async IO call is not allowed.
func SuspendInParallel(f *ast.File) []diag.Diagnostic {
	c := &parallelChecker{}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
			c.block(fn.Body)
		}
	}
	return c.bag.Diagnostics()
}

type parallelChecker struct {
	bag   diag.Bag
	depth int
}

func (c *parallelChecker) suspend(pos ast.Span) {
	if c.depth == 0 {
		return
	}
	c.bag.Error(pos.Pos(), diag.CodeSuspendInParallel,
		"a parallel block cannot suspend",
		"use spawn and Join for CPU work, or move the wait outside the block")
}

func (c *parallelChecker) block(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		c.stmt(st)
	}
}

func (c *parallelChecker) stmt(st ast.Stmt) {
	switch s := st.(type) {
	case *ast.VarStmt:
		c.exprDecl(s.Decl)
	case *ast.AssignStmt:
		c.expr(s.Target)
		c.expr(s.Value)
	case *ast.ExprStmt:
		c.expr(s.Expr)
	case *ast.DeferStmt:
		c.expr(s.Call)
	case *ast.ReturnStmt:
		c.expr(s.Value)
	case *ast.ForStmt:
		c.expr(s.Iter)
		c.block(s.Body)
	case *ast.RegionStmt:
		c.block(s.Body)
	case *ast.ParallelStmt:
		c.depth++
		c.block(s.Body)
		c.depth--
	case *ast.SelectStmt:
		c.suspend(s.Span)
		for _, arm := range s.Arms {
			c.expr(arm.Chan)
			c.expr(arm.Timeout)
			c.stmt(arm.Body)
		}
	case *ast.IfExpr:
		c.expr(s.Cond)
		c.block(s.Then)
		c.stmt(s.Else)
	case *ast.MatchExpr:
		c.expr(s.Subject)
		for _, arm := range s.Arms {
			c.expr(arm.Guard)
			c.stmt(arm.Body)
		}
	case *ast.DeadlineExpr:
		c.expr(s.Duration)
		c.block(s.Body)
	}
}

func (c *parallelChecker) exprDecl(d ast.Decl) {
	switch v := d.(type) {
	case *ast.VarDecl:
		c.expr(v.Value)
	case *ast.ConstDecl:
		c.expr(v.Value)
	}
}

func (c *parallelChecker) expr(e ast.Expr) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.CallExpr:
		if name, ok := x.Callee.(*ast.NameExpr); ok && name.Name == "wait" {
			c.suspend(x.Span)
		}
		c.expr(x.Callee)
		c.args(x.Args)
	case *ast.MethodCallExpr:
		if isSuspendMethod(x.Name) {
			c.suspend(x.Span)
		}
		c.expr(x.Recv)
		c.args(x.Args)
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
	case *ast.ArrayLitExpr:
		for _, elem := range x.Elems {
			c.expr(elem)
		}
	case *ast.NewExpr:
		c.args(x.Args)
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
		c.expr(x.Call)
	case *ast.DeadlineExpr:
		c.expr(x.Duration)
		c.block(x.Body)
	case *ast.IfExpr:
		c.expr(x.Cond)
		c.block(x.Then)
		c.stmt(x.Else)
	case *ast.MatchExpr:
		c.expr(x.Subject)
		for _, arm := range x.Arms {
			c.expr(arm.Guard)
			c.stmt(arm.Body)
		}
	case *ast.InterpExpr:
		for _, part := range x.Parts {
			c.expr(part.Expr)
		}
	}
}

func (c *parallelChecker) args(args []*ast.CallArg) {
	for _, a := range args {
		c.expr(a.Expr)
	}
}

func isSuspendMethod(name string) bool {
	switch name {
	case "Recv", "Send", "Sleep", "AsyncRead", "AsyncWrite", "AsyncFlush",
		"AsyncSeek", "AsyncSync", "AsyncStat", "AsyncNext":
		return true
	}
	return len(name) > 5 && name[:5] == "Async"
}
