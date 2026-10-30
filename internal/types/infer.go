package types

import (
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/token"
)

// inferScope is one lexical scope of the inference pass.
type inferScope struct {
	parent *inferScope
	vars   map[string]*Type
	result *Type
	tp     map[string]bool
	// async is set by an async function body. allowAsyncCall is raised while
	// an await, wait, or spawn operand is inferred, so the async call itself
	// is consumed instead of reported.
	async          bool
	allowAsyncCall bool
	parallel       bool
}

func newInferScope(parent *inferScope) *inferScope {
	s := &inferScope{parent: parent, vars: map[string]*Type{}}
	if parent != nil {
		s.result = parent.result
		s.tp = parent.tp
		s.async = parent.async
		s.allowAsyncCall = parent.allowAsyncCall
		s.parallel = parent.parallel
	}
	return s
}

func (s *inferScope) lookup(name string) (*Type, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if t, ok := cur.vars[name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (c *checker) inferFile(f *ast.File) {
	global := newInferScope(nil)
	c.inferGlobals(global, f)
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.ConstDecl:
			c.checkValue(global, decl.Value, c.env.vars[decl.Name])
		case *ast.VarDecl:
			c.checkValue(global, decl.Value, c.env.vars[decl.Name])
		case *ast.StructDecl:
			tp := typeParamSet(decl.TypeParams)
			fs := newInferScope(nil)
			fs.tp = tp
			for _, fd := range decl.Fields {
				if fd.Default != nil {
					c.checkValue(fs, fd.Default, c.buildType(fd.Type, tp))
				}
			}
		case *ast.FuncDecl:
			c.inferFunc(decl)
		case *ast.LaneDecl:
			if decl.Decl != nil {
				c.inferFile(&ast.File{Decls: []ast.Decl{decl.Decl}})
			}
		}
	}
}

// deferSuspends reports whether a defer body contains await or calls an async
// function. A defer runs on the normal return path, so it cannot become a
// suspension point.
func (c *checker) deferSuspends(s *inferScope, e ast.Expr) bool {
	var hasAwait func(ast.Expr) bool
	hasAwait = func(e ast.Expr) bool {
		switch x := e.(type) {
		case nil:
			return false
		case *ast.AwaitExpr:
			return true
		case *ast.CallExpr:
			if hasAwait(x.Callee) {
				return true
			}
			for _, a := range x.Args {
				if hasAwait(a.Expr) {
					return true
				}
			}
		case *ast.MethodCallExpr:
			if hasAwait(x.Recv) {
				return true
			}
			for _, a := range x.Args {
				if hasAwait(a.Expr) {
					return true
				}
			}
		case *ast.UnaryExpr:
			return hasAwait(x.X)
		case *ast.BinaryExpr:
			return hasAwait(x.L) || hasAwait(x.R)
		}
		return false
	}
	if hasAwait(e) {
		return true
	}
	switch call := e.(type) {
	case *ast.CallExpr:
		name, ok := call.Callee.(*ast.NameExpr)
		if !ok {
			return false
		}
		sig := c.env.funcs[name.Name]
		return sig != nil && sig.async
	case *ast.MethodCallExpr:
		recv := c.inferExpr(s, call.Recv)
		if recv == nil || recv.Kind() != NamedKind {
			return false
		}
		if sig := c.env.methods[recv.Name()+"."+call.Name]; sig != nil {
			return sig.async
		}
		for _, m := range c.env.ifaces[recv.Name()] {
			if m.name == call.Name {
				return m.sig != nil && m.sig.async
			}
		}
	}
	return false
}

// inferGlobals resolves top-level var and const initializers before any
// function body is checked, so an inferred global has a type at every use.
func (c *checker) inferGlobals(global *inferScope, f *ast.File) {
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.ConstDecl:
			t := c.buildType(decl.Type, nil)
			if t == nil && decl.Value != nil {
				t = c.inferExpr(global, decl.Value)
			}
			c.env.vars[decl.Name] = t
			global.vars[decl.Name] = t
			if decl.Value != nil {
				c.checkValue(global, decl.Value, t)
			}
		case *ast.VarDecl:
			t := c.buildType(decl.Type, nil)
			if t == nil && decl.Value != nil {
				t = c.inferExpr(global, decl.Value)
			}
			c.env.vars[decl.Name] = t
			global.vars[decl.Name] = t
			if decl.Value != nil {
				c.checkValue(global, decl.Value, t)
			}
		}
	}
}

func (c *checker) inferFunc(decl *ast.FuncDecl) {
	tp := typeParamSet(decl.TypeParams)
	if decl.Recv != nil && len(decl.Recv.Type.Args) != 0 {
		// A method of a generic type names the type parameters in its
		// receiver. They are free in the body like the method's own.
		if tp == nil {
			tp = map[string]bool{}
		}
		for _, a := range decl.Recv.Type.Args {
			if a.Kind == ast.NamedType && len(a.Args) == 0 {
				tp[a.Name] = true
			}
		}
	}
	s := newInferScope(nil)
	s.tp = tp
	s.async = decl.Async
	s.result = c.buildType(decl.Result, tp)
	if decl.Recv != nil {
		recvType := c.buildType(decl.Recv.Type, tp)
		s.vars[decl.Recv.Name] = recvType
		s.vars["self"] = recvType
	}
	for _, pm := range decl.Params {
		t := c.buildType(pm.Type, tp)
		if pm.Variadic && t != nil {
			t = c.in.view(t)
		}
		s.vars[pm.Name] = t
	}
	for i, pm := range decl.Params {
		if pm.Default == nil {
			continue
		}
		if pm.Variadic {
			c.bag.Error(pm.Default.Pos(), diag.CodeBadArgument,
				"a variadic parameter has no default",
				"remove the default value")
			continue
		}
		if !simpleDefault(pm.Default) {
			c.bag.Error(pm.Default.Pos(), diag.CodeBadArgument,
				"a default value must be a constant expression",
				"use a literal or a simple constant")
		}
		for name := range defaultNames(pm.Default) {
			for j := i + 1; j < len(decl.Params); j++ {
				if decl.Params[j].Name == name {
					c.bag.Error(pm.Default.Pos(), diag.CodeBadArgument,
						"a default value cannot refer to a later parameter",
						"move "+name+" before this parameter")
				}
			}
		}
		c.checkValue(s, pm.Default, c.buildType(pm.Type, tp))
	}
	body := c.inferBlock(s, decl.Body)
	c.checkFrom(decl, s)
	if s.result == nil && body != nil && body.Kind() == NamedKind && body.Name() == "Result" {
		if n := len(decl.Body.Stmts); n != 0 {
			c.bag.Error(decl.Body.Stmts[n-1].Pos(), diag.CodeResultDropped,
				"this Result is not handled",
				"use ?, !, match, or discard it with _ =")
		}
	}
}

// simpleDefault accepts the expression shapes a default value may use. The
// value is evaluated at the call site, so it must not observe call-local state.
func defaultNames(e ast.Expr) map[string]bool {
	out := map[string]bool{}
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch x := e.(type) {
		case *ast.NameExpr:
			out[x.Name] = true
		case *ast.UnaryExpr:
			walk(x.X)
		case *ast.BinaryExpr:
			walk(x.L)
			walk(x.R)
		}
	}
	walk(e)
	return out
}

func simpleDefault(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.LiteralExpr, *ast.NameExpr:
		return true
	case *ast.UnaryExpr:
		return simpleDefault(x.X)
	case *ast.BinaryExpr:
		return simpleDefault(x.L) && simpleDefault(x.R)
	}
	return false
}

// checkFrom enforces the return-borrow source rule. A result that contains a
// borrow needs exactly one source parameter or an explicit from list. The
// checker does not guess a source when several inputs could own the data.
func (c *checker) checkFrom(decl *ast.FuncDecl, s *inferScope) {
	result := c.buildType(decl.Result, s.tp)
	if result == nil {
		return
	}
	borrows := c.borrowsType(result, map[*Type]bool{})
	if !borrows {
		if len(decl.From) != 0 {
			c.bag.Error(decl.From[0].Pos(), diag.CodeBorrowOutlives,
				"from is only valid on a function that returns a borrow",
				"remove from, or return a ref/view")
		}
		return
	}
	eligible := 0
	names := map[string]bool{}
	if decl.Recv != nil {
		names[decl.Recv.Name] = true
		names["self"] = true
		if c.borrowsType(c.buildType(decl.Recv.Type, s.tp), map[*Type]bool{}) {
			eligible++
		}
	}
	for _, pm := range decl.Params {
		names[pm.Name] = true
		pt := c.buildType(pm.Type, s.tp)
		if pm.Variadic && pt != nil {
			pt = c.in.view(pt)
		}
		if c.borrowsType(pt, map[*Type]bool{}) {
			eligible++
		}
	}
	roots := make(map[string]bool)
	c.collectReturnRoots(decl.Body, s, names, roots)
	if len(decl.From) == 0 {
		direct := result.IsRef() || result.IsMutRef() || result.Kind() == ViewKind
		if direct && eligible > 1 {
			pos := decl.Result.Pos()
			c.bag.Error(pos, diag.CodeBorrowOutlives,
				"this returned borrow needs an explicit from source",
				"write from <parameter>, or return an owned value")
			return
		}
		if len(roots) == 1 {
			return
		}
		if len(roots) == 0 && eligible == 0 {
			return
		}
		pos := decl.Result.Pos()
		c.bag.Error(pos, diag.CodeBorrowOutlives,
			"this returned borrow needs an explicit from source",
			"write from <parameter>, or return an owned value")
		return
	}
	fromRoots := map[string]bool{}
	validFrom := true
	for _, src := range decl.From {
		root := sourceRoot(src)
		if root == "" || !names[root] {
			c.bag.Error(src.Pos(), diag.CodeBorrowOutlives,
				"from source is not a parameter or receiver",
				"name a parameter or self")
			validFrom = false
			continue
		}
		fromRoots[root] = true
		// The source names the owner, not necessarily a value of the result
		// type: from self may return a field view or reference.
		_ = c.inferExpr(s, src)
	}
	if decl.Recv != nil && fromRoots["self"] {
		fromRoots[decl.Recv.Name] = true
	}
	if validFrom {
		for root := range roots {
			if !fromRoots[root] {
				c.bag.Error(decl.Result.Pos(), diag.CodeBorrowOutlives,
					"this returned borrow comes from "+root+", which is not in from",
					"add it to the from list, or return an owned value")
			}
		}
	}
}

// borrowsType reports whether a checked type contains an explicit borrow or a
// slice view. Struct fields are followed so a returned page with a view field
// still requires a source.
func (c *checker) borrowsType(t *Type, seen map[*Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	if t.Borrow() != ast.OwnedBorrow || t.Kind() == ViewKind {
		return true
	}
	if t.Kind() == NamedKind {
		if fields, ok := c.env.fields[t.Name()]; ok {
			for _, f := range fields {
				if c.borrowsType(f.typ, seen) {
					return true
				}
			}
		}
	}
	for _, a := range t.Args() {
		if c.borrowsType(a, seen) {
			return true
		}
	}
	return false
}

// collectReturnRoots records which parameters appear in return values. A view
// or ref built from two parameters needs from; a function that returns only a
// page built from one receiver does not.
func (c *checker) collectReturnRoots(st ast.Stmt, scope *inferScope, names, out map[string]bool) {
	switch s := st.(type) {
	case *ast.Block:
		for _, inner := range s.Stmts {
			c.collectReturnRoots(inner, scope, names, out)
		}
	case *ast.ReturnStmt:
		c.collectExprRoots(s.Value, scope, names, out)
	case *ast.IfExpr:
		c.collectReturnRoots(s.Then, scope, names, out)
		if s.Else != nil {
			c.collectReturnRoots(s.Else, scope, names, out)
		}
	case *ast.MatchExpr:
		for _, arm := range s.Arms {
			c.collectReturnRoots(arm.Body, scope, names, out)
		}
	case *ast.ForStmt:
		c.collectReturnRoots(s.Body, scope, names, out)
	case *ast.RegionStmt:
		c.collectReturnRoots(s.Body, scope, names, out)
	case *ast.ParallelStmt:
		c.collectReturnRoots(s.Body, scope, names, out)
	case *ast.DeadlineExpr:
		c.collectReturnRoots(s.Body, scope, names, out)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			c.collectReturnRoots(arm.Body, scope, names, out)
		}
	}
}

