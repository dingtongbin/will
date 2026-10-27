package willc

import (
	"github.com/will-lang/will/internal/ast"
)

// earlyRelease is the point after which one rc local can be released. Only the
// statements that copy or discard the handle are accepted; a call, field
// access, view, closure, defer, return, or container transfer can keep a
// borrow of the object alive and therefore pins the local to its block end.
type earlyRelease struct {
	after int
	node  ast.Node
}

func rcType(t *ast.Type) bool {
	if t == nil || t.Kind != ast.NamedType {
		return false
	}
	name := t.Name
	if i := lastDot(name); i >= 0 {
		name = name[i+1:]
	}
	return name == "rc" || name == "arc"
}

func lastDot(name string) int {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return i
		}
	}
	return -1
}

// earlyReleases finds rc locals whose remaining uses are only copies or
// discards. The release point is the statement containing the last such use.
// Any nested block use is pinned because a later use can escape the direct
// statement sequence.
func earlyReleases(b *ast.Block) map[*ast.VarDecl]earlyRelease {
	out := map[*ast.VarDecl]earlyRelease{}
	if b == nil {
		return out
	}
	for i, st := range b.Stmts {
		vs, ok := st.(*ast.VarStmt)
		if !ok {
			continue
		}
		vd, ok := vs.Decl.(*ast.VarDecl)
		if !ok || !rcType(vd.Type) || vd.Name == "" || vd.Name == "_" {
			continue
		}
		name := vd.Name
		last := -1
		var lastNode ast.Node
		safe := true
		for j, stmt := range b.Stmts {
			if j == i {
				// The initializer is evaluated before the binding exists, but
				// it must not refer to the new name.
				if containsNameInStmt(stmt, name) {
					safe = false
				}
				continue
			}
			if containsNameInNested(stmt, name) {
				safe = false
				break
			}
			ok, node := safeDirectUse(stmt, name)
			if !ok {
				safe = false
				break
			}
			if node != nil {
				last = j
				lastNode = node
			}
		}
		if !safe || last < 0 {
			continue
		}
		out[vd] = earlyRelease{after: last, node: lastNode}
	}
	return out
}

// safeDirectUse accepts a statement that only copies or discards the handle.
func safeDirectUse(st ast.Stmt, name string) (bool, ast.Node) {
	switch s := st.(type) {
	case *ast.VarStmt:
		d, ok := s.Decl.(*ast.VarDecl)
		if !ok {
			return true, nil
		}
		if n, ok := d.Value.(*ast.NameExpr); ok && n.Name == name {
			return true, n
		}
		return !containsNameInExpr(d.Value, name), nil
	case *ast.AssignStmt:
		if target, ok := s.Target.(*ast.NameExpr); ok && target.Name == name {
			return false, nil
		}
		if _, isDiscard := s.Target.(*ast.NameExpr); !isDiscard {
			if containsNameInExpr(s.Target, name) {
				return false, nil
			}
		}
		if n, ok := s.Value.(*ast.NameExpr); ok && n.Name == name {
			return true, n
		}
		return !containsNameInExpr(s.Value, name), nil
	case *ast.ExprStmt:
		return !containsNameInExpr(s.Expr, name), nil
	}
	return !containsNameInStmt(st, name), nil
}

func containsNameInNested(st ast.Stmt, name string) bool {
	switch s := st.(type) {
	case *ast.Block:
		return containsNameInStmts(s.Stmts, name)
	case *ast.ForStmt:
		return containsNameInStmts(s.Body.Stmts, name)
	case *ast.RegionStmt:
		return containsNameInStmts(s.Body.Stmts, name)
	case *ast.ParallelStmt:
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
	case *ast.ParallelStmt:
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
