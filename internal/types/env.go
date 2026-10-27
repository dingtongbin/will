package types

import (
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

// signature is a checked function or method signature.
type signature struct {
	name       string
	params     []*Type
	paramNames []string
	hasDefault []bool
	variadic   bool
	result     *Type
	typeParams int
	typeNames  []string
	recvParams []string
	recvMut    bool
	async      bool
}

// fieldInfo is one struct field.
type fieldInfo struct {
	name string
	typ  *Type
}

// variantInfo is one enum variant.
type variantInfo struct {
	enum    *Type
	payload []*Type
}

// methodInfo is one interface method.
type methodInfo struct {
	name string
	sig  *signature
}

// env is the declaration environment of one file.
type env struct {
	types    map[string]*Type
	arity    map[string]int
	params   map[string][]string
	fields   map[string][]*fieldInfo
	enums    map[string]bool
	variants map[string]*variantInfo
	funcs    map[string]*signature
	methods  map[string]*signature
	inits    map[string]*signature
	vars     map[string]*Type
	ifaces   map[string][]*methodInfo
	derives  map[string]map[string]bool
	// aliases holds source-level type aliases. The target is expanded on use,
	// so an alias never becomes a distinct type identity.
	aliases    map[string]*ast.TypeAliasDecl
	aliasOrder []*ast.TypeAliasDecl
	// userTypes holds the types declared in this file. A method on one of
	// them that no declaration provides is an error; built-in types have
	// methods the checker does not model, so they are not in this set.
	userTypes  map[string]bool
	typeParams map[string]bool
}

func newEnv() *env {
	return &env{
		types:      map[string]*Type{},
		arity:      map[string]int{},
		params:     map[string][]string{},
		fields:     map[string][]*fieldInfo{},
		enums:      map[string]bool{},
		variants:   map[string]*variantInfo{},
		funcs:      map[string]*signature{},
		methods:    map[string]*signature{},
		inits:      map[string]*signature{},
		vars:       map[string]*Type{},
		ifaces:     map[string][]*methodInfo{},
		derives:    map[string]map[string]bool{},
		aliases:    map[string]*ast.TypeAliasDecl{},
		userTypes:  map[string]bool{},
		typeParams: map[string]bool{},
	}
}

// buildEnvAll builds the declaration environment of one package from every
// file of it, so one package is checked as a unit.
func buildEnvAll(c *checker, files []*ast.File) *env {
	e := newEnv()
	// buildType reads the environment, so publish it before the walk.
	c.env = e
	for name, n := range builtinArity {
		e.arity[name] = n
	}
	for _, name := range scalarNames {
		e.types[name] = c.in.scalar(name)
	}
	for _, name := range []string{"Value", "Duration", "Show", "Error", "TaskError"} {
		e.types[name] = c.in.named(name, nil)
	}
	e.ifaces["Show"] = []*methodInfo{{name: "show", sig: &signature{name: "show", result: c.in.scalar("str")}}}
	e.ifaces["Error"] = []*methodInfo{
		{name: "message", sig: &signature{name: "message", result: c.in.scalar("str")}},
		{name: "status", sig: &signature{name: "status", result: c.in.scalar("i32")}},
	}
	e.ifaces["TaskError"] = e.ifaces["Error"]
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, d := range f.Decls {
			b := &envBuilder{c: c, e: e}
			b.decl(d)
		}
	}
	c.checkAliases(e)
	return e
}

// envBuilder adds one declaration to the environment. It exists so the
// declaration switch reads the same for every file of the package.
type envBuilder struct {
	c *checker
	e *env
}