func (c *checker) collectExprRoots(e ast.Expr, scope *inferScope, names, out map[string]bool) {
	if e == nil {
		return
	}
	add := func(root string) {
		if root != "" && names[root] {
			out[root] = true
		}
	}
	switch x := e.(type) {
	case *ast.BorrowExpr:
		add(sourceRoot(x.X))
		c.collectExprRoots(x.X, scope, names, out)
	case *ast.SliceExpr:
		add(sourceRoot(x.Recv))
		c.collectExprRoots(x.Recv, scope, names, out)
		c.collectExprRoots(x.Lo, scope, names, out)
		c.collectExprRoots(x.Hi, scope, names, out)
	case *ast.CallExpr:
		for _, a := range x.Args {
			c.collectExprRoots(a.Expr, scope, names, out)
		}
	case *ast.MethodCallExpr:
		c.collectExprRoots(x.Recv, scope, names, out)
		for _, a := range x.Args {
			c.collectExprRoots(a.Expr, scope, names, out)
		}
	case *ast.FieldExpr:
		root := sourceRoot(x)
		if t, ok := scope.lookup(sourceRoot(x)); ok && c.borrowsType(t, map[*Type]bool{}) {
			add(root)
		}
		c.collectExprRoots(x.Recv, scope, names, out)
	case *ast.IndexExpr:
		root := sourceRoot(x)
		if t, ok := scope.lookup(sourceRoot(x)); ok && c.borrowsType(t, map[*Type]bool{}) {
			add(root)
		}
		c.collectExprRoots(x.Recv, scope, names, out)
		c.collectExprRoots(x.Index, scope, names, out)
	case *ast.NameExpr:
		if t, ok := scope.lookup(x.Name); ok && c.borrowsType(t, map[*Type]bool{}) {
			add(x.Name)
		}
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			c.collectExprRoots(f.Value, scope, names, out)
		}
	case *ast.ArrayLitExpr:
		for _, elem := range x.Elems {
			c.collectExprRoots(elem, scope, names, out)
		}
	case *ast.TryExpr:
		c.collectExprRoots(x.X, scope, names, out)
	case *ast.AwaitExpr:
		c.collectExprRoots(x.X, scope, names, out)
	case *ast.UnwrapExpr:
		c.collectExprRoots(x.X, scope, names, out)
	case *ast.UnaryExpr:
		c.collectExprRoots(x.X, scope, names, out)
	case *ast.BinaryExpr:
		c.collectExprRoots(x.L, scope, names, out)
		c.collectExprRoots(x.R, scope, names, out)
	}
}

func sourceRoot(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.NameExpr:
		return x.Name
	case *ast.FieldExpr:
		return sourceRoot(x.Recv)
	case *ast.IndexExpr:
		return sourceRoot(x.Recv)
	case *ast.SliceExpr:
		return sourceRoot(x.Recv)
	}
	return ""
}

// inferBlock checks a block and returns the type of its last statement when
// that statement is an expression, which is the block's value.
func (c *checker) inferBlock(s *inferScope, b *ast.Block) *Type {
	if b == nil {
		return nil
	}
	inner := newInferScope(s)
	for i, st := range b.Stmts {
		if i == len(b.Stmts)-1 {
			if es, ok := st.(*ast.ExprStmt); ok {
				if _, spawn := es.Expr.(*ast.SpawnExpr); spawn {
					c.bag.Error(es.Expr.Pos(), diag.CodeTaskNotAwaited,
						"spawn result is not awaited",
						"bind it to a Task, then await it")
				}
				return c.inferExpr(inner, es.Expr)
			}
		}
		c.inferStmt(inner, st)
	}
	return nil
}

// blockWant checks a block whose value must have type want.
func (c *checker) blockWant(s *inferScope, b *ast.Block, want *Type) {
	if b == nil {
		return
	}
	inner := newInferScope(s)
	for i, st := range b.Stmts {
		if i == len(b.Stmts)-1 {
			if es, ok := st.(*ast.ExprStmt); ok {
				if _, spawn := es.Expr.(*ast.SpawnExpr); spawn {
					c.bag.Error(es.Expr.Pos(), diag.CodeTaskNotAwaited,
						"spawn result is not awaited",
						"bind it to a Task, then await it")
				}
				c.checkValue(inner, es.Expr, want)
				return
			}
		}
		c.inferStmt(inner, st)
	}
}

func (c *checker) inferStmt(s *inferScope, st ast.Stmt) {
	switch x := st.(type) {
	case *ast.Block:
		c.inferBlock(s, x)
	case *ast.VarStmt:
		switch d := x.Decl.(type) {
		case *ast.ConstDecl:
			want := c.buildType(d.Type, s.tp)
			if want == nil && d.Value != nil {
				want = c.inferExpr(s, d.Value)
			}
			c.checkValue(s, d.Value, want)
			s.vars[d.Name] = want
		case *ast.VarDecl:
			want := c.buildType(d.Type, s.tp)
			if want == nil && d.Value != nil {
				want = c.inferExpr(s, d.Value)
				c.rejectRawAddress(d.Value)
				s.vars[d.Name] = want
				return
			}
			c.rejectRawAddress(d.Value)
			if d.Value == nil {
				if isGuardType(want) {
					c.bag.Error(d.Pos(), diag.CodeGuardCopied,
						"a guard must be created by lock",
						"let the lock call initialize it")
				} else if !c.hasZero(want) {
					c.bag.Error(d.Pos(), diag.CodeNoZero,
						"type "+want.String()+" has no zero value",
						"initialize it: var "+d.Name+": "+want.String()+" = ...")
				}
			} else {
				c.checkValue(s, d.Value, want)
			}
			s.vars[d.Name] = want
		}
	case *ast.AssignStmt:
		c.rejectRawAddress(x.Value)
		if c.discardedSpawn(x.Target, x.Value) {
			c.bag.Error(x.Value.Pos(), diag.CodeTaskNotAwaited,
				"spawn result is not awaited",
				"bind it to a Task, then await it")
		}
		if c.readGuardWrite(s, x.Target) {
			c.bag.Error(x.Target.Pos(), diag.CodeReadGuardWrite,
				"a read guard is read-only",
				"take the write guard with w_lock")
		}
		want := c.inferPlace(s, x.Target)
		if x.Op != "=" && want != nil && !want.IsNumeric() {
			c.typeMismatch(x.Target.Pos(), want, nil)
		}
		c.checkValue(s, x.Value, want)
	case *ast.ExprStmt:
		c.rejectRawAddress(x.Expr)
		if _, ok := x.Expr.(*ast.SpawnExpr); ok {
			c.bag.Error(x.Expr.Pos(), diag.CodeTaskNotAwaited,
				"spawn result is not awaited",
				"bind it to a Task, then await it")
		}
		if t := c.inferExpr(s, x.Expr); t != nil && t.Kind() == NamedKind && t.Name() == "Result" {
			c.bag.Error(x.Expr.Pos(), diag.CodeResultDropped,
				"this Result is not handled",
				"use ?, !, match, or discard it with _ =")
		}
	case *ast.DeferStmt:
		if c.deferSuspends(s, x.Call) {
			c.bag.Error(x.Call.Pos(), diag.CodeDeferSuspend,
				"defer cannot suspend",
				"run the async cleanup before the suspension point")
			previous := s.allowAsyncCall
			s.allowAsyncCall = true
			c.inferExpr(s, x.Call)
			s.allowAsyncCall = previous
		} else {
			c.inferExpr(s, x.Call)
		}
	case *ast.ReturnStmt:
		c.rejectRawAddress(x.Value)
		if s.result == nil {
			if x.Value != nil {
				c.typeMismatch(x.Pos(), c.inferExpr(s, x.Value), c.in.unit())
			}
			return
		}
		if x.Value == nil {
			c.typeMismatch(x.Pos(), c.in.unit(), s.result)
			return
		}
		c.checkValue(s, x.Value, s.result)
	case *ast.ForStmt:
		c.inferFor(s, x)
	case *ast.RegionStmt:
		c.inferBlock(s, x.Body)
	case *ast.ParallelStmt:
		inner := newInferScope(s)
		inner.parallel = true
		c.inferBlock(inner, x.Body)
	case *ast.SelectStmt:
		if !s.async {
			c.bag.Error(x.Pos(), diag.CodeSelectOutsideAsync,
				"select is only allowed inside an async function",
				"mark the function async, or move the select into an async helper")
		}
		for _, arm := range x.Arms {
			inner := newInferScope(s)
			previous := s.allowAsyncCall
			s.allowAsyncCall = true
			t := c.inferExpr(s, arm.Chan)
			s.allowAsyncCall = previous
			if arm.Bind != "" {
				inner.vars[arm.Bind] = elemOf(c.in, t)
			}
			c.inferExpr(s, arm.Timeout)
			c.inferStmt(inner, arm.Body)
		}
	case *ast.IfExpr:
		c.checkValue(s, x.Cond, c.in.scalar("bool"))
		c.inferBlock(s, x.Then)
		if x.Else != nil {
			c.inferStmt(s, x.Else)
		}
	case *ast.MatchExpr:
		c.inferExpr(s, x.Subject)
		for _, arm := range x.Arms {
			inner := newInferScope(s)
			c.inferExpr(inner, arm.Guard)
			c.inferStmt(inner, arm.Body)
		}
	case *ast.DeadlineExpr:
		c.inferExpr(s, x.Duration)
		c.inferBlock(s, x.Body)
	}
}

func (c *checker) inferFor(s *inferScope, st *ast.ForStmt) {
	if st.Cond != nil {
		c.checkValue(s, st.Cond, c.in.scalar("bool"))
		c.inferBlock(s, st.Body)
		return
	}
	names := len(st.Names)
	nameTypes := make([]*Type, names)
	if st.Iter != nil {
		t := c.inferExpr(s, st.Iter)
		switch {
		case t != nil && t.Kind() == NamedKind && t.Name() == "Map":
			if names >= 2 {
				nameTypes[0] = argAt(t, 0)
				nameTypes[1] = argAt(t, 1)
			} else if names == 1 {
				nameTypes[0] = argAt(t, 1)
			}
		default:
			elem := elemOf(c.in, t)
			if names >= 2 {
				nameTypes[0] = c.in.scalar("usize")
				nameTypes[1] = elem
			} else if names == 1 {
				nameTypes[0] = elem
			}
		}
	}
	inner := newInferScope(s)
	for i, name := range st.Names {
		var t *Type
		if i < len(nameTypes) {
			t = nameTypes[i]
		}
		inner.vars[name] = t
	}
	c.inferBlock(inner, st.Body)
}

func (c *checker) inferPlace(s *inferScope, e ast.Expr) *Type {
	switch x := e.(type) {
	case *ast.NameExpr:
		if x.Name == "_" {
			return nil
		}
		return c.inferExpr(s, x)
	case *ast.FieldExpr:
		return c.inferExpr(s, x)
	case *ast.IndexExpr:
		return c.inferExpr(s, x)
	}
	return c.inferExpr(s, e)
}

// checkValue checks e against want. A nil want only infers the type.
func (c *checker) checkValue(s *inferScope, e ast.Expr, want *Type) {
	if e == nil || want == nil {
		c.inferExpr(s, e)
		return
	}
	c.recordWant(e, want)
	if c.checkNegativeLiteral(e, want) {
		return
	}
	if c.checkPredeclaredInto(s, e, want) {
		return
	}
	got := c.exprWithWant(s, e, want)
	if isGuardType(got) && !guardProducer(e) {
		c.bag.Error(e.Pos(), diag.CodeGuardCopied,
			"a guard cannot be copied",
			"keep it in the local that lock returned")
	}
	if Equal(got, want) || derefScalar(got, want) {
		return
	}
	c.mismatch(e.Pos(), got, want, false, "", 0)
}

// derefScalar reads a scalar out of a reference. Aggregates and owning values
// stay borrowed: moving out of a ref needs clone.
func derefScalar(got, want *Type) bool {
	if got == nil || want == nil || !got.IsRef() && !got.IsMutRef() {
		return false
	}
	if want.Borrow() != ast.OwnedBorrow || want.Kind() != ScalarKind {
		return false
	}
	return Equal(got.Unref(), want)
}

