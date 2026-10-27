package types

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

// checkCrossTypes rejects the two predeclared containers whose payload must
// cross a task boundary by construction: a channel element and a task result.
func (c *checker) checkCrossTypes(files []*ast.File) {
	probe := &Result{env: c.env}
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, d := range f.Decls {
			c.crossDecl(probe, d)
		}
	}
}

func (c *checker) crossDecl(probe *Result, d ast.Decl) {
	switch decl := d.(type) {
	case *ast.StructDecl:
		for _, f := range decl.Fields {
			c.crossType(probe, f.Type)
			c.crossExpr(probe, f.Default)
		}
	case *ast.EnumDecl:
		for _, v := range decl.Variants {
			for _, t := range v.Args {
				c.crossType(probe, t)
			}
		}
	case *ast.InterfaceDecl:
		for _, m := range decl.Methods {
			for _, p := range m.Params {
				c.crossType(probe, p.Type)
			}
			c.crossType(probe, m.Result)
		}
	case *ast.FuncDecl:
		if decl.Recv != nil {
			c.crossType(probe, decl.Recv.Type)
		}
		for _, p := range decl.Params {
			c.crossType(probe, p.Type)
		}
		c.crossType(probe, decl.Result)
		c.crossBlock(probe, decl.Body)
	case *ast.VarDecl:
		c.crossType(probe, decl.Type)
		c.crossExpr(probe, decl.Value)
	case *ast.ConstDecl:
		c.crossType(probe, decl.Type)
		c.crossExpr(probe, decl.Value)
	case *ast.TypeAliasDecl:
		c.crossType(probe, decl.Type)
	}
}

func (c *checker) crossBlock(probe *Result, b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		c.crossStmt(probe, st)
	}
}

func (c *checker) crossStmt(probe *Result, st ast.Stmt) {
	switch s := st.(type) {
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			c.crossDecl(probe, d)
		case *ast.ConstDecl:
			c.crossDecl(probe, d)
		}
	case *ast.AssignStmt:
		c.crossExpr(probe, s.Target)
		c.crossExpr(probe, s.Value)
	case *ast.ExprStmt:
		c.crossExpr(probe, s.Expr)
	case *ast.DeferStmt:
		c.crossExpr(probe, s.Call)
	case *ast.ReturnStmt:
		c.crossExpr(probe, s.Value)
	case *ast.Block:
		c.crossBlock(probe, s)
	case *ast.ForStmt:
		c.crossExpr(probe, s.Iter)
		c.crossBlock(probe, s.Body)
	case *ast.RegionStmt:
		c.crossBlock(probe, s.Body)
	case *ast.ParallelStmt:
		c.crossBlock(probe, s.Body)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			c.crossExpr(probe, arm.Chan)
			c.crossExpr(probe, arm.Timeout)
			c.crossStmt(probe, arm.Body)
		}
	case *ast.IfExpr:
		c.crossExpr(probe, s.Cond)
		c.crossBlock(probe, s.Then)
		c.crossStmt(probe, s.Else)
	case *ast.MatchExpr:
		c.crossExpr(probe, s.Subject)
		for _, arm := range s.Arms {
			c.crossStmt(probe, arm.Body)
		}
	case *ast.DeadlineExpr:
		c.crossExpr(probe, s.Duration)
		c.crossBlock(probe, s.Body)
	}
}

func (c *checker) crossExpr(probe *Result, e ast.Expr) {
	switch x := e.(type) {
	case nil:
	case *ast.CallExpr:
		for _, t := range x.TypeArgs {
			c.crossType(probe, t)
		}
		c.crossExpr(probe, x.Callee)
		for _, a := range x.Args {
			c.crossExpr(probe, a.Expr)
		}
	case *ast.MethodCallExpr:
		c.crossExpr(probe, x.Recv)
		for _, t := range x.TypeArgs {
			c.crossType(probe, t)
		}
		for _, a := range x.Args {
			c.crossExpr(probe, a.Expr)
		}
	case *ast.NewExpr:
		c.crossType(probe, x.Type)
		for _, a := range x.Args {
			c.crossExpr(probe, a.Expr)
		}
	case *ast.LambdaExpr:
		c.crossBlock(probe, x.Body)
	case *ast.UnaryExpr:
		c.crossExpr(probe, x.X)
	case *ast.BinaryExpr:
		c.crossExpr(probe, x.L)
		c.crossExpr(probe, x.R)
	case *ast.FieldExpr:
		c.crossExpr(probe, x.Recv)
	case *ast.IndexExpr:
		c.crossExpr(probe, x.Recv)
		c.crossExpr(probe, x.Index)
	case *ast.SliceExpr:
		c.crossExpr(probe, x.Recv)
		c.crossExpr(probe, x.Lo)
		c.crossExpr(probe, x.Hi)
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			c.crossExpr(probe, f.Value)
		}
	case *ast.ArrayLitExpr:
		for _, elem := range x.Elems {
			c.crossExpr(probe, elem)
		}
	case *ast.InterpExpr:
		for _, p := range x.Parts {
			c.crossExpr(probe, p.Expr)
		}
	case *ast.TryExpr:
		c.crossExpr(probe, x.X)
	case *ast.AwaitExpr:
		c.crossExpr(probe, x.X)
	case *ast.UnwrapExpr:
		c.crossExpr(probe, x.X)
	case *ast.SpawnExpr:
		c.crossExpr(probe, x.Call)
	case *ast.IfExpr:
		c.crossStmt(probe, x)
	case *ast.MatchExpr:
		c.crossStmt(probe, x)
	case *ast.DeadlineExpr:
		c.crossStmt(probe, x)
	}
}

// crossType walks one written type and checks the payload of chan and Task.
func (c *checker) crossType(probe *Result, t *ast.Type) {
	if t == nil {
		return
	}
	if t.Kind == ast.NamedType {
		switch t.Name {
		case "chan":
			if len(t.Args) == 1 && !probe.send(c.buildType(t.Args[0], nil), map[string]bool{}) {
				c.bag.Error(t.Pos(), diag.CodeCrossTask,
					"channel element type cannot cross a task boundary",
					"use a sendable element type")
			}
		case "Task":
			if len(t.Args) == 1 && !probe.send(c.buildType(t.Args[0], nil), map[string]bool{}) {
				c.bag.Error(t.Pos(), diag.CodeCrossTask,
					"task result type cannot cross a task boundary",
					"use a sendable result type")
			}
		}
		for _, a := range t.Args {
			c.crossType(probe, a)
		}
	}
	if t.Elem != nil {
		c.crossType(probe, t.Elem)
	}
	if t.Result != nil {
		c.crossType(probe, t.Result)
	}
	for _, p := range t.Params {
		c.crossType(probe, p)
	}
}
