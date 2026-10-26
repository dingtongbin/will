package check

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

// Borrows reports O0003 and O0006 for one file. The analysis covers the shapes
// a single block can decide: a view made from a name in this block keeps that
// name read-borrowed until the block ends. While the borrow is live, a mut
// parameter, a mut receiver, or a write through an index is O0003, and moving
// the name is O0006. Calls whose declaration is outside this file are left
// alone because their parameter modes are not visible here.
func Borrows(f *ast.File) []diag.Diagnostic {
	return BorrowsWithOwnership(f, nil)
}

// BorrowsWithOwnership runs the borrow analysis with recursive type facts. A
// nil set keeps the syntax-only fallback for callers that have no declarations.
func BorrowsWithOwnership(f *ast.File, own *Ownership) []diag.Diagnostic {
	return BorrowsWithEffects(f, own, nil)
}

// BorrowsWithEffects adds the cross-file mut summaries of one package. Without
// them a call to a sibling file's mut method or mut parameter is invisible.
func BorrowsWithEffects(f *ast.File, own *Ownership, effects *MutEffects) []diag.Diagnostic {
	b := &borrower{
		own:        own,
		effects:    effects,
		funcs:      map[string]*ast.FuncDecl{},
		mutMethods: map[string]bool{},
		locals:     map[string]bool{},
		params:     map[string]bool{},
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		b.funcs[fn.Name] = fn
		if fn.Recv != nil && fn.Recv.Mut {
			b.mutMethods[fn.Name] = true
		}
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
			b.locals = map[string]bool{}
			b.params = map[string]bool{}
			b.localTypes = map[string]*ast.Type{}
			b.copied = map[string]source.Pos{}
			b.closureOwners = map[string]string{}
			for _, p := range fn.Params {
				b.params[p.Name] = true
				b.localTypes[p.Name] = p.Type
			}
			b.block(fn.Body, map[string]source.Pos{})
		}
	}
	return b.bag.Diagnostics()
}

type borrower struct {
	bag        diag.Bag
	own        *Ownership
	effects    *MutEffects
	funcs      map[string]*ast.FuncDecl
	mutMethods map[string]bool
	locals     map[string]bool
	params     map[string]bool
	localTypes map[string]*ast.Type
	// lastUses and localViews are scoped to one block. A view ends its
	// borrower's read borrow after its last lexical use; inherited means the
	// owner was already borrowed by an enclosing block and stays live there.
	lastUses   map[string]source.Pos
	localViews map[string]string
	inherited  map[string]bool
	// pinned names the owners whose borrow is captured by a defer, closure, or
	// spawn. Only those owners stay borrowed to the block end; an unrelated
	// closure no longer widens every view in the block.
	pinned map[string]bool
	// copied records a local initialized from a field or index place. Mutating
	// that local through a mut parameter or receiver is O0008: it changes the
	// copy, not the place the programmer named.
	copied map[string]source.Pos
	// closureOwners records a closure local whose body captures a view of a
	// local owner. Returning or storing that closure would let the view outlive
	// the owner.
	closureOwners map[string]string
}

func cloneBorrows(in map[string]source.Pos) map[string]source.Pos {
	out := make(map[string]source.Pos, len(in))
	for name, pos := range in {
		out[name] = pos
	}
	return out
}

func (b *borrower) block(blk *ast.Block, inherited map[string]source.Pos) {
	if blk == nil {
		return
	}
	borrowed := cloneBorrows(inherited)
	oldUses, oldViews, oldInherited, oldPinned := b.lastUses, b.localViews, b.inherited, b.pinned
	b.lastUses = lastUses(blk)
	b.localViews = map[string]string{}
	b.inherited = map[string]bool{}
	b.pinned = clonePins(b.pinned)
	for name := range pinnedNames(blk) {
		b.pinned[name] = true
	}
	for name := range inherited {
		b.inherited[name] = true
	}
	for _, st := range blk.Stmts {
		b.stmt(st, borrowed)
		b.addView(st, borrowed)
		b.prune(st, borrowed)
	}
	b.lastUses, b.localViews, b.inherited, b.pinned = oldUses, oldViews, oldInherited, oldPinned
}

