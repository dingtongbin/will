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
		views := em.blockViews(b, vd.Name, types)
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
			direct := containsNameInStmt(stmt, vd.Name)
			viewUse := containsAnyView(stmt, views)
			if !direct && !viewUse {
				continue
			}
			if containsViewInNested(stmt, views) {
				safe = false
				break
			}
			if !direct {
				if !em.safeViewStmt(stmt, views, vd.Name, types) {
					safe = false
					break
				}
				last = j
				lastNode = lastViewNode(stmt, views)
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
		if _, ok := em.structs[shortName(t.Name)]; ok {
			return true
		}
		_, ok := em.external[t.Name]
		if !ok {
			_, ok = em.external[shortName(t.Name)]
		}
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
		if d.Type != nil && d.Type.Kind == ast.ViewType && viewRootOwner(d.Value) == name {
			return true, lastNameNode(d.Value, name)
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
	case *ast.CallExpr:
		if !containsNameInExpr(x, name) {
			return true
		}
		return em.safeLocalCall(x, name, typ, types)
	case *ast.BinaryExpr:
		if !containsNameInExpr(x.L, name) && !containsNameInExpr(x.R, name) {
			return true
		}
		return em.safeNonRcExpr(x.L, name, typ, types) && em.safeNonRcExpr(x.R, name, typ, types)
	case *ast.UnaryExpr:
		return em.safeNonRcExpr(x.X, name, typ, types)
	case *ast.LambdaExpr:
		// The closure environment deep-copies an owning capture, so the source
		// only has to survive closure construction. A lambda passed, returned,
		// or stored is rejected by its parent expression, not here.
		return containsNameInExpr(x, name)
	}
	return !containsNameInExpr(e, name)
}

// safeLocalCall admits a call to a local free function that returns a scalar
// or nothing and takes no callback or variadic parameter. Such a call borrows
// its arguments only for the duration of the call. A returned view, aggregate,
// callback, generic, or imported callee still pins the owner.
func (em *emitter) safeLocalCall(call *ast.CallExpr, name string, typ *ast.Type, types map[string]*ast.Type) bool {
	callee, ok := call.Callee.(*ast.NameExpr)
	if !ok {
		return false
	}
	fn := em.funcs[callee.Name]
	if fn == nil || fn.Body == nil || len(fn.TypeParams) != 0 {
		return false
	}
	if fn.Result != nil {
		if fn.Result.Kind == ast.ScalarType {
			if fn.Result.Name == "str" {
				return false
			}
		} else if fn.Result.Kind != ast.UnitType {
			return false
		}
	}
	for _, p := range fn.Params {
		if p.Variadic || (p.Type != nil && p.Type.Kind == ast.FuncType) {
			return false
		}
	}
	for _, a := range call.Args {
		if !containsNameInExpr(a.Expr, name) {
			continue
		}
		if n, ok := a.Expr.(*ast.NameExpr); ok && n.Name == name {
			continue
		}
		return false
	}
	return true
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
		s = em.external[t.Name]
	}
	if s == nil {
		s = em.external[shortName(t.Name)]
	}
	if s == nil {
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
		s := em.structs[shortName(t.Name)]
		if s == nil {
			s = em.external[t.Name]
		}
		if s == nil {
			s = em.external[shortName(t.Name)]
		}
		if s == nil {
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

// blockViews maps a local view name to its owner for the direct declarations
// of one block. Only a view whose value is a slice/index of the owner, or an
// alias of such a view, is tracked; an arbitrary value typed as a view stays
// out because its source is not known.
func (em *emitter) blockViews(b *ast.Block, owner string, types map[string]*ast.Type) map[string]bool {
	out := map[string]bool{}
	if b == nil {
		return out
	}
	for _, st := range b.Stmts {
		vs, ok := st.(*ast.VarStmt)
		if !ok {
			continue
		}
		d, ok := vs.Decl.(*ast.VarDecl)
		if !ok || d.Type == nil || d.Type.Kind != ast.ViewType || d.Name == "" || d.Name == "_" {
			continue
		}
		if viewRootOwner(d.Value) == owner {
			out[d.Name] = true
			continue
		}
		if n, ok := d.Value.(*ast.NameExpr); ok && out[n.Name] {
			out[d.Name] = true
			continue
		}
		if call, ok := d.Value.(*ast.MethodCallExpr); ok && call.Name == "Slice" {
			if viewRootOwner(call.Recv) == owner {
				out[d.Name] = true
			}
		}
	}
	return out
}

func viewRootOwner(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.SliceExpr:
		return exprRootName(x.Recv)
	case *ast.IndexExpr:
		return exprRootName(x.Recv)
	case *ast.MethodCallExpr:
		if x.Name == "Slice" {
			return exprRootName(x.Recv)
		}
	}
	return ""
}

func containsAnyView(st ast.Stmt, views map[string]bool) bool {
	if len(views) == 0 {
		return false
	}
	found := false
	walkStmtExprs(st, func(e ast.Expr) {
		if found {
			return
		}
		if containsViewInExpr(e, views) {
			found = true
		}
	})
	return found
}

func containsViewInExpr(e ast.Expr, views map[string]bool) bool {
	found := false
	walkExprs(e, func(x ast.Expr) {
		if found {
			return
		}
		if n, ok := x.(*ast.NameExpr); ok && views[n.Name] {
			found = true
		}
	})
	return found
}

func containsViewInNested(st ast.Stmt, views map[string]bool) bool {
	if len(views) == 0 {
		return false
	}
	switch s := st.(type) {
	case *ast.Block:
		return containsViewInBlock(s, views)
	case *ast.ForStmt:
		return containsViewInBlock(s.Body, views)
	case *ast.RegionStmt:
		return containsViewInBlock(s.Body, views)
	case *ast.ParallelStmt:
		return containsViewInBlock(s.Body, views)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			if containsViewInStmt(arm.Body, views) {
				return true
			}
		}
	case *ast.IfExpr:
		return containsViewInBlock(s.Then, views) || (s.Else != nil && containsViewInStmt(s.Else, views))
	case *ast.MatchExpr:
		for _, arm := range s.Arms {
			if containsViewInStmt(arm.Body, views) {
				return true
			}
		}
	case *ast.DeadlineExpr:
		return containsViewInBlock(s.Body, views)
	}
	return false
}

