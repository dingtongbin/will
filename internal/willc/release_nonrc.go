package willc

import "github.com/will-lang/will/internal/ast"

// nonRcReleases finds value owners whose remaining uses only inspect or mutate
// the owner itself. A view, field read, call argument, move, closure, defer, or
// nested statement could keep a buffer reachable past the lexical use, so those
// shapes pin the owner to the block end.
func nonRcReleases(b *ast.Block) map[*ast.VarDecl]earlyRelease {
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
		if !ok || vd.Type == nil || vd.Name == "" || vd.Name == "_" || rcType(vd.Type) || !ast.NeedsRelease(vd.Type) {
			continue
		}
		if !safeContainerType(vd.Type) {
			continue
		}
		last := -1
		var lastNode ast.Node
		safe := true
		for j, stmt := range b.Stmts {
			if j == i {
				// The initializer runs before the name exists.
				if containsNameInStmt(stmt, vd.Name) {
					safe = false
				}
				continue
			}
			if containsNameInNested(stmt, vd.Name) {
				safe = false
				break
			}
			ok, node := safeNonRcStmt(stmt, vd.Name, vd.Type)
			if !ok {
				safe = false
				break
			}
			if node != nil {
				last = j
				lastNode = node
			}
		}
		if safe && last >= 0 {
			out[vd] = earlyRelease{after: last, node: lastNode}
		}
	}
	return out
}

func safeContainerType(t *ast.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ast.ScalarType:
		return t.Name == "str"
	case ast.NamedType:
		switch t.Name {
		case "Array", "Map", "Set":
			return true
		}
		// A user struct may own nested buffers, but only a discard is safe
		// without its field types, so it stays out of this fast path.
		return false
	}
	return false
}

// safeNonRcStmt accepts a statement that only calls methods which do not keep
// or return a borrow of the owner. It returns the last name node on success.
func safeNonRcStmt(st ast.Stmt, name string, typ *ast.Type) (bool, ast.Node) {
	switch s := st.(type) {
	case *ast.VarStmt:
		d, ok := s.Decl.(*ast.VarDecl)
		if !ok {
			return !containsNameInStmt(st, name), nil
		}
		if !safeNonRcExpr(d.Value, name, typ) {
			return false, nil
		}
		return true, lastNameNode(d.Value, name)
	case *ast.AssignStmt:
		if target, ok := s.Target.(*ast.NameExpr); ok && target.Name == name {
			return false, nil
		}
		if target, discard := s.Target.(*ast.NameExpr); !discard || target.Name != "_" {
			if containsNameInExpr(s.Target, name) {
				return false, nil
			}
		} else if n, ok := s.Value.(*ast.NameExpr); ok && n.Name == name {
			return true, n
		}
		if !safeNonRcExpr(s.Value, name, typ) {
			return false, nil
		}
		return true, lastNameNode(s.Value, name)
	case *ast.ExprStmt:
		if !safeNonRcExpr(s.Expr, name, typ) {
			return false, nil
		}
		return true, lastNameNode(s.Expr, name)
	}
	return !containsNameInStmt(st, name), nil
}

// safeNonRcExpr accepts a method call on the owner whose method does not build a
// view, plus scalar operators over such calls. A bare name is never accepted:
// it is either a move/copy or a value passed to something unknown.
func safeNonRcExpr(e ast.Expr, name string, typ *ast.Type) bool {
	switch x := e.(type) {
	case nil:
		return true
	case *ast.NameExpr:
		return x.Name != name
	case *ast.MethodCallExpr:
		recv, ok := x.Recv.(*ast.NameExpr)
		if !ok || recv.Name != name || !safeContainerMethod(typ, x.Name) {
			return false
		}
		for _, a := range x.Args {
			if containsNameInExpr(a.Expr, name) && !safeNonRcExpr(a.Expr, name, typ) {
				return false
			}
		}
		return true
	case *ast.BinaryExpr:
		if !containsNameInExpr(x.L, name) && !containsNameInExpr(x.R, name) {
			return true
		}
		return safeNonRcExpr(x.L, name, typ) && safeNonRcExpr(x.R, name, typ)
	case *ast.UnaryExpr:
		return safeNonRcExpr(x.X, name, typ)
	}
	return !containsNameInExpr(e, name)
}

func safeContainerMethod(t *ast.Type, method string) bool {
	if t == nil {
		return false
	}
	if t.Kind == ast.ScalarType && t.Name == "str" {
		switch method {
		case "Len", "IsEmpty", "Clone", "Compare", "HasPrefix", "HasSuffix":
			return true
		}
		return false
	}
	if t.Kind != ast.NamedType {
		return false
	}
	switch t.Name {
	case "Array":
		switch method {
		case "Len", "Cap", "IsEmpty", "Push", "Append", "Set", "Insert", "Pop", "Remove", "Clear", "Reserve", "Resize", "ShrinkToFit", "Clone":
			return true
		}
	case "Map":
		switch method {
		case "Len", "IsEmpty", "Has", "Set", "Delete", "Clear":
			return true
		}
	case "Set":
		switch method {
		case "Len", "IsEmpty", "Has", "Add", "Delete", "Clear":
			return true
		}
	}
	return false
}

func lastNode(e ast.Expr, name string) ast.Node {
	found := false
	var node ast.Node
	walkExprs(e, func(x ast.Expr) {
		if n, ok := x.(*ast.NameExpr); ok && n.Name == name {
			node = n
			found = true
		}
	})
	if found {
		return node
	}
	return nil
}

func lastNameNode(e ast.Expr, name string) ast.Node {
	return lastNode(e, name)
}
