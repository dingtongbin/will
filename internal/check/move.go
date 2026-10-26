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
		m.own = own
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
	bag        diag.Bag
	own        *Ownership
	owns       func(*ast.Type) bool
	types      map[string]*ast.Type
	params     map[string]bool
	variadic   map[string]bool
	locals     map[string]bool
	borrowed   map[string]bool
	placeMoves []placeMove
	// lambdaDepth counts enclosing lambda bodies. A direct closure created in
	// the original function moves a local capture; a nested closure may move a
	// local declared in its immediate lambda body but copies from an outer
	// environment.
	lambdaDepth int
	// lambdaLocals is one declared-name set per open lambda body. A nested
	// closure may move only a name from the innermost set.
	lambdaLocals []map[string]bool
}

// placeMove is a field or index initializer that may move the place out when
// the root local has no later use. The candidate is accepted after the whole
// function has been scanned; a later use turns it back into O0002.
type placeMove struct {
	root string
	pos  source.Pos
}

// owned reports whether a declared type carries a heap buffer, which is what
// makes a transfer a move rather than a copy.
func owned(t *ast.Type) bool {
	return ast.OwnsHeap(t)
}

// exprType returns the declared type of a name, field, or index expression.
// It is deliberately small: the move pass only needs the place types it can
// name from the declarations in this function. An unknown type stays nil, so a
// copy is not rejected on a guess.
func (m *mover) exprType(e ast.Expr) *ast.Type {
	switch x := e.(type) {
	case *ast.NameExpr:
		return m.types[x.Name]
	case *ast.FieldExpr:
		recv := m.exprType(x.Recv)
		if m.own == nil {
			return nil
		}
		return m.own.FieldType(recv, x.Name)
	case *ast.IndexExpr:
		recv := m.exprType(x.Recv)
		if m.own == nil {
			return nil
		}
		return m.own.IndexType(recv)
	case *ast.ArrayLitExpr:
		return x.Type
	}
	return nil
}

// placeCopy reports O0002 when a transfer takes an owned field or element out
// of a place that still owns it. A declaration is allowed to move the place
// when the root local has no later use; the function-level check below turns it
// back into O0002 if any later use exists.
func (m *mover) placeCopy(e ast.Expr, allowMove bool) {
	if m.own == nil || e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.TryExpr:
		m.placeCopy(x.X, false)
		return
	case *ast.AwaitExpr:
		m.placeCopy(x.X, false)
		return
	case *ast.UnwrapExpr:
		m.placeCopy(x.X, false)
		return
	case *ast.FieldExpr:
		root := rootName(x.Recv)
		if root == "" || m.types[root] == nil || m.variadic[root] {
			return
		}
		if m.owns(m.own.FieldType(m.exprType(x.Recv), x.Name)) {
			if allowMove && m.canMovePlace(root) {
				m.placeMoves = append(m.placeMoves, placeMove{root: root, pos: x.Pos()})
				return
			}
			m.copyAt(x.Pos())
		}
	case *ast.IndexExpr:
		root := rootName(x.Recv)
		if root == "" || m.types[root] == nil || m.variadic[root] {
			return
		}
		if m.owns(m.own.IndexType(m.exprType(x.Recv))) {
			if allowMove && m.canMovePlace(root) {
				m.placeMoves = append(m.placeMoves, placeMove{root: root, pos: x.Pos()})
				return
			}
			m.copyAt(x.Pos())
		}
	case *ast.MethodCallExpr:
		m.containerGetCopy(x)
	}
}

func (m *mover) canMovePlace(root string) bool {
	if !m.locals[root] || m.params[root] || m.variadic[root] {
		return false
	}
	t := m.types[root]
	if t == nil || t.Kind == ast.ViewType {
		return false
	}
	if t.Kind == ast.NamedType && (t.Name == "rc" || t.Name == "weak") {
		return false
	}
	return true
}

