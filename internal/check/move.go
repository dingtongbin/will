package check

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

// noteMovedHere marks where the value was moved, so a use of it afterwards has
// one real position to point at.
const noteMovedHere = "this value moved here"

// Move codes and their fixed text, from the diagnostics volume.
const (
	movedMessage = "this value has been moved"
	movedHelp    = "call Clone() to keep a copy, or take a borrow"
	copyMessage  = "a copy is required here and cannot be proven"
	copyHelp     = "call Clone()"
)

// Moves reports O0001 and O0002 over one file.
//
// A transfer is an assignment to a new binding, a return, or a call argument
// that hands one of these values on. The shapes covered today are the ones a
// block can decide without a control flow graph:
//
//   - a transfer followed by a use in the same block: the source is still
//     live, so a copy is required at the transfer (O0002), and the use itself
//     is not reported again;
//   - inside a loop body, a use that comes before the transfer in the body but
//     runs after it on the next iteration: the use reads a moved value
//     (O0001), with a note at the transfer.
//
// Anything else is left alone: a false move error would reject a correct
// program, so shapes the block cannot decide are not guessed at.
func Moves(f *ast.File) []diag.Diagnostic {
	m := &mover{}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			m.funcDecl(fn)
		}
	}
	return m.bag.Diagnostics()
}

type mover struct {
	bag diag.Bag
}

// owned reports whether a declared type carries a heap buffer, which is what
// makes a transfer a move rather than a copy.
func owned(t *ast.Type) bool {
	return ast.OwnsHeap(t)
}

func (m *mover) funcDecl(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	params := map[string]bool{}
	for _, p := range fn.Params {
		if owned(p.Type) {
			params[p.Name] = true
		}
	}
	m.block(fn.Body, false, params)
}

// nameUse is one occurrence of a name with its position.
type nameUse struct {
	name string
	pos  source.Pos
}

// events are the uses and transfers of one statement, in the order the
// statement evaluates them.
type events struct {
	uses      []nameUse
	transfers []nameUse
}

// hasTransfer reports whether this statement already transfers name.
func (e events) hasTransfer(name string) bool {
	for _, tr := range e.transfers {
		if tr.name == name {
			return true
		}
	}
	return false
}

func (m *mover) block(b *ast.Block, inLoop bool, inherited map[string]bool) {
	if b == nil {
		return
	}
	live := map[string]bool{}
	for name, ok := range inherited {
		live[name] = ok
	}
	var seq []events
	for _, st := range b.Stmts {
		ev := m.stmt(st, live)
		seq = append(seq, ev)
		// A declaration adds the name after its value is evaluated.
		if vs, ok := st.(*ast.VarStmt); ok {
			if vd, ok := vs.Decl.(*ast.VarDecl); ok && owned(vd.Type) && vd.Name != "" && vd.Name != "_" {
				live[vd.Name] = true
			}
		}
	}

	// A later use in the same block means the transfer cannot be a move.
	type moveRec struct {
		pos   source.Pos
		event int
	}
	moved := map[string]moveRec{}
	for i, ev := range seq {
		for _, tr := range ev.transfers {
			if !live[tr.name] {
				continue
			}
			if laterUse(seq, i+1, tr.name) {
				m.bag.Error(tr.pos, diag.CodeMissingClone, copyMessage, copyHelp)
				continue
			}
			moved[tr.name] = moveRec{pos: tr.pos, event: i}
		}
		for _, u := range ev.uses {
			if mv, ok := moved[u.name]; ok {
				m.reportMove(u.pos, mv.pos)
			}
		}
	}

	// On the next iteration of a loop, a use that comes before the transfer
	// in the body reads the value the body moved.
	if inLoop {
		for name, mv := range moved {
			for i := 0; i < mv.event; i++ {
				for _, u := range seq[i].uses {
					if u.name == name {
						m.reportMove(u.pos, mv.pos)
					}
				}
			}
		}
	}
}

// reportMove reports O0001 at the use, with the transfer as its one note.
func (m *mover) reportMove(use, transfer source.Pos) {
	m.bag.Add(diag.Diagnostic{
		Pos: use, End: use, Severity: diag.Error,
		Code: diag.CodeMoved, Message: movedMessage, Help: movedHelp,
		Notes: []diag.Note{{Pos: transfer, Message: noteMovedHere}},
	})
}

func laterUse(seq []events, from int, name string) bool {
	for i := from; i < len(seq); i++ {
		for _, u := range seq[i].uses {
			if u.name == name {
				return true
			}
		}
	}
	return false
}

