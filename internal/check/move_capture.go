package check

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/source"
)

// captureUse is one outer name a lambda body reads.
type captureUse struct {
	name string
	pos  source.Pos
}

// lambdaExpr records the captures of one closure. A closure created directly in
// the function being analyzed moves its owned local captures; a nested closure
// reads from the outer environment and keeps the copy path. Uses inside the
// lambda body are not uses of the enclosing statement.
func (m *mover) lambdaExpr(lam *ast.LambdaExpr, live map[string]bool, ev *events) {
	if lam == nil || lam.Body == nil {
		return
	}
	start := len(ev.uses)
	savedTypes := cloneTypes(m.types)
	savedLocals := cloneNames(m.locals)
	savedBorrowed := cloneNames(m.borrowed)
	direct := m.lambdaDepth == 0
	m.lambdaDepth++
	m.lambdaLocals = append(m.lambdaLocals, map[string]bool{})
	m.block(lam.Body, false, live)
	m.lambdaLocals = m.lambdaLocals[:len(m.lambdaLocals)-1]
	m.lambdaDepth--
	m.types = savedTypes
	m.locals = savedLocals
	m.borrowed = savedBorrowed

	captures := lambdaCaptures(lam)
	captured := map[string]bool{}
	for _, cap := range captures {
		captured[cap.name] = true
	}
	if len(ev.uses) > start {
		kept := ev.uses[:start]
		for _, use := range ev.uses[start:] {
			if !captured[use.name] {
				kept = append(kept, use)
			}
		}
		ev.uses = kept
	}
	for _, cap := range captures {
		if !live[cap.name] {
			ev.uses = append(ev.uses, nameUse{name: cap.name, pos: cap.pos})
			continue
		}
		movable := direct
		if !movable && len(m.lambdaLocals) != 0 {
			movable = m.lambdaLocals[len(m.lambdaLocals)-1][cap.name]
		}
		if !movable {
			continue
		}
		typ := m.types[cap.name]
		if typ == nil || !m.owns(typ) || !m.locals[cap.name] ||
			m.params[cap.name] || m.borrowed[cap.name] || m.variadic[cap.name] {
			continue
		}
		if m.loopDepth > 0 {
			fresh := false
			if len(m.loopLocals) != 0 {
				fresh = m.loopLocals[len(m.loopLocals)-1][cap.name]
			}
			if !fresh {
				// Moving an outer owner on the first iteration would leave an
				// empty slot for the next one. Ask for a clone or a fresh local
				// declared inside the loop.
				m.copyAt(cap.pos)
				continue
			}
		}
		if m.borrowTransfer(cap.name, cap.pos) {
			continue
		}
		ev.transfers = append(ev.transfers, nameUse{name: cap.name, pos: cap.pos})
	}
}

func cloneTypes(in map[string]*ast.Type) map[string]*ast.Type {
	out := make(map[string]*ast.Type, len(in))
	for name, typ := range in {
		out[name] = typ
	}
	return out
}

func cloneNames(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for name, ok := range in {
		out[name] = ok
	}
	return out
}

// lambdaCaptures lists the names a lambda reads from outside its body. Names
// declared by the lambda's own parameters, locals, loop bindings, select binds,
// or match patterns do not escape that scope.
func lambdaCaptures(lam *ast.LambdaExpr) []captureUse {
	if lam == nil || lam.Body == nil {
		return nil
	}
	w := &captureWalker{seen: map[string]bool{}, scopes: []map[string]bool{{}}}
	for _, p := range lam.Params {
		w.declare(p.Name)
	}
	w.block(lam.Body)
	return w.uses
}

type captureWalker struct {
	scopes []map[string]bool
	seen   map[string]bool
	uses   []captureUse
}

func (w *captureWalker) push() {
	w.scopes = append(w.scopes, map[string]bool{})
}

func (w *captureWalker) pop() {
	w.scopes = w.scopes[:len(w.scopes)-1]
}

func (w *captureWalker) declare(name string) {
	if name == "" || name == "_" {
		return
	}
	w.scopes[len(w.scopes)-1][name] = true
}

func (w *captureWalker) bound(name string) bool {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if w.scopes[i][name] {
			return true
		}
	}
	return false
}

func (w *captureWalker) use(name string, pos source.Pos) {
	if name == "" || name == "_" || w.bound(name) || w.seen[name] {
		return
	}
	w.seen[name] = true
	w.uses = append(w.uses, captureUse{name: name, pos: pos})
}

