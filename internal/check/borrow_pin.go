package check

import "github.com/will-lang/will/internal/ast"

// pinnedNames returns the names captured by a defer, closure, or spawn in one
// block. Those constructs can run after the statement that mentions them, so
// only a view whose owner (or view name) is in this set has to stay borrowed
// until the block ends. A closure that mentions no outer name does not widen
// every view in the block.
func pinnedNames(b *ast.Block) map[string]bool {
	set := map[string]bool{}
	if b == nil {
		return set
	}
	var scanStmt func(ast.Stmt)
	var scanExpr func(ast.Expr)
	var collectStmt func(ast.Stmt)
	var collectExpr func(ast.Expr)

	collectExpr = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
		case *ast.NameExpr:
			if x.Name != "" && x.Name != "_" {
				set[x.Name] = true
			}
		case *ast.CallExpr:
			collectExpr(x.Callee)
			for _, a := range x.Args {
				collectExpr(a.Expr)
			}
		case *ast.MethodCallExpr:
			collectExpr(x.Recv)
			for _, a := range x.Args {
				collectExpr(a.Expr)
			}
		case *ast.FieldExpr:
			collectExpr(x.Recv)
		case *ast.IndexExpr:
			collectExpr(x.Recv)
			collectExpr(x.Index)
		case *ast.SliceExpr:
			collectExpr(x.Recv)
			collectExpr(x.Lo)
			collectExpr(x.Hi)
		case *ast.BorrowExpr:
			collectExpr(x.X)
		case *ast.UnaryExpr:
			collectExpr(x.X)
		case *ast.BinaryExpr:
			collectExpr(x.L)
			collectExpr(x.R)
		case *ast.NewExpr:
			for _, a := range x.Args {
				collectExpr(a.Expr)
			}
		case *ast.AggregateExpr:
			for _, f := range x.Fields {
				collectExpr(f.Value)
			}
		case *ast.ArrayLitExpr:
			for _, elem := range x.Elems {
				collectExpr(elem)
			}
		case *ast.RangeExpr:
			collectExpr(x.Lo)
			collectExpr(x.Hi)
		case *ast.TryExpr:
			collectExpr(x.X)
		case *ast.AwaitExpr:
			collectExpr(x.X)
		case *ast.UnwrapExpr:
			collectExpr(x.X)
		case *ast.SpawnExpr:
			collectExpr(x.Call)
		case *ast.LambdaExpr:
			for _, st := range x.Body.Stmts {
				collectStmt(st)
			}
		case *ast.InterpExpr:
			for _, p := range x.Parts {
				collectExpr(p.Expr)
			}
		case *ast.IfExpr:
			collectStmt(x)
		case *ast.MatchExpr:
			collectStmt(x)
		case *ast.DeadlineExpr:
			collectStmt(x)
		}
	}

	collectStmt = func(st ast.Stmt) {
		switch s := st.(type) {
		case nil:
		case *ast.Block:
			for _, inner := range s.Stmts {
				collectStmt(inner)
			}
		case *ast.VarStmt:
			switch d := s.Decl.(type) {
			case *ast.VarDecl:
				collectExpr(d.Value)
			case *ast.ConstDecl:
				collectExpr(d.Value)
			}
		case *ast.AssignStmt:
			collectExpr(s.Target)
			collectExpr(s.Value)
		case *ast.ExprStmt:
			collectExpr(s.Expr)
		case *ast.DeferStmt:
			collectExpr(s.Call)
		case *ast.ReturnStmt:
			collectExpr(s.Value)
		case *ast.ForStmt:
			collectExpr(s.Iter)
			collectStmt(s.Body)
		case *ast.RegionStmt:
			collectStmt(s.Body)
		case *ast.ParallelStmt:
			collectStmt(s.Body)
		case *ast.SelectStmt:
			for _, arm := range s.Arms {
				collectExpr(arm.Chan)
				collectExpr(arm.Timeout)
				collectStmt(arm.Body)
			}
		case *ast.IfExpr:
			collectExpr(s.Cond)
			collectStmt(s.Then)
			if s.Else != nil {
				collectStmt(s.Else)
			}
		case *ast.MatchExpr:
			collectExpr(s.Subject)
			for _, arm := range s.Arms {
				collectExpr(arm.Guard)
				collectStmt(arm.Body)
			}
		case *ast.DeadlineExpr:
			collectExpr(s.Duration)
			collectStmt(s.Body)
		}
	}

	scanExpr = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
		case *ast.LambdaExpr:
			for _, st := range x.Body.Stmts {
				collectStmt(st)
			}
		case *ast.SpawnExpr:
			collectExpr(x.Call)
		case *ast.CallExpr:
			scanExpr(x.Callee)
			for _, a := range x.Args {
				scanExpr(a.Expr)
			}
		case *ast.MethodCallExpr:
			scanExpr(x.Recv)
			for _, a := range x.Args {
				scanExpr(a.Expr)
			}
		case *ast.FieldExpr:
			scanExpr(x.Recv)
		case *ast.IndexExpr:
			scanExpr(x.Recv)
			scanExpr(x.Index)
		case *ast.SliceExpr:
			scanExpr(x.Recv)
			scanExpr(x.Lo)
			scanExpr(x.Hi)
		case *ast.BorrowExpr:
			scanExpr(x.X)
		case *ast.UnaryExpr:
			scanExpr(x.X)
		case *ast.BinaryExpr:
			scanExpr(x.L)
			scanExpr(x.R)
		case *ast.NewExpr:
			for _, a := range x.Args {
				scanExpr(a.Expr)
			}
		case *ast.AggregateExpr:
			for _, f := range x.Fields {
				scanExpr(f.Value)
			}
		case *ast.ArrayLitExpr:
			for _, elem := range x.Elems {
				scanExpr(elem)
			}
		case *ast.RangeExpr:
			scanExpr(x.Lo)
			scanExpr(x.Hi)
		case *ast.TryExpr:
			scanExpr(x.X)
		case *ast.AwaitExpr:
			scanExpr(x.X)
		case *ast.UnwrapExpr:
			scanExpr(x.X)
		case *ast.InterpExpr:
			for _, p := range x.Parts {
				scanExpr(p.Expr)
			}
		case *ast.IfExpr:
			scanStmt(x)
		case *ast.MatchExpr:
			scanStmt(x)
		case *ast.DeadlineExpr:
			scanStmt(x)
		}
	}

	scanStmt = func(st ast.Stmt) {
		switch s := st.(type) {
		case nil:
		case *ast.Block:
			for _, inner := range s.Stmts {
				scanStmt(inner)
			}
		case *ast.VarStmt:
			switch d := s.Decl.(type) {
			case *ast.VarDecl:
				scanExpr(d.Value)
			case *ast.ConstDecl:
				scanExpr(d.Value)
			}
		case *ast.AssignStmt:
			scanExpr(s.Target)
			scanExpr(s.Value)
		case *ast.ExprStmt:
			scanExpr(s.Expr)
		case *ast.DeferStmt:
			collectExpr(s.Call)
		case *ast.ReturnStmt:
			scanExpr(s.Value)
		case *ast.ForStmt:
			scanExpr(s.Iter)
			for _, inner := range s.Body.Stmts {
				scanStmt(inner)
			}
		case *ast.RegionStmt:
			for _, inner := range s.Body.Stmts {
				scanStmt(inner)
			}
		case *ast.ParallelStmt:
			for _, inner := range s.Body.Stmts {
				scanStmt(inner)
			}
		case *ast.SelectStmt:
			for _, arm := range s.Arms {
				scanExpr(arm.Chan)
				scanExpr(arm.Timeout)
				scanStmt(arm.Body)
			}
		case *ast.IfExpr:
			scanExpr(s.Cond)
			for _, inner := range s.Then.Stmts {
				scanStmt(inner)
			}
			if s.Else != nil {
				scanStmt(s.Else)
			}
		case *ast.MatchExpr:
			scanExpr(s.Subject)
			for _, arm := range s.Arms {
				scanExpr(arm.Guard)
				scanStmt(arm.Body)
			}
		case *ast.DeadlineExpr:
			scanExpr(s.Duration)
			for _, inner := range s.Body.Stmts {
				scanStmt(inner)
			}
		}
	}

	for _, st := range b.Stmts {
		scanStmt(st)
	}
	return set
}