// discardedSpawn reports _ = spawn call. A task handle must stay named until
// its region awaits it; the blank target would lose the only handle.
func (c *checker) discardedSpawn(target, value ast.Expr) bool {
	name, ok := target.(*ast.NameExpr)
	if !ok || name.Name != "_" {
		return false
	}
	_, spawn := value.(*ast.SpawnExpr)
	return spawn
}

// checkNegativeLiteral checks a negated integer literal against a signed
// expected type. The literal itself is written without a sign, so the smallest
// value of the type has a magnitude one above the largest positive one; the
// check therefore allows that one extra step only when the unary minus is
// present.
func (c *checker) checkNegativeLiteral(e ast.Expr, want *Type) bool {
	u, ok := e.(*ast.UnaryExpr)
	if !ok || u.Op != token.Minus || want == nil {
		return false
	}
	lit, ok := u.X.(*ast.LiteralExpr)
	if !ok || lit.Kind != token.Int {
		return false
	}
	bits, signed, known := intWidth(want.Name())
	if !known || !signed {
		return false
	}
	value, _, ok := parseIntLiteral(lit.Text)
	if !ok {
		return false
	}
	limit := uint64(1) << (bits - 1)
	if value.overflow || value.mag > limit {
		c.bag.Error(lit.Pos(), diag.CodeConstantRange,
			"constant "+lit.Text+" is out of range for "+want.Name(),
			"write a suffix, or a smaller value")
		return true
	}
	c.recordLiteral(lit, want)
	return true
}

// checkPredeclaredInto checks Some, None, Ok, and Err against the complete
// Option or Result the context expects. The constructor alone knows one side
// and cannot infer the other, so the expected type supplies it.
func (c *checker) checkPredeclaredInto(s *inferScope, e ast.Expr, want *Type) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || want.Kind() != NamedKind {
		return false
	}
	name, ok := call.Callee.(*ast.NameExpr)
	if !ok {
		return false
	}
	switch name.Name {
	case "Some":
		if want.Name() != "Option" || len(call.Args) != 1 {
			return false
		}
		if t := argAt(want, 0); t != nil {
			c.checkValue(s, call.Args[0].Expr, t)
		}
		return true
	case "None":
		return want.Name() == "Option"
	case "Ok":
		if want.Name() != "Result" || len(call.Args) != 1 {
			return false
		}
		if t := argAt(want, 0); t != nil {
			c.checkValue(s, call.Args[0].Expr, t)
		}
		return true
	case "Err":
		if want.Name() != "Result" || len(call.Args) != 1 {
			return false
		}
		if t := argAt(want, 1); t != nil {
			c.checkValue(s, call.Args[0].Expr, t)
		}
		return true
	}
	return false
}

// recordWant keeps the type a predeclared constructor or value takes from its
// context. Some and Ok cannot infer the other side of an Option or Result, so
// the expected type is the only complete answer.
func (c *checker) recordWant(e ast.Expr, want *Type) {
	switch x := e.(type) {
	case *ast.CallExpr:
		name, ok := x.Callee.(*ast.NameExpr)
		if !ok {
			return
		}
		switch name.Name {
		case "Some", "Ok", "Err":
			if c.callTypes == nil {
				c.callTypes = map[*ast.CallExpr]string{}
			}
			c.callTypes[x] = want.String()
		}
	case *ast.NameExpr:
		if x.Name != "None" && x.Name != "Canceled" && x.Name != "Deadline" {
			return
		}
		if c.nameTypes == nil {
			c.nameTypes = map[*ast.NameExpr]string{}
		}
		c.nameTypes[x] = want.String()
	}
}

// exprWithWant infers e, letting a numeric literal and the branch blocks of an
// if or match take want as their type.
func (c *checker) exprWithWant(s *inferScope, e ast.Expr, want *Type) *Type {
	switch x := e.(type) {
	case *ast.LiteralExpr:
		return c.literalType(x, want)
	case *ast.IfExpr:
		return c.ifWant(s, x, want)
	case *ast.MatchExpr:
		return c.matchWant(s, x, want)
	}
	return c.inferExpr(s, e)
}

func (c *checker) ifWant(s *inferScope, ifx *ast.IfExpr, want *Type) *Type {
	c.checkValue(s, ifx.Cond, c.in.scalar("bool"))
	c.blockWant(s, ifx.Then, want)
	if ifx.Else == nil {
		return nil
	}
	switch e := ifx.Else.(type) {
	case *ast.IfExpr:
		c.ifWant(s, e, want)
	case *ast.Block:
		c.blockWant(s, e, want)
	default:
		c.inferStmt(s, ifx.Else)
	}
	return want
}

func (c *checker) matchWant(s *inferScope, m *ast.MatchExpr, want *Type) *Type {
	c.inferExpr(s, m.Subject)
	for _, arm := range m.Arms {
		inner := newInferScope(s)
		c.inferExpr(inner, arm.Guard)
		switch body := arm.Body.(type) {
		case *ast.Block:
			c.blockWant(inner, body, want)
		case *ast.ExprStmt:
			c.checkValue(inner, body.Expr, want)
		default:
			c.inferStmt(inner, arm.Body)
		}
	}
	return want
}

func (c *checker) typeMismatch(pos source.Pos, got, want *Type) {
	c.bag.Error(pos, diag.CodeTypeMismatch, "type mismatch: got "+got.String()+", want "+want.String(),
		"convert explicitly with T(x)")
}

func (c *checker) checkArg(s *inferScope, e ast.Expr, want *Type, callee string, n int) {
	if want == nil {
		c.inferExpr(s, e)
		return
	}
	if c.checkNegativeLiteral(e, want) {
		return
	}
	got := c.exprWithWant(s, e, want)
	if isGuardType(got) && !guardProducer(e) {
		c.bag.Error(e.Pos(), diag.CodeGuardCopied,
			"a guard cannot be copied",
			"keep it in the local that lock returned")
	}
	if Equal(got, want) || c.borrowArg(got, want, e) || derefScalar(got, want) {
		return
	}
	c.mismatch(e.Pos(), got, want, true, callee, n)
}

// borrowArg accepts a place where a parameter asks for a borrow. The borrow
// stays implicit at the call boundary because the parameter type already spells
// the access; an explicit ref or mut expression is still accepted.
func (c *checker) borrowArg(got, want *Type, e ast.Expr) bool {
	if got == nil || want == nil {
		return false
	}
	if want.IsRef() {
		if got.IsRef() {
			return Equal(got.Unref(), want.Unref())
		}
		return got.Borrow() == ast.OwnedBorrow && isPlaceExpr(e) && Equal(got, want.Unref())
	}
	if want.IsMutRef() {
		if got.IsMutRef() {
			return Equal(got.Unref(), want.Unref())
		}
		return got.Borrow() == ast.OwnedBorrow && isPlaceExpr(e) && Equal(got, want.Unref())
	}
	if want.Kind() == ViewKind && want.ViewMut() {
		return got.Kind() == ViewKind && got.ViewMut() && Equal(got.Elem(), want.Elem())
	}
	return false
}

// mismatch reports a value that does not fit a required type. A concrete type
// assigned to an interface is checked for the interface methods first.
func (c *checker) mismatch(pos source.Pos, got, want *Type, isArg bool, callee string, n int) {
	if want.Kind() == NamedKind && c.env.types[want.Name()] == nil && !c.env.userTypes[want.Name()] {
		// The name comes from another package, whose declarations this
		// environment does not build. The lowering checks the layout and the
		// vtable, so a local concrete value is accepted here.
		if got != nil && got.Kind() == NamedKind {
			return
		}
	}
	if want.Kind() == NamedKind && want.Name() == "interface {}" {
		// The empty interface has no required method, so every value
		// satisfies it. Only Is/As can recover the concrete type.
		return
	}
	if methods, ok := c.env.ifaces[want.Name()]; ok {
		if got != nil && got.Kind() == NamedKind && strings.Contains(got.Name(), ".") {
			// The concrete type belongs to another package, whose declarations
			// this environment does not hold. The vtable built during lowering
			// checks that every method exists.
			return
		}
		if want.Name() == "Show" && showsItself(got) {
			return
		}
		if got != nil && got.Kind() == NamedKind && c.env.types[got.Name()] == nil &&
			!c.env.userTypes[got.Name()] {
			// A named type this environment does not declare comes from
			// another package. The vtable built during lowering resolves the
			// method set there.
			return
		}
		if got != nil && c.env.derives[got.Name()][want.Name()] {
			return
		}
		if have, isIface := c.env.ifaces[got.Name()]; isIface {
			if missing := missingFromMethods(have, methods); missing != "" {
				c.bag.Error(pos, diag.CodeInterfaceUnsatisfied,
					"type "+got.String()+" does not satisfy "+want.String(),
					"missing method "+missing)
			}
			return
		}
		if missing := c.missingMethod(got, methods); missing != "" {
			c.bag.Error(pos, diag.CodeInterfaceUnsatisfied,
				"type "+got.String()+" does not satisfy "+want.String(),
				"missing method "+missing)
		}
		return
	}
	if isArg {
		c.bag.Error(pos, diag.CodeBadArgument,
			"argument "+strconv.Itoa(n)+" has type "+got.String()+", want "+want.String(),
			"see the declaration of "+callee)
		return
	}
	c.typeMismatch(pos, got, want)
}

// missingMethod returns the first method of the interface that got lacks or
// declares with a different signature.
func (c *checker) missingMethod(got *Type, methods []*methodInfo) string {
	if got == nil {
		return ""
	}
	for _, m := range methods {
		sig, ok := c.env.methods[got.Name()+"."+m.name]
		if !ok || !sameSignature(sig, m.sig) {
			return m.name
		}
	}
	return ""
}

// showsItself reports whether a value shows itself without a declared method.
// Every predeclared scalar does; Show is the one interface that accepts them.
func showsItself(t *Type) bool {
	if t == nil || t.Kind() != ScalarKind {
		return false
	}
	switch t.Name() {
	case "bool", "str", "i8", "i16", "i32", "i64", "isize",
		"u8", "u16", "u32", "u64", "usize", "f32", "f64":
		return true
	}
	return false
}

// missingFromMethods reports the first required method that the interface
// have does not declare with the same signature.
func missingFromMethods(have, want []*methodInfo) string {
	for _, w := range want {
		found := false
		for _, h := range have {
			if h.name == w.name && sameSignature(h.sig, w.sig) {
				found = true
				break
			}
		}
		if !found {
			return w.name
		}
	}
	return ""
}

func sameSignature(a, b *signature) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.params) != len(b.params) || a.variadic != b.variadic || a.async != b.async {
		return false
	}
	for i := range a.params {
		if !Equal(a.params[i], b.params[i]) {
			return false
		}
	}
	return Equal(a.result, b.result)
}

func (c *checker) checkCallArgs(s *inferScope, sig *signature, args []*ast.CallArg, pos source.Pos) {
	name := sig.name
	named := false
	for _, a := range args {
		if a.Name != "" {
			named = true
			break
		}
	}
	if sig.variadic && named {
		c.bag.Error(pos, diag.CodeBadArgument,
			"named arguments are not allowed for a variadic call",
			"pass the variadic tail positionally")
		for _, a := range args {
			c.inferExpr(s, a.Expr)
		}
		return
	}
	if sig.variadic {
		fixed := len(sig.params) - 1
		if len(args) < fixed {
			c.badArity(pos, name)
			return
		}
		for i := 0; i < fixed; i++ {
			c.checkArg(s, args[i].Expr, sig.params[i], name, i+1)
		}
		last := sig.params[len(sig.params)-1]
		elem := last.Elem()
		for i := fixed; i < len(args); i++ {
			c.checkArg(s, args[i].Expr, elem, name, i+1)
		}
		return
	}
	slots := make([]*ast.CallArg, len(sig.params))
	positional := 0
	seenNamed := false
	tooMany := false
	for _, a := range args {
		if a.Name == "" {
			if seenNamed {
				c.bag.Error(a.Pos(), diag.CodeBadArgument,
					"positional argument after a named argument",
					"move positional arguments before named ones")
			}
			if positional >= len(sig.params) {
				tooMany = true
				continue
			}
			slots[positional] = a
			positional++
			continue
		}
		seenNamed = true
		idx := -1
		for i, p := range sig.paramNames {
			if p == a.Name {
				idx = i
				break
			}
		}
		if idx < 0 {
			c.bag.Error(a.Pos(), diag.CodeBadArgument,
				"unknown named argument "+a.Name,
				"see the declaration of "+name)
			continue
		}
		if slots[idx] != nil {
			c.bag.Error(a.Pos(), diag.CodeBadArgument,
				"duplicate named argument "+a.Name,
				"pass it once")
			continue
		}
		slots[idx] = a
	}
	if tooMany {
		c.badArity(pos, name)
	}
	for i, a := range slots {
		if a == nil {
			if i >= len(sig.hasDefault) || !sig.hasDefault[i] {
				c.badArity(pos, name)
				return
			}
			continue
		}
		c.checkArg(s, a.Expr, sig.params[i], name, i+1)
	}
}

