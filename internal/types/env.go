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

// env is the declaration environment of one file.
type env struct {
	types    map[string]*Type
	arity    map[string]int
	fields   map[string][]*fieldInfo
	variants map[string]*variantInfo
	funcs    map[string]*signature
	methods  map[string]*signature
	inits    map[string]*signature
	vars     map[string]*Type
}

func newEnv() *env {
	return &env{
		types:    map[string]*Type{},
		arity:    map[string]int{},
		fields:   map[string][]*fieldInfo{},
		variants: map[string]*variantInfo{},
		funcs:    map[string]*signature{},
		methods:  map[string]*signature{},
		inits:    map[string]*signature{},
		vars:     map[string]*Type{},
	}
}

func buildEnv(c *checker, f *ast.File) *env {
	e := newEnv()
	for name, n := range builtinArity {
		e.arity[name] = n
	}
	for _, name := range scalarNames {
		e.types[name] = c.in.scalar(name)
	}
	for _, name := range []string{"Value", "Duration", "Show", "Error"} {
		e.types[name] = c.in.named(name, nil)
	}
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.StructDecl:
			tp := typeParamSet(decl.TypeParams)
			t := c.in.named(decl.Name, nil)
			e.types[decl.Name] = t
			e.arity[decl.Name] = len(decl.TypeParams)
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
			for _, v := range decl.Variants {
				payload := make([]*Type, len(v.Args))
				for i, at := range v.Args {
					payload[i] = c.buildType(at, tp)
				}
				e.variants[v.Name] = &variantInfo{enum: t, payload: payload}
			}
		case *ast.InterfaceDecl:
			e.types[decl.Name] = c.in.named(decl.Name, nil)
			e.arity[decl.Name] = len(decl.TypeParams)
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
		}
	}
	return e
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

func (c *checker) buildSig(decl *ast.FuncDecl) *signature {
	tp := typeParamSet(decl.TypeParams)
	sig := &signature{name: decl.Name, typeParams: len(decl.TypeParams)}
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
		args := make([]*Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = c.buildType(a, tp)
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
