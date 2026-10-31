package cli

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/decisions"
)

// collectObligations appends one index obligation for every loop that iterates
// a container. The loop borrows the container for its whole run, so its length
// cannot change and the index can never leave the range: the check is proved
// and the lowering emits no bound test. A Range loop is its own index sequence
// and has no container to prove, so it records nothing.
func collectObligations(f *ast.File, out *[]decisions.Record) {
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			walkObligationBlock(decl.Body, out)
		case *ast.LaneDecl:
			if fn, ok := decl.Decl.(*ast.FuncDecl); ok {
				walkObligationBlock(fn.Body, out)
			}
		}
	}
}

// walkObligationBlock visits the statements of one block in source order.
func walkObligationBlock(b *ast.Block, out *[]decisions.Record) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkObligationStmt(s, out)
	}
}

// walkObligationStmt visits one statement and everything nested in it.
func walkObligationStmt(s ast.Stmt, out *[]decisions.Record) {
	switch st := s.(type) {
	case *ast.Block:
		walkObligationBlock(st, out)
	case *ast.ForStmt:
		if !isRangeIter(st.Iter) {
			*out = append(*out, decisions.Obligation(st.Pos().String(), "index", "proved", "range"))
		}
		walkObligationExpr(st.Iter, out)
		walkObligationBlock(st.Body, out)
	case *ast.IfExpr:
		walkObligationExpr(st, out)
	case *ast.MatchExpr:
		walkObligationExpr(st, out)
	case *ast.DeadlineExpr:
		walkObligationExpr(st, out)
	case *ast.RegionStmt:
		walkObligationBlock(st.Body, out)
	case *ast.SelectStmt:
		for _, arm := range st.Arms {
			walkObligationExpr(arm.Chan, out)
			walkObligationExpr(arm.Timeout, out)
			walkObligationStmt(arm.Body, out)
		}
	case *ast.ExprStmt:
		walkObligationExpr(st.Expr, out)
	case *ast.VarStmt:
		if decl, ok := st.Decl.(*ast.VarDecl); ok {
			walkObligationExpr(decl.Value, out)
		}
	case *ast.AssignStmt:
		walkObligationExpr(st.Target, out)
		walkObligationExpr(st.Value, out)
	case *ast.ReturnStmt:
		walkObligationExpr(st.Value, out)
	case *ast.DeferStmt:
		walkObligationExpr(st.Call, out)
	}
}

// walkObligationExpr visits the expressions that can carry a block, which is
// the only place a loop can be nested in.
func walkObligationExpr(e ast.Expr, out *[]decisions.Record) {
	switch x := e.(type) {
	case nil:
		return
	case *ast.CallExpr:
		walkObligationExpr(x.Callee, out)
		for _, a := range x.Args {
			walkObligationExpr(a.Expr, out)
		}
	case *ast.MethodCallExpr:
		walkObligationExpr(x.Recv, out)
		for _, a := range x.Args {
			walkObligationExpr(a.Expr, out)
		}
	case *ast.FieldExpr:
		walkObligationExpr(x.Recv, out)
	case *ast.IndexExpr:
		walkObligationExpr(x.Recv, out)
		walkObligationExpr(x.Index, out)
	case *ast.SliceExpr:
		walkObligationExpr(x.Recv, out)
		walkObligationExpr(x.Lo, out)
		walkObligationExpr(x.Hi, out)
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			walkObligationExpr(f.Value, out)
		}
	case *ast.UnaryExpr:
		walkObligationExpr(x.X, out)
	case *ast.BinaryExpr:
		walkObligationExpr(x.L, out)
		walkObligationExpr(x.R, out)
	case *ast.RangeExpr:
		walkObligationExpr(x.Lo, out)
		walkObligationExpr(x.Hi, out)
	case *ast.LambdaExpr:
		walkObligationBlock(x.Body, out)
	case *ast.TryExpr:
		walkObligationExpr(x.X, out)
	case *ast.AwaitExpr:
		walkObligationExpr(x.X, out)
	case *ast.UnwrapExpr:
		walkObligationExpr(x.X, out)
	case *ast.SpawnExpr:
		walkObligationExpr(x.Call, out)
	case *ast.DeadlineExpr:
		walkObligationExpr(x.Duration, out)
		walkObligationBlock(x.Body, out)
	case *ast.IfExpr:
		walkObligationExpr(x.Cond, out)
		walkObligationBlock(x.Then, out)
		walkObligationStmt(x.Else, out)
	case *ast.MatchExpr:
		walkObligationExpr(x.Subject, out)
		for _, arm := range x.Arms {
			walkObligationExpr(arm.Guard, out)
			walkObligationStmt(arm.Body, out)
		}
	case *ast.InterpExpr:
		for _, p := range x.Parts {
			walkObligationExpr(p.Expr, out)
		}
	}
}

// isRangeIter reports whether a loop iterates Range(lo, hi). The parser makes
// a range expression for the call, and a hand-written call node is accepted
// too.
func isRangeIter(e ast.Expr) bool {
	if _, ok := e.(*ast.RangeExpr); ok {
		return true
	}
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	name, ok := call.Callee.(*ast.NameExpr)
	return ok && name.Name == "Range"
}
