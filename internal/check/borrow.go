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
	b := &borrower{
		funcs:      map[string]*ast.FuncDecl{},
		mutMethods: map[string]bool{},
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
			b.block(fn.Body, map[string]source.Pos{})
		}
	}
	return b.bag.Diagnostics()
}

type borrower struct {
	bag        diag.Bag
	funcs      map[string]*ast.FuncDecl
	mutMethods map[string]bool
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
	for _, st := range blk.Stmts {
		b.stmt(st, borrowed)
		b.addView(st, borrowed)
	}
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
	owner, pos, ok := viewOwner(vd.Value)
	if !ok {
		return
	}
	if _, exists := borrowed[owner]; !exists {
		borrowed[owner] = pos
	}
}

// viewOwner names the value a slice or index borrows from.
func viewOwner(e ast.Expr) (string, source.Pos, bool) {
	switch x := e.(type) {
	case *ast.SliceExpr:
		if name := rootName(x.Recv); name != "" {
			return name, x.Pos(), true
		}
	case *ast.IndexExpr:
		if name := rootName(x.Recv); name != "" {
			return name, x.Pos(), true
		}
	}
	return "", source.Pos{}, false
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
		case *ast.ConstDecl:
			b.expr(d.Value, borrowed)
		}
	case *ast.AssignStmt:
		b.writeTarget(s.Target, borrowed)
		b.expr(s.Value, borrowed)
		b.move(s.Value, borrowed)
	case *ast.ExprStmt:
		b.expr(s.Expr, borrowed)
	case *ast.DeferStmt:
		b.expr(s.Call, borrowed)
	case *ast.ReturnStmt:
		b.expr(s.Value, borrowed)
		b.move(s.Value, borrowed)
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
		if b.mutMethods[x.Name] {
			b.mutAt(x.Pos(), rootName(x.Recv), borrowed)
		}
		b.expr(x.Recv, borrowed)
		for _, a := range x.Args {
			b.expr(a.Expr, borrowed)
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
	case *ast.UnwrapExpr:
		b.expr(x.X, borrowed)
	case *ast.SpawnExpr:
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

func (b *borrower) call(call *ast.CallExpr, borrowed map[string]source.Pos) {
	name, ok := call.Callee.(*ast.NameExpr)
	if ok {
		if fn := b.funcs[name.Name]; fn != nil {
			for i, arg := range call.Args {
				if i >= len(fn.Params) {
					break
				}
				if fn.Params[i].Mut {
					b.mutAt(arg.Pos(), rootName(arg.Expr), borrowed)
				}
			}
		}
	}
	b.expr(call.Callee, borrowed)
	for _, a := range call.Args {
		b.expr(a.Expr, borrowed)
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