// stmt returns the uses and transfers of one statement. Nested blocks are
// analyzed on their own, with their own scope.
func (m *mover) stmt(st ast.Stmt, live map[string]bool) events {
	var ev events
	switch s := st.(type) {
	case *ast.Block:
		m.block(s, false, live)
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			m.exprEvents(d.Value, live, &ev)
			if b, ok := d.Value.(*ast.NameExpr); ok && owned(d.Type) && b.Name != "_" && live[b.Name] {
				// The value moves into the new binding.
				ev.transfers = append(ev.transfers, nameUse{name: b.Name, pos: b.Pos()})
				ev.uses = removeUse(ev.uses, b.Name, b.Pos())
			}
		case *ast.ConstDecl:
			m.exprEvents(d.Value, live, &ev)
		}
	case *ast.AssignStmt:
		m.exprEvents(s.Value, live, &ev)
	case *ast.ExprStmt:
		m.exprEvents(s.Expr, live, &ev)
	case *ast.DeferStmt:
		m.exprEvents(s.Call, live, &ev)
	case *ast.ReturnStmt:
		m.exprEvents(s.Value, live, &ev)
		if b, ok := s.Value.(*ast.NameExpr); ok && live[b.Name] {
			ev.transfers = append(ev.transfers, nameUse{name: b.Name, pos: b.Pos()})
			ev.uses = removeUse(ev.uses, b.Name, b.Pos())
		}
	case *ast.ForStmt:
		m.exprEvents(s.Iter, live, &ev)
		m.block(s.Body, true, live)
	case *ast.RegionStmt:
		m.block(s.Body, false, live)
	case *ast.ParallelStmt:
		m.block(s.Body, false, live)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			m.exprEvents(arm.Chan, live, &ev)
			m.exprEvents(arm.Timeout, live, &ev)
			m.stmt(arm.Body, live)
		}
	case *ast.IfExpr:
		m.exprEvents(s.Cond, live, &ev)
		m.block(s.Then, false, live)
		if s.Else != nil {
			m.stmt(s.Else, live)
		}
	case *ast.MatchExpr:
		m.exprEvents(s.Subject, live, &ev)
		for _, arm := range s.Arms {
			m.exprEvents(arm.Guard, live, &ev)
			m.stmt(arm.Body, live)
		}
	case *ast.DeadlineExpr:
		m.exprEvents(s.Duration, live, &ev)
		m.block(s.Body, false, live)
	}
	return ev
}

// exprEvents collects the name occurrences of one expression. A bare name that
// is an argument of a call is a transfer; every other occurrence is a use.
func (m *mover) exprEvents(e ast.Expr, live map[string]bool, ev *events) {
	switch x := e.(type) {
	case nil, *ast.LiteralExpr, *ast.UnitExpr, *ast.LaneHereExpr:
	case *ast.NameExpr:
		if x.Name != "_" {
			ev.uses = append(ev.uses, nameUse{name: x.Name, pos: x.Pos()})
		}
	case *ast.CallExpr, *ast.MethodCallExpr:
		var args []*ast.CallArg
		switch c := x.(type) {
		case *ast.CallExpr:
			m.exprEvents(c.Callee, live, ev)
			args = c.Args
		case *ast.MethodCallExpr:
			m.exprEvents(c.Recv, live, ev)
			args = c.Args
		}
		for _, a := range args {
			if b, ok := a.Expr.(*ast.NameExpr); ok && live[b.Name] {
				if ev.hasTransfer(b.Name) {
					// The same value cannot be moved twice in one call; the
					// second occurrence is a use of the moved value.
					ev.uses = append(ev.uses, nameUse{name: b.Name, pos: b.Pos()})
					continue
				}
				ev.transfers = append(ev.transfers, nameUse{name: b.Name, pos: b.Pos()})
				continue
			}
			m.exprEvents(a.Expr, live, ev)
		}
	case *ast.FieldExpr:
		m.exprEvents(x.Recv, live, ev)
	case *ast.IndexExpr:
		m.exprEvents(x.Recv, live, ev)
		m.exprEvents(x.Index, live, ev)
	case *ast.SliceExpr:
		m.exprEvents(x.Recv, live, ev)
		m.exprEvents(x.Lo, live, ev)
		m.exprEvents(x.Hi, live, ev)
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			m.exprEvents(f.Value, live, ev)
		}
	case *ast.NewExpr:
		for _, a := range x.Args {
			m.exprEvents(a.Expr, live, ev)
		}
	case *ast.UnaryExpr:
		m.exprEvents(x.X, live, ev)
	case *ast.BinaryExpr:
		m.exprEvents(x.L, live, ev)
		m.exprEvents(x.R, live, ev)
	case *ast.RangeExpr:
		m.exprEvents(x.Lo, live, ev)
		m.exprEvents(x.Hi, live, ev)
	case *ast.LambdaExpr:
		m.block(x.Body, false, live)
	case *ast.TryExpr:
		m.exprEvents(x.X, live, ev)
	case *ast.UnwrapExpr:
		m.exprEvents(x.X, live, ev)
	case *ast.SpawnExpr:
		m.exprEvents(x.Call, live, ev)
	case *ast.DeadlineExpr:
		m.stmt(x, live)
	case *ast.IfExpr:
		m.stmt(x, live)
	case *ast.MatchExpr:
		m.stmt(x, live)
	case *ast.InterpExpr:
		for _, part := range x.Parts {
			m.exprEvents(part.Expr, live, ev)
		}
	}
}

// removeUse drops one name occurrence that was reclassified as a transfer.
func removeUse(uses []nameUse, name string, pos source.Pos) []nameUse {
	out := uses[:0]
	for _, u := range uses {
		if u.name == name && u.pos == pos {
			continue
		}
		out = append(out, u)
	}
	return out
}
