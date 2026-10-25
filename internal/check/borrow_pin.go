package check

import "github.com/will-lang/will/internal/ast"

// blockPins reports whether a block contains a construct that can run later
// than the statement that mentions it: deferred calls, closures, or spawned
// captures. The borrow analysis keeps views in such a block live to the block
// end instead of pruning at the last lexical mention.
func blockPins(b *ast.Block) bool {
	if b == nil {
		return false
	}
	var pinStmt func(ast.Stmt) bool
	var pinExpr func(ast.Expr) bool
	pinStmt = func(st ast.Stmt) bool {
		switch s := st.(type) {
		case *ast.Block:
			for _, inner := range s.Stmts {
				if pinStmt(inner) {
					return true
				}
			}
		case *ast.VarStmt:
			switch d := s.Decl.(type) {
			case *ast.VarDecl:
				return pinExpr(d.Value)
			case *ast.ConstDecl:
				return pinExpr(d.Value)
			}
		case *ast.AssignStmt:
			return pinExpr(s.Target) || pinExpr(s.Value)
		case *ast.ExprStmt:
			return pinExpr(s.Expr)
		case *ast.DeferStmt:
			return true
		case *ast.ReturnStmt:
			return pinExpr(s.Value)
		case *ast.ForStmt:
			if pinExpr(s.Iter) {
				return true
			}
			for _, inner := range s.Body.Stmts {
				if pinStmt(inner) {
					return true
				}
			}
		case *ast.RegionStmt:
			for _, inner := range s.Body.Stmts {
				if pinStmt(inner) {
					return true
				}
			}
		case *ast.ParallelStmt:
			for _, inner := range s.Body.Stmts {
				if pinStmt(inner) {
					return true
				}
			}
		case *ast.SelectStmt:
			for _, arm := range s.Arms {
				if pinExpr(arm.Chan) || pinExpr(arm.Timeout) || pinStmt(arm.Body) {
					return true
				}
			}
		case *ast.IfExpr:
			if pinExpr(s.Cond) {
				return true
			}
			for _, inner := range s.Then.Stmts {
				if pinStmt(inner) {
					return true
				}
			}
			if s.Else != nil {
				return pinStmt(s.Else)
			}
		case *ast.MatchExpr:
			if pinExpr(s.Subject) {
				return true
			}
			for _, arm := range s.Arms {
				if pinExpr(arm.Guard) || pinStmt(arm.Body) {
					return true
				}
			}
		case *ast.DeadlineExpr:
			if pinExpr(s.Duration) {
				return true
			}
			for _, inner := range s.Body.Stmts {
				if pinStmt(inner) {
					return true
				}
			}
		}
		return false
	}
	pinExpr = func(e ast.Expr) bool {
		switch x := e.(type) {
		case nil, *ast.NameExpr, *ast.LiteralExpr, *ast.UnitExpr, *ast.LaneHereExpr:
			return false
		case *ast.CallExpr:
			if pinExpr(x.Callee) {
				return true
			}
			for _, a := range x.Args {
				if pinExpr(a.Expr) {
					return true
				}
			}
		case *ast.MethodCallExpr:
			if pinExpr(x.Recv) {
				return true
			}
			for _, a := range x.Args {
				if pinExpr(a.Expr) {
					return true
				}
			}
		case *ast.FieldExpr:
			return pinExpr(x.Recv)
		case *ast.IndexExpr:
			return pinExpr(x.Recv) || pinExpr(x.Index)
		case *ast.SliceExpr:
			return pinExpr(x.Recv) || pinExpr(x.Lo) || pinExpr(x.Hi)
		case *ast.UnaryExpr:
			return pinExpr(x.X)
		case *ast.BinaryExpr:
			return pinExpr(x.L) || pinExpr(x.R)
		case *ast.NewExpr:
			for _, a := range x.Args {
				if pinExpr(a.Expr) {
					return true
				}
			}
		case *ast.AggregateExpr:
			for _, f := range x.Fields {
				if pinExpr(f.Value) {
					return true
				}
			}
		case *ast.RangeExpr:
			return pinExpr(x.Lo) || pinExpr(x.Hi)
		case *ast.TryExpr:
			return pinExpr(x.X)
		case *ast.AwaitExpr:
			return pinExpr(x.X)
		case *ast.UnwrapExpr:
			return pinExpr(x.X)
		case *ast.SpawnExpr:
			return true
		case *ast.LambdaExpr:
			return true
		case *ast.InterpExpr:
			for _, p := range x.Parts {
				if pinExpr(p.Expr) {
					return true
				}
			}
		case *ast.IfExpr:
			return pinStmt(x)
		case *ast.MatchExpr:
			return pinStmt(x)
		case *ast.DeadlineExpr:
			return pinStmt(x)
		}
		return false
	}
	for _, st := range b.Stmts {
		if pinStmt(st) {
			return true
		}
	}
	return false
}
