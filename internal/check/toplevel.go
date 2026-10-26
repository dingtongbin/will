// This file owns O0009: a top-level var is writable only while the package
// initializes. Every function body runs after that point, so a write through a
// top-level name is rejected there.
package check

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

const topWriteHelp = "put the mutable state behind an rc and a guard"

// TopLevelWrites reports O0009 for writes to a package-level var or const from
// any function body, including writes through a field or index. A call to a
// mut receiver or a mut parameter counts as a write. Top-level handles of rc,
// weak, chan, and Task are immutable bindings; their pointees stay usable.
func TopLevelWrites(f *ast.File) []diag.Diagnostic {
	return TopLevelWritesWithEffects(f, nil)
}

// TopLevelWritesWithEffects adds the package-wide mut summaries, so a write
// through a sibling file's mut method or mut parameter is still O0009.
func TopLevelWritesWithEffects(f *ast.File, effects *MutEffects) []diag.Diagnostic {
	if f == nil {
		return nil
	}
	w := &topWriteWalker{
		top:        map[string]*ast.Type{},
		consts:     map[string]bool{},
		funcs:      map[string]*ast.FuncDecl{},
		mutMethods: map[string]bool{},
		effects:    effects,
	}
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.VarDecl:
			w.top[decl.Name] = decl.Type
		case *ast.ConstDecl:
			w.consts[decl.Name] = true
		case *ast.FuncDecl:
			w.funcs[decl.Name] = decl
			if decl.Recv != nil && decl.Recv.Mut {
				w.mutMethods[decl.Name] = true
			}
		}
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		scope := map[string]bool{}
		if fn.Recv != nil {
			scope[fn.Recv.Name] = true
		}
		for _, p := range fn.Params {
			scope[p.Name] = true
		}
		w.block(fn.Body, scope)
	}
	return w.bag.Diagnostics()
}

type topWriteWalker struct {
	bag        diag.Bag
	top        map[string]*ast.Type
	consts     map[string]bool
	funcs      map[string]*ast.FuncDecl
	mutMethods map[string]bool
	effects    *MutEffects
	scopes     []map[string]bool
}

func (w *topWriteWalker) shadowed(name string) bool {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if w.scopes[i][name] {
			return true
		}
	}
	return false
}

func (w *topWriteWalker) isTop(name string) bool {
	if name == "" || w.shadowed(name) {
		return false
	}
	_, variable := w.top[name]
	return variable || w.consts[name]
}

func (w *topWriteWalker) report(name string, pos source.Pos) {
	w.bag.Error(pos, diag.CodeTopLevelWrite,
		"top-level "+name+" is published and read-only",
		topWriteHelp)
}

func (w *topWriteWalker) push(scope map[string]bool) {
	if scope == nil {
		scope = map[string]bool{}
	}
	w.scopes = append(w.scopes, scope)
}

func (w *topWriteWalker) pop() {
	w.scopes = w.scopes[:len(w.scopes)-1]
}

func (w *topWriteWalker) block(b *ast.Block, scope map[string]bool) {
	if b == nil {
		return
	}
	w.push(scope)
	for _, st := range b.Stmts {
		w.stmt(st)
	}
	w.pop()
}

func (w *topWriteWalker) stmt(st ast.Stmt) {
	switch s := st.(type) {
	case *ast.Block:
		w.block(s, nil)
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			w.expr(d.Value)
			if d.Name != "" && d.Name != "_" {
				w.scopes[len(w.scopes)-1][d.Name] = true
			}
		case *ast.ConstDecl:
			w.expr(d.Value)
			if d.Name != "" && d.Name != "_" {
				w.scopes[len(w.scopes)-1][d.Name] = true
			}
		}
	case *ast.AssignStmt:
		w.write(s.Target)
		w.expr(s.Value)
	case *ast.ExprStmt:
		w.expr(s.Expr)
	case *ast.DeferStmt:
		w.expr(s.Call)
	case *ast.ReturnStmt:
		w.expr(s.Value)
	case *ast.ForStmt:
		w.expr(s.Iter)
		scope := map[string]bool{}
		for _, name := range s.Names {
			scope[name] = true
		}
		w.block(s.Body, scope)
	case *ast.RegionStmt:
		w.block(s.Body, nil)
	case *ast.ParallelStmt:
		w.block(s.Body, nil)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			scope := map[string]bool{}
			if arm.Bind != "" && arm.Bind != "_" {
				scope[arm.Bind] = true
			}
			w.push(scope)
			w.expr(arm.Chan)
			w.expr(arm.Timeout)
			w.stmt(arm.Body)
			w.pop()
		}
	case *ast.IfExpr:
		w.expr(s.Cond)
		w.block(s.Then, nil)
		if s.Else != nil {
			w.stmt(s.Else)
		}
	case *ast.MatchExpr:
		w.expr(s.Subject)
		for _, arm := range s.Arms {
			scope := map[string]bool{}
			patternNames(arm.Pattern, scope)
			w.push(scope)
			w.expr(arm.Guard)
			w.stmt(arm.Body)
			w.pop()
		}
	case *ast.DeadlineExpr:
		w.expr(s.Duration)
		w.block(s.Body, nil)
	}
}