func clonePins(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for name, ok := range in {
		out[name] = ok
	}
	return out
}

// addView records the read borrow a view declaration creates. The borrow lasts
// to the end of the block; a later block gets a copy and cannot widen it.
func (b *borrower) addView(st ast.Stmt, borrowed map[string]source.Pos) {
	vs, ok := st.(*ast.VarStmt)
	if !ok {
		return
	}
	vd, ok := vs.Decl.(*ast.VarDecl)
	if !ok || vd.Value == nil {
		return
	}
	owner, pos, ok := b.viewOwner(vd.Value)
	if !ok {
		// A view copied from another view keeps the same owner. The alias can
		// outlive the original name, so this block keeps the borrow until its
		// end rather than guessing at the alias's last use.
		if name, isName := vd.Value.(*ast.NameExpr); isName {
			owner = b.localViews[name.Name]
			if owner != "" && vd.Name != "" && vd.Name != "_" {
				b.localViews[vd.Name] = owner
				// Keep the owner borrowed until every alias's own last use.
				// prune removes one alias at a time and keeps the owner while
				// another view still points at it.
			}
		}
		return
	}
	if b.pinned[owner] || b.pinned[vd.Name] {
		b.pinned[owner] = true
	}
	if _, exists := borrowed[owner]; !exists {
		borrowed[owner] = pos
	}
	if vd.Name != "" && vd.Name != "_" {
		b.localViews[vd.Name] = owner
	}
}

// prune ends the borrow of every local view whose last use was in st. An
// enclosing block's view keeps the owner borrowed until that block ends.
func (b *borrower) prune(st ast.Stmt, borrowed map[string]source.Pos) {
	end := st.EndPos().Offset
	for view, owner := range b.localViews {
		if b.pinned[owner] || b.pinned[view] {
			continue
		}
		last, seen := b.lastUses[view]
		if seen && last.Offset > end {
			continue
		}
		delete(b.localViews, view)
		if b.inherited[owner] || b.hasLocalView(owner) {
			continue
		}
		delete(borrowed, owner)
	}
}

func (b *borrower) hasLocalView(owner string) bool {
	for _, current := range b.localViews {
		if current == owner {
			return true
		}
	}
	return false
}

// viewOwner names the value a slice or index borrows from. An index of an
// owned element is a value transfer, not a borrow; only an element that is a
// view (directly or inside a struct) keeps the owner borrowed.
func (b *borrower) viewOwner(e ast.Expr) (string, source.Pos, bool) {
	switch x := e.(type) {
	case *ast.SliceExpr:
		if name := rootName(x.Recv); name != "" {
			return name, x.Pos(), true
		}
	case *ast.IndexExpr:
		name := rootName(x.Recv)
		if name == "" {
			return "", source.Pos{}, false
		}
		if b.own == nil {
			return name, x.Pos(), true
		}
		elem := b.own.IndexType(b.exprType(x.Recv))
		if elem != nil && !b.own.HasView(elem) {
			return "", source.Pos{}, false
		}
		return name, x.Pos(), true
	}
	return "", source.Pos{}, false
}

// exprType returns the declared type of a name, field, or index expression.
// The borrow pass only needs places it can name from declarations in the file.
// It is also used to key the cross-file mut method summary by receiver type.
func (b *borrower) exprType(e ast.Expr) *ast.Type {
	switch x := e.(type) {
	case *ast.NameExpr:
		return b.localTypes[x.Name]
	case *ast.FieldExpr:
		if b.own == nil {
			return nil
		}
		return b.own.FieldType(b.exprType(x.Recv), x.Name)
	case *ast.IndexExpr:
		if b.own == nil {
			return nil
		}
		return b.own.IndexType(b.exprType(x.Recv))
	}
	return nil
}

