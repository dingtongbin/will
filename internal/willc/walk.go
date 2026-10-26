package willc

import "github.com/will-lang/will/internal/ast"

// walkStmtExprs visits the expressions that a statement evaluates itself, not
// the statements of a nested block: those are emitted with their own scope.
func walkStmtExprs(s ast.Stmt, visit func(ast.Expr)) {
	switch st := s.(type) {
	case *ast.VarStmt:
		switch d := st.Decl.(type) {
		case *ast.VarDecl:
			walkExprs(d.Value, visit)
		case *ast.ConstDecl:
			walkExprs(d.Value, visit)
		}
	case *ast.AssignStmt:
		walkExprs(st.Target, visit)
		walkExprs(st.Value, visit)
	case *ast.ExprStmt:
		walkExprs(st.Expr, visit)
	case *ast.DeferStmt:
		walkExprs(st.Call, visit)
	case *ast.ReturnStmt:
		walkExprs(st.Value, visit)
	case *ast.ForStmt:
		walkExprs(st.Iter, visit)
	case *ast.SelectStmt:
		for _, arm := range st.Arms {
			walkExprs(arm.Chan, visit)
			walkExprs(arm.Timeout, visit)
		}
	case *ast.IfExpr:
		walkExprs(st.Cond, visit)
	case *ast.MatchExpr:
		walkExprs(st.Subject, visit)
		for _, arm := range st.Arms {
			walkExprs(arm.Guard, visit)
		}
	case *ast.DeadlineExpr:
		walkExprs(st.Duration, visit)
	}
}

// walkExprs visits one expression tree. A lambda body is a separate scope and
// is not visited here.
func walkExprs(e ast.Expr, visit func(ast.Expr)) {
	if e == nil {
		return
	}
	visit(e)
	switch x := e.(type) {
	case *ast.CallExpr:
		walkExprs(x.Callee, visit)
		for _, a := range x.Args {
			walkExprs(a.Expr, visit)
		}
	case *ast.MethodCallExpr:
		walkExprs(x.Recv, visit)
		for _, a := range x.Args {
			walkExprs(a.Expr, visit)
		}
	case *ast.FieldExpr:
		walkExprs(x.Recv, visit)
	case *ast.IndexExpr:
		walkExprs(x.Recv, visit)
		walkExprs(x.Index, visit)
	case *ast.SliceExpr:
		walkExprs(x.Recv, visit)
		walkExprs(x.Lo, visit)
		walkExprs(x.Hi, visit)
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			walkExprs(f.Value, visit)
		}
	case *ast.ArrayLitExpr:
		for _, elem := range x.Elems {
			walkExprs(elem, visit)
		}
	case *ast.NewExpr:
		for _, a := range x.Args {
			walkExprs(a.Expr, visit)
		}
	case *ast.UnaryExpr:
		walkExprs(x.X, visit)
	case *ast.BinaryExpr:
		walkExprs(x.L, visit)
		walkExprs(x.R, visit)
	case *ast.RangeExpr:
		walkExprs(x.Lo, visit)
		walkExprs(x.Hi, visit)
	case *ast.TryExpr:
		walkExprs(x.X, visit)
	case *ast.AwaitExpr:
		walkExprs(x.X, visit)
	case *ast.UnwrapExpr:
		walkExprs(x.X, visit)
	case *ast.SpawnExpr:
		walkExprs(x.Call, visit)
	case *ast.DeadlineExpr:
		walkExprs(x.Duration, visit)
	case *ast.IfExpr:
		walkExprs(x.Cond, visit)
	case *ast.MatchExpr:
		walkExprs(x.Subject, visit)
		for _, arm := range x.Arms {
			walkExprs(arm.Guard, visit)
		}
	case *ast.InterpExpr:
		for _, part := range x.Parts {
			walkExprs(part.Expr, visit)
		}
	}
}
