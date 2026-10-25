package willc

import "github.com/will-lang/will/internal/ast"

// markStmtTree walks one arm body, which the parser stores as a statement.
func (em *emitter) markStmtTree(st ast.Stmt) {
	if b, ok := st.(*ast.Block); ok {
		for _, inner := range b.Stmts {
			em.markMoves(inner)
		}
		return
	}
	em.markMoves(st)
}

// markMoves records the owning locals a statement moves into another owner or
// out of the function. A moved local must not also receive a release record:
// the destination owns the storage now. The marks are conservative across
// branches, so a move on one path suppresses the release on every path; that
// leaks on the other path but never frees the moved storage twice. A later
// control-flow release plan can narrow this to the path that still owns it.
func (em *emitter) markMoves(st ast.Stmt) {
	switch s := st.(type) {
	case *ast.Block:
		for _, inner := range s.Stmts {
			em.markMoves(inner)
		}
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			if d.Value != nil && em.moveType(d.Type) {
				em.scanMoveExpr(d.Value, true)
			} else {
				em.scanMoveExpr(d.Value, false)
			}
		case *ast.ConstDecl:
			em.scanMoveExpr(d.Value, false)
		}
	case *ast.AssignStmt:
		transfer := true
		if target, ok := s.Target.(*ast.NameExpr); ok && target.Name == "_" {
			transfer = false
		}
		em.scanMoveExpr(s.Value, transfer)
	case *ast.ExprStmt:
		em.scanMoveExpr(s.Expr, false)
	case *ast.DeferStmt:
		em.scanDeferredArgs(s.Call)
	case *ast.ReturnStmt:
		em.scanMoveExpr(s.Value, true)
	case *ast.ForStmt:
		em.scanMoveExpr(s.Iter, false)
		for _, inner := range s.Body.Stmts {
			em.markMoves(inner)
		}
	case *ast.RegionStmt:
		for _, inner := range s.Body.Stmts {
			em.markMoves(inner)
		}
	case *ast.ParallelStmt:
		for _, inner := range s.Body.Stmts {
			em.markMoves(inner)
		}
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			em.scanMoveExpr(arm.Chan, false)
			em.scanMoveExpr(arm.Timeout, false)
			em.markStmtTree(arm.Body)
		}
	case *ast.IfExpr:
		em.scanMoveExpr(s.Cond, false)
		for _, inner := range s.Then.Stmts {
			em.markMoves(inner)
		}
		if s.Else != nil {
			em.markMoves(s.Else)
		}
	case *ast.MatchExpr:
		em.scanMoveExpr(s.Subject, false)
		for _, arm := range s.Arms {
			em.scanMoveExpr(arm.Guard, false)
			em.markStmtTree(arm.Body)
		}
	case *ast.DeadlineExpr:
		em.scanMoveExpr(s.Duration, false)
		for _, inner := range s.Body.Stmts {
			em.markMoves(inner)
		}
	}
}

// scanDeferredArgs marks moves into a deferred call's captured argument frame.
// The arguments are evaluated at the defer statement, not when the body runs.
func (em *emitter) scanDeferredArgs(call ast.Expr) {
	c, ok := call.(*ast.CallExpr)
	if !ok {
		em.scanMoveExpr(call, false)
		return
	}
	em.scanMoveExpr(c.Callee, false)
	for _, a := range c.Args {
		em.scanMoveExpr(a.Expr, true)
	}
}