func (b *borrower) stmt(st ast.Stmt, borrowed map[string]source.Pos) {
	switch s := st.(type) {
	case *ast.Block:
		b.block(s, borrowed)
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			b.expr(d.Value, borrowed)
			b.move(d.Value, borrowed)
			b.store(d.Value, borrowed)
			if d.Name != "" && d.Name != "_" {
				b.locals[d.Name] = true
				b.localTypes[d.Name] = d.Type
				b.noteCopiedPlace(d.Name, d.Value)
				b.noteClosure(d.Name, d.Value)
			}
		case *ast.ConstDecl:
			b.expr(d.Value, borrowed)
		}
	case *ast.AssignStmt:
		b.writeTarget(s.Target, borrowed)
		b.expr(s.Value, borrowed)
		b.move(s.Value, borrowed)
		b.store(s.Value, borrowed)
		if _, targetName := s.Target.(*ast.NameExpr); !targetName {
			if owner, ok := b.closureInExpr(s.Value); ok && b.locals[owner] {
				b.bag.Error(s.Value.Pos(), diag.CodeBorrowStored,
					"this borrow is stored where it outlives its owner",
					"store a value, not a borrow")
			}
		}
		if target, ok := s.Target.(*ast.NameExpr); ok && target.Name != "_" {
			b.noteCopiedPlace(target.Name, s.Value)
			b.noteClosure(target.Name, s.Value)
		}
	case *ast.ExprStmt:
		b.expr(s.Expr, borrowed)
	case *ast.DeferStmt:
		b.expr(s.Call, borrowed)
	case *ast.ReturnStmt:
		b.expr(s.Value, borrowed)
		b.move(s.Value, borrowed)
		b.escape(s.Value)
		b.store(s.Value, borrowed)
	case *ast.ForStmt:
		b.expr(s.Iter, borrowed)
		b.block(s.Body, borrowed)
	case *ast.RegionStmt:
		b.block(s.Body, borrowed)
	case *ast.ParallelStmt:
		b.block(s.Body, borrowed)
	case *ast.IfExpr:
		b.expr(s.Cond, borrowed)
		b.block(s.Then, borrowed)
		if s.Else != nil {
			b.stmt(s.Else, borrowed)
		}
	case *ast.MatchExpr:
		b.expr(s.Subject, borrowed)
		for _, arm := range s.Arms {
			b.expr(arm.Guard, borrowed)
			b.stmt(arm.Body, borrowed)
		}
	case *ast.DeadlineExpr:
		b.expr(s.Duration, borrowed)
		b.block(s.Body, borrowed)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			b.expr(arm.Chan, borrowed)
			b.expr(arm.Timeout, borrowed)
			b.stmt(arm.Body, borrowed)
		}
	}
}

// writeTarget reports a write through a borrowed value. A plain name
// assignment replaces the value, so it is a move rather than a mutable borrow.
func (b *borrower) writeTarget(t ast.Expr, borrowed map[string]source.Pos) {
	switch x := t.(type) {
	case *ast.NameExpr:
		if pos, ok := borrowed[x.Name]; ok {
			b.moveAt(x.Pos(), x.Name, pos)
		}
	case *ast.IndexExpr:
		b.expr(x.Index, borrowed)
		b.mutAt(x.Pos(), rootName(x.Recv), borrowed)
	case *ast.SliceExpr:
		b.expr(x.Lo, borrowed)
		b.expr(x.Hi, borrowed)
		b.mutAt(x.Pos(), rootName(x.Recv), borrowed)
	case *ast.FieldExpr:
		b.mutAt(x.Pos(), rootName(x.Recv), borrowed)
	}
}

// move reports a value transfer of a borrowed name.
func (b *borrower) move(e ast.Expr, borrowed map[string]source.Pos) {
	n, ok := e.(*ast.NameExpr)
	if !ok {
		return
	}
	if pos, ok := borrowed[n.Name]; ok {
		b.moveAt(n.Pos(), n.Name, pos)
	}
}