func (c *checker) badArity(pos source.Pos, callee string) {
	c.bag.Error(pos, diag.CodeBadArgument, "wrong number of arguments", "see the declaration of "+callee)
}

func (c *checker) inferExpr(s *inferScope, e ast.Expr) *Type {
	t := c.inferExprInner(s, e)
	if e != nil && c.exprTypes != nil {
		c.exprTypes[e] = t
	}
	return t
}

func (c *checker) inferExprInner(s *inferScope, e ast.Expr) *Type {
	switch x := e.(type) {
	case nil:
		return nil
	case *ast.LiteralExpr:
		return c.literalType(x, nil)
	case *ast.BorrowExpr:
		base := c.inferExpr(s, x.X)
		if base == nil {
			return nil
		}
		if !isPlaceExpr(x.X) {
			c.bag.Error(x.Pos(), diag.CodeTypeMismatch,
				"a borrow needs a place", "borrow a name, field, index, or slice")
			return base
		}
		if base.Borrow() != ast.OwnedBorrow {
			c.bag.Error(x.Pos(), diag.CodeTypeMismatch,
				"cannot borrow a borrow", "copy the ref value instead")
			return base
		}
		if x.Mut && base.Kind() == ViewKind {
			if base.ViewMut() {
				return base
			}
			return c.in.view(base.Elem(), true)
		}
		if x.Mut {
			return c.in.borrowed(base, ast.MutBorrow)
		}
		return c.in.borrowed(base, ast.RefBorrow)
	case *ast.NameExpr:
		if x.Name == "_" {
			return nil
		}
		if x.Name == "Canceled" || x.Name == "Deadline" {
			return c.in.named("Error", nil)
		}
		if t, ok := s.lookup(x.Name); ok {
			return t
		}
		if t, ok := c.env.vars[x.Name]; ok {
			return t
		}
		return nil
	case *ast.CallExpr:
		return c.callType(s, x)
	case *ast.MethodCallExpr:
		return c.methodType(s, x)
	case *ast.FieldExpr:
		return c.fieldType(c.inferExpr(s, x.Recv), x.Name, x.Pos())
	case *ast.IndexExpr:
		recv := c.inferExpr(s, x.Recv)
		// A container index is a usize, and saying so is what gives a literal
		// its width; the backend refuses a mismatched pair instead of
		// guessing. A str index is a byte offset and keeps its own type.
		if !isStrType(recv) {
			c.checkValue(s, x.Index, c.in.scalar("usize"))
		}
		return elemOf(c.in, recv)
	case *ast.SliceExpr:
		recv := c.inferExpr(s, x.Recv)
		c.checkValue(s, x.Lo, c.in.scalar("usize"))
		c.checkValue(s, x.Hi, c.in.scalar("usize"))
		return c.sliceOf(recv)
	case *ast.AggregateExpr:
		return c.aggregateType(s, x)
	case *ast.ArrayLitExpr:
		return c.arrayLitType(s, x)
	case *ast.NewExpr:
		return c.newType(s, x)
	case *ast.UnaryExpr:
		return c.unaryType(s, x)
	case *ast.BinaryExpr:
		return c.binaryType(s, x)
	case *ast.RangeExpr:
		c.inferExpr(s, x.Lo)
		c.inferExpr(s, x.Hi)
		return c.in.rangeT()
	case *ast.LambdaExpr:
		params := make([]*Type, len(x.Params))
		for i, pm := range x.Params {
			params[i] = c.buildType(pm.Type, s.tp)
		}
		result := c.buildType(x.Result, s.tp)
		inner := newInferScope(s)
		inner.result = result
		for i, pm := range x.Params {
			inner.vars[pm.Name] = params[i]
		}
		got := c.inferBlock(inner, x.Body)
		if result != nil && got != nil {
			c.unifyTypes(x.Body.Pos(), got, result)
		}
		return c.in.funcT(params, result)
	case *ast.TryExpr:
		c.inferExpr(s, x.X)
		return nil
	case *ast.AwaitExpr:
		if !s.async {
			c.bag.Error(x.Pos(), diag.CodeAwaitOutsideAsync,
				"await is only allowed inside an async function",
				"mark the function async")
		}
		previous := s.allowAsyncCall
		s.allowAsyncCall = true
		t := c.inferExpr(s, x.X)
		s.allowAsyncCall = previous
		if t != nil && t.Kind() == NamedKind && t.Name() == "Task" && len(t.Args()) == 1 {
			return c.in.named("Result", []*Type{t.Args()[0], c.in.named("TaskError", nil)})
		}
		if !awaitOperand(x.X) {
			c.bag.Error(x.Pos(), diag.CodeAwaitNotAsync,
				"await needs a Task or an async call",
				"remove await or call an async function")
		}
		return t
	case *ast.UnwrapExpr:
		c.inferExpr(s, x.X)
		return nil
	case *ast.SpawnExpr:
		previous := s.allowAsyncCall
		s.allowAsyncCall = true
		t := c.inferExpr(s, x.Call)
		s.allowAsyncCall = previous
		return c.in.named("Task", []*Type{t})
	case *ast.DeadlineExpr:
		c.inferExpr(s, x.Duration)
		c.inferBlock(s, x.Body)
		return nil
	case *ast.IfExpr:
		return c.ifType(s, x)
	case *ast.MatchExpr:
		return c.matchType(s, x)
	case *ast.InterpExpr:
		for _, p := range x.Parts {
			c.inferExpr(s, p.Expr)
		}
		return c.in.scalar("str")
	case *ast.LaneHereExpr:
		return c.in.scalar("usize")
	case *ast.UnitExpr:
		return c.in.unit()
	}
	return nil
}

func isPlaceExpr(e ast.Expr) bool {
	switch e.(type) {
	case *ast.NameExpr, *ast.FieldExpr, *ast.IndexExpr, *ast.SliceExpr:
		return true
	}
	return false
}

func (c *checker) literalType(lit *ast.LiteralExpr, want *Type) *Type {
	var t *Type
	switch lit.Kind {
	case token.Int:
		value, suffix, ok := parseIntLiteral(lit.Text)
		if suffix != "" && intTypeName(suffix) {
			if ok {
				c.checkIntConstant(lit.Pos(), value, lit.Text, suffix)
			}
			t = c.in.scalar(suffix)
			break
		}
		if want != nil && want.IsInteger() {
			if ok {
				c.checkIntConstant(lit.Pos(), value, lit.Text, want.Name())
			}
			t = want
			break
		}
		if ok {
			c.checkIntConstant(lit.Pos(), value, lit.Text, "i32")
		}
		t = c.in.scalar("i32")
	case token.Float:
		if len(lit.Text) >= 3 && lit.Text[len(lit.Text)-3:] == "f32" {
			t = c.in.scalar("f32")
			break
		}
		if want != nil && (want.Name() == "f32" || want.Name() == "f64") {
			t = want
			break
		}
		t = c.in.scalar("f64")
	case token.Duration:
		t = c.env.types["Duration"]
	case token.Char:
		t = c.in.scalar("u32")
	case token.String:
		t = c.in.scalar("str")
	case token.Bytes:
		t = c.in.view(c.in.scalar("u8"))
	case token.KwTrue, token.KwFalse:
		t = c.in.scalar("bool")
	}
	c.recordLiteral(lit, t)
	return t
}

// recordLiteral remembers the type a literal resolved to. The intermediate
// format records it as the literal's type, and the backend reads it from
// there instead of inferring a width.
func (c *checker) recordLiteral(lit *ast.LiteralExpr, t *Type) {
	if t == nil {
		return
	}
	if c.litTypes == nil {
		c.litTypes = map[*ast.LiteralExpr]string{}
	}
	c.litTypes[lit] = t.String()
}

func (c *checker) callType(s *inferScope, call *ast.CallExpr) *Type {
	if field, ok := call.Callee.(*ast.FieldExpr); ok {
		if recv, ok := field.Recv.(*ast.NameExpr); ok {
			if decl := c.foreignFuncs[recv.Name][field.Name]; decl != nil {
				sig := c.buildForeignSig(decl, recv.Name)
				sig = c.inferCallTypeArgs(s, call.Args, call.TypeArgs, sig)
				c.checkTypeArgs(call, sig)
				inst := sig
				if sig.typeParams > 0 && len(call.TypeArgs) == sig.typeParams && len(sig.typeNames) == sig.typeParams {
					subst := make(map[string]*Type, sig.typeParams)
					for i, ta := range call.TypeArgs {
						subst[sig.typeNames[i]] = c.buildType(ta, nil)
					}
					inst = c.instantiate(sig, subst)
				}
				c.checkCallArgs(s, inst, call.Args, call.Pos())
				return inst.result
			}
		}
	}
	name, ok := call.Callee.(*ast.NameExpr)
	if !ok {
		t := c.inferExpr(s, call.Callee)
		if t != nil && t.Kind() == FuncKind {
			c.checkPayload(s, t.Params(), call.Args, "", call.Pos())
			return t.Result()
		}
		for _, a := range call.Args {
			c.inferExpr(s, a.Expr)
		}
		return nil
	}
	sym := c.symbolOf(name)
	if name.Name == "wait" && len(call.Args) == 1 {
		previous := s.allowAsyncCall
		s.allowAsyncCall = true
		t := c.inferExpr(s, call.Args[0].Expr)
		s.allowAsyncCall = previous
		return t
	}
	switch {
	case sym != nil && sym.Kind == resolve.FuncKind:
		sig := c.env.funcs[name.Name]
		if sig != nil {
			sig = c.inferCallTypeArgs(s, call.Args, call.TypeArgs, sig)
			c.checkTypeArgs(call, sig)
			inst := sig
			if sig.typeParams > 0 && len(call.TypeArgs) == sig.typeParams && len(sig.typeNames) == sig.typeParams {
				subst := make(map[string]*Type, sig.typeParams)
				for i, ta := range call.TypeArgs {
					subst[sig.typeNames[i]] = c.buildType(ta, nil)
				}
				inst = c.instantiate(sig, subst)
			}
			c.checkCallArgs(s, inst, call.Args, call.Pos())
			if inst.async {
				c.requireWait(s, call.Pos())
			}
			return inst.result
		}
	case sym != nil && sym.Kind == resolve.VariantKind:
		vi := c.env.variants[name.Name]
		if vi != nil {
			c.checkPayload(s, vi.payload, call.Args, name.Name, call.Pos())
			return vi.enum
		}
	case sym != nil && sym.Predeclared:
		return c.predeclaredCall(s, name.Name, call)
	case sym != nil && sym.IsType():
		if decl, ok := sym.Node.(*ast.StructDecl); ok {
			if decl.Class {
				return c.classConstruct(s, name, call)
			}
			return c.structConstruct(s, name, call)
		}
		return c.castType(s, name, call)
	case sym != nil && sym.Kind == resolve.ImportKind:
		for _, a := range call.Args {
			c.inferExpr(s, a.Expr)
		}
		return nil
	}
	for _, a := range call.Args {
		c.inferExpr(s, a.Expr)
	}
	return nil
}

func (c *checker) symbolOf(name *ast.NameExpr) *resolve.Symbol {
	if c.res == nil {
		return nil
	}
	return c.res.Refs[name]
}