func (b *envBuilder) decl(d ast.Decl) {
	c, e := b.c, b.e
	switch decl := d.(type) {
	case *ast.LaneDecl:
		if decl.Decl != nil {
			b.decl(decl.Decl)
		}
	case *ast.StructDecl:
		tp := typeParamSet(decl.TypeParams)
		t := c.in.named(decl.Name, nil)
		e.types[decl.Name] = t
		e.arity[decl.Name] = len(decl.TypeParams)
		e.params[decl.Name] = typeParamNames(decl.TypeParams)
		e.userTypes[decl.Name] = true
		for _, p := range decl.TypeParams {
			e.typeParams[p.Name] = true
		}
		var fields []*fieldInfo
		for _, fd := range decl.Fields {
			fields = append(fields, &fieldInfo{name: fd.Name, typ: c.buildType(fd.Type, tp)})
		}
		e.fields[decl.Name] = fields
	case *ast.EnumDecl:
		tp := typeParamSet(decl.TypeParams)
		t := c.in.named(decl.Name, nil)
		e.types[decl.Name] = t
		e.arity[decl.Name] = len(decl.TypeParams)
		e.params[decl.Name] = typeParamNames(decl.TypeParams)
		e.userTypes[decl.Name] = true
		for _, p := range decl.TypeParams {
			e.typeParams[p.Name] = true
		}
		e.enums[decl.Name] = true
		for _, v := range decl.Variants {
			payload := make([]*Type, len(v.Args))
			for i, at := range v.Args {
				payload[i] = c.buildType(at, tp)
			}
			e.variants[v.Name] = &variantInfo{enum: t, payload: payload}
		}
	case *ast.InterfaceDecl:
		tp := typeParamSet(decl.TypeParams)
		e.types[decl.Name] = c.in.named(decl.Name, nil)
		e.arity[decl.Name] = len(decl.TypeParams)
		e.params[decl.Name] = typeParamNames(decl.TypeParams)
		for _, p := range decl.TypeParams {
			e.typeParams[p.Name] = true
		}
		for _, m := range decl.Methods {
			sig := &signature{name: m.Name, async: m.Async}
			for _, pm := range m.Params {
				sig.paramNames = append(sig.paramNames, pm.Name)
				sig.hasDefault = append(sig.hasDefault, pm.Default != nil)
				sig.params = append(sig.params, c.buildType(pm.Type, tp))
			}
			sig.result = c.buildType(m.Result, tp)
			e.ifaces[decl.Name] = append(e.ifaces[decl.Name], &methodInfo{name: m.Name, sig: sig})
		}
	case *ast.FuncDecl:
		for _, p := range decl.TypeParams {
			e.typeParams[p.Name] = true
		}
		sig := c.buildSig(decl)
		switch {
		case decl.Recv == nil:
			e.funcs[decl.Name] = sig
		case decl.Name == "init":
			e.inits[receiverName(decl.Recv.Type)] = sig
		default:
			e.methods[receiverName(decl.Recv.Type)+"."+decl.Name] = sig
		}
	case *ast.ConstDecl:
		e.vars[decl.Name] = c.buildType(decl.Type, nil)
	case *ast.VarDecl:
		e.vars[decl.Name] = c.buildType(decl.Type, nil)
	case *ast.TypeAliasDecl:
		e.aliases[decl.Name] = decl
		e.aliasOrder = append(e.aliasOrder, decl)
	case *ast.DeriveDecl:
		if decl.Name == "Show" {
			name := receiverName(decl.Type)
			if e.derives[name] == nil {
				e.derives[name] = map[string]bool{}
			}
			e.derives[name]["Show"] = true
		}
	}
}

func typeParamNames(params []*ast.TypeParam) []string {
	if len(params) == 0 {
		return nil
	}
	names := make([]string, 0, len(params))
	for _, p := range params {
		names = append(names, p.Name)
	}
	return names
}

func typeParamSet(params []*ast.TypeParam) map[string]bool {
	if len(params) == 0 {
		return nil
	}
	set := make(map[string]bool, len(params))
	for _, p := range params {
		set[p.Name] = true
	}
	return set
}

