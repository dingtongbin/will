package types

import (
	"strconv"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

// signature is a checked function or method signature.
type signature struct {
	name       string
	params     []*Type
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
	// userTypes holds the types declared in this file. A method on one of
	// them that no declaration provides is an error; built-in types have
	// methods the checker does not model, so they are not in this set.
	userTypes map[string]bool
}

func newEnv() *env {
	return &env{
		types:     map[string]*Type{},
		arity:     map[string]int{},
		params:    map[string][]string{},
		fields:    map[string][]*fieldInfo{},
		enums:     map[string]bool{},
		variants:  map[string]*variantInfo{},
		funcs:     map[string]*signature{},
		methods:   map[string]*signature{},
		inits:     map[string]*signature{},
		vars:      map[string]*Type{},
		ifaces:    map[string][]*methodInfo{},
		derives:   map[string]map[string]bool{},
		userTypes: map[string]bool{},
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
	e.ifaces["Show"] = []*methodInfo{{name: "Show", sig: &signature{name: "Show", result: c.in.scalar("str")}}}
	e.ifaces["Error"] = []*methodInfo{
		{name: "Message", sig: &signature{name: "Message", result: c.in.scalar("str")}},
		{name: "Status", sig: &signature{name: "Status", result: c.in.scalar("i32")}},
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
		for _, m := range decl.Methods {
			sig := &signature{name: m.Name}
			for _, pm := range m.Params {
				sig.params = append(sig.params, c.buildType(pm.Type, tp))
			}
			sig.result = c.buildType(m.Result, tp)
			e.ifaces[decl.Name] = append(e.ifaces[decl.Name], &methodInfo{name: m.Name, sig: sig})
		}
	case *ast.FuncDecl:
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
	switch t.Kind() {
	case ScalarKind:
		name := t.Name()
		return name != "raw" && name != "f32" && name != "f64"
	case NamedKind:
		switch t.Name() {
		case "raw", "rc", "weak", "chan", "Task", "f32", "f64":
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
		if pm.Variadic {
			sig.variadic = true
			sig.params = append(sig.params, c.in.view(c.buildType(pm.Type, tp)))
			_ = i
			continue
		}
		sig.params = append(sig.params, c.buildType(pm.Type, tp))
	}
	sig.result = c.buildType(decl.Result, tp)
	return sig
}

// buildType turns an AST type into a checked type. It reports T0004 when a
// generic type gets the wrong number of type arguments.
func (c *checker) buildType(t *ast.Type, tp map[string]bool) *Type {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case ast.ScalarType:
		if tp[t.Name] {
			return c.in.named(t.Name, nil)
		}
		return c.in.builtin(t.Name)
	case ast.NamedType:
		if tp[t.Name] {
			return c.in.named(t.Name, nil)
		}
		if t.Name == "Equal" || t.Name == "Ord" {
			// Both are predeclared constraints: they constrain a type
			// parameter and are not interfaces, so this use is an error.
			c.bag.Error(t.Pos(), diag.CodeBuiltinConstraintAsInterface,
				t.Name+" is a builtin constraint, not an interface",
				"use it only as a type-parameter constraint")
			return c.in.named(t.Name, nil)
		}
		args := make([]*Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = c.buildType(a, tp)
		}
		if t.Name == "Set" && len(args) == 1 && !c.keyType(args[0]) {
			c.bag.Error(t.Pos(), diag.CodeTypeMismatch, "Set element type does not support Equal",
				"use a type with a stable equality, such as an integer, str, struct, or enum")
		}
		if t.Name == "Map" && len(args) == 2 && !c.keyType(args[0]) {
			c.bag.Error(t.Pos(), diag.CodeTypeMismatch, "Map key type does not support Equal",
				"use a type with a stable equality, such as an integer, str, struct, or enum")
		}
		if want, ok := c.env.arity[t.Name]; ok {
			if len(args) != want {
				c.bag.Error(t.Pos(), diag.CodeTypeArgCount, "wrong number of type arguments",
					"the declaration takes "+strconv.Itoa(want))
			}
			return c.in.named(t.Name, args)
		}
		if bt := c.in.builtin(t.Name); bt != nil {
			if len(args) != 0 {
				c.bag.Error(t.Pos(), diag.CodeTypeArgCount, "wrong number of type arguments", "the declaration takes 0")
			}
			return bt
		}
		if u, ok := c.env.types[t.Name]; ok {
			return u
		}
		// A package-qualified or unknown name stays opaque; resolve already
		// reported an undefined name when it could.
		if len(t.Name) > 0 {
			return c.in.named(t.Name, args)
		}
	case ast.ArrayType:
		return c.in.array(t.Len, c.buildType(t.Elem, tp))
	case ast.ViewType:
		return c.in.view(c.buildType(t.Elem, tp))
	case ast.FuncType:
		params := make([]*Type, len(t.Params))
		for i, p := range t.Params {
			params[i] = c.buildType(p, tp)
		}
		return c.in.funcT(params, c.buildType(t.Result, tp))
	case ast.UnitType:
		return c.in.unit()
	}
	return nil
}
