package willc

import "github.com/will-lang/will/internal/ast"

// markMoves records the owning locals that are transferred by constructs whose
// codegen does not zero the source yet: spawn, Send, defer, and ?/!. The direct
// value moves (var, assignment, return, aggregate, Option/Result, and Array
// insert/push/set) are zeroed by codegen, so their release records stay and the
// block-end release sees an empty slot on the moved path.
func (em *emitter) markMoves(st ast.Stmt) {
	switch s := st.(type) {
	case *ast.Block:
		for _, inner := range s.Stmts {
			em.markMoves(inner)
		}
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			em.scanUnzeroed(d.Value)
		case *ast.ConstDecl:
			em.scanUnzeroed(d.Value)
		}
	case *ast.AssignStmt:
		em.scanUnzeroed(s.Target)
		em.scanUnzeroed(s.Value)
	case *ast.ExprStmt:
		em.scanUnzeroed(s.Expr)
	case *ast.DeferStmt:
		em.scanUnzeroed(s.Call)
	case *ast.ReturnStmt:
		em.scanUnzeroed(s.Value)
	case *ast.ForStmt:
		em.scanUnzeroed(s.Iter)
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
			em.scanUnzeroed(arm.Chan)
			em.scanUnzeroed(arm.Timeout)
			em.markStmtTree(arm.Body)
		}
	case *ast.IfExpr:
		em.scanUnzeroed(s.Cond)
		for _, inner := range s.Then.Stmts {
			em.markMoves(inner)
		}
		if s.Else != nil {
			em.markMoves(s.Else)
		}
	case *ast.MatchExpr:
		em.scanUnzeroed(s.Subject)
		for _, arm := range s.Arms {
			em.scanUnzeroed(arm.Guard)
			em.markStmtTree(arm.Body)
		}
	case *ast.DeadlineExpr:
		em.scanUnzeroed(s.Duration)
		for _, inner := range s.Body.Stmts {
			em.markMoves(inner)
		}
	}
}

func (em *emitter) markStmtTree(st ast.Stmt) {
	if b, ok := st.(*ast.Block); ok {
		for _, inner := range b.Stmts {
			em.markMoves(inner)
		}
		return
	}
	em.markMoves(st)
}

// scanUnzeroed finds the transfers that still rely on suppressing the source
// release. A direct value move is deliberately not marked: its codegen zeroes
// the source, and the release record must remain for the paths that did not
// move it.
func (em *emitter) scanUnzeroed(x ast.Expr) {
	switch v := x.(type) {
	case nil, *ast.LiteralExpr, *ast.UnitExpr, *ast.LaneHereExpr, *ast.NameExpr:
	case *ast.CallExpr:
		for _, a := range v.Args {
			em.scanUnzeroed(a.Expr)
		}
		em.scanUnzeroed(v.Callee)
	case *ast.MethodCallExpr:
		em.scanUnzeroed(v.Recv)
		for _, a := range v.Args {
			em.scanUnzeroed(a.Expr)
		}
	case *ast.FieldExpr:
		em.scanUnzeroed(v.Recv)
	case *ast.IndexExpr:
		em.scanUnzeroed(v.Recv)
		em.scanUnzeroed(v.Index)
	case *ast.SliceExpr:
		em.scanUnzeroed(v.Recv)
		em.scanUnzeroed(v.Lo)
		em.scanUnzeroed(v.Hi)
	case *ast.UnaryExpr:
		em.scanUnzeroed(v.X)
	case *ast.BinaryExpr:
		em.scanUnzeroed(v.L)
		em.scanUnzeroed(v.R)
	case *ast.NewExpr:
		for _, a := range v.Args {
			em.scanUnzeroed(a.Expr)
		}
	case *ast.AggregateExpr:
		for _, f := range v.Fields {
			em.scanUnzeroed(f.Value)
		}
	case *ast.ArrayLitExpr:
		for _, elem := range v.Elems {
			em.scanUnzeroed(elem)
		}
	case *ast.RangeExpr:
		em.scanUnzeroed(v.Lo)
		em.scanUnzeroed(v.Hi)
	case *ast.TryExpr:
		if em.consumesAdt(v.X) {
			em.markUnzeroedName(v.X)
		}
		em.scanUnzeroed(v.X)
	case *ast.AwaitExpr:
		em.scanUnzeroed(v.X)
	case *ast.UnwrapExpr:
		if em.consumesAdt(v.X) {
			em.markUnzeroedName(v.X)
		}
		em.scanUnzeroed(v.X)
	case *ast.SpawnExpr:
		em.scanUnzeroed(v.Call)
	case *ast.LambdaExpr:
		for _, st := range v.Body.Stmts {
			em.markMoves(st)
		}
	case *ast.InterpExpr:
		for _, p := range v.Parts {
			em.scanUnzeroed(p.Expr)
		}
	case *ast.IfExpr:
		em.markMoves(v)
	case *ast.MatchExpr:
		em.markMoves(v)
	case *ast.DeadlineExpr:
		em.markMoves(v)
	}
}

func (em *emitter) markUnzeroedName(x ast.Expr) {
	name, ok := x.(*ast.NameExpr)
	if !ok {
		return
	}
	vd := em.ownerForName(name.Name)
	if vd == nil || !em.moveType(vd.Type) {
		return
	}
	em.moved[vd] = true
}

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
