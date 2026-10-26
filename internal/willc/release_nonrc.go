package willc

import "github.com/will-lang/will/internal/ast"

// nonRcReleases finds value owners whose remaining uses only inspect or mutate
// the owner itself. A view, field read, call argument, move, closure, defer, or
// nested statement could keep a buffer reachable past the lexical use, so those
// shapes pin the owner to the block end.
func (em *emitter) nonRcReleases(b *ast.Block, types map[string]*ast.Type) map[*ast.VarDecl]earlyRelease {
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
		if !em.releaseType(vd.Type) {
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
			ok, used, node := em.safeNonRcTree(stmt, vd.Name, vd.Type, types)
			if !ok {
				safe = false
				break
			}
			if used {
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

// releaseType allows a predeclared container or a local struct. An imported
// struct has no field table here, so its reads stay pinned.
func (em *emitter) releaseType(t *ast.Type) bool {
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
		_, ok := em.structs[shortName(t.Name)]
		return ok
	}
	return false
}

// safeNonRcTree accepts direct and nested uses. The release point stays after
// the top-level statement, so a loop use releases after the loop and an if use
// releases after the if. A view, call, move, defer, or parallel use still pins.
func (em *emitter) safeNonRcTree(st ast.Stmt, name string, typ *ast.Type, types map[string]*ast.Type) (bool, bool, ast.Node) {
	switch s := st.(type) {
	case *ast.Block:
		return em.safeNonRcBlock(s, name, typ, types)
	case *ast.IfExpr:
		ok, used, node := em.safeNonRcExprUse(s.Cond, name, typ, types)
		if !ok {
			return false, false, nil
		}
		tok, tused, tnode := em.safeNonRcBlock(s.Then, name, typ, types)
		if !tok {
			return false, false, nil
		}
		used = used || tused
		if tnode != nil {
			node = tnode
		}
		if s.Else != nil {
			eok, eused, enode := em.safeNonRcTree(s.Else, name, typ, types)
			if !eok {
				return false, false, nil
			}
			used = used || eused
			if enode != nil {
				node = enode
			}
		}
		return true, used, node
	case *ast.ForStmt:
		ok, used, node := em.safeNonRcExprUse(s.Iter, name, typ, types)
		if !ok {
			return false, false, nil
		}
		bok, bused, bnode := em.safeNonRcBlock(s.Body, name, typ, types)
		if !bok {
			return false, false, nil
		}
		used = used || bused
		if bnode != nil {
			node = bnode
		}
		return true, used, node
	case *ast.RegionStmt:
		return em.safeNonRcBlock(s.Body, name, typ, types)
	case *ast.ParallelStmt:
		return em.safeNonRcBlock(s.Body, name, typ, types)
	case *ast.SelectStmt:
		used := false
		var node ast.Node
		for _, arm := range s.Arms {
			ok, u, n := em.safeNonRcExprUse(arm.Chan, name, typ, types)
			if !ok {
				return false, false, nil
			}
			used = used || u
			if n != nil {
				node = n
			}
			ok, u, n = em.safeNonRcExprUse(arm.Timeout, name, typ, types)
			if !ok {
				return false, false, nil
			}
			used = used || u
			if n != nil {
				node = n
			}
			ok, u, n = em.safeNonRcTree(arm.Body, name, typ, types)
			if !ok {
				return false, false, nil
			}
			used = used || u
			if n != nil {
				node = n
			}
		}
		return true, used, node
	case *ast.MatchExpr:
		ok, used, node := em.safeNonRcExprUse(s.Subject, name, typ, types)
		if !ok {
			return false, false, nil
		}
		for _, arm := range s.Arms {
			gok, gused, gnode := em.safeNonRcExprUse(arm.Guard, name, typ, types)
			if !gok {
				return false, false, nil
			}
			used = used || gused
			if gnode != nil {
				node = gnode
			}
			bok, bused, bnode := em.safeNonRcTree(arm.Body, name, typ, types)
			if !bok {
				return false, false, nil
			}
			used = used || bused
			if bnode != nil {
				node = bnode
			}
		}
		return true, used, node
	case *ast.DeadlineExpr:
		ok, used, node := em.safeNonRcExprUse(s.Duration, name, typ, types)
		if !ok {
			return false, false, nil
		}
		bok, bused, bnode := em.safeNonRcBlock(s.Body, name, typ, types)
		if !bok {
			return false, false, nil
		}
		used = used || bused
		if bnode != nil {
			node = bnode
		}
		return true, used, node
	}
	ok, node := em.safeNonRcStmt(st, name, typ, types)
	if !ok {
		return false, false, nil
	}
	used := containsNameInStmt(st, name)
	if node == nil && used {
		node = lastNodeInStmt(st, name)
	}
	return true, used, node
}

func (em *emitter) safeNonRcBlock(b *ast.Block, name string, typ *ast.Type, types map[string]*ast.Type) (bool, bool, ast.Node) {
	used := false
	var node ast.Node
	if b == nil {
		return true, false, nil
	}
	for _, st := range b.Stmts {
		ok, u, n := em.safeNonRcTree(st, name, typ, types)
		if !ok {
			return false, false, nil
		}
		used = used || u
		if n != nil {
			node = n
		}
	}
	return true, used, node
}

func (em *emitter) safeNonRcExprUse(e ast.Expr, name string, typ *ast.Type, types map[string]*ast.Type) (bool, bool, ast.Node) {
	if e == nil || !containsNameInExpr(e, name) {
		return true, false, nil
	}
	if !em.safeNonRcExpr(e, name, typ, types) {
		return false, false, nil
	}
	return true, true, lastNode(e, name)
}

func lastNodeInStmt(st ast.Stmt, name string) ast.Node {
	found := false
	var node ast.Node
	walkStmtExprs(st, func(e ast.Expr) {
		if n := lastNode(e, name); n != nil {
			node = n
			found = true
		}
	})
	if found {
		return node
	}
	return nil
}

// safeNonRcStmt accepts a statement that only calls methods which do not keep
// or return a borrow of the owner, or reads a non-owning scalar/rc field. It
// returns the last name node on success.
func (em *emitter) safeNonRcStmt(st ast.Stmt, name string, typ *ast.Type, types map[string]*ast.Type) (bool, ast.Node) {
	switch s := st.(type) {
	case *ast.VarStmt:
		d, ok := s.Decl.(*ast.VarDecl)
		if !ok {
			return !containsNameInStmt(st, name), nil
		}
		if !em.safeNonRcExpr(d.Value, name, typ, types) {
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
		if !em.safeNonRcExpr(s.Value, name, typ, types) {
			return false, nil
		}
		return true, lastNameNode(s.Value, name)
	case *ast.ExprStmt:
		if !em.safeNonRcExpr(s.Expr, name, typ, types) {
			return false, nil
		}
		return true, lastNameNode(s.Expr, name)
	}
	return !containsNameInStmt(st, name), nil
}

// safeNonRcExpr accepts a method call on the owner whose method does not build a
// view, a read of a scalar or rc field, plus scalar operators over such calls.
// A bare name is never accepted: it is either a move/copy or a value passed to
// something unknown.
func (em *emitter) safeNonRcExpr(e ast.Expr, name string, typ *ast.Type, types map[string]*ast.Type) bool {
	switch x := e.(type) {
	case nil:
		return true
	case *ast.NameExpr:
		return x.Name != name
	case *ast.FieldExpr:
		if !containsNameInExpr(x, name) {
			return true
		}
		return em.fieldReadSafe(x, name, typ, types)
	case *ast.MethodCallExpr:
		if !containsNameInExpr(x, name) {
			return true
		}
		recv, ok := x.Recv.(*ast.NameExpr)
		if !ok || recv.Name != name || !safeContainerMethod(typ, x.Name) {
			return false
		}
		for _, a := range x.Args {
			if containsNameInExpr(a.Expr, name) && !em.safeNonRcExpr(a.Expr, name, typ, types) {
				return false
			}
		}
		return true
	case *ast.BinaryExpr:
		if !containsNameInExpr(x.L, name) && !containsNameInExpr(x.R, name) {
			return true
		}
		return em.safeNonRcExpr(x.L, name, typ, types) && em.safeNonRcExpr(x.R, name, typ, types)
	case *ast.UnaryExpr:
		return em.safeNonRcExpr(x.X, name, typ, types)
	}
	return !containsNameInExpr(e, name)
}

// fieldReadSafe accepts a field chain rooted at the owner when every read is a
// scalar or a copyable rc handle. A view or an owning field would either borrow
// the owner or need an explicit Clone, so those pin it.
func (em *emitter) fieldReadSafe(e ast.Expr, name string, typ *ast.Type, types map[string]*ast.Type) bool {
	field, ok := e.(*ast.FieldExpr)
	if !ok {
		return false
	}
	root := exprRootName(field.Recv)
	if root != name {
		return false
	}
	t, ok := em.placeType(field, typ, types)
	if !ok {
		return false
	}
	return !em.typeOwns(t) && !em.typeBorrow(t)
}

func (em *emitter) placeType(e ast.Expr, typ *ast.Type, types map[string]*ast.Type) (*ast.Type, bool) {
	switch x := e.(type) {
	case *ast.NameExpr:
		if x.Name == "" {
			return nil, false
		}
		if t, ok := types[x.Name]; ok {
			return t, true
		}
		return typ, true
	case *ast.FieldExpr:
		base, ok := em.placeType(x.Recv, typ, types)
		if !ok {
			return nil, false
		}
		ft := em.fieldType(base, x.Name)
		if ft == nil {
			return nil, false
		}
		return ft, true
	}
	return nil, false
}

func (em *emitter) fieldType(t *ast.Type, name string) *ast.Type {
	if t == nil {
		return nil
	}
	if t.Kind == ast.NamedType && t.Name == "rc" && len(t.Args) == 1 {
		t = t.Args[0]
	}
	if t.Kind != ast.NamedType {
		return nil
	}
	s, ok := em.structs[shortName(t.Name)]
	if !ok {
		return nil
	}
	for _, f := range s.Fields {
		if f.Name == name {
			return f.Type
		}
	}
	return nil
}

func (em *emitter) typeOwns(t *ast.Type) bool {
	return em.typeOwnsDepth(t, map[string]bool{})
}

func (em *emitter) typeOwnsDepth(t *ast.Type, active map[string]bool) bool {
	if t == nil {
		return true
	}
	switch t.Kind {
	case ast.ScalarType:
		return t.Name == "str"
	case ast.ViewType:
		return false
	case ast.ArrayType:
		return em.typeOwnsDepth(t.Elem, active)
	case ast.FuncType, ast.UnitType:
		return false
	case ast.NamedType:
		switch t.Name {
		case "rc", "weak", "chan", "Task", "Duration", "Value", "Show", "Error":
			return false
		case "str", "Array", "Map", "Set":
			return true
		case "Option", "Result":
			for _, a := range t.Args {
				if em.typeOwnsDepth(a, active) {
					return true
				}
			}
			return false
		}
		s, ok := em.structs[shortName(t.Name)]
		if !ok {
			return true
		}
		if active[s.Name] {
			return false
		}
		active[s.Name] = true
		defer delete(active, s.Name)
		for _, f := range s.Fields {
			if em.typeOwnsDepth(f.Type, active) {
				return true
			}
		}
		return false
	}
	return true
}

func (em *emitter) typeBorrow(t *ast.Type) bool {
	return t != nil && t.Kind == ast.ViewType
}

func exprRootName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.NameExpr:
		return x.Name
	case *ast.FieldExpr:
		return exprRootName(x.Recv)
	case *ast.IndexExpr:
		return exprRootName(x.Recv)
	case *ast.SliceExpr:
		return exprRootName(x.Recv)
	}
	return ""
}

func shortName(name string) string {
	if i := lastDot(name); i >= 0 {
		return name[i+1:]
	}
	return name
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