// scanMoveExpr marks the names consumed by one expression. transfer says the
// expression's value is moved into a new owner; a constructor inside it still
// moves its own operands even when the constructor result is only borrowed.
func (em *emitter) scanMoveExpr(x ast.Expr, transfer bool) {
	switch v := x.(type) {
	case nil:
	case *ast.NameExpr:
		if transfer {
			em.markMovedName(v.Name)
		}
	case *ast.CallExpr:
		switch name := v.Callee.(type) {
		case *ast.NameExpr:
			switch name.Name {
			case "Some", "Ok", "Err":
				for _, a := range v.Args {
					em.scanMoveExpr(a.Expr, true)
				}
				return
			}
		}
		em.scanMoveExpr(v.Callee, false)
		for _, a := range v.Args {
			em.scanMoveExpr(a.Expr, false)
		}
	case *ast.MethodCallExpr:
		em.scanMoveExpr(v.Recv, false)
		moveArgs := v.Name == "Send" || em.containerMove(v)
		for _, a := range v.Args {
			em.scanMoveExpr(a.Expr, moveArgs)
		}
	case *ast.FieldExpr:
		em.scanMoveExpr(v.Recv, false)
	case *ast.IndexExpr:
		em.scanMoveExpr(v.Recv, false)
		em.scanMoveExpr(v.Index, false)
	case *ast.SliceExpr:
		em.scanMoveExpr(v.Recv, false)
		em.scanMoveExpr(v.Lo, false)
		em.scanMoveExpr(v.Hi, false)
	case *ast.UnaryExpr:
		em.scanMoveExpr(v.X, false)
	case *ast.BinaryExpr:
		em.scanMoveExpr(v.L, false)
		em.scanMoveExpr(v.R, false)
	case *ast.NewExpr:
		for _, a := range v.Args {
			em.scanMoveExpr(a.Expr, false)
		}
	case *ast.AggregateExpr:
		for _, f := range v.Fields {
			em.scanMoveExpr(f.Value, true)
		}
	case *ast.RangeExpr:
		em.scanMoveExpr(v.Lo, false)
		em.scanMoveExpr(v.Hi, false)
	case *ast.TryExpr:
		if em.consumesAdt(v.X) {
			em.scanMoveExpr(v.X, true)
		} else {
			em.scanMoveExpr(v.X, false)
		}
	case *ast.AwaitExpr:
		em.scanMoveExpr(v.X, true)
	case *ast.UnwrapExpr:
		if em.consumesAdt(v.X) {
			em.scanMoveExpr(v.X, true)
		} else {
			em.scanMoveExpr(v.X, false)
		}
	case *ast.SpawnExpr:
		em.scanSpawnArgs(v.Call)
	case *ast.LambdaExpr:
		for _, st := range v.Body.Stmts {
			em.markMoves(st)
		}
	case *ast.InterpExpr:
		for _, p := range v.Parts {
			em.scanMoveExpr(p.Expr, false)
		}
	case *ast.IfExpr:
		em.markMoves(v)
	case *ast.MatchExpr:
		em.markMoves(v)
	case *ast.DeadlineExpr:
		em.markMoves(v)
	}
}

func (em *emitter) scanSpawnArgs(call ast.Expr) {
	c, ok := call.(*ast.CallExpr)
	if !ok {
		em.scanMoveExpr(call, true)
		return
	}
	em.scanMoveExpr(c.Callee, false)
	for _, a := range c.Args {
		em.scanMoveExpr(a.Expr, true)
	}
}

// markMovedName marks the innermost local declaration named name when its type
// is a value owner. An rc handle is copied, so a transfer of it leaves the
// source owner in place.
// consumesAdt reports whether the expression is a local Option or Result value,
// the only values ? and ! consume. A str in the same position is invalid; the
// type checker rejects it, and the release walk must not silently drop it.
func (em *emitter) consumesAdt(x ast.Expr) bool {
	name, ok := x.(*ast.NameExpr)
	if !ok {
		return false
	}
	vd := em.ownerForName(name.Name)
	if vd == nil || vd.Type == nil || vd.Type.Kind != ast.NamedType {
		return false
	}
	return vd.Type.Name == "Option" || vd.Type.Name == "Result"
}

func (em *emitter) markMovedName(name string) {
	vd := em.ownerForName(name)
	if vd == nil || !em.moveType(vd.Type) {
		return
	}
	em.moved[vd] = true
}

func (em *emitter) ownerForName(name string) *ast.VarDecl {
	for i := len(em.scopes) - 1; i >= 0; i-- {
		for _, node := range em.scopes[i] {
			vd, ok := node.(*ast.VarDecl)
			if ok && vd.Name == name {
				return vd
			}
		}
	}
	return nil
}

func (em *emitter) moveType(t *ast.Type) bool {
	if t == nil || !ast.NeedsRelease(t) {
		return false
	}
	if rcType(t) {
		return false
	}
	if t.Kind == ast.NamedType && t.Name == "weak" {
		return false
	}
	return true
}

func (em *emitter) containerMove(call *ast.MethodCallExpr) bool {
	// The receiver type of a parameter is not in the local scope table here,
	// so the method name stays conservative. A user method with one of these
	// names only loses a release, never gains a second one.
	switch call.Name {
	case "Push", "Append", "Insert", "Set", "Add":
		return true
	}
	return false
}