// escape reports a view returned from the function that owns the underlying
// value. Parameters are excluded because their object belongs to the caller.
func (b *borrower) escape(e ast.Expr) {
	if _, aggregate := e.(*ast.AggregateExpr); !aggregate {
		if owner, ok := b.closureInExpr(e); ok && b.locals[owner] {
			b.bag.Error(e.Pos(), diag.CodeBorrowOutlives,
				"this borrow outlives "+owner,
				"release the borrow first")
			return
		}
	}
	owner, pos, ok := b.viewOwner(e)
	if !ok || b.params[owner] || !b.locals[owner] {
		return
	}
	b.bag.Error(pos, diag.CodeBorrowOutlives,
		"this borrow outlives "+owner,
		"release the borrow first")
}

// store reports a view put into an aggregate field, where the aggregate can
// outlive the local the view points into.
func (b *borrower) store(e ast.Expr, borrowed map[string]source.Pos) {
	agg, ok := e.(*ast.AggregateExpr)
	if !ok {
		return
	}
	for _, f := range agg.Fields {
		if owner, ok := b.closureCaptureOwner(f.Value); ok && b.locals[owner] {
			b.bag.Error(f.Value.Pos(), diag.CodeBorrowStored,
				"this borrow is stored where it outlives its owner",
				"store a value, not a borrow")
			continue
		}
		owner, pos, ok := b.viewOwner(f.Value)
		if !ok || b.params[owner] || !b.locals[owner] {
			continue
		}
		_ = borrowed
		b.bag.Error(pos, diag.CodeBorrowStored,
			"this borrow is stored where it outlives its owner",
			"store a value, not a borrow")
	}
}

// noteCopiedPlace records a local created from a field or index place. A later
// mut parameter or receiver on that local reports O0008: the local is a copy,
// not a name for the place.
func (b *borrower) noteCopiedPlace(name string, value ast.Expr) {
	if name == "" || name == "_" {
		return
	}
	delete(b.copied, name)
	if !copyPlaceType(b.localTypes[name]) {
		return
	}
	root := ""
	switch v := value.(type) {
	case *ast.FieldExpr:
		root = rootName(v.Recv)
	case *ast.IndexExpr:
		root = rootName(v.Recv)
	}
	if root == "" || !b.params[root] {
		// A local root with no later use can move the field or element out;
		// that is not a copy of a place the caller can still name.
		return
	}
	b.copied[name] = value.Pos()
}

func copyPlaceType(t *ast.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ast.NamedType:
		switch t.Name {
		case "rc", "weak", "chan", "Task", "Error", "Value", "Show", "Duration", "raw", "str":
			return false
		}
		return true
	case ast.ArrayType:
		return true
	}
	return false
}

// noteClosure records a local closure that captures a view of a local owner.
// An alias of such a closure keeps the same owner.
func (b *borrower) noteClosure(name string, value ast.Expr) {
	if name == "" || name == "_" {
		return
	}
	delete(b.closureOwners, name)
	if owner, ok := b.closureCaptureOwner(value); ok {
		b.closureOwners[name] = owner
	}
}

// closureCaptureOwner finds a view captured by a lambda, or the owner carried
// by a closure variable named in the expression.
func (b *borrower) closureCaptureOwner(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.LambdaExpr:
		var owner string
		for _, st := range x.Body.Stmts {
			if o := b.closureViewInStmt(st); o != "" {
				owner = o
				break
			}
		}
		if owner == "" {
			return "", false
		}
		return owner, true
	case *ast.NameExpr:
		owner := b.closureOwners[x.Name]
		return owner, owner != ""
	}
	return "", false
}

