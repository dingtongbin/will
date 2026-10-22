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
			c.checkValue(s, d.Value, want)
			s.vars[d.Name] = want
		case *ast.VarDecl:
			want := c.buildType(d.Type, s.tp)
			c.rejectRawAddress(d.Value)
			if d.Value == nil {
				if !c.hasZero(want) {
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
		want := c.inferPlace(s, x.Target)
		if x.Op != "=" && want != nil && !want.IsNumeric() {
			c.typeMismatch(x.Target.Pos(), want, nil)
		}
		c.checkValue(s, x.Value, want)
	case *ast.ExprStmt:
		c.rejectRawAddress(x.Expr)
		c.inferExpr(s, x.Expr)
	case *ast.DeferStmt:
		c.inferExpr(s, x.Call)
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
		c.inferBlock(s, x.Body)
	case *ast.SelectStmt:
		for _, arm := range x.Arms {
			inner := newInferScope(s)
			if t := c.inferExpr(s, arm.Chan); arm.Bind != "" {
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
	if !Equal(got, want) {
		c.mismatch(e.Pos(), got, want, false, "", 0)
	}
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
	if !Equal(got, want) {
		c.mismatch(e.Pos(), got, want, true, callee, n)
	}
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
	if methods, ok := c.env.ifaces[want.Name()]; ok {
		if got != nil && got.Kind() == NamedKind && strings.Contains(got.Name(), ".") {
			// The concrete type belongs to another package, whose declarations
			// this environment does not hold. The vtable built during lowering
			// checks that every method exists.
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
	if len(a.params) != len(b.params) || a.variadic != b.variadic {
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
	if len(args) != len(sig.params) {
		c.badArity(pos, name)
		return
	}
	for i, a := range args {
		c.checkArg(s, a.Expr, sig.params[i], name, i+1)
	}
}

func (c *checker) badArity(pos source.Pos, callee string) {
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
		return elemOf(c.in, c.inferExpr(s, x.Recv))
	case *ast.SliceExpr:
		recv := c.inferExpr(s, x.Recv)
		c.checkValue(s, x.Lo, c.in.scalar("usize"))
		c.checkValue(s, x.Hi, c.in.scalar("usize"))
		return c.sliceOf(recv)
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
	case *ast.UnitExpr:
		return c.in.unit()
	}
	return nil
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
				sig := c.buildSig(decl)
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
	case "Some":
		return c.in.named("Option", []*Type{c.oneArg(s, call)})
	case "None":
		return c.in.named("Option", nil)
	case "Ok":
		return c.in.named("Result", []*Type{c.oneArg(s, call), nil})
	case "Err":
		return c.in.named("Result", []*Type{nil, c.oneArg(s, call)})
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
	return c.in.named("rc", []*Type{elem})
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
	if t, ok := c.env.types[name]; ok {
		return t
	}
	if t := c.in.builtin(name); t != nil {
		return t
	}
	return c.in.named(name, nil)
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
	if sig == nil || len(sig.recvParams) == 0 {
		return nil
	}
	subst := c.substFor(recv.Name(), recv.Args())
	if subst == nil {
		return nil
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

func (c *checker) methodType(s *inferScope, call *ast.MethodCallExpr) *Type {
	if call.Link != nil {
		return c.cLinkType(s, call)
	}
	if recv, ok := call.Recv.(*ast.NameExpr); ok {
		if decl := c.foreignFuncs[recv.Name][call.Name]; decl != nil {
			sig := c.buildSig(decl)
			sig = c.inferCallTypeArgs(s, call.Args, call.TypeArgs, sig)
			if sig.typeParams > 0 && len(call.TypeArgs) == sig.typeParams && len(sig.typeNames) == sig.typeParams {
				subst := make(map[string]*Type, sig.typeParams)
				for i, ta := range call.TypeArgs {
					subst[sig.typeNames[i]] = c.buildType(ta, nil)
				}
				sig = c.instantiate(sig, subst)
			}
			c.checkCallArgs(s, sig, call.Args, call.Pos())
			return sig.result
		}
	}
	recv := c.inferExpr(s, call.Recv)
	for _, a := range call.Args {
		c.inferExpr(s, a.Expr)
	}
	if recv != nil && recv.Kind() == NamedKind && recv.Name() == "rc" && len(recv.Args()) == 1 {
		switch call.Name {
		case "Weak":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.named("weak", []*Type{recv.Args()[0]})
		case "Clone":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return recv
		}
		recv = recv.Args()[0]
	}
	if recv != nil && recv.Kind() == NamedKind && recv.Name() == "weak" && len(recv.Args()) == 1 {
		if call.Name == "Upgrade" {
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.named("Option", []*Type{c.in.named("rc", []*Type{recv.Args()[0]})})
		}
		return nil
	}
	switch call.Name {
	case "Recv":
		return elemOf(c.in, recv)
	case "Send":
		if recv != nil && recv.Kind() == NamedKind && recv.Name() == "chan" {
			var elem *Type
			if len(recv.Args()) == 1 {
				elem = recv.Args()[0]
			}
			c.checkPayload(s, []*Type{elem}, call.Args, call.Name, call.Pos())
			return c.in.named("Result", []*Type{c.in.unit(), c.in.named("Error", nil)})
		}
	case "Join":
		return nil
	case "Close":
		if recv != nil && recv.Kind() == NamedKind && recv.Name() == "chan" {
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.unit()
		}
	}
	if recv == nil {
		return nil
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
	if methods, ok := c.env.ifaces[recv.Name()]; ok {
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
		case "Len":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return usize, true
		case "Contains", "HasPrefix", "HasSuffix":
			c.checkPayload(s, []*Type{strT}, call.Args, call.Name, call.Pos())
			return boolean, true
		case "Index":
			c.checkPayload(s, []*Type{strT}, call.Args, call.Name, call.Pos())
			return c.in.named("Option", []*Type{usize}), true
		case "Trim", "TrimLeft", "TrimRight", "Upper", "Lower", "Clone":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return strT, true
		case "Repeat":
			c.checkPayload(s, []*Type{usize}, call.Args, call.Name, call.Pos())
			return strT, true
		case "Replace":
			c.checkPayload(s, []*Type{strT, strT}, call.Args, call.Name, call.Pos())
			return strT, true
		case "Split":
			c.checkPayload(s, []*Type{strT}, call.Args, call.Name, call.Pos())
			return c.in.view(strT), true
		case "SplitN":
			c.checkPayload(s, []*Type{strT, c.in.scalar("i32")}, call.Args, call.Name, call.Pos())
			return c.in.view(strT), true
		case "Fields":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.view(strT), true
		case "Chars":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.named("Array", []*Type{c.in.scalar("u32")}), true
		}
		return nil, false
	}
	if recv.Kind() == ViewKind {
		if call.Name == "Len" {
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return usize, true
		}
		return nil, false
	}
	if recv.Kind() == NamedKind && recv.Name() == "Array" {
		elem := argAt(recv, 0)
		switch call.Name {
		case "Len", "Cap":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return usize, true
		case "Get":
			c.checkPayload(s, []*Type{usize}, call.Args, call.Name, call.Pos())
			return elem, true
		case "Set":
			c.checkPayload(s, []*Type{usize, elem}, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "Push":
			c.checkPayload(s, []*Type{elem}, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "Append":
			c.checkPayload(s, []*Type{c.in.view(elem)}, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "Pop":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.named("Option", []*Type{elem}), true
		case "Insert":
			c.checkPayload(s, []*Type{usize, elem}, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "Remove":
			c.checkPayload(s, []*Type{usize}, call.Args, call.Name, call.Pos())
			return elem, true
		case "Clear", "ShrinkToFit":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "Slice":
			c.checkPayload(s, []*Type{usize, usize}, call.Args, call.Name, call.Pos())
			return c.in.view(elem), true
		case "Clone":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.named("Array", []*Type{elem}), true
		}
		return nil, false
	}
	if recv.Kind() != NamedKind {
		return nil, false
	}
	switch recv.Name() {
	case "Map":
		k, v := argAt(recv, 0), argAt(recv, 1)
		switch call.Name {
		case "Len":
			return usize, true
		case "Get":
			c.checkPayload(s, []*Type{k}, call.Args, call.Name, call.Pos())
			return c.in.named("Option", []*Type{v}), true
		case "Set":
			c.checkPayload(s, []*Type{k, v}, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "Has", "Delete":
			c.checkPayload(s, []*Type{k}, call.Args, call.Name, call.Pos())
			return boolean, true
		case "Clear":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
			return c.in.unit(), true
		case "Keys":
			return c.in.named("Array", []*Type{k}), true
		case "Values":
			return c.in.named("Array", []*Type{v}), true
		}
	case "Set":
		elem := argAt(recv, 0)
		switch call.Name {
		case "Len":
			return usize, true
		case "Add", "Has", "Delete":
			c.checkPayload(s, []*Type{elem}, call.Args, call.Name, call.Pos())
			return boolean, true
		case "Clear":
			c.checkPayload(s, nil, call.Args, call.Name, call.Pos())
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
	if recv.Name() == "rc" && len(recv.Args()) == 1 {
		recv = recv.Args()[0]
		if recv == nil || recv.Kind() != NamedKind {
			return nil
		}
	}
	fields, known := c.env.fields[recv.Name()]
	if !known {
		return nil
	}
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
	sig := c.buildSig(decl)
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
	if name == "rc" && t != nil && len(t.Args()) == 1 {
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