// containerConstruct checks one predeclared container constructor. The
// constructor takes its type arguments in brackets and accepts either no
// capacity or one usize capacity.
func (c *checker) containerConstruct(s *inferScope, name string, call *ast.CallExpr) *Type {
	arity := builtinArity[name]
	if len(call.TypeArgs) != arity {
		c.bag.Error(call.Pos(), diag.CodeTypeArgCount, "wrong number of type arguments",
			"the declaration takes "+strconv.Itoa(arity))
		for _, a := range call.Args {
			c.inferExpr(s, a.Expr)
		}
		return c.in.named(name, nil)
	}
	args := make([]*Type, arity)
	for i, ta := range call.TypeArgs {
		args[i] = c.buildType(ta, s.tp)
	}
	if len(call.Args) > 1 {
		c.badArity(call.Pos(), name)
	}
	for _, a := range call.Args {
		c.checkValue(s, a.Expr, c.in.scalar("usize"))
	}
	return c.in.named(name, args)
}

// predeclaredCall checks one call to a predeclared value.
func (c *checker) predeclaredCall(s *inferScope, name string, call *ast.CallExpr) *Type {
	switch name {
	case "panic":
		c.checkPayload(s, []*Type{c.in.scalar("str")}, call.Args, name, call.Pos())
		return nil
	case "assert":
		c.checkPayload(s, []*Type{c.in.scalar("bool"), c.in.scalar("str")}, call.Args, name, call.Pos())
		return nil
	case "print":
		for _, a := range call.Args {
			c.inferExpr(s, a.Expr)
		}
		return nil
	case "leak":
		if len(call.Args) != 1 {
			c.bag.Error(call.Pos(), diag.CodeBadArgument,
				"wrong number of arguments", "leak takes one value")
			return nil
		}
		c.inferExpr(s, call.Args[0].Expr)
		return nil
	case "len":
		if len(call.Args) != 1 {
			c.bag.Error(call.Pos(), diag.CodeBadArgument,
				"wrong number of arguments", "len takes one value")
			return c.in.scalar("usize")
		}
		c.inferExpr(s, call.Args[0].Expr)
		return c.in.scalar("usize")
	case "Some":
		return c.in.named("Option", []*Type{c.oneArg(s, call)})
	case "None":
		return c.in.named("Option", nil)
	case "Ok":
		return c.in.named("Result", []*Type{c.oneArg(s, call), nil})
	case "Err":
		return c.in.named("Result", []*Type{nil, c.oneArg(s, call)})
	case "list", "dict", "set":
		return c.containerConstruct(s, name, call)
	case "take_c", "take_c_rc":
		return c.takeCType(s, call, name)
	case "SizeOf", "AlignOf", "OffsetOf":
		return c.layoutCall(s, call, name)
	}
	for _, a := range call.Args {
		c.inferExpr(s, a.Expr)
	}
	return nil
}

// takeCType checks the explicit element type and the C callback pair. The
// call returns rc<T>; the pointer itself stays a raw address for the runtime.
func (c *checker) takeCType(s *inferScope, call *ast.CallExpr, name string) *Type {
	if len(call.TypeArgs) != 1 {
		c.bag.Error(call.Pos(), diag.CodeTypeArgCount,
			"wrong number of type arguments", "the declaration takes 1")
	}
	var elem *Type
	if len(call.TypeArgs) == 1 {
		elem = c.buildType(call.TypeArgs[0], nil)
	}
	raw := c.in.scalar("raw")
	release := c.in.funcT([]*Type{raw}, nil)
	params := []*Type{raw, release}
	if name == "take_c_rc" {
		params = []*Type{raw, release, release}
	}
	c.checkPayload(s, params, call.Args, name, call.Pos())
	if elem == nil {
		return nil
	}
	return c.in.named("arc", []*Type{elem})
}

// layoutCall checks the three compile-time layout queries. Each takes one type
// argument; OffsetOf additionally takes the field path as a str.
func (c *checker) layoutCall(s *inferScope, call *ast.CallExpr, name string) *Type {
	if len(call.TypeArgs) != 1 {
		c.bag.Error(call.Pos(), diag.CodeTypeArgCount,
			"wrong number of type arguments", "the declaration takes 1")
	}
	if name == "OffsetOf" {
		c.checkPayload(s, []*Type{c.in.scalar("str")}, call.Args, name, call.Pos())
	} else if len(call.Args) != 0 {
		c.checkPayload(s, nil, call.Args, name, call.Pos())
	}
	return c.in.scalar("usize")
}

func (c *checker) oneArg(s *inferScope, call *ast.CallExpr) *Type {
	if len(call.Args) != 1 {
		c.badArity(call.Pos(), "")
		return nil
	}
	return c.inferExpr(s, call.Args[0].Expr)
}

func (c *checker) checkPayload(s *inferScope, params []*Type, args []*ast.CallArg, callee string, pos source.Pos) {
	sig := &signature{name: callee, params: params}
	c.checkCallArgs(s, sig, args, pos)
}

// classConstruct checks T(...) for a class. An init method supplies the
// parameter contract; without init the written fields supply it positionally or
// by name, in declaration order.
func (c *checker) classConstruct(s *inferScope, name *ast.NameExpr, call *ast.CallExpr) *Type {
	t := c.instantiateNamed(s, name.Name, call.TypeArgs, true)
	if sig := c.env.inits[name.Name]; sig != nil {
		if t != nil {
			if inst := c.instantiateReceiver(sig, t); inst != nil {
				sig = inst
			}
		}
		c.checkCallArgs(s, sig, call.Args, call.Pos())
		return t
	}
	fields := c.env.fields[name.Name]
	if len(fields) == 0 {
		for _, a := range call.Args {
			c.inferExpr(s, a.Expr)
		}
		if len(call.Args) != 0 {
			c.badArity(call.Pos(), name.Name)
		}
		return t
	}
	used := map[int]bool{}
	subst := c.substFor(name.Name, t.Args())
	for i, a := range call.Args {
		var want *Type
		index := -1
		if a.Name != "" {
			for j, f := range fields {
				if f.name == a.Name {
					want = c.substitute(f.typ, subst)
					index = j
					break
				}
			}
			if want == nil {
				c.bag.Error(a.Pos(), diag.CodeNoField, "no field "+a.Name+" on "+name.Name,
					"check the field name or the type")
				c.inferExpr(s, a.Expr)
				continue
			}
		} else if i < len(fields) {
			want = c.substitute(fields[i].typ, subst)
			index = i
		} else {
			c.badArity(call.Pos(), name.Name)
			c.inferExpr(s, a.Expr)
			continue
		}
		used[index] = true
		c.checkValue(s, a.Expr, want)
	}
	for i, f := range fields {
		if used[i] || f.hasDefault {
			continue
		}
		c.bag.Error(call.Pos(), diag.CodeNoZero,
			"type "+name.Name+" has no value for field "+f.name,
			"initialize it in "+name.Name+"(...) or give it a default")
	}
	return t
}

// structConstruct checks T(...) for a value type. It has the same init or
// field-default contract as a class constructor, but the result is an inline
// value.
func (c *checker) structConstruct(s *inferScope, name *ast.NameExpr, call *ast.CallExpr) *Type {
	t := c.instantiateNamed(s, name.Name, call.TypeArgs, false)
	if sig := c.env.inits[name.Name]; sig != nil {
		if t != nil {
			if inst := c.instantiateReceiver(sig, t); inst != nil {
				sig = inst
			}
		}
		c.checkCallArgs(s, sig, call.Args, call.Pos())
		return t
	}
	fields := c.env.fields[name.Name]
	used := map[int]bool{}
	subst := c.substFor(name.Name, t.Args())
	for i, a := range call.Args {
		index := -1
		if a.Name != "" {
			for j, f := range fields {
				if f.name == a.Name {
					index = j
					break
				}
			}
			if index < 0 {
				c.bag.Error(a.Pos(), diag.CodeNoField, "no field "+a.Name+" on "+name.Name,
					"check the field name or the type")
				c.inferExpr(s, a.Expr)
				continue
			}
		} else if i < len(fields) {
			index = i
		} else {
			c.badArity(call.Pos(), name.Name)
			c.inferExpr(s, a.Expr)
			continue
		}
		used[index] = true
		c.checkValue(s, a.Expr, c.substitute(fields[index].typ, subst))
	}
	for i, f := range fields {
		if used[i] || f.hasDefault {
			continue
		}
		c.bag.Error(call.Pos(), diag.CodeNoZero,
			"type "+name.Name+" has no value for field "+f.name,
			"initialize it in "+name.Name+"(...) or give it a default")
	}
	return t
}

func (c *checker) castType(s *inferScope, name *ast.NameExpr, call *ast.CallExpr) *Type {
	if len(call.Args) != 1 {
		c.badArity(call.Pos(), name.Name)
		return nil
	}
	arg := call.Args[0].Expr
	got := c.inferExpr(s, arg)
	if name.Name == "raw" && got != nil && !got.IsInteger() {
		switch arg.(type) {
		case *ast.NameExpr, *ast.FieldExpr, *ast.IndexExpr, *ast.SliceExpr:
		default:
			c.typeMismatch(arg.Pos(), got, c.in.scalar("usize"))
		}
	}
	return c.typeByName(name.Name)
}

func (c *checker) typeByName(name string) *Type {
	if name == "int" {
		return c.in.scalar("i64")
	}
	if name == "float" {
		return c.in.scalar("f64")
	}
	if decl, ok := c.env.aliases[name]; ok {
		return c.resolveAlias(decl)
	}
	if t, ok := c.env.types[name]; ok {
		return t
	}
	if t := c.in.builtin(name); t != nil {
		return t
	}
	return c.in.named(name, nil)
}

// instantiateNamed builds the type a constructor call names. A generic class or
// struct writes its arguments on the constructor, as in Box<i32>(...), so the
// field and init contract must be specialized before the arguments are checked.
func (c *checker) instantiateNamed(s *inferScope, name string, typeArgs []*ast.Type, class bool) *Type {
	if len(typeArgs) == 0 {
		return c.typeByName(name)
	}
	args := make([]*Type, len(typeArgs))
	for i, a := range typeArgs {
		args[i] = c.buildType(a, s.tp)
	}
	if want, ok := c.env.arity[name]; ok && len(args) != want {
		c.bag.Error(typeArgs[0].Pos(), diag.CodeTypeArgCount, "wrong number of type arguments",
			"the declaration takes "+strconv.Itoa(want))
		return c.typeByName(name)
	}
	if class {
		return c.in.namedClass(name, args)
	}
	return c.in.named(name, args)
}

// substFor pairs the declared type parameters of a named type with the type
// arguments of one use. It returns nil when the use is not generic.
func (c *checker) substFor(name string, args []*Type) map[string]*Type {
	params := c.env.params[name]
	if len(params) == 0 || len(params) != len(args) {
		return nil
	}
	subst := make(map[string]*Type, len(params))
	for i, p := range params {
		subst[p] = args[i]
	}
	return subst
}

// instantiateReceiver specializes the signature of a method or init on a
// generic receiver for one receiver type.
func (c *checker) instantiateReceiver(sig *signature, recv *Type) *signature {
	if sig == nil || recv == nil || len(sig.recvParams) == 0 {
		return nil
	}
	if len(sig.recvParams) != len(recv.Args()) {
		return nil
	}
	subst := make(map[string]*Type, len(sig.recvParams))
	for i, param := range sig.recvParams {
		subst[param] = recv.Args()[i]
	}
	return c.instantiate(sig, subst)
}

// instantiate replaces the type parameters of one signature with the types the
// call site wrote explicitly.
func (c *checker) instantiate(sig *signature, subst map[string]*Type) *signature {
	out := *sig
	out.params = make([]*Type, len(sig.params))
	for i, p := range sig.params {
		out.params[i] = c.substitute(p, subst)
	}
	out.result = c.substitute(sig.result, subst)
	return &out
}