func (b *borrower) closureViewInStmt(st ast.Stmt) string {
	var owner string
	visit := func(e ast.Expr) {
		if owner != "" || e == nil {
			return
		}
		if n, ok := e.(*ast.NameExpr); ok {
			if o := b.localViews[n.Name]; o != "" {
				owner = o
			}
		}
	}
	switch s := st.(type) {
	case *ast.Block:
		for _, inner := range s.Stmts {
			if owner = b.closureViewInStmt(inner); owner != "" {
				return owner
			}
		}
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			if lambda, ok := d.Value.(*ast.LambdaExpr); ok {
				for _, inner := range lambda.Body.Stmts {
					if owner = b.closureViewInStmt(inner); owner != "" {
						return owner
					}
				}
			} else {
				walkExprsForClosure(d.Value, visit)
			}
		case *ast.ConstDecl:
			walkExprsForClosure(d.Value, visit)
		}
	case *ast.AssignStmt:
		walkExprsForClosure(s.Target, visit)
		walkExprsForClosure(s.Value, visit)
	case *ast.ExprStmt:
		walkExprsForClosure(s.Expr, visit)
	case *ast.DeferStmt:
		walkExprsForClosure(s.Call, visit)
	case *ast.ReturnStmt:
		walkExprsForClosure(s.Value, visit)
	case *ast.ForStmt:
		walkExprsForClosure(s.Iter, visit)
		for _, inner := range s.Body.Stmts {
			if owner = b.closureViewInStmt(inner); owner != "" {
				return owner
			}
		}
	case *ast.RegionStmt:
		for _, inner := range s.Body.Stmts {
			if owner = b.closureViewInStmt(inner); owner != "" {
				return owner
			}
		}
	case *ast.ParallelStmt:
		for _, inner := range s.Body.Stmts {
			if owner = b.closureViewInStmt(inner); owner != "" {
				return owner
			}
		}
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			walkExprsForClosure(arm.Chan, visit)
			walkExprsForClosure(arm.Timeout, visit)
			if owner = b.closureViewInStmt(arm.Body); owner != "" {
				return owner
			}
		}
	case *ast.IfExpr:
		walkExprsForClosure(s.Cond, visit)
		for _, inner := range s.Then.Stmts {
			if owner = b.closureViewInStmt(inner); owner != "" {
				return owner
			}
		}
		if s.Else != nil {
			if owner = b.closureViewInStmt(s.Else); owner != "" {
				return owner
			}
		}
	case *ast.MatchExpr:
		walkExprsForClosure(s.Subject, visit)
		for _, arm := range s.Arms {
			walkExprsForClosure(arm.Guard, visit)
			if owner = b.closureViewInStmt(arm.Body); owner != "" {
				return owner
			}
		}
	case *ast.DeadlineExpr:
		walkExprsForClosure(s.Duration, visit)
		for _, inner := range s.Body.Stmts {
			if owner = b.closureViewInStmt(inner); owner != "" {
				return owner
			}
		}
	}
	return owner
}