// containerGetCopy reports O0002 when a predeclared container Get copies an
// owning element into a new owner. The call itself returns a copy; storing it
// without Clone would leave two owners of one buffer.
func (m *mover) containerGetCopy(call *ast.MethodCallExpr) {
	if m.own == nil || (call.Name != "Get" && call.Name != "Peek") {
		return
	}
	recv := m.exprType(call.Recv)
	if recv == nil {
		return
	}
	var elem *ast.Type
	switch recv.Kind {
	case ast.ArrayType, ast.ViewType:
		elem = m.own.IndexType(recv)
	case ast.NamedType:
		switch recv.Name {
		case "Array", "Map", "Set":
			elem = m.own.ElementType(recv)
		default:
			return
		}
	default:
		return
	}
	if m.owns(elem) {
		m.copyAt(call.Pos())
	}
}

func (m *mover) copyAt(pos source.Pos) {
	m.bag.Error(pos, diag.CodeMissingClone, copyMessage, copyHelp)
}

// borrowTransfer reports O0002 when a transfer would move an owning value out
// of a parameter, receiver, or pattern binding. Those are borrows of storage
// owned elsewhere, so the caller must make an explicit clone.
func (m *mover) borrowTransfer(name string, pos source.Pos) bool {
	if (!m.params[name] && !m.borrowed[name]) || m.variadic[name] {
		return false
	}
	if m.own != nil && m.own.IsTypeParam(m.types[name]) {
		// A bare type parameter has no concrete ownership until the generic
		// instance is checked. Treating it as always owning would reject every
		// constructor that stores its argument through a generic field.
		return false
	}
	m.copyAt(pos)
	return true
}

