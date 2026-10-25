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
	return MovesWithOwnership(f, nil)
}

// MovesWithOwnership runs the move analysis with recursive ownership facts for
// user structs, enums, arrays, and generic instances. A nil ownership set keeps
// the syntax-only behavior used by the small unit tests.
func MovesWithOwnership(f *ast.File, own *Ownership) []diag.Diagnostic {
	m := &mover{owns: func(t *ast.Type) bool { return ast.OwnsHeap(t) }}
	if own != nil {
		m.owns = own.Owns
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			m.funcDecl(fn)
		}
	}
	return m.bag.Diagnostics()
}

type mover struct {
	bag  diag.Bag
	owns func(*ast.Type) bool
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
		if m.owns(p.Type) {
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

// blockFlow is the ownership effect of one block: the names that can reach
// the next statement already moved, and whether every path leaves the block.
type blockFlow struct {
	moved map[string]source.Pos
	exits bool
}

func mergeFlow(dst *blockFlow, src blockFlow, reaches bool) {
	if !reaches {
		return
	}
	for name, pos := range src.moved {
		if _, ok := dst.moved[name]; !ok {
			dst.moved[name] = pos
		}
	}
}

func (m *mover) block(b *ast.Block, inLoop bool, inherited map[string]bool) blockFlow {
	out := blockFlow{moved: map[string]source.Pos{}}
	if b == nil {
		return out
	}
	live := map[string]bool{}
	for name, ok := range inherited {
		live[name] = ok
	}
	var seq []events
	var flows []blockFlow
	for _, st := range b.Stmts {
		ev, flow := m.stmt(st, live)
		seq = append(seq, ev)
		flows = append(flows, flow)
		// A declaration adds the name after its value is evaluated.
		if vs, ok := st.(*ast.VarStmt); ok {
			if vd, ok := vs.Decl.(*ast.VarDecl); ok && m.owns(vd.Type) && vd.Name != "" && vd.Name != "_" {
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
			if mv, ok := moved[tr.name]; ok {
				m.reportMove(tr.pos, mv.pos)
				continue
			}
			if laterUse(seq, i+1, tr.name) {
				m.bag.Error(tr.pos, diag.CodeMissingClone, copyMessage, copyHelp)
				continue
			}
			moved[tr.name] = moveRec{pos: tr.pos, event: i}
		}
		for name, pos := range flows[i].moved {
			if !live[name] {
				continue
			}
			if _, ok := moved[name]; !ok {
				moved[name] = moveRec{pos: pos, event: i}
			}
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

	for name, mv := range moved {
		if inherited[name] {
			out.moved[name] = mv.pos
		}
	}
	if len(flows) > 0 {
		out.exits = flows[len(flows)-1].exits
	}
	return out
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

// stmt returns the uses, transfers, and nested-block ownership effect of one
// statement. A nested block reports a move only if a path can reach the next
// statement after it.
func (m *mover) stmt(st ast.Stmt, live map[string]bool) (events, blockFlow) {
	var ev events
	flow := blockFlow{moved: map[string]source.Pos{}}
	switch s := st.(type) {
	case *ast.Block:
		return ev, m.block(s, false, live)
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			m.exprEvents(d.Value, live, &ev)
			if b, ok := d.Value.(*ast.NameExpr); ok && m.owns(d.Type) && b.Name != "_" && live[b.Name] {
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
		flow.exits = true
	case *ast.ForStmt:
		m.exprEvents(s.Iter, live, &ev)
		body := m.block(s.Body, true, live)
		if !body.exits {
			for name, pos := range body.moved {
				if live[name] {
					flow.moved[name] = pos
				}
			}
		}
	case *ast.RegionStmt:
		return ev, m.block(s.Body, false, live)
	case *ast.ParallelStmt:
		return ev, m.block(s.Body, false, live)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			m.exprEvents(arm.Chan, live, &ev)
			m.exprEvents(arm.Timeout, live, &ev)
			_, armFlow := m.stmt(arm.Body, live)
			mergeFlow(&flow, armFlow, !armFlow.exits)
		}
	case *ast.IfExpr:
		m.exprEvents(s.Cond, live, &ev)
		thenFlow := m.block(s.Then, false, live)
		elseFlow := blockFlow{moved: map[string]source.Pos{}}
		if s.Else != nil {
			_, elseFlow = m.stmt(s.Else, live)
		}
		mergeFlow(&flow, thenFlow, !thenFlow.exits)
		mergeFlow(&flow, elseFlow, !elseFlow.exits)
		flow.exits = thenFlow.exits && (s.Else != nil && elseFlow.exits)
	case *ast.MatchExpr:
		m.exprEvents(s.Subject, live, &ev)
		for _, arm := range s.Arms {
			m.exprEvents(arm.Guard, live, &ev)
			_, armFlow := m.stmt(arm.Body, live)
			mergeFlow(&flow, armFlow, !armFlow.exits)
		}
	case *ast.DeadlineExpr:
		m.exprEvents(s.Duration, live, &ev)
		return ev, m.block(s.Body, false, live)
	case *ast.BreakStmt, *ast.ContinueStmt:
		flow.exits = true
	}
	return ev, flow
}

// exprEvents collects the name occurrences of one expression.
func (m *mover) exprEvents(e ast.Expr, live map[string]bool, ev *events) {
	m.exprEventsIn(e, live, ev, false)
}

// exprEventsIn collects one expression. transferArgs says that the arguments
// of the calls inside it move their value (a spawn capture), which is the one
// place where passing a value is not a borrow.
func (m *mover) exprEventsIn(e ast.Expr, live map[string]bool, ev *events, transferArgs bool) {
	switch x := e.(type) {
	case nil, *ast.LiteralExpr, *ast.UnitExpr, *ast.LaneHereExpr:
	case *ast.NameExpr:
		if x.Name != "_" {
			ev.uses = append(ev.uses, nameUse{name: x.Name, pos: x.Pos()})
		}
	case *ast.CallExpr:
		m.exprEventsIn(x.Callee, live, ev, false)
		m.args(x.Args, live, ev, transferArgs)
	case *ast.MethodCallExpr:
		m.exprEventsIn(x.Recv, live, ev, false)
		// Send hands the value to the channel; every other method borrows it.
		m.args(x.Args, live, ev, transferArgs || x.Name == "Send")
	case *ast.FieldExpr:
		m.exprEventsIn(x.Recv, live, ev, false)
	case *ast.IndexExpr:
		m.exprEventsIn(x.Recv, live, ev, false)
		m.exprEventsIn(x.Index, live, ev, false)
	case *ast.SliceExpr:
		m.exprEventsIn(x.Recv, live, ev, false)
		m.exprEventsIn(x.Lo, live, ev, false)
		m.exprEventsIn(x.Hi, live, ev, false)
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			// A field takes the value into the aggregate.
			m.args([]*ast.CallArg{{Expr: f.Value}}, live, ev, true)
		}
	case *ast.NewExpr:
		for _, a := range x.Args {
			m.exprEventsIn(a.Expr, live, ev, false)
		}
	case *ast.UnaryExpr:
		m.exprEventsIn(x.X, live, ev, false)
	case *ast.BinaryExpr:
		m.exprEventsIn(x.L, live, ev, false)
		m.exprEventsIn(x.R, live, ev, false)
	case *ast.RangeExpr:
		m.exprEventsIn(x.Lo, live, ev, false)
		m.exprEventsIn(x.Hi, live, ev, false)
	case *ast.LambdaExpr:
		m.block(x.Body, false, live)
	case *ast.TryExpr:
		m.exprEventsIn(x.X, live, ev, false)
	case *ast.AwaitExpr:
		m.exprEventsIn(x.X, live, ev, false)
	case *ast.UnwrapExpr:
		m.exprEventsIn(x.X, live, ev, false)
	case *ast.SpawnExpr:
		m.exprEventsIn(x.Call, live, ev, true)
	case *ast.DeadlineExpr:
		m.stmt(x, live)
	case *ast.IfExpr:
		m.stmt(x, live)
	case *ast.MatchExpr:
		m.stmt(x, live)
	case *ast.InterpExpr:
		for _, part := range x.Parts {
			m.exprEventsIn(part.Expr, live, ev, false)
		}
	}
}

// args records the argument occurrences of one call. A bare name that is passed
// on (transfer) moves the value; otherwise the argument is a borrow and counts
// as a use.
func (m *mover) args(args []*ast.CallArg, live map[string]bool, ev *events, transfer bool) {
	for _, a := range args {
		if b, ok := a.Expr.(*ast.NameExpr); ok && live[b.Name] && transfer {
			if ev.hasTransfer(b.Name) {
				// The same value cannot be moved twice in one place; the
				// second occurrence is a use of the moved value.
				ev.uses = append(ev.uses, nameUse{name: b.Name, pos: b.Pos()})
				continue
			}
			ev.transfers = append(ev.transfers, nameUse{name: b.Name, pos: b.Pos()})
			continue
		}
		m.exprEventsIn(a.Expr, live, ev, false)
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
