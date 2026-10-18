package types

import (
	"strconv"

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
}

func newInferScope(parent *inferScope) *inferScope {
	s := &inferScope{parent: parent, vars: map[string]*Type{}}
	if parent != nil {
		s.result = parent.result
		s.tp = parent.tp
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

func (c *checker) inferFunc(decl *ast.FuncDecl) {
	tp := typeParamSet(decl.TypeParams)
	s := newInferScope(nil)
	s.tp = tp
	s.result = c.buildType(decl.Result, tp)
	if decl.Recv != nil {
		s.vars[decl.Recv.Name] = c.buildType(decl.Recv.Type, tp)
	}
	for _, pm := range decl.Params {
		t := c.buildType(pm.Type, tp)
		if pm.Variadic && t != nil {
			t = c.in.view(t)
		}
		s.vars[pm.Name] = t
	}
	c.inferBlock(s, decl.Body)
}

func (c *checker) inferBlock(s *inferScope, b *ast.Block) {
	if b == nil {
		return
	}
	inner := newInferScope(s)
	for _, st := range b.Stmts {
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
			c.checkValue(s, d.Value, want)
			s.vars[d.Name] = want
		case *ast.VarDecl:
			want := c.buildType(d.Type, s.tp)
			c.checkValue(s, d.Value, want)
			s.vars[d.Name] = want
		}
	case *ast.AssignStmt:
		want := c.inferPlace(s, x.Target)
		if x.Op != "=" && want != nil && !want.IsNumeric() {
			c.typeMismatch(x.Target.Pos(), want, nil)
		}
		c.checkValue(s, x.Value, want)
	case *ast.ExprStmt:
		c.inferExpr(s, x.Expr)
	case *ast.DeferStmt:
		c.inferExpr(s, x.Call)
	case *ast.ReturnStmt:
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
		c.inferBlock(s, x.Body)
	case *ast.SelectStmt:
		for _, arm := range x.Arms {
			inner := newInferScope(s)
			if t := c.inferExpr(s, arm.Chan); arm.Bind != "" {
				inner.vars[arm.Bind] = elemOf(t)
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
			elem := elemOf(t)
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
	got := c.exprWithWant(s, e, want)
	if !Equal(got, want) {
		c.typeMismatch(e.Pos(), got, want)
	}
}

// exprWithWant infers e, letting a numeric literal take want as its type.
func (c *checker) exprWithWant(s *inferScope, e ast.Expr, want *Type) *Type {
	if lit, ok := e.(*ast.LiteralExpr); ok {
		return c.literalType(lit, want)
	}
	return c.inferExpr(s, e)
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
	got := c.exprWithWant(s, e, want)
	if !Equal(got, want) {
		c.bag.Error(e.Pos(), diag.CodeBadArgument,
			"argument "+strconv.Itoa(n)+" has type "+got.String()+", want "+want.String(),
			"see the declaration of "+callee)
	}
}

func (c *checker) checkCallArgs(s *inferScope, sig *signature, args []*ast.CallArg) {
	name := sig.name
	if sig.variadic {
		fixed := len(sig.params) - 1
		if len(args) < fixed {
			c.badArity(args, name)
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
	if len(args) != len(sig.params) {
		c.badArity(args, name)
		return
	}
	for i, a := range args {
		c.checkArg(s, a.Expr, sig.params[i], name, i+1)
	}
}

func (c *checker) badArity(args []*ast.CallArg, callee string) {
	var pos source.Pos
	if len(args) > 0 {
		pos = args[0].Pos()
	}
	c.bag.Error(pos, diag.CodeBadArgument, "wrong number of arguments", "see the declaration of "+callee)
}

func (c *checker) inferExpr(s *inferScope, e ast.Expr) *Type {
	switch x := e.(type) {
	case nil:
		return nil
	case *ast.LiteralExpr:
		return c.literalType(x, nil)
	case *ast.NameExpr:
		if x.Name == "_" {
			return nil
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
		return c.fieldType(c.inferExpr(s, x.Recv), x.Name)
	case *ast.IndexExpr:
		return elemOf(c.inferExpr(s, x.Recv))
	case *ast.SliceExpr:
		return sliceOf(c.inferExpr(s, x.Recv))
	case *ast.AggregateExpr:
		return c.aggregateType(s, x)
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
		return c.in.funcT(params, c.buildType(x.Result, s.tp))
	case *ast.TryExpr:
		c.inferExpr(s, x.X)
		return nil
	case *ast.UnwrapExpr:
		c.inferExpr(s, x.X)
		return nil
	case *ast.SpawnExpr:
		return c.in.named("Task", []*Type{c.inferExpr(s, x.Call)})
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
	}
	return nil
}

func (c *checker) literalType(lit *ast.LiteralExpr, want *Type) *Type {
	switch lit.Kind {
	case token.Int:
		value, suffix, ok := parseIntLiteral(lit.Text)
		if suffix != "" && intTypeName(suffix) {
			if ok {
				c.checkIntConstant(lit.Pos(), value, lit.Text, suffix)
			}
			return c.in.scalar(suffix)
		}
		if want != nil && want.IsInteger() {
			if ok {
				c.checkIntConstant(lit.Pos(), value, lit.Text, want.Name())
			}
			return want
		}
		if ok {
			c.checkIntConstant(lit.Pos(), value, lit.Text, "i32")
		}
		return c.in.scalar("i32")
	case token.Float:
		if len(lit.Text) >= 3 && lit.Text[len(lit.Text)-3:] == "f32" {
			return c.in.scalar("f32")
		}
		if want != nil && (want.Name() == "f32" || want.Name() == "f64") {
			return want
		}
		return c.in.scalar("f64")
	case token.Duration:
		return c.env.types["Duration"]
	case token.Char:
		return c.in.scalar("u32")
	case token.String:
		return c.in.scalar("str")
	case token.Bytes:
		return c.in.view(c.in.scalar("u8"))
	case token.KwTrue, token.KwFalse:
		return c.in.scalar("bool")
	}
	return nil
}

func (c *checker) callType(s *inferScope, call *ast.CallExpr) *Type {
	name, ok := call.Callee.(*ast.NameExpr)
	if !ok {
		t := c.inferExpr(s, call.Callee)
		if t != nil && t.Kind() == FuncKind {
			c.checkPayload(s, t.Params(), call.Args, "")
			return t.Result()
		}
		for _, a := range call.Args {
			c.inferExpr(s, a.Expr)
		}
		return nil
	}
	sym := c.symbolOf(name)
	switch {
	case sym != nil && sym.Kind == resolve.FuncKind:
		sig := c.env.funcs[name.Name]
		if sig != nil {
			c.checkTypeArgs(call, sig)
			c.checkCallArgs(s, sig, call.Args)
			return sig.result
		}
	case sym != nil && sym.Kind == resolve.VariantKind:
		vi := c.env.variants[name.Name]
		if vi != nil {
			c.checkPayload(s, vi.payload, call.Args, name.Name)
			return vi.enum
		}
	case sym != nil && sym.Predeclared:
		return c.predeclaredCall(s, name.Name, call)
	case sym != nil && sym.IsType():
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

func (c *checker) predeclaredCall(s *inferScope, name string, call *ast.CallExpr) *Type {
	switch name {
	case "panic":
		c.checkPayload(s, []*Type{c.in.scalar("str")}, call.Args, name)
		return nil
	case "assert":
		c.checkPayload(s, []*Type{c.in.scalar("bool"), c.in.scalar("str")}, call.Args, name)
		return nil
	case "print":
		for _, a := range call.Args {
			c.inferExpr(s, a.Expr)
		}
		return nil
	case "Some":
		return c.in.named("Option", []*Type{c.oneArg(s, call)})
	case "None":
		return c.in.named("Option", nil)
	case "Ok":
		return c.in.named("Result", []*Type{c.oneArg(s, call), nil})
	case "Err":
		return c.in.named("Result", []*Type{nil, c.oneArg(s, call)})
	}
	for _, a := range call.Args {
		c.inferExpr(s, a.Expr)
	}
	return nil
}

func (c *checker) oneArg(s *inferScope, call *ast.CallExpr) *Type {
	if len(call.Args) != 1 {
		c.badArity(call.Args, "")
		return nil
	}
	return c.inferExpr(s, call.Args[0].Expr)
}

func (c *checker) checkPayload(s *inferScope, params []*Type, args []*ast.CallArg, callee string) {
	sig := &signature{name: callee, params: params}
	c.checkCallArgs(s, sig, args)
}

func (c *checker) castType(s *inferScope, name *ast.NameExpr, call *ast.CallExpr) *Type {
	if len(call.Args) != 1 {
		c.badArity(call.Args, name.Name)
		return nil
	}
	c.inferExpr(s, call.Args[0].Expr)
	return c.typeByName(name.Name)
}

func (c *checker) typeByName(name string) *Type {
	if t, ok := c.env.types[name]; ok {
		return t
	}
	if t := c.in.builtin(name); t != nil {
		return t
	}
	return c.in.named(name, nil)
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

func (c *checker) methodType(s *inferScope, call *ast.MethodCallExpr) *Type {
	recv := c.inferExpr(s, call.Recv)
	for _, a := range call.Args {
		c.inferExpr(s, a.Expr)
	}
	switch call.Name {
	case "Recv":
		return elemOf(recv)
	case "Join":
		return nil
	}
	if recv == nil || recv.Kind() != NamedKind {
		return nil
	}
	if sig, ok := c.env.methods[recv.Name()+"."+call.Name]; ok {
		c.checkTypeArgsForMethod(call, sig)
		c.checkPayload(s, sig.params, call.Args, call.Name)
		return sig.result
	}
	return nil
}

func (c *checker) checkTypeArgsForMethod(call *ast.MethodCallExpr, sig *signature) {
	if sig.typeParams > 0 && len(call.TypeArgs) != sig.typeParams {
		c.bag.Error(call.Pos(), diag.CodeTypeArgCount, "wrong number of type arguments",
			"the declaration takes "+strconv.Itoa(sig.typeParams))
	}
}

func (c *checker) fieldType(recv *Type, name string) *Type {
	if recv == nil || recv.Kind() != NamedKind {
		return nil
	}
	for _, f := range c.env.fields[recv.Name()] {
		if f.name == name {
			return f.typ
		}
	}
	return nil
}

func (c *checker) aggregateType(s *inferScope, agg *ast.AggregateExpr) *Type {
	t := c.typeByName(agg.TypeName)
	for _, f := range agg.Fields {
		var want *Type
		for _, fi := range c.env.fields[agg.TypeName] {
			if fi.name == f.Name {
				want = fi.typ
			}
		}
		c.checkValue(s, f.Value, want)
	}
	return t
}

func (c *checker) newType(s *inferScope, n *ast.NewExpr) *Type {
	t := c.buildType(n.Type, nil)
	name := ""
	if t != nil {
		name = t.Name()
	}
	if sig, ok := c.env.inits[name]; ok {
		c.checkCallArgs(s, sig, n.Args)
		if sig.result != nil {
			return sig.result
		}
		return t
	}
	for _, a := range n.Args {
		c.checkArg(s, a.Expr, c.in.scalar("i32"), name, 1)
	}
	return t
}

func (c *checker) unaryType(s *inferScope, u *ast.UnaryExpr) *Type {
	t := c.inferExpr(s, u.X)
	switch u.Op {
	case token.Minus:
		if t != nil && !t.IsNumeric() {
			c.typeMismatch(u.Pos(), t, nil)
		}
	case token.Bang:
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
	rt := c.exprWithWant(s, b.R, lt)
	switch b.Op {
	case token.Shl, token.Shr, token.ShlWrap:
		if lt != nil && !lt.IsInteger() {
			c.typeMismatch(b.Pos(), lt, nil)
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

// ifType and matchType check conditions and bodies. The value of a block (its
// last expression) is not typed yet; expression values are added with the
// block-value pass.
func (c *checker) ifType(s *inferScope, ifx *ast.IfExpr) *Type {
	c.checkValue(s, ifx.Cond, c.in.scalar("bool"))
	c.inferBlock(s, ifx.Then)
	if ifx.Else != nil {
		c.inferStmt(s, ifx.Else)
	}
	return nil
}

func (c *checker) matchType(s *inferScope, m *ast.MatchExpr) *Type {
	c.inferExpr(s, m.Subject)
	for _, arm := range m.Arms {
		inner := newInferScope(s)
		c.inferExpr(inner, arm.Guard)
		c.inferStmt(inner, arm.Body)
	}
	return nil
}

func elemOf(t *Type) *Type {
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
		return t
	}
	return nil
}

func sliceOf(t *Type) *Type {
	if t == nil {
		return nil
	}
	switch t.Kind() {
	case ArrayKind, ViewKind:
		return t
	case NamedKind:
		if t.Name() == "Array" {
			return t
		}
	}
	if t.Kind() == ScalarKind && t.Name() == "str" {
		return t
	}
	return nil
}

func argAt(t *Type, i int) *Type {
	if t == nil || i >= len(t.Args()) {
		return nil
	}
	return t.Args()[i]
}