func (m *mover) funcDecl(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	m.types = map[string]*ast.Type{}
	m.params = map[string]bool{}
	m.variadic = map[string]bool{}
	m.locals = map[string]bool{}
	m.borrowed = map[string]bool{}
	m.placeMoves = nil
	m.lambdaDepth = 0
	m.lambdaLocals = nil
	params := map[string]bool{}
	if fn.Recv != nil {
		m.types[fn.Recv.Name] = fn.Recv.Type
		m.params[fn.Recv.Name] = true
	}
	for _, p := range fn.Params {
		m.types[p.Name] = p.Type
		m.params[p.Name] = true
		if p.Variadic {
			m.variadic[p.Name] = true
		}
		if m.owns(p.Type) && !p.Variadic {
			params[p.Name] = true
		}
	}
	m.block(fn.Body, false, params)
	last := lastUses(fn.Body)
	for _, move := range m.placeMoves {
		if use, ok := last[move.root]; ok && use.Offset > move.pos.Offset {
			m.copyAt(move.pos)
		}
	}
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
			if vd, ok := vs.Decl.(*ast.VarDecl); ok {
				if m.types != nil && vd.Name != "" && vd.Name != "_" {
					m.types[vd.Name] = vd.Type
				}
				if vd.Name != "" && vd.Name != "_" {
					m.locals[vd.Name] = true
					if m.lambdaDepth > 0 && len(m.lambdaLocals) > 0 {
						m.lambdaLocals[len(m.lambdaLocals)-1][vd.Name] = true
					}
				}
				if m.owns(vd.Type) && vd.Name != "" && vd.Name != "_" {
					live[vd.Name] = true
				}
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
			if !inherited[name] {
				continue
			}
			if mv.event < len(flows) && flows[mv.event].exits {
				continue
			}
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
			m.placeCopy(d.Value, true)
			if b, ok := d.Value.(*ast.NameExpr); ok && m.owns(d.Type) && b.Name != "_" && live[b.Name] {
				if !m.borrowTransfer(b.Name, b.Pos()) {
					// The value moves into the new binding.
					ev.transfers = append(ev.transfers, nameUse{name: b.Name, pos: b.Pos()})
					ev.uses = removeUse(ev.uses, b.Name, b.Pos())
				}
			}
		case *ast.ConstDecl:
			m.exprEvents(d.Value, live, &ev)
			m.placeCopy(d.Value, false)
		}
	case *ast.AssignStmt:
		m.exprEvents(s.Value, live, &ev)
		m.placeCopy(s.Value, true)
		discard := false
		if target, ok := s.Target.(*ast.NameExpr); ok && target.Name == "_" {
			discard = true
		}
		if b, ok := s.Value.(*ast.NameExpr); ok && live[b.Name] && !discard {
			if m.borrowTransfer(b.Name, b.Pos()) {
				return ev, flow
			}
			ev.transfers = append(ev.transfers, nameUse{name: b.Name, pos: b.Pos()})
			ev.uses = removeUse(ev.uses, b.Name, b.Pos())
		}
	case *ast.ExprStmt:
		m.exprEvents(s.Expr, live, &ev)
	case *ast.DeferStmt:
		m.exprEvents(s.Call, live, &ev)
	case *ast.ReturnStmt:
		m.exprEvents(s.Value, live, &ev)
		m.placeCopy(s.Value, true)
		if b, ok := s.Value.(*ast.NameExpr); ok && live[b.Name] {
			if m.borrowTransfer(b.Name, b.Pos()) {
				flow.exits = true
				return ev, flow
			}
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
		subjectType := m.exprType(s.Subject)
		for _, arm := range s.Arms {
			binds := map[string]*ast.Type{}
			m.patternBinds(arm.Pattern, subjectType, binds)
			savedTypes := map[string]*ast.Type{}
			savedBorrowed := map[string]bool{}
			armLive := map[string]bool{}
			for name, ok := range live {
				armLive[name] = ok
			}
			for name, typ := range binds {
				if name == "" || name == "_" {
					continue
				}
				savedTypes[name] = m.types[name]
				savedBorrowed[name] = m.borrowed[name]
				m.types[name] = typ
				m.borrowed[name] = true
				if m.owns(typ) {
					armLive[name] = true
				}
			}
			m.exprEvents(arm.Guard, armLive, &ev)
			_, armFlow := m.stmt(arm.Body, armLive)
			if name, pos, ok := armValueName(arm.Body); ok {
				if typ := binds[name]; m.owns(typ) {
					m.copyAt(pos)
				}
			}
			mergeFlow(&flow, armFlow, !armFlow.exits)
			for name := range binds {
				if old, ok := savedTypes[name]; ok {
					m.types[name] = old
				} else {
					delete(m.types, name)
				}
				if savedBorrowed[name] {
					m.borrowed[name] = true
				} else {
					delete(m.borrowed, name)
				}
			}
		}
	case *ast.DeadlineExpr:
		m.exprEvents(s.Duration, live, &ev)
		return ev, m.block(s.Body, false, live)
	case *ast.BreakStmt, *ast.ContinueStmt:
		flow.exits = true
	}
	return ev, flow
}

// armValueName returns the name a match arm yields directly. Such a name is a
// copy of the subject payload when it is an owning binding, so it needs Clone.
func armValueName(st ast.Stmt) (string, source.Pos, bool) {
	if b, ok := st.(*ast.Block); ok {
		if len(b.Stmts) == 0 {
			return "", source.Pos{}, false
		}
		return armValueName(b.Stmts[len(b.Stmts)-1])
	}
	es, ok := st.(*ast.ExprStmt)
	if !ok {
		return "", source.Pos{}, false
	}
	name, ok := es.Expr.(*ast.NameExpr)
	if !ok {
		return "", source.Pos{}, false
	}
	return name.Name, name.Pos(), true
}