// keyType reports whether a type can be a Map key or Set element. The language
// gives Equal only to values with stable equality, so handles, raw addresses,
// floats, and structs holding one of them are rejected here.
func (c *checker) keyType(t *Type) bool {
	if t == nil {
		return true
	}
	if t.Borrow() != ast.OwnedBorrow || t.Kind() == ViewKind {
		return false
	}
	switch t.Kind() {
	case ScalarKind:
		name := t.Name()
		return name != "raw" && name != "f32" && name != "f64"
	case NamedKind:
		switch t.Name() {
		case "raw", "rc", "arc", "weak_rc", "weak_arc", "chan", "Task", "f32", "f64":
			return false
		}
		if fields, ok := c.env.fields[t.Name()]; ok {
			for _, field := range fields {
				if !c.keyType(field.typ) {
					return false
				}
			}
		}
		return true
	case ArrayKind, ViewKind:
		return c.keyType(t.Elem())
	case FuncKind:
		return false
	}
	return true
}

func (c *checker) buildSig(decl *ast.FuncDecl) *signature {
	tp := typeParamSet(decl.TypeParams)
	sig := &signature{name: decl.Name, typeParams: len(decl.TypeParams), async: decl.Async}
	for _, p := range decl.TypeParams {
		sig.typeNames = append(sig.typeNames, p.Name)
	}
	if decl.Recv != nil {
		sig.recvMut = decl.Recv.Mut
		for _, a := range decl.Recv.Type.Args {
			sig.recvParams = append(sig.recvParams, a.Name)
		}
	}
	for i, pm := range decl.Params {
		_ = i
		sig.paramNames = append(sig.paramNames, pm.Name)
		sig.hasDefault = append(sig.hasDefault, pm.Default != nil)
		if pm.Variadic {
			sig.variadic = true
			sig.params = append(sig.params, c.in.view(c.buildType(pm.Type, tp)))
			continue
		}
		sig.params = append(sig.params, c.buildType(pm.Type, tp))
	}
	sig.result = c.buildType(decl.Result, tp)
	return sig
}

// buildForeignSig builds a signature whose bare named types belong to the
// imported package. The local declaration env names those types without a
// qualifier, so a caller would otherwise compare []Row against []csv.Row.
func (c *checker) buildForeignSig(decl *ast.FuncDecl, pkg string) *signature {
	sig := c.buildSig(decl)
	params := make(map[string]bool, len(sig.typeNames))
	for _, name := range sig.typeNames {
		params[name] = true
	}
	if decl.Recv != nil {
		for _, a := range decl.Recv.Type.Args {
			if a.Kind == ast.NamedType && len(a.Args) == 0 {
				params[a.Name] = true
			}
		}
	}
	for i, p := range sig.params {
		sig.params[i] = c.qualifyForeignType(p, pkg, params)
	}
	sig.result = c.qualifyForeignType(sig.result, pkg, params)
	return sig
}

// qualifyForeignType adds the imported package name to every bare user type
// inside one signature type. Predeclared names, type parameters, and already
// qualified names stay as they are.
func (c *checker) qualifyForeignType(t *Type, pkg string, params map[string]bool) *Type {
	if t == nil {
		return nil
	}
	if t.borrow != ast.OwnedBorrow {
		base := *t
		base.borrow = ast.OwnedBorrow
		base.text = strings.TrimPrefix(strings.TrimPrefix(t.text, "ref "), "mut ")
		return c.in.borrowed(c.qualifyForeignType(&base, pkg, params), t.borrow)
	}
	args := func() []*Type {
		if len(t.Args()) == 0 {
			return nil
		}
		out := make([]*Type, len(t.Args()))
		for i, a := range t.Args() {
			out[i] = c.qualifyForeignType(a, pkg, params)
		}
		return out
	}
	switch t.Kind() {
	case NamedKind:
		name := t.Name()
		a := args()
		if params[name] || strings.Contains(name, ".") || isScalarName(name) ||
			builtinArity[name] > 0 || name == "Value" || name == "Duration" ||
			name == "Show" || name == "Error" || name == "TaskError" ||
			name == "interface {}" {
			if len(a) == 0 {
				return t
			}
			return c.in.named(name, a)
		}
		return c.in.named(pkg+"."+name, a)
	case ViewKind:
		return c.in.view(c.qualifyForeignType(t.Elem(), pkg, params), t.viewMut)
	case ArrayKind:
		return c.in.array(t.length, c.qualifyForeignType(t.Elem(), pkg, params))
	case FuncKind:
		ps := make([]*Type, len(t.Params()))
		for i, p := range t.Params() {
			ps[i] = c.qualifyForeignType(p, pkg, params)
		}
		return c.in.funcT(ps, c.qualifyForeignType(t.Result(), pkg, params))
	}
	return t
}

