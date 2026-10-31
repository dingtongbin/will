package willc

import "github.com/will-lang/will/internal/ast"

// earlyRelease is the statement after which one local owner can be released.
type earlyRelease struct {
	after int
	node  ast.Node
}

func lastDot(name string) int {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return i
		}
	}
	return -1
}

func containsNameInNested(st ast.Stmt, name string) bool {
	switch s := st.(type) {
	case *ast.Block:
		return containsNameInStmts(s.Stmts, name)
	case *ast.ForStmt:
		return containsNameInStmts(s.Body.Stmts, name)
	case *ast.RegionStmt:
		return containsNameInStmts(s.Body.Stmts, name)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			if containsNameInStmt(arm.Body, name) {
				return true
			}
		}
	case *ast.IfExpr:
		if containsNameInStmts(s.Then.Stmts, name) {
			return true
		}
		if s.Else != nil && containsNameInStmt(s.Else, name) {
			return true
		}
	case *ast.MatchExpr:
		for _, arm := range s.Arms {
			if containsNameInStmt(arm.Body, name) {
				return true
			}
		}
	case *ast.DeadlineExpr:
		return containsNameInStmts(s.Body.Stmts, name)
	}
	return false
}

func containsNameInStmts(stmts []ast.Stmt, name string) bool {
	for _, st := range stmts {
		if containsNameInStmt(st, name) {
			return true
		}
	}
	return false
}

func containsNameInStmt(st ast.Stmt, name string) bool {
	found := false
	walkStmtExprs(st, func(e ast.Expr) {
		if !found && containsNameInExpr(e, name) {
			found = true
		}
	})
	if found {
		return true
	}
	switch s := st.(type) {
	case *ast.Block:
		return containsNameInStmts(s.Stmts, name)
	case *ast.ForStmt:
		return containsNameInStmts(s.Body.Stmts, name)
	case *ast.RegionStmt:
		return containsNameInStmts(s.Body.Stmts, name)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			if containsNameInStmt(arm.Body, name) {
				return true
			}
		}
	case *ast.IfExpr:
		if containsNameInStmts(s.Then.Stmts, name) {
			return true
		}
		if s.Else != nil && containsNameInStmt(s.Else, name) {
			return true
		}
	case *ast.MatchExpr:
		for _, arm := range s.Arms {
			if containsNameInStmt(arm.Body, name) {
				return true
			}
		}
	case *ast.DeadlineExpr:
		return containsNameInStmts(s.Body.Stmts, name)
	}
	return false
}

func containsNameInExpr(e ast.Expr, name string) bool {
	found := false
	walkExprs(e, func(x ast.Expr) {
		if found {
			return
		}
		switch n := x.(type) {
		case *ast.NameExpr:
			if n.Name == name {
				found = true
			}
		case *ast.LambdaExpr:
			if containsNameInStmts(n.Body.Stmts, name) {
				found = true
			}
		}
	})
	return found
}