func containsViewInBlock(b *ast.Block, views map[string]bool) bool {
	if b == nil {
		return false
	}
	for _, st := range b.Stmts {
		if containsViewInStmt(st, views) {
			return true
		}
	}
	return false
}

func containsViewInStmt(st ast.Stmt, views map[string]bool) bool {
	if containsAnyView(st, views) {
		return true
	}
	return containsViewInNested(st, views)
}

func lastViewNode(st ast.Stmt, views map[string]bool) ast.Node {
	var node ast.Node
	walkStmtExprs(st, func(e ast.Expr) {
		walkExprs(e, func(x ast.Expr) {
			if n, ok := x.(*ast.NameExpr); ok && views[n.Name] {
				node = n
			}
		})
	})
	return node
}

// safeViewStmt admits only direct reads of a tracked view: discards, aliases,
// and methods that neither return nor store a borrow. A view used in a control
// statement, call, aggregate, or return pins the owner.
func (em *emitter) safeViewStmt(st ast.Stmt, views map[string]bool, owner string, types map[string]*ast.Type) bool {
	switch s := st.(type) {
	case *ast.VarStmt:
		d, ok := s.Decl.(*ast.VarDecl)
		if !ok {
			return false
		}
		if d.Type != nil && d.Type.Kind == ast.ViewType {
			if n, ok := d.Value.(*ast.NameExpr); ok && views[n.Name] {
				return true
			}
			if viewRootOwner(d.Value) == owner {
				return true
			}
		}
		return em.safeViewExpr(d.Value, views)
	case *ast.AssignStmt:
		if target, ok := s.Target.(*ast.NameExpr); ok && target.Name == "_" {
			return em.safeViewExpr(s.Value, views)
		}
		if containsViewInExpr(s.Target, views) {
			return false
		}
		return em.safeViewExpr(s.Value, views)
	case *ast.ExprStmt:
		return em.safeViewExpr(s.Expr, views)
	}
	return false
}

func (em *emitter) safeViewExpr(e ast.Expr, views map[string]bool) bool {
	switch x := e.(type) {
	case nil:
		return true
	case *ast.NameExpr:
		return views[x.Name]
	case *ast.MethodCallExpr:
		recv, ok := x.Recv.(*ast.NameExpr)
		if !ok || !views[recv.Name] || !safeViewMethod(x.Name) {
			return false
		}
		for _, a := range x.Args {
			if containsViewInExpr(a.Expr, views) {
				return false
			}
		}
		return true
	case *ast.BinaryExpr:
		if !containsViewInExpr(x.L, views) && !containsViewInExpr(x.R, views) {
			return true
		}
		return em.safeViewExpr(x.L, views) && em.safeViewExpr(x.R, views)
	case *ast.UnaryExpr:
		return em.safeViewExpr(x.X, views)
	}
	return !containsViewInExpr(e, views)
}

func safeViewMethod(name string) bool {
	switch name {
	case "Len", "IsEmpty", "Equal", "Compare", "HasPrefix", "HasSuffix", "Index", "Clone":
		return true
	}
	return false
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
		switch v := x.(type) {
		case *ast.NameExpr:
			if v.Name == name {
				node = v
				found = true
			}
		case *ast.LambdaExpr:
			for _, st := range v.Body.Stmts {
				if n := lastNodeInStmt(st, name); n != nil {
					node = n
					found = true
				}
			}
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