// buildType turns an AST type into a checked type. It reports T0004 when a
// generic type gets the wrong number of type arguments.
func (c *checker) buildType(t *ast.Type, tp map[string]bool) *Type {
	if t == nil {
		return nil
	}
	var out *Type
	switch t.Kind {
	case ast.ScalarType:
		if tp[t.Name] {
			out = c.in.named(t.Name, nil)
		} else {
			out = c.in.builtin(t.Name)
		}
	case ast.NamedType:
		if tp[t.Name] {
			out = c.in.named(t.Name, nil)
			break
		}
		switch t.Name {
		case "int":
			out = c.in.scalar("i64")
			break
		case "float":
			out = c.in.scalar("f64")
			break
		case "interface {}":
			out = c.in.named(t.Name, nil)
			break
		}
		if out != nil {
			break
		}
		if alias, ok := c.env.aliases[t.Name]; ok {
			if len(t.Args) != 0 {
				c.bag.Error(t.Pos(), diag.CodeTypeArgCount, "wrong number of type arguments",
					"the declaration takes 0")
			}
			out = c.resolveAlias(alias)
			break
		}
		if t.Name == "equal" || t.Name == "Ord" {
			// Both are predeclared constraints: they constrain a type
			// parameter and are not interfaces, so this use is an error.
			c.bag.Error(t.Pos(), diag.CodeBuiltinConstraintAsInterface,
				t.Name+" is a builtin constraint, not an interface",
				"use it only as a type-parameter constraint")
			out = c.in.named(t.Name, nil)
			break
		}
		args := make([]*Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = c.buildType(a, tp)
		}
		if t.Name == "Set" && len(args) == 1 && !c.keyType(args[0]) {
			c.bag.Error(t.Pos(), diag.CodeTypeMismatch, "Set element type does not support equal",
				"use a type with a stable equality, such as an integer, str, struct, or enum")
		}
		if t.Name == "Map" && len(args) == 2 && !c.keyType(args[0]) {
			c.bag.Error(t.Pos(), diag.CodeTypeMismatch, "Map key type does not support equal",
				"use a type with a stable equality, such as an integer, str, struct, or enum")
		}
		if want, ok := c.env.arity[t.Name]; ok {
			if len(args) != want {
				c.bag.Error(t.Pos(), diag.CodeTypeArgCount, "wrong number of type arguments",
					"the declaration takes "+strconv.Itoa(want))
			}
			out = c.in.named(t.Name, args)
			break
		}
		if bt := c.in.builtin(t.Name); bt != nil {
			if len(args) != 0 {
				c.bag.Error(t.Pos(), diag.CodeTypeArgCount, "wrong number of type arguments", "the declaration takes 0")
			}
			out = bt
			break
		}
		if u, ok := c.env.types[t.Name]; ok {
			out = u
			break
		}
		// A package-qualified or unknown name stays opaque; resolve already
		// reported an undefined name when it could.
		if len(t.Name) > 0 {
			out = c.in.named(t.Name, args)
		}
	case ast.ArrayType:
		out = c.in.array(t.Len, c.buildType(t.Elem, tp))
	case ast.ViewType:
		out = c.in.view(c.buildType(t.Elem, tp), t.ViewMut)
	case ast.FuncType:
		params := make([]*Type, len(t.Params))
		for i, p := range t.Params {
			params[i] = c.buildType(p, tp)
		}
		out = c.in.funcT(params, c.buildType(t.Result, tp))
	case ast.UnitType:
		out = c.in.unit()
	}
	if out == nil {
		return nil
	}
	return c.in.borrowed(out, t.Borrow)
}