// walkExprsForClosure is a small expression walker without the view scanner's
// type knowledge. It intentionally stops at nested lambda bodies because those
// are handled by closureViewInStmt when the lambda itself is a statement value.
func walkExprsForClosure(e ast.Expr, visit func(ast.Expr)) {
	if e == nil {
		return
	}
	visit(e)
	switch x := e.(type) {
	case *ast.CallExpr:
		walkExprsForClosure(x.Callee, visit)
		for _, a := range x.Args {
			walkExprsForClosure(a.Expr, visit)
		}
	case *ast.MethodCallExpr:
		walkExprsForClosure(x.Recv, visit)
		for _, a := range x.Args {
			walkExprsForClosure(a.Expr, visit)
		}
	case *ast.FieldExpr:
		walkExprsForClosure(x.Recv, visit)
	case *ast.IndexExpr:
		walkExprsForClosure(x.Recv, visit)
		walkExprsForClosure(x.Index, visit)
	case *ast.SliceExpr:
		walkExprsForClosure(x.Recv, visit)
		walkExprsForClosure(x.Lo, visit)
		walkExprsForClosure(x.Hi, visit)
	case *ast.UnaryExpr:
		walkExprsForClosure(x.X, visit)
	case *ast.BinaryExpr:
		walkExprsForClosure(x.L, visit)
		walkExprsForClosure(x.R, visit)
	case *ast.NewExpr:
		for _, a := range x.Args {
			walkExprsForClosure(a.Expr, visit)
		}
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			walkExprsForClosure(f.Value, visit)
		}
	case *ast.RangeExpr:
		walkExprsForClosure(x.Lo, visit)
		walkExprsForClosure(x.Hi, visit)
	case *ast.TryExpr:
		walkExprsForClosure(x.X, visit)
	case *ast.AwaitExpr:
		walkExprsForClosure(x.X, visit)
	case *ast.UnwrapExpr:
		walkExprsForClosure(x.X, visit)
	case *ast.SpawnExpr:
		walkExprsForClosure(x.Call, visit)
	case *ast.InterpExpr:
		for _, p := range x.Parts {
			walkExprsForClosure(p.Expr, visit)
		}
	case *ast.IfExpr:
		walkExprsForClosure(x.Cond, visit)
	case *ast.MatchExpr:
		walkExprsForClosure(x.Subject, visit)
		for _, arm := range x.Arms {
			walkExprsForClosure(arm.Guard, visit)
		}
	case *ast.DeadlineExpr:
		walkExprsForClosure(x.Duration, visit)
	}
}

// closureInExpr finds a closure that captures a local view anywhere inside an
// expression, including a constructor or a call argument.
func (b *borrower) closureInExpr(e ast.Expr) (string, bool) {
	if owner, ok := b.closureCaptureOwner(e); ok {
		return owner, true
	}
	switch x := e.(type) {
	case *ast.CallExpr:
		if owner, ok := b.closureInExpr(x.Callee); ok {
			return owner, true
		}
		for _, a := range x.Args {
			if owner, ok := b.closureInExpr(a.Expr); ok {
				return owner, true
			}
		}
	case *ast.MethodCallExpr:
		if owner, ok := b.closureInExpr(x.Recv); ok {
			return owner, true
		}
		for _, a := range x.Args {
			if owner, ok := b.closureInExpr(a.Expr); ok {
				return owner, true
			}
		}
	case *ast.FieldExpr:
		return b.closureInExpr(x.Recv)
	case *ast.IndexExpr:
		if owner, ok := b.closureInExpr(x.Recv); ok {
			return owner, true
		}
		return b.closureInExpr(x.Index)
	case *ast.SliceExpr:
		if owner, ok := b.closureInExpr(x.Recv); ok {
			return owner, true
		}
		if owner, ok := b.closureInExpr(x.Lo); ok {
			return owner, true
		}
		return b.closureInExpr(x.Hi)
	case *ast.UnaryExpr:
		return b.closureInExpr(x.X)
	case *ast.BinaryExpr:
		if owner, ok := b.closureInExpr(x.L); ok {
			return owner, true
		}
		return b.closureInExpr(x.R)
	case *ast.NewExpr:
		for _, a := range x.Args {
			if owner, ok := b.closureInExpr(a.Expr); ok {
				return owner, true
			}
		}
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			if owner, ok := b.closureInExpr(f.Value); ok {
				return owner, true
			}
		}
	case *ast.RangeExpr:
		if owner, ok := b.closureInExpr(x.Lo); ok {
			return owner, true
		}
		return b.closureInExpr(x.Hi)
	case *ast.TryExpr:
		return b.closureInExpr(x.X)
	case *ast.AwaitExpr:
		return b.closureInExpr(x.X)
	case *ast.UnwrapExpr:
		return b.closureInExpr(x.X)
	case *ast.SpawnExpr:
		return b.closureInExpr(x.Call)
	case *ast.InterpExpr:
		for _, p := range x.Parts {
			if owner, ok := b.closureInExpr(p.Expr); ok {
				return owner, true
			}
		}
	case *ast.IfExpr:
		if owner, ok := b.closureInExpr(x.Cond); ok {
			return owner, true
		}
	case *ast.MatchExpr:
		if owner, ok := b.closureInExpr(x.Subject); ok {
			return owner, true
		}
	case *ast.DeadlineExpr:
		return b.closureInExpr(x.Duration)
	}
	return "", false
}

