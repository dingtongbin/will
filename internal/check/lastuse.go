package check

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/source"
)

// lastUses records the last occurrence of every name in one block. It is a
// conservative lexical scan: a use inside a nested block is still a use of the
// outer name, while a declaration in the nested block does not escape.
func lastUses(b *ast.Block) map[string]source.Pos {
	out := map[string]source.Pos{}
	if b == nil {
		return out
	}
	var walkStmt func(ast.Stmt)
	var walkExpr func(ast.Expr)
	note := func(name string, pos source.Pos) {
		if name == "" || name == "_" {
			return
		}
		if prev, ok := out[name]; !ok || pos.Offset > prev.Offset {
			out[name] = pos
		}
	}
	walkStmt = func(st ast.Stmt) {
		switch s := st.(type) {
		case *ast.Block:
			for _, inner := range s.Stmts {
				walkStmt(inner)
			}
		case *ast.VarStmt:
			switch d := s.Decl.(type) {
			case *ast.VarDecl:
				walkExpr(d.Value)
			case *ast.ConstDecl:
				walkExpr(d.Value)
			}
		case *ast.AssignStmt:
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
			for _, inner := range s.Body.Stmts {
				walkStmt(inner)
			}
		case *ast.RegionStmt:
			for _, inner := range s.Body.Stmts {
				walkStmt(inner)
			}
		case *ast.ParallelStmt:
			for _, inner := range s.Body.Stmts {
				walkStmt(inner)
			}
		case *ast.SelectStmt:
			for _, arm := range s.Arms {
				walkExpr(arm.Chan)
				walkExpr(arm.Timeout)
				walkStmt(arm.Body)
			}
		case *ast.IfExpr:
			walkExpr(s.Cond)
			for _, inner := range s.Then.Stmts {
				walkStmt(inner)
			}
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
			for _, inner := range s.Body.Stmts {
				walkStmt(inner)
			}
		}
	}
	walkExpr = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
		case *ast.NameExpr:
			note(x.Name, x.Pos())
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
		case *ast.BorrowExpr:
			walkExpr(x.X)
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
		case *ast.ArrayLitExpr:
			for _, elem := range x.Elems {
				walkExpr(elem)
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
		case *ast.DeadlineExpr:
			walkStmt(x)
		case *ast.InterpExpr:
			for _, p := range x.Parts {
				walkExpr(p.Expr)
			}
		case *ast.IfExpr:
			walkStmt(x)
		case *ast.MatchExpr:
			walkStmt(x)
		case *ast.LambdaExpr:
			for _, inner := range x.Body.Stmts {
				walkStmt(inner)
			}
		}
	}
	for _, st := range b.Stmts {
		walkStmt(st)
	}
	return out
}