// substitute rebuilds a type with every type parameter replaced. A parameter is
// a named type with no arguments; arrays, views, function types, and named
// types with arguments are rebuilt so nested parameters are substituted too.
func (c *checker) substitute(t *Type, subst map[string]*Type) *Type {
	if t == nil {
		return nil
	}
	if t.Kind() == NamedKind && len(t.Args()) == 0 {
		if repl := subst[t.Name()]; repl != nil {
			return repl
		}
	}
	switch t.Kind() {
	case ArrayKind:
		return c.in.array(t.length, c.substitute(t.Elem(), subst))
	case ViewKind:
		return c.in.view(c.substitute(t.Elem(), subst))
	case FuncKind:
		params := make([]*Type, len(t.Params()))
		for i, p := range t.Params() {
			params[i] = c.substitute(p, subst)
		}
		return c.in.funcT(params, c.substitute(t.Result(), subst))
	case NamedKind:
		if len(t.Args()) > 0 {
			args := make([]*Type, len(t.Args()))
			for i, a := range t.Args() {
				args[i] = c.substitute(a, subst)
			}
			if c.env.classes[t.Name()] {
				return c.in.namedClass(t.Name(), args)
			}
			return c.in.named(t.Name(), args)
		}
	}
	return t
}

func (c *checker) checkTypeArgs(call *ast.CallExpr, sig *signature) {
	if len(call.TypeArgs) > 0 && sig.typeParams == 0 {
		c.bag.Error(call.Pos(), diag.CodeTypeArgCount, "wrong number of type arguments", "the declaration takes 0")
		return
	}
	if sig.typeParams > 0 && len(call.TypeArgs) != sig.typeParams {
		c.bag.Error(call.Pos(), diag.CodeTypeArgCount, "wrong number of type arguments",
			"the declaration takes "+strconv.Itoa(sig.typeParams))
	}
	for _, ta := range call.TypeArgs {
		c.buildType(ta, nil)
	}
}

// blockingSyncMethod reports whether one method is a CPU blocking entry of
// std/sync. They park a real worker thread and are only valid from a normal def
// (CPU job), never from an async state machine.
func blockingSyncMethod(recv *Type, name string) bool {
	if recv == nil {
		return false
	}
	base := recv.Name()
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		base = base[i+1:]
	}
	switch base {
	case "Mutex":
		return name == "lock_blocking"
	case "RwLock":
		return name == "r_lock_blocking" || name == "w_lock_blocking"
	case "Semaphore":
		return name == "acquire_blocking"
	case "Count":
		return name == "wait_blocking"
	case "Barrier":
		return name == "wait_blocking"
	case "Once":
		return name == "do_blocking"
	}
	return false
}

func (c *checker) methodType(s *inferScope, call *ast.MethodCallExpr) *Type {
	if call.Link != nil {
		return c.cLinkType(s, call)
	}
	if recv, ok := call.Recv.(*ast.NameExpr); ok && call.Name != "" {
		if sym := c.symbolOf(recv); sym != nil && sym.Kind == resolve.ImportKind &&
			call.Name[0] >= 'A' && call.Name[0] <= 'Z' {
			for _, a := range call.Args {
				c.inferExpr(s, a.Expr)
			}
			return c.in.named(recv.Name+"."+call.Name, nil)
		}
	}
	if recv, ok := call.Recv.(*ast.NameExpr); ok {
		if decl := c.foreignFuncs[recv.Name][call.Name]; decl != nil {
			sig := c.buildForeignSig(decl, recv.Name)
			sig = c.inferCallTypeArgs(s, call.Args, call.TypeArgs, sig)
			if sig.typeParams > 0 && len(call.TypeArgs) == sig.typeParams && len(sig.typeNames) == sig.typeParams {
				subst := make(map[string]*Type, sig.typeParams)
				for i, ta := range call.TypeArgs {
					subst[sig.typeNames[i]] = c.buildType(ta, nil)
				}
				sig = c.instantiate(sig, subst)
			}
			c.checkCallArgs(s, sig, call.Args, call.Pos())
			if sig.async {
				c.requireWait(s, call.Pos())
			}
			return sig.result
		}
	}
	recv := c.inferExpr(s, call.Recv)
	for _, a := range call.Args {
		c.inferExpr(s, a.Expr)
	}
	if recv != nil && recv.Kind() == NamedKind && (recv.Name() == "rc" || recv.Name() == "arc") && len(recv.Args()) == 1 {
		switch call.Name {
		case "weak":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			weak := "weak_rc"
			if recv.Name() == "arc" {
				weak = "weak_arc"
			}
			return c.in.named(weak, []*Type{recv.Args()[0]})
		case "clone":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return recv
		}
		recv = recv.Args()[0]
	}
	if isGuardType(recv) {
		readOnly := isReadGuardType(recv)
		recv = recv.Args()[0]
		if readOnly && recv != nil && recv.Kind() == NamedKind {
			if sig, ok := c.env.methods[recv.Name()+"."+call.Name]; ok && sig.recvMut {
				c.bag.Error(call.Pos(), diag.CodeReadGuardWrite,
					"a read guard is read-only",
					"take the write guard with w_lock")
			}
		}
	}
	if s.async && blockingSyncMethod(recv, call.Name) {
		c.bag.Error(call.Pos(), diag.CodeBlockingSyncInAsync,
			"a blocking CPU sync call cannot run in an async function",
			"use the awaitable form, or move the CPU work to a normal def")
	}
	if recv != nil && recv.Kind() == NamedKind && (recv.Name() == "weak_rc" || recv.Name() == "weak_arc") && len(recv.Args()) == 1 {
		if call.Name == "upgrade" {
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			strong := "rc"
			if recv.Name() == "weak_arc" {
				strong = "arc"
			}
			return c.in.named("Option", []*Type{c.in.named(strong, []*Type{recv.Args()[0]})})
		}
		return nil
	}
	if decl := c.foreignMethod(recv, call.Name); decl != nil {
		pkg, _ := packageAndType(recv.Name())
		sig := c.buildForeignSig(decl, pkg)
		sig = c.inferCallTypeArgs(s, call.Args, call.TypeArgs, sig)
		if sig.typeParams > 0 && len(call.TypeArgs) == sig.typeParams && len(sig.typeNames) == sig.typeParams {
			subst := make(map[string]*Type, sig.typeParams)
			for i, ta := range call.TypeArgs {
				subst[sig.typeNames[i]] = c.buildType(ta, nil)
			}
			sig = c.instantiate(sig, subst)
		}
		if inst := c.instantiateReceiver(sig, recv); inst != nil {
			sig = inst
		}
		c.checkCallArgs(s, sig, call.Args, call.Pos())
		if sig.async && !s.allowAsyncCall {
			c.bag.Error(call.Pos(), diag.CodeAsyncNotAwaited,
				"this async call is not awaited",
				"write await, or spawn")
		}
		return sig.result
	}
	switch call.Name {
	case "recv":
		c.requireWait(s, call.Pos())
		return elemOf(c.in, recv)
	case "send":
		if recv != nil && recv.Kind() == NamedKind && recv.Name() == "chan" {
			c.requireWait(s, call.Pos())
			var elem *Type
			if len(recv.Args()) == 1 {
				elem = recv.Args()[0]
			}
			c.checkPayload(s, []*Type{elem}, call.Args, call.Name, call.Pos())
			return c.in.named("Result", []*Type{c.in.unit(), c.in.named("Error", nil)})
		}
	case "cancel":
		if recv != nil && recv.Kind() == NamedKind && recv.Name() == "Task" {
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.unit()
		}
		return nil
	case "close":
		if recv != nil && recv.Kind() == NamedKind && recv.Name() == "chan" {
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.unit()
		}
	}
	if recv == nil {
		return nil
	}
	if call.Name == "clone" && len(call.Args) == 0 && !isGuardType(recv) {
		if recv.Kind() == ViewKind {
			return nil
		}
		if recv.Kind() == NamedKind {
			switch recv.Name() {
			case "chan", "Task":
				return nil
			case "str", "Array", "Map", "Set":
				return nil
			}
		}
		c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
		return recv
	}
	if strings.HasPrefix(call.Name, "Async") {
		c.requireWait(s, call.Pos())
	}
	if recv.Kind() == ScalarKind && recv.Name() == "raw" {
		return c.rawMethod(s, call)
	}
	if t, ok := c.builtinContainerMethod(s, recv, call); ok {
		return t
	}
	if recv.Kind() != NamedKind {
		return nil
	}
	methods, isIface := c.env.ifaces[recv.Name()]
	if recv.Name() == "interface {}" {
		methods, isIface = nil, true
	}
	if isIface {
		if call.Name == "is" || call.Name == "as" {
			if len(call.TypeArgs) != 1 {
				c.bag.Error(call.Pos(), diag.CodeTypeArgCount, "wrong number of type arguments",
					"a downcast takes one target type")
				return nil
			}
			target := c.buildType(call.TypeArgs[0], nil)
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			if call.Name == "is" {
				return c.in.scalar("bool")
			}
			if target == nil {
				return nil
			}
			return c.in.named("Option", []*Type{target})
		}
		c.dispatch = append(c.dispatch, DispatchPoint{Pos: call.Pos(), Interface: recv.Name()})
		for _, m := range methods {
			if m.name == call.Name {
				c.checkTypeArgsForMethod(call, m.sig)
				c.checkPayload(s, m.sig.params, call.Args, call.Name, call.Pos())
				return m.sig.result
			}
		}
		return nil
	}
	if fields, ok := c.env.fields[recv.Name()]; ok {
		for _, f := range fields {
			if f.name != call.Name || f.typ == nil || f.typ.Kind() != FuncKind {
				continue
			}
			c.checkPayload(s, f.typ.Params(), call.Args, call.Name, call.Pos())
			return f.typ.Result()
		}
	}
	if sig, ok := c.env.methods[recv.Name()+"."+call.Name]; ok {
		c.checkTypeArgsForMethod(call, sig)
		if inst := c.instantiateReceiver(sig, recv); inst != nil {
			sig = inst
		}
		if sig.variadic {
			c.checkCallArgs(s, sig, call.Args, call.Pos())
		} else {
			c.checkPayload(s, sig.params, call.Args, call.Name, call.Pos())
		}
		if sig.async && !s.allowAsyncCall {
			c.bag.Error(call.Pos(), diag.CodeAsyncNotAwaited,
				"this async call is not awaited",
				"write await, or spawn")
		}
		return sig.result
	}
	if c.env.userTypes[recv.Name()] {
		c.bag.Error(call.NamePos, diag.CodeNoMethod, "no method "+call.Name+" on "+recv.Name(),
			"check the method name and the receiver")
	}
	return nil
}

// rawMethod types the fixed method set of raw pointers. Every load names the
// width it reads; a store takes the same width; the atomic and volatile forms
// are the u32 subset the language exposes.
func (c *checker) rawMethod(s *inferScope, call *ast.MethodCallExpr) *Type {
	usize := c.in.scalar("usize")
	boolean := c.in.scalar("bool")
	raw := c.in.scalar("raw")
	width := map[string]string{
		"LoadU8": "u8", "LoadU16": "u16", "LoadU32": "u32", "LoadU64": "u64",
		"LoadI8": "i8", "LoadI16": "i16", "LoadI32": "i32", "LoadI64": "i64",
		"LoadF32": "f32", "LoadF64": "f64",
	}
	if name, ok := width[call.Name]; ok {
		c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
		return c.in.scalar(name)
	}
	if call.Name == "LoadRaw" {
		c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
		return raw
	}
	storeWidth := map[string]string{
		"StoreU8": "u8", "StoreU16": "u16", "StoreU32": "u32", "StoreU64": "u64",
		"StoreI8": "i8", "StoreI16": "i16", "StoreI32": "i32", "StoreI64": "i64",
		"StoreF32": "f32", "StoreF64": "f64",
	}
	if name, ok := storeWidth[call.Name]; ok {
		c.checkPayload(s, []*Type{c.in.scalar(name)}, call.Args, call.Name, call.Pos())
		return c.in.unit()
	}
	switch call.Name {
	case "StoreRaw":
		c.checkPayload(s, []*Type{raw}, call.Args, call.Name, call.Pos())
		return c.in.unit()
	case "VolatileLoadU32", "AtomicLoadU32":
		c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
		return c.in.scalar("u32")
	case "VolatileStoreU32", "AtomicStoreU32":
		c.checkPayload(s, []*Type{c.in.scalar("u32")}, call.Args, call.Name, call.Pos())
		return c.in.unit()
	case "AtomicAddU32":
		c.checkPayload(s, []*Type{c.in.scalar("u32")}, call.Args, call.Name, call.Pos())
		return c.in.scalar("u32")
	case "AtomicCasU32":
		c.checkPayload(s, []*Type{c.in.scalar("u32"), c.in.scalar("u32")}, call.Args, call.Name, call.Pos())
		return boolean
	}
	_ = usize
	return nil
}