func (b *borrower) copyNotBorrow(pos source.Pos) {
	b.bag.Error(pos, diag.CodeCopyNotBorrow,
		"this is a copy, not a borrow",
		"write through the place (xs[i].f = v), or use a method with a mut receiver")
}

func (b *borrower) mutAt(pos source.Pos, name string, borrowed map[string]source.Pos) {
	if name == "" {
		return
	}
	borrowPos, ok := borrowed[name]
	if !ok {
		return
	}
	b.bag.Add(diag.Diagnostic{
		Pos: pos, End: pos, Severity: diag.Error,
		Code:    diag.CodeBorrowConflict,
		Message: "cannot borrow " + name + " mutably while it is borrowed",
		Help:    "end the read borrow first",
		Notes:   []diag.Note{{Pos: borrowPos, Message: "borrowed here"}},
	})
}

// containerMutator names the predeclared methods that can reallocate or
// restructure container storage. A user method with the same name is not in
// this set, and writing one element is a mutable element borrow, not a
// structural change.
func (b *borrower) containerMutator(recv ast.Expr, name string) bool {
	n, ok := recv.(*ast.NameExpr)
	if !ok {
		return false
	}
	t := b.localTypes[n.Name]
	if t == nil || t.Kind != ast.NamedType {
		return false
	}
	switch t.Name {
	case "Array":
		switch name {
		case "Push", "Append", "Clear", "Reserve", "Resize":
			return true
		}
	case "Map":
		switch name {
		case "Set", "Clear", "Delete":
			return true
		}
	case "Set":
		switch name {
		case "Add", "Clear", "Delete":
			return true
		}
	}
	return false
}

func (b *borrower) containerMutAt(pos source.Pos, name string, borrowed map[string]source.Pos) {
	if name == "" {
		return
	}
	borrowPos, ok := borrowed[name]
	if !ok {
		return
	}
	b.bag.Add(diag.Diagnostic{
		Pos: pos, End: pos, Severity: diag.Error,
		Code:    diag.CodeContainerChanged,
		Message: "cannot change " + name + " while it is borrowed",
		Help:    "copy what you need before changing it",
		Notes:   []diag.Note{{Pos: borrowPos, Message: "borrowed here"}},
	})
}

func (b *borrower) moveAt(pos source.Pos, name string, borrowPos source.Pos) {
	b.bag.Add(diag.Diagnostic{
		Pos: pos, End: pos, Severity: diag.Error,
		Code:    diag.CodeMoveWhileBorrowed,
		Message: "cannot move " + name + " while it is borrowed",
		Help:    "end the borrow first",
		Notes:   []diag.Note{{Pos: borrowPos, Message: "borrowed here"}},
	})
}