func (w *captureWalker) block(b *ast.Block) {
	if b == nil {
		return
	}
	w.push()
	for _, st := range b.Stmts {
		w.stmt(st)
	}
	w.pop()
}

func (w *captureWalker) stmt(st ast.Stmt) {
	switch s := st.(type) {
	case nil:
	case *ast.Block:
		w.block(s)
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			w.expr(d.Value)
			w.declare(d.Name)
		case *ast.ConstDecl:
			w.expr(d.Value)
			w.declare(d.Name)
		}
	case *ast.AssignStmt:
		w.expr(s.Target)
		w.expr(s.Value)
	case *ast.ExprStmt:
		w.expr(s.Expr)
	case *ast.DeferStmt:
		w.expr(s.Call)
	case *ast.ReturnStmt:
		w.expr(s.Value)
	case *ast.ForStmt:
		w.expr(s.Iter)
		w.push()
		for _, name := range s.Names {
			w.declare(name)
		}
		w.block(s.Body)
		w.pop()
	case *ast.RegionStmt:
		w.block(s.Body)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			w.expr(arm.Chan)
			w.expr(arm.Timeout)
			w.push()
			w.declare(arm.Bind)
			w.stmt(arm.Body)
			w.pop()
		}
	case *ast.IfExpr:
		w.expr(s.Cond)
		w.block(s.Then)
		w.stmt(s.Else)
	case *ast.MatchExpr:
		w.expr(s.Subject)
		for _, arm := range s.Arms {
			w.push()
			w.pattern(arm.Pattern)
			w.expr(arm.Guard)
			w.stmt(arm.Body)
			w.pop()
		}
	case *ast.DeadlineExpr:
		w.expr(s.Duration)
		w.block(s.Body)
	case *ast.BreakStmt, *ast.ContinueStmt:
	}
}

func (w *captureWalker) pattern(p *ast.Pattern) {
	if p == nil {
		return
	}
	switch p.Kind {
	case ast.BindPattern:
		w.declare(p.Name)
	case ast.VariantPattern:
		for _, child := range p.Args {
			w.pattern(child)
		}
	case ast.OrPattern:
		if len(p.Alts) != 0 {
			w.pattern(p.Alts[0])
		}
	}
}

func (w *captureWalker) expr(e ast.Expr) {
	switch x := e.(type) {
	case nil, *ast.LiteralExpr, *ast.UnitExpr, *ast.LaneHereExpr:
	case *ast.NameExpr:
		w.use(x.Name, x.Pos())
	case *ast.CallExpr:
		w.expr(x.Callee)
		for _, a := range x.Args {
			w.expr(a.Expr)
		}
	case *ast.MethodCallExpr:
		w.expr(x.Recv)
		for _, a := range x.Args {
			w.expr(a.Expr)
		}
	case *ast.FieldExpr:
		w.expr(x.Recv)
	case *ast.IndexExpr:
		w.expr(x.Recv)
		w.expr(x.Index)
	case *ast.SliceExpr:
		w.expr(x.Recv)
		w.expr(x.Lo)
		w.expr(x.Hi)
	case *ast.UnaryExpr:
		w.expr(x.X)
	case *ast.BinaryExpr:
		w.expr(x.L)
		w.expr(x.R)
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			w.expr(f.Value)
		}
	case *ast.ArrayLitExpr:
		for _, elem := range x.Elems {
			w.expr(elem)
		}
	case *ast.RangeExpr:
		w.expr(x.Lo)
		w.expr(x.Hi)
	case *ast.LambdaExpr:
		w.push()
		for _, p := range x.Params {
			w.declare(p.Name)
		}
		w.block(x.Body)
		w.pop()
	case *ast.TryExpr:
		w.expr(x.X)
	case *ast.AwaitExpr:
		w.expr(x.X)
	case *ast.UnwrapExpr:
		w.expr(x.X)
	case *ast.SpawnExpr:
		w.expr(x.Call)
	case *ast.IfExpr:
		w.stmt(x)
	case *ast.MatchExpr:
		w.stmt(x)
	case *ast.DeadlineExpr:
		w.stmt(x)
	case *ast.InterpExpr:
		for _, part := range x.Parts {
			w.expr(part.Expr)
		}
	}
}