// patternBinds records the type each name in one pattern receives. A pattern
// binding is a read borrow of the subject; transferring an owned payload out
// therefore needs an explicit Clone.
func (m *mover) patternBinds(pat *ast.Pattern, subject *ast.Type, out map[string]*ast.Type) {
	if pat == nil {
		return
	}
	switch pat.Kind {
	case ast.BindPattern:
		if pat.Name != "" && pat.Name != "_" {
			out[pat.Name] = subject
		}
	case ast.VariantPattern:
		args := m.variantArgTypes(subject, pat.Name)
		for i, child := range pat.Args {
			var typ *ast.Type
			if i < len(args) {
				typ = args[i]
			}
			m.patternBinds(child, typ, out)
		}
	case ast.OrPattern:
		if len(pat.Alts) != 0 {
			m.patternBinds(pat.Alts[0], subject, out)
		}
	}
}

// variantArgTypes returns the payload types of one variant pattern. The
// predeclared Option and Result variants are handled before user enums.
func (m *mover) variantArgTypes(subject *ast.Type, variant string) []*ast.Type {
	if subject == nil || subject.Kind != ast.NamedType || m.own == nil {
		return nil
	}
	name := shortTypeName(subject.Name)
	switch name {
	case "Option":
		if len(subject.Args) == 1 && variant != "None" {
			return []*ast.Type{subject.Args[0]}
		}
		return nil
	case "Result":
		if len(subject.Args) == 2 {
			switch variant {
			case "Ok":
				return []*ast.Type{subject.Args[0]}
			case "Err":
				return []*ast.Type{subject.Args[1]}
			}
		}
		return nil
	}
	enums := m.own.enums[name]
	if len(enums) != 1 {
		return nil
	}
	decl := enums[0]
	bindings := typeBindings(decl.TypeParams, subject.Args)
	for _, v := range decl.Variants {
		if v.Name != variant {
			continue
		}
		out := make([]*ast.Type, len(v.Args))
		for i, arg := range v.Args {
			out[i] = substituteType(arg, bindings)
		}
		return out
	}
	return nil
}

// containerTransfer reports whether a method call moves an argument into
// predeclared container storage. The receiver type comes from the nearest
// declaration in this function.
func (m *mover) containerTransfer(call *ast.MethodCallExpr) bool {
	name, ok := call.Recv.(*ast.NameExpr)
	if !ok || m.types == nil {
		return false
	}
	t := m.types[name.Name]
	if t == nil || t.Kind != ast.NamedType {
		return false
	}
	switch t.Name {
	case "Array":
		switch call.Name {
		case "Push", "Append", "Set", "Insert":
			return true
		}
	case "Map":
		return call.Name == "Set"
	case "Set":
		return call.Name == "Add"
	}
	return false
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
		transfer := transferArgs
		if name, ok := x.Callee.(*ast.NameExpr); ok {
			switch name.Name {
			case "Some", "Ok", "Err":
				transfer = true
			}
		}
		m.args(x.Args, live, ev, transfer)
	case *ast.MethodCallExpr:
		m.exprEventsIn(x.Recv, live, ev, false)
		// Send hands the value to the channel; the predeclared container
		// methods move the value into their storage. A user method with the
		// same name stays a borrow.
		transfer := transferArgs || x.Name == "Send" || m.containerTransfer(x)
		m.args(x.Args, live, ev, transfer)
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
	case *ast.ArrayLitExpr:
		for _, elem := range x.Elems {
			// A fixed-array element takes its value into the literal.
			m.args([]*ast.CallArg{{Expr: elem}}, live, ev, true)
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
		m.lambdaExpr(x, live, ev)
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
			if m.borrowTransfer(b.Name, b.Pos()) {
				continue
			}
			if ev.hasTransfer(b.Name) {
				// The same value cannot be moved twice in one place; the
				// second occurrence is a use of the moved value.
				ev.uses = append(ev.uses, nameUse{name: b.Name, pos: b.Pos()})
				continue
			}
			ev.transfers = append(ev.transfers, nameUse{name: b.Name, pos: b.Pos()})
			continue
		}
		if transfer {
			m.placeCopy(a.Expr, true)
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