// expr walks the expression shapes that can contain a call or a view. It does
// not need a type; a name is enough to name the borrow owner.
func (b *borrower) expr(e ast.Expr, borrowed map[string]source.Pos) {
	switch x := e.(type) {
	case nil:
		return
	case *ast.CallExpr:
		b.call(x, borrowed)
	case *ast.MethodCallExpr:
		if b.methodMutates(x) {
			root := rootName(x.Recv)
			if _, ok := b.copied[root]; ok {
				b.copyNotBorrow(x.Pos())
			} else {
				b.mutAt(x.Pos(), root, borrowed)
			}
		} else if b.containerMutator(x.Recv, x.Name) {
			b.containerMutAt(x.Pos(), rootName(x.Recv), borrowed)
		}
		b.expr(x.Recv, borrowed)
		for _, a := range x.Args {
			b.expr(a.Expr, borrowed)
			if owner, ok := b.closureInExpr(a.Expr); ok && b.locals[owner] {
				b.bag.Error(a.Expr.Pos(), diag.CodeBorrowStored,
					"this borrow is stored where it outlives its owner",
					"store a value, not a borrow")
			}
		}
	case *ast.FieldExpr:
		b.expr(x.Recv, borrowed)
	case *ast.IndexExpr:
		b.expr(x.Recv, borrowed)
		b.expr(x.Index, borrowed)
	case *ast.SliceExpr:
		b.expr(x.Recv, borrowed)
		b.expr(x.Lo, borrowed)
		b.expr(x.Hi, borrowed)
	case *ast.UnaryExpr:
		b.expr(x.X, borrowed)
	case *ast.BinaryExpr:
		b.expr(x.L, borrowed)
		b.expr(x.R, borrowed)
	case *ast.NewExpr:
		for _, a := range x.Args {
			b.expr(a.Expr, borrowed)
		}
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			b.expr(f.Value, borrowed)
		}
	case *ast.RangeExpr:
		b.expr(x.Lo, borrowed)
		b.expr(x.Hi, borrowed)
	case *ast.TryExpr:
		b.expr(x.X, borrowed)
	case *ast.AwaitExpr:
		b.expr(x.X, borrowed)
	case *ast.UnwrapExpr:
		b.expr(x.X, borrowed)
	case *ast.SpawnExpr:
		if owner, ok := b.closureInExpr(x.Call); ok && b.locals[owner] {
			b.bag.Error(x.Pos(), diag.CodeBorrowOutlives,
				"this borrow outlives "+owner,
				"release the borrow first")
		}
		b.expr(x.Call, borrowed)
	case *ast.DeadlineExpr:
		b.stmt(x, borrowed)
	case *ast.InterpExpr:
		for _, p := range x.Parts {
			b.expr(p.Expr, borrowed)
		}
	case *ast.IfExpr:
		b.stmt(x, borrowed)
	case *ast.MatchExpr:
		b.stmt(x, borrowed)
	case *ast.LambdaExpr:
		b.block(x.Body, borrowed)
	}
}

func (b *borrower) methodMutates(call *ast.MethodCallExpr) bool {
	if b.mutMethods[call.Name] {
		return true
	}
	return b.effects.MethodMut(b.exprType(call.Recv), call.Name)
}

func (b *borrower) call(call *ast.CallExpr, borrowed map[string]source.Pos) {
	name, ok := call.Callee.(*ast.NameExpr)
	if ok {
		var modes []bool
		if fn := b.funcs[name.Name]; fn != nil {
			modes = make([]bool, len(fn.Params))
			for i, p := range fn.Params {
				modes[i] = p.Mut
			}
		} else {
			modes = b.effects.FuncMut(name.Name)
		}
		for i, arg := range call.Args {
			if i >= len(modes) {
				break
			}
			if modes[i] {
				root := rootName(arg.Expr)
				if _, ok := b.copied[root]; ok {
					b.copyNotBorrow(arg.Pos())
				} else {
					b.mutAt(arg.Pos(), root, borrowed)
				}
			}
		}
	}
	b.expr(call.Callee, borrowed)
	for _, a := range call.Args {
		b.expr(a.Expr, borrowed)
		if owner, ok := b.closureInExpr(a.Expr); ok && b.locals[owner] {
			b.bag.Error(a.Expr.Pos(), diag.CodeBorrowStored,
				"this borrow is stored where it outlives its owner",
				"store a value, not a borrow")
		}
	}
}

func rootName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.NameExpr:
		return x.Name
	case *ast.FieldExpr:
		return rootName(x.Recv)
	case *ast.IndexExpr:
		return rootName(x.Recv)
	case *ast.SliceExpr:
		return rootName(x.Recv)
	}
	return ""
}
