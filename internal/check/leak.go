// This file owns M0004: an explicit leak gives one value to the process and is
// never released.
package check

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

// Leaks reports M0004 for every leak call. The warning is not an error; the
// release planner suppresses the owner's release so the value really leaks.
func Leaks(f *ast.File) []diag.Diagnostic {
	if f == nil {
		return nil
	}
	var bag diag.Bag
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		walkLeaks(fn.Body, func(call *ast.CallExpr) {
			bag.Warning(call.Pos(), diag.CodeExplicitLeak,
				"this value is explicitly leaked",
				"it will not be released")
		})
	}
	return bag.Diagnostics()
}

func walkLeaks(st ast.Stmt, visit func(*ast.CallExpr)) {
	if st == nil {
		return
	}
	var expr func(ast.Expr)
	expr = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
		case *ast.CallExpr:
			if name, ok := x.Callee.(*ast.NameExpr); ok && name.Name == "leak" {
				visit(x)
			}
			expr(x.Callee)
			for _, a := range x.Args {
				expr(a.Expr)
			}
		case *ast.MethodCallExpr:
			expr(x.Recv)
			for _, a := range x.Args {
				expr(a.Expr)
			}
		case *ast.FieldExpr:
			expr(x.Recv)
		case *ast.IndexExpr:
			expr(x.Recv)
			expr(x.Index)
		case *ast.SliceExpr:
			expr(x.Recv)
			expr(x.Lo)
			expr(x.Hi)
		case *ast.UnaryExpr:
			expr(x.X)
		case *ast.BinaryExpr:
			expr(x.L)
			expr(x.R)
		case *ast.NewExpr:
			for _, a := range x.Args {
				expr(a.Expr)
			}
		case *ast.AggregateExpr:
			for _, f := range x.Fields {
				expr(f.Value)
			}
		case *ast.RangeExpr:
			expr(x.Lo)
			expr(x.Hi)
		case *ast.TryExpr:
			expr(x.X)
		case *ast.AwaitExpr:
			expr(x.X)
		case *ast.UnwrapExpr:
			expr(x.X)
		case *ast.SpawnExpr:
			expr(x.Call)
		case *ast.InterpExpr:
			for _, p := range x.Parts {
				expr(p.Expr)
			}
		case *ast.LambdaExpr:
			for _, inner := range x.Body.Stmts {
				walkLeaks(inner, visit)
			}
		case *ast.IfExpr:
			walkLeaks(x, visit)
		case *ast.MatchExpr:
			walkLeaks(x, visit)
		case *ast.DeadlineExpr:
			walkLeaks(x, visit)
		}
	}
	switch s := st.(type) {
	case *ast.Block:
		for _, inner := range s.Stmts {
			walkLeaks(inner, visit)
		}
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			expr(d.Value)
		case *ast.ConstDecl:
			expr(d.Value)
		}
	case *ast.AssignStmt:
		expr(s.Target)
		expr(s.Value)
	case *ast.ExprStmt:
		expr(s.Expr)
	case *ast.DeferStmt:
		expr(s.Call)
	case *ast.ReturnStmt:
		expr(s.Value)
	case *ast.ForStmt:
		expr(s.Iter)
		walkLeaks(s.Body, visit)
	case *ast.RegionStmt:
		walkLeaks(s.Body, visit)
	case *ast.ParallelStmt:
		walkLeaks(s.Body, visit)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			expr(arm.Chan)
			expr(arm.Timeout)
			walkLeaks(arm.Body, visit)
		}
	case *ast.IfExpr:
		expr(s.Cond)
		walkLeaks(s.Then, visit)
		walkLeaks(s.Else, visit)
	case *ast.MatchExpr:
		expr(s.Subject)
		for _, arm := range s.Arms {
			expr(arm.Guard)
			walkLeaks(arm.Body, visit)
		}
	case *ast.DeadlineExpr:
		expr(s.Duration)
		walkLeaks(s.Body, visit)
	}
}