// cLinkType types one call through the c qualifier from its header prototype.
// An untyped link keeps the old permissive behavior; the generator then uses
// the i32 fallback.
func (c *checker) cLinkType(s *inferScope, call *ast.MethodCallExpr) *Type {
	link := call.Link
	if !link.Typed {
		for _, a := range call.Args {
			c.inferExpr(s, a.Expr)
		}
		return nil
	}
	params := make([]*Type, len(link.Params))
	for i, p := range link.Params {
		params[i] = c.buildType(p, nil)
	}
	for i, a := range call.Args {
		if i < len(params) {
			c.checkValue(s, a.Expr, params[i])
			continue
		}
		if !link.Variadic {
			c.badArity(call.Pos(), link.Name)
			break
		}
		extra := c.inferExpr(s, a.Expr)
		if extra != nil {
			switch extra.Name() {
			case "i8", "u8", "i16", "u16", "f32":
				c.bag.Error(a.Expr.Pos(), diag.CodeVariadicWiden,
					"variadic C arguments must be widened",
					"write i32(x) or f64(x)")
			}
		}
	}
	if !link.Variadic && len(call.Args) < len(params) {
		c.badArity(call.Pos(), link.Name)
	}
	result := c.in.unit()
	if link.Result != nil {
		result = c.buildType(link.Result, nil)
	}
	if link.Blocking {
		return c.in.named("Task", []*Type{result})
	}
	return result
}