func patternNames(p *ast.Pattern, out map[string]bool) {
	if p == nil {
		return
	}
	if p.Kind == ast.BindPattern && p.Name != "" && p.Name != "_" {
		out[p.Name] = true
	}
	for _, a := range p.Args {
		patternNames(a, out)
	}
	for _, a := range p.Alts {
		patternNames(a, out)
	}
}

// write checks one assignment target. A root top-level handle of rc, weak,
// chan, or Task may still have its pointee mutated; only the binding itself is
// read-only.
func (w *topWriteWalker) write(target ast.Expr) {
	root := rootName(target)
	if w.isTop(root) {
		handle := isHandleType(w.top[root])
		if _, plain := target.(*ast.NameExpr); plain || !handle {
			w.report(root, target.Pos())
		}
	}
	switch x := target.(type) {
	case *ast.IndexExpr:
		w.expr(x.Index)
		w.expr(x.Recv)
	case *ast.SliceExpr:
		w.expr(x.Lo)
		w.expr(x.Hi)
		w.expr(x.Recv)
	case *ast.FieldExpr:
		w.expr(x.Recv)
	}
}

func isHandleType(t *ast.Type) bool {
	if t == nil || t.Kind != ast.NamedType {
		return false
	}
	switch t.Name {
	case "rc", "weak", "chan", "Task":
		return true
	}
	return false
}

func (w *topWriteWalker) expr(e ast.Expr) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.CallExpr:
		if name, ok := x.Callee.(*ast.NameExpr); ok {
			var modes []bool
			if fn := w.funcs[name.Name]; fn != nil {
				modes = make([]bool, len(fn.Params))
				for i, p := range fn.Params {
					modes[i] = p.Mut
				}
			} else {
				modes = w.effects.FuncMut(name.Name)
			}
			for i, arg := range x.Args {
				if i < len(modes) && modes[i] && w.isTop(rootName(arg.Expr)) {
					w.report(rootName(arg.Expr), arg.Pos())
				}
			}
		}
		w.expr(x.Callee)
		for _, a := range x.Args {
			w.expr(a.Expr)
		}
	case *ast.MethodCallExpr:
		root := rootName(x.Recv)
		if w.isTop(root) && !isHandleType(w.top[root]) &&
			w.mutatingMethod(w.top[root], x.Name) {
			w.report(root, x.Pos())
		}
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
	case *ast.NewExpr:
		for _, a := range x.Args {
			w.expr(a.Expr)
		}
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
	case *ast.TryExpr:
		w.expr(x.X)
	case *ast.AwaitExpr:
		w.expr(x.X)
	case *ast.UnwrapExpr:
		w.expr(x.X)
	case *ast.SpawnExpr:
		w.expr(x.Call)
	case *ast.InterpExpr:
		for _, p := range x.Parts {
			w.expr(p.Expr)
		}
	case *ast.IfExpr:
		w.stmt(x)
	case *ast.MatchExpr:
		w.stmt(x)
	case *ast.DeadlineExpr:
		w.stmt(x)
	case *ast.LambdaExpr:
		scope := map[string]bool{}
		for _, p := range x.Params {
			scope[p.Name] = true
		}
		w.block(x.Body, scope)
	}
}

func (w *topWriteWalker) mutatingMethod(recv *ast.Type, name string) bool {
	if w.mutMethods[name] || w.effects.MethodMut(recv, name) {
		return true
	}
	switch name {
	case "Push", "Append", "Set", "Insert", "Remove", "Pop", "Clear", "Reserve", "Resize", "ShrinkToFit", "Add", "Delete":
		return true
	}
	return false
}