// builtinContainerMethod types the Map and Set methods the lowering provides.
// The predeclared containers do not have declarations in the environment, so
// their signatures are written here once.
func (c *checker) builtinContainerMethod(s *inferScope, recv *Type, call *ast.MethodCallExpr) (*Type, bool) {
	usize := c.in.scalar("usize")
	boolean := c.in.scalar("bool")
	if recv.Kind() == ScalarKind && recv.Name() == "str" {
		strT := c.in.scalar("str")
		switch call.Name {
		case "len":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return usize, true
		case "contains", "has_prefix", "has_suffix":
			c.checkPayload(s, []*Type{strT}, call.Args, call.Name, call.Pos())
			return boolean, true
		case "index":
			c.checkPayload(s, []*Type{strT}, call.Args, call.Name, call.Pos())
			return c.in.named("Option", []*Type{usize}), true
		case "trim", "trim_left", "trim_right", "upper", "lower", "clone":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return strT, true
		case "repeat":
			c.checkPayload(s, []*Type{usize}, call.Args, call.Name, call.Pos())
			return strT, true
		case "replace":
			c.checkPayload(s, []*Type{strT, strT}, call.Args, call.Name, call.Pos())
			return strT, true
		case "split":
			c.checkPayload(s, []*Type{strT}, call.Args, call.Name, call.Pos())
			return c.in.view(strT), true
		case "split_n":
			c.checkPayload(s, []*Type{strT, c.in.scalar("i32")}, call.Args, call.Name, call.Pos())
			return c.in.view(strT), true
		case "fields":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.view(strT), true
		case "Chars":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.named("Array", []*Type{c.in.scalar("u32")}), true
		}
		return nil, false
	}
	if recv.Kind() == ViewKind {
		if call.Name == "len" {
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return usize, true
		}
		return nil, false
	}
	if recv.Kind() == NamedKind && (recv.Name() == "Array" || recv.Name() == "list") {
		elem := argAt(recv, 0)
		switch call.Name {
		case "len", "cap":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return usize, true
		case "get":
			c.checkPayload(s, []*Type{usize}, call.Args, call.Name, call.Pos())
			return elem, true
		case "set":
			c.checkPayload(s, []*Type{usize, elem}, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "push":
			c.checkPayload(s, []*Type{elem}, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "append":
			c.checkPayload(s, []*Type{c.in.view(elem)}, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "pop":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.named("Option", []*Type{elem}), true
		case "insert":
			c.checkPayload(s, []*Type{usize, elem}, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "remove":
			c.checkPayload(s, []*Type{usize}, call.Args, call.Name, call.Pos())
			return elem, true
		case "clear", "shrink_to_fit":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "slice":
			c.checkPayload(s, []*Type{usize, usize}, call.Args, call.Name, call.Pos())
			return c.in.view(elem), true
		case "clone":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.named(recv.Name(), []*Type{elem}), true
		}
		return nil, false
	}
	if recv.Kind() != NamedKind {
		return nil, false
	}
	switch recv.Name() {
	case "dict":
		k, v := argAt(recv, 0), argAt(recv, 1)
		listName := "Array"
		if recv.Name() == "dict" {
			listName = "list"
		}
		switch call.Name {
		case "len":
			return usize, true
		case "get":
			c.checkPayload(s, []*Type{k}, call.Args, call.Name, call.Pos())
			return c.in.named("Option", []*Type{v}), true
		case "set":
			c.checkPayload(s, []*Type{k, v}, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "has", "delete":
			c.checkPayload(s, []*Type{k}, call.Args, call.Name, call.Pos())
			return boolean, true
		case "clear":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "keys":
			return c.in.named(listName, []*Type{k}), true
		case "values":
			return c.in.named(listName, []*Type{v}), true
		case "each":
			c.checkPayload(s, []*Type{c.in.funcT([]*Type{k, v}, nil)}, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		}
	case "set":
		elem := argAt(recv, 0)
		switch call.Name {
		case "len":
			return usize, true
		case "add", "has", "delete":
			c.checkPayload(s, []*Type{elem}, call.Args, call.Name, call.Pos())
			return boolean, true
		case "clear":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "each":
			c.checkPayload(s, []*Type{c.in.funcT([]*Type{elem}, nil)}, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		}
	}
	return nil, false
}

func (c *checker) checkTypeArgsForMethod(call *ast.MethodCallExpr, sig *signature) {
	if sig.typeParams > 0 && len(call.TypeArgs) != sig.typeParams {
		c.bag.Error(call.Pos(), diag.CodeTypeArgCount, "wrong number of type arguments",
			"the declaration takes "+strconv.Itoa(sig.typeParams))
	}
}

func (c *checker) fieldType(recv *Type, name string, pos source.Pos) *Type {
	if recv == nil || recv.Kind() != NamedKind {
		return nil
	}
	if (recv.Name() == "rc" || recv.Name() == "arc") && len(recv.Args()) == 1 {
		recv = recv.Args()[0]
		if recv == nil || recv.Kind() != NamedKind {
			return nil
		}
	}
	// A guard forwards every field to the value it guards. "value" names that
	// value itself; a field of the guarded type is forwarded as well.
	if isGuardType(recv) && len(recv.Args()) == 1 {
		elem := recv.Args()[0]
		if name == "value" {
			return elem
		}
		if elem != nil && elem.Kind() == NamedKind {
			if fields, known := c.env.fields[elem.Name()]; known {
				return c.fieldOf(elem, fields, name, pos)
			}
		}
		return nil
	}
	if fields, known := c.env.fields[recv.Name()]; known {
		return c.fieldOf(recv, fields, name, pos)
	}
	return nil
}

// isGuardType reports whether a type is the transparent guard of std/sync.
// The name test accepts the qualified spelling the checker uses for an
// imported type and the bare spelling inside std/sync itself.
func isGuardType(t *Type) bool {
	if t == nil || t.Kind() != NamedKind || len(t.Args()) != 1 {
		return false
	}
	name := t.Name()
	return name == "Guard" || name == "ReadGuard" ||
		strings.HasSuffix(name, "sync.Guard") || strings.HasSuffix(name, "sync.ReadGuard")
}

// isReadGuardType reports whether the type is the read side only.
func isReadGuardType(t *Type) bool {
	if t == nil || t.Kind() != NamedKind {
		return false
	}
	name := t.Name()
	return name == "ReadGuard" || strings.HasSuffix(name, "sync.ReadGuard")
}

// packageAndType splits a qualified type name into its package name and its
// bare type name. It returns empty strings for an unqualified name, which is a
// type of the current package.
func packageAndType(name string) (string, string) {
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 || dot+1 >= len(name) {
		return "", ""
	}
	path, typ := name[:dot], name[dot+1:]
	if slash := strings.LastIndexByte(path, '/'); slash >= 0 {
		path = path[slash+1:]
	}
	return path, typ
}

// foreignMethod returns the declaration of a method declared by an imported
// package. The receiver type name carries the package qualifier, so the lookup
// key is Type.Method under the package's short name.
func (c *checker) foreignMethod(recv *Type, name string) *ast.FuncDecl {
	if recv == nil || recv.Kind() != NamedKind {
		return nil
	}
	pkg, typ := packageAndType(recv.Name())
	if pkg == "" || typ == "" {
		return nil
	}
	methods := c.foreignFuncs[pkg]
	if methods == nil {
		return nil
	}
	return methods[typ+"."+name]
}

// requireWait rejects a wait operation outside await, wait, or spawn.
func (c *checker) requireWait(s *inferScope, pos source.Pos) {
	if s.async || s.allowAsyncCall || s.parallel {
		return
	}
	c.bag.Error(pos, diag.CodeAsyncNotAwaited,
		"this async call is not awaited",
		"write await, wait(...), or spawn")
}

// awaitOperand accepts the shapes await may wait on: an async call, a method
// wait operation, or a ?/! wrapper around one.
func awaitOperand(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.CallExpr, *ast.MethodCallExpr, *ast.DeadlineExpr:
		return true
	case *ast.TryExpr:
		return awaitOperand(x.X)
	case *ast.UnwrapExpr:
		return awaitOperand(x.X)
	}
	return false
}

// guardProducer reports whether an expression creates a guard rather than
// copying one. A call creates it; unwrapping an Option<Guard<T>> still does.
func guardProducer(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.CallExpr, *ast.MethodCallExpr, *ast.AggregateExpr:
		return true
	case *ast.UnwrapExpr:
		return guardProducer(x.X)
	case *ast.AwaitExpr:
		return guardProducer(x.X)
	case *ast.TryExpr:
		return guardProducer(x.X)
	}
	return false
}

// readGuardWrite reports whether a place is reached through a read guard. A
// field or an index through the guard is a write to the guarded value; a name
// is a rebinding of the guard itself.
func (c *checker) readGuardWrite(s *inferScope, e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.NameExpr:
		return isReadGuardType(c.inferExpr(s, x))
	case *ast.FieldExpr:
		return isReadGuardType(c.inferExpr(s, x.Recv))
	case *ast.IndexExpr:
		return c.readGuardWrite(s, x.Recv)
	}
	return false
}

// fieldOf looks one name up in a known field list and substitutes the type
// arguments of the receiver.
func (c *checker) fieldOf(recv *Type, fields []*fieldInfo, name string, pos source.Pos) *Type {
	subst := c.substFor(recv.Name(), recv.Args())
	for _, f := range fields {
		if f.name == name {
			if subst != nil {
				return c.substitute(f.typ, subst)
			}
			return f.typ
		}
	}
	c.bag.Error(pos, diag.CodeNoField, "no field "+name+" on "+recv.Name(), "check the field name or the type")
	return nil
}

// arrayLitType checks one fixed-array literal. The explicit type supplies the
// element type, and the written length must match the number of elements.
func (c *checker) arrayLitType(s *inferScope, lit *ast.ArrayLitExpr) *Type {
	t := c.buildType(lit.Type, nil)
	if t == nil || t.Kind() != ArrayKind {
		return t
	}
	for _, elem := range lit.Elems {
		c.checkValue(s, elem, t.Elem())
	}
	n, err := strconv.Atoi(t.Length())
	if err == nil && n != len(lit.Elems) {
		c.bag.Error(lit.Pos(), diag.CodeBadArgument,
			"wrong number of array elements",
			"write exactly "+strconv.Itoa(n)+" elements")
	}
	return t
}

func (c *checker) aggregateType(s *inferScope, agg *ast.AggregateExpr) *Type {
	t := c.buildType(&ast.Type{Span: agg.Span, Kind: ast.NamedType, Name: agg.TypeName, Args: agg.TypeArgs}, nil)
	var subst map[string]*Type
	if t != nil {
		subst = c.substFor(agg.TypeName, t.Args())
	}
	fields, known := c.env.fields[agg.TypeName]
	for _, f := range agg.Fields {
		var want *Type
		found := false
		for _, fi := range fields {
			if fi.name == f.Name {
				want = fi.typ
				if subst != nil {
					want = c.substitute(want, subst)
				}
				found = true
			}
		}
		if known && !found {
			c.bag.Error(f.Pos(), diag.CodeNoField, "no field "+f.Name+" on "+agg.TypeName,
				"check the field name or the type")
		}
		c.checkValue(s, f.Value, want)
	}
	return t
}

// foreignInitSig builds the instantiated init signature of a qualified type.
// The receiver type arguments of the declaration are replaced by the concrete
// arguments the new expression writes.
func (c *checker) foreignInitSig(name string, recv *Type) *signature {
	if recv == nil {
		return nil
	}
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 {
		return nil
	}
	decl := c.foreignInits[name[:dot]][name[dot+1:]]
	if decl == nil || decl.Recv == nil || len(decl.Recv.Type.Args) != len(recv.Args()) {
		return nil
	}
	sig := c.buildForeignSig(decl, name[:dot])
	subst := make(map[string]*Type, len(recv.Args()))
	for i, a := range decl.Recv.Type.Args {
		if a.Kind == ast.NamedType && len(a.Args) == 0 {
			subst[a.Name] = recv.Args()[i]
		}
	}
	return c.instantiate(sig, subst)
}

func (c *checker) newType(s *inferScope, n *ast.NewExpr) *Type {
	t := c.buildType(n.Type, nil)
	name := ""
	if t != nil {
		name = t.Name()
	}
	if (name == "rc" || name == "arc") && t != nil && len(t.Args()) == 1 {
		elem := t.Args()[0]
		if sig, ok := c.env.inits[elem.Name()]; ok {
			if inst := c.instantiateReceiver(sig, elem); inst != nil {
				sig = inst
			}
			c.checkCallArgs(s, sig, n.Args, n.Pos())
			return t
		}
		if len(n.Args) != 1 {
			c.badArity(n.Pos(), name)
			for _, a := range n.Args {
				c.inferExpr(s, a.Expr)
			}
			return t
		}
		c.checkValue(s, n.Args[0].Expr, elem)
		return t
	}
	if strings.Contains(name, ".") {
		// The defining package owns the init declaration. Use it when it is
		// available: new returns the init's Result when init has one.
		if sig := c.foreignInitSig(name, t); sig != nil {
			c.checkCallArgs(s, sig, n.Args, n.Pos())
			if sig.result != nil {
				return sig.result
			}
			return t
		}
		for _, a := range n.Args {
			c.inferExpr(s, a.Expr)
		}
		return t
	}
	if sig, ok := c.env.inits[name]; ok {
		if t != nil {
			if inst := c.instantiateReceiver(sig, t); inst != nil {
				sig = inst
			}
		}
		c.checkCallArgs(s, sig, n.Args, n.Pos())
		if sig.result != nil {
			return sig.result
		}
		return t
	}
	if c.isUserType(name) {
		c.bag.Error(n.Pos(), diag.CodeNewWithoutInit, "type "+name+" has no init; use a literal",
			"write "+name+" { ... } or add an init method")
		for _, a := range n.Args {
			c.inferExpr(s, a.Expr)
		}
		return t
	}
	// The predeclared containers take their capacity as usize. The generic
	// fallback below is for a target whose constructor has no visible
	// declaration, and its i32 assumption must not leak into Array, Map, or
	// Set.
	if name == "Array" || name == "Map" || name == "Set" {
		capacity := c.in.scalar("usize")
		for i, a := range n.Args {
			c.checkArg(s, a.Expr, capacity, name, i+1)
		}
		return t
	}
	if name == "chan" {
		if len(n.Args) != 1 {
			c.badArity(n.Pos(), name)
			for _, a := range n.Args {
				c.inferExpr(s, a.Expr)
			}
			return t
		}
		c.checkArg(s, n.Args[0].Expr, c.in.scalar("usize"), name, 1)
		return t
	}
	for _, a := range n.Args {
		c.checkArg(s, a.Expr, c.in.scalar("i32"), name, 1)
	}
	return t
}

// isUserType reports whether name is a struct or enum declared in this file.
func (c *checker) isUserType(name string) bool {
	if name == "" {
		return false
	}
	if _, ok := c.env.fields[name]; ok {
		return true
	}
	for _, vi := range c.env.variants {
		if vi.enum.Name() == name {
			return true
		}
	}
	return false
}

func (c *checker) unaryType(s *inferScope, u *ast.UnaryExpr) *Type {
	t := c.inferExpr(s, u.X)
	switch u.Op {
	case token.Minus:
		if t != nil && !t.IsNumeric() {
			c.typeMismatch(u.Pos(), t, nil)
		}
	case token.Bang, token.KwNot:
		if t != nil && !isBool(t) {
			c.typeMismatch(u.Pos(), t, c.in.scalar("bool"))
		}
		return c.in.scalar("bool")
	case token.Tilde:
		if t != nil && !t.IsInteger() {
			c.typeMismatch(u.Pos(), t, nil)
		}
	}
	return t
}

// chainedOperand reports whether either side of b is itself a comparison, so
// the T0006 report is the only one.
func chainedOperand(b *ast.BinaryExpr) bool {
	if l, ok := b.L.(*ast.BinaryExpr); ok && isComparison(l.Op) {
		return true
	}
	if r, ok := b.R.(*ast.BinaryExpr); ok && isComparison(r.Op) {
		return true
	}
	return false
}

func (c *checker) binaryType(s *inferScope, b *ast.BinaryExpr) *Type {
	if isComparison(b.Op) {
		lt := c.inferExpr(s, b.L)
		rt := c.exprWithWant(s, b.R, lt)
		if !chainedOperand(b) && !Equal(lt, rt) {
			c.typeMismatch(b.Pos(), rt, lt)
		}
		return c.in.scalar("bool")
	}
	if b.Op == token.AndAnd || b.Op == token.OrOr {
		c.checkValue(s, b.L, c.in.scalar("bool"))
		c.checkValue(s, b.R, c.in.scalar("bool"))
		return c.in.scalar("bool")
	}
	lt := c.inferExpr(s, b.L)
	if b.Op == token.Pow {
		rt := c.inferExpr(s, b.R)
		if lt != nil && !lt.IsNumeric() {
			c.typeMismatch(b.Pos(), lt, nil)
			return lt
		}
		if lt != nil && lt.IsInteger() && (rt == nil || !rt.IsInteger()) {
			c.typeMismatch(b.R.Pos(), rt, c.in.scalar("u32"))
		}
		return lt
	}
	if b.Op == token.Plus && lt != nil && lt.Kind() == ScalarKind && lt.Name() == "raw" {
		c.checkValue(s, b.R, c.in.scalar("usize"))
		return lt
	}
	rt := c.exprWithWant(s, b.R, lt)
	if b.Op == token.Plus && lt != nil && lt.Kind() == ScalarKind && lt.Name() == "str" {
		if !Equal(lt, rt) {
			c.typeMismatch(b.Pos(), rt, lt)
		}
		return lt
	}
	if lt != nil && lt.Kind() == NamedKind && lt.Name() == "Duration" {
		if Equal(lt, rt) && (b.Op == token.Plus || b.Op == token.Minus) {
			return lt
		}
		if rt != nil && rt.IsInteger() && (b.Op == token.Star || b.Op == token.Slash) {
			return lt
		}
	}
	if rt != nil && rt.Kind() == NamedKind && rt.Name() == "Duration" &&
		lt != nil && lt.IsInteger() && b.Op == token.Star {
		return rt
	}
	switch b.Op {
	case token.Shl, token.Shr, token.ShlWrap:
		if lt != nil && !lt.IsInteger() {
			c.typeMismatch(b.Pos(), lt, nil)
		}
		if b.Op != token.ShlWrap {
			c.checkShiftAmount(b.R, lt)
		}
		return lt
	}
	if !Equal(lt, rt) {
		c.typeMismatch(b.Pos(), rt, lt)
		return lt
	}
	if lt != nil && !lt.IsNumeric() {
		c.typeMismatch(b.Pos(), lt, nil)
	}
	return lt
}

// checkShiftAmount reports a constant shift amount that is not less than the
// width of the shifted type. A shift by a variable amount is a runtime panic
// when it is out of range, so only a literal is checked here. The explicit
// truncating shift is the way out and is not checked.
func (c *checker) checkShiftAmount(e ast.Expr, lt *Type) {
	lit, ok := e.(*ast.LiteralExpr)
	if !ok || lit.Kind != token.Int || lt == nil {
		return
	}
	value, _, ok := parseIntLiteral(lit.Text)
	if !ok || value.overflow {
		return
	}
	width, _, known := intWidth(lt.Name())
	if !known || value.mag < uint64(width) {
		return
	}
	c.bag.Error(lit.Pos(), diag.CodeShiftAmount,
		"shift amount "+strconv.FormatUint(value.mag, 10)+" is not less than the width of "+lt.Name(),
		"shift by less than "+strconv.FormatUint(uint64(width), 10)+", or write <<% to truncate")
}

// ifType and matchType check bodies and unify the branch value types.
func (c *checker) ifType(s *inferScope, ifx *ast.IfExpr) *Type {
	c.checkValue(s, ifx.Cond, c.in.scalar("bool"))
	then := c.inferBlock(s, ifx.Then)
	var els *Type
	if ifx.Else != nil {
		switch e := ifx.Else.(type) {
		case *ast.IfExpr:
			els = c.ifType(s, e)
		case *ast.Block:
			els = c.inferBlock(s, e)
		default:
			c.inferStmt(s, ifx.Else)
		}
	}
	return c.unifyTypes(ifx.Pos(), then, els)
}

func (c *checker) matchType(s *inferScope, m *ast.MatchExpr) *Type {
	c.inferExpr(s, m.Subject)
	var out *Type
	for _, arm := range m.Arms {
		inner := newInferScope(s)
		c.inferExpr(inner, arm.Guard)
		switch body := arm.Body.(type) {
		case *ast.Block:
			out = c.unifyTypes(m.Pos(), out, c.inferBlock(inner, body))
		case *ast.ExprStmt:
			out = c.unifyTypes(m.Pos(), out, c.inferExpr(inner, body.Expr))
		default:
			c.inferStmt(inner, arm.Body)
		}
	}
	return out
}

func (c *checker) unifyTypes(pos source.Pos, a, b *Type) *Type {
	if a == nil {
		return b
	}
	if b == nil || Equal(a, b) {
		return a
	}
	c.mismatch(pos, b, a, false, "", 0)
	return a
}

func elemOf(in *interner, t *Type) *Type {
	if t == nil {
		return nil
	}
	switch t.Kind() {
	case ArrayKind, ViewKind:
		return t.Elem()
	case NamedKind:
		switch t.Name() {
		case "Array", "Set":
			return argAt(t, 0)
		case "Map":
			return argAt(t, 1)
		}
	}
	if t.Kind() == ScalarKind && t.Name() == "str" {
		return in.scalar("u32")
	}
	return nil
}

// isStrType reports whether t is the predeclared str type.
func isStrType(t *Type) bool {
	return t != nil && t.Kind() == ScalarKind && t.Name() == "str"
}

// sliceOf returns the type of a slice. Slicing a fixed array yields a view of
// its element type, which is the two-word value the layout volume defines;
// slicing a view, an Array, or a str keeps that type.
func (c *checker) sliceOf(t *Type) *Type {
	if t == nil {
		return nil
	}
	switch t.Kind() {
	case ArrayKind:
		return c.in.view(t.Elem())
	case ViewKind:
		return t
	case NamedKind:
		if t.Name() == "Array" {
			// Slicing an Array yields the read-only view the methods return,
			// not the Array itself.
			return c.in.view(argAt(t, 0))
		}
	}
	if t.Kind() == ScalarKind && t.Name() == "str" {
		// A str slice is a byte view, not a str.
		return c.in.view(c.in.scalar("u8"))
	}
	return nil
}

func argAt(t *Type, i int) *Type {
	if t == nil || i >= len(t.Args()) {
		return nil
	}
	return t.Args()[i]
}
