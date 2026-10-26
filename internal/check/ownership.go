package check

import (
	"strings"

	"github.com/will-lang/will/internal/ast"
)

// Ownership answers whether a declared type has a value the compiler must
// release. The checker's move analysis needs the answer for structs, enums,
// arrays, and generic instances, not just for the predeclared containers that
// the syntax alone can name.
type Ownership struct {
	structs    map[string][]*ast.StructDecl
	enums      map[string][]*ast.EnumDecl
	interfaces map[string]bool
	// typeParams holds every declared type-parameter name. A bare T has no
	// concrete ownership until instantiation, so the transfer check defers it
	// rather than rejecting every generic constructor that stores its argument.
	typeParams map[string]bool
}

// NewOwnership collects the struct and enum declarations of every loaded file.
// A short name declared by more than one package is ambiguous; the conservative
// answer for it is "owns", so the move analysis never misses a release.
func NewOwnership(files []*ast.File) *Ownership {
	o := &Ownership{
		structs:    map[string][]*ast.StructDecl{},
		enums:      map[string][]*ast.EnumDecl{},
		interfaces: map[string]bool{},
		typeParams: map[string]bool{},
	}
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, d := range f.Decls {
			switch decl := d.(type) {
			case *ast.StructDecl:
				o.structs[decl.Name] = append(o.structs[decl.Name], decl)
				for _, p := range decl.TypeParams {
					o.typeParams[p.Name] = true
				}
			case *ast.EnumDecl:
				o.enums[decl.Name] = append(o.enums[decl.Name], decl)
				for _, p := range decl.TypeParams {
					o.typeParams[p.Name] = true
				}
			case *ast.InterfaceDecl:
				o.interfaces[decl.Name] = true
			case *ast.FuncDecl:
				for _, p := range decl.TypeParams {
					o.typeParams[p.Name] = true
				}
			}
		}
	}
	return o
}

// IsTypeParam reports whether t is a bare type parameter rather than a
// concrete named type. An actual struct, enum, or interface with the same name
// wins, so a real type is never mistaken for a parameter.
func (o *Ownership) IsTypeParam(t *ast.Type) bool {
	if o == nil || t == nil || t.Kind != ast.NamedType || len(t.Args) != 0 {
		return false
	}
	name := shortTypeName(t.Name)
	if !o.typeParams[name] {
		return false
	}
	return len(o.structs[name]) == 0 && len(o.enums[name]) == 0 && !o.interfaces[name]
}

// Owns reports whether one type carries a value that a scope must release.
func (o *Ownership) Owns(t *ast.Type) bool {
	if o == nil {
		return ast.OwnsHeap(t)
	}
	return o.owns(t, nil, map[string]bool{})
}

// HasView reports whether a value of this type contains a read-only view. An
// indexed owned element is a value transfer; an indexed view keeps its owner
// borrowed.
func (o *Ownership) HasView(t *ast.Type) bool {
	if o == nil || t == nil {
		return false
	}
	return o.hasView(t, nil, map[string]bool{})
}

func (o *Ownership) hasView(t *ast.Type, bindings map[string]*ast.Type, active map[string]bool) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ast.ViewType:
		return true
	case ast.ArrayType:
		return o.hasView(t.Elem, bindings, active)
	case ast.FuncType, ast.UnitType:
		return false
	case ast.NamedType:
		if bound := bindings[t.Name]; bound != nil && len(t.Args) == 0 {
			return o.hasView(bound, bindings, active)
		}
		if t.Name == "Array" || t.Name == "Option" || t.Name == "Result" {
			for _, arg := range t.Args {
				if o.hasView(arg, bindings, active) {
					return true
				}
			}
			return false
		}
		short := shortTypeName(t.Name)
		if active[short] {
			return false
		}
		if structs := o.structs[short]; len(structs) == 1 {
			active[short] = true
			defer delete(active, short)
			local := bindings
			if params := structs[0].TypeParams; len(params) > 0 && len(t.Args) == len(params) {
				local = make(map[string]*ast.Type, len(bindings)+len(params))
				for k, v := range bindings {
					local[k] = v
				}
				for i, p := range params {
					local[p.Name] = t.Args[i]
				}
			}
			for _, f := range structs[0].Fields {
				if o.hasView(f.Type, local, active) {
					return true
				}
			}
		}
		if enums := o.enums[short]; len(enums) == 1 {
			active[short] = true
			defer delete(active, short)
			for _, v := range enums[0].Variants {
				for _, arg := range v.Args {
					if o.hasView(arg, bindings, active) {
						return true
					}
				}
			}
		}
	}
	return false
}

// FieldType returns the declared type of one field after substituting the type
// arguments of a generic instance. A field of an rc<T> receiver is reached
// through the handle, so the receiver is unwrapped first. An unknown field or
// receiver returns nil; the caller must not guess at ownership in that case.
func (o *Ownership) FieldType(recv *ast.Type, name string) *ast.Type {
	if o == nil || recv == nil {
		return nil
	}
	recv = o.unwrapRc(recv)
	if recv.Kind != ast.NamedType {
		return nil
	}
	short := shortTypeName(recv.Name)
	structs := o.structs[short]
	if len(structs) != 1 {
		return nil
	}
	decl := structs[0]
	bindings := typeBindings(decl.TypeParams, recv.Args)
	for _, f := range decl.Fields {
		if f.Name == name {
			return substituteType(f.Type, bindings)
		}
	}
	return nil
}

// IndexType returns the element type of one index expression. Only the types
// that support indexing in the language answer: []T, [N]T, Array<T>, and str.
// Map and Set are not indexable and return nil.
func (o *Ownership) IndexType(recv *ast.Type) *ast.Type {
	if recv == nil {
		return nil
	}
	switch recv.Kind {
	case ast.ArrayType, ast.ViewType:
		return recv.Elem
	case ast.ScalarType:
		if recv.Name == "str" {
			return &ast.Type{Kind: ast.ScalarType, Name: "u32"}
		}
	case ast.NamedType:
		switch recv.Name {
		case "Array":
			if len(recv.Args) == 1 {
				return recv.Args[0]
			}
		case "str":
			return &ast.Type{Kind: ast.ScalarType, Name: "u32"}
		}
	}
	return nil
}

// ElementType and ValueType expose the payload of the predeclared containers
// that hold one value. They are used where a field or index is not enough, such
// as a Map value in a Get result.
func (o *Ownership) ElementType(t *ast.Type) *ast.Type {
	if t == nil || t.Kind != ast.NamedType {
		return nil
	}
	switch t.Name {
	case "Array", "Set", "Option":
		if len(t.Args) == 1 {
			return t.Args[0]
		}
	case "Map":
		if len(t.Args) == 2 {
			return t.Args[1]
		}
	}
	return nil
}

func (o *Ownership) unwrapRc(t *ast.Type) *ast.Type {
	for t != nil && t.Kind == ast.NamedType && t.Name == "rc" && len(t.Args) == 1 {
		t = t.Args[0]
	}
	return t
}

func shortTypeName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func typeBindings(params []*ast.TypeParam, args []*ast.Type) map[string]*ast.Type {
	if len(params) == 0 || len(params) != len(args) {
		return nil
	}
	out := make(map[string]*ast.Type, len(params))
	for i, p := range params {
		out[p.Name] = args[i]
	}
	return out
}

// substituteType replaces bare type parameters in one type. Unsubstituted
// parameters stay in place so a caller never mistakes an unknown type for a
// concrete one.
func substituteType(t *ast.Type, bindings map[string]*ast.Type) *ast.Type {
	if t == nil {
		return nil
	}
	if bound := bindings[t.Name]; bound != nil && len(t.Args) == 0 && t.Kind == ast.NamedType {
		return bound
	}
	out := *t
	if len(t.Args) != 0 {
		out.Args = make([]*ast.Type, len(t.Args))
		for i, a := range t.Args {
			out.Args[i] = substituteType(a, bindings)
		}
	}
	if t.Elem != nil {
		out.Elem = substituteType(t.Elem, bindings)
	}
	if len(t.Params) != 0 {
		out.Params = make([]*ast.Type, len(t.Params))
		for i, p := range t.Params {
			out.Params[i] = substituteType(p, bindings)
		}
	}
	if t.Result != nil {
		out.Result = substituteType(t.Result, bindings)
	}
	return &out
}

// owns substitutes the type arguments of a generic instance and walks the
// fields of user structs and enums. An unresolved named type is treated as
// owning: a false negative would let a moved value be released twice, while a
// false positive only asks for a Clone the program may not need.
func (o *Ownership) owns(t *ast.Type, bindings map[string]*ast.Type, active map[string]bool) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ast.ScalarType:
		return t.Name == "str"
	case ast.ViewType:
		return false
	case ast.ArrayType:
		return o.owns(t.Elem, bindings, active)
	case ast.FuncType, ast.UnitType:
		return false
	case ast.NamedType:
		return o.ownsNamed(t, bindings, active)
	}
	return true
}

func (o *Ownership) ownsNamed(t *ast.Type, bindings map[string]*ast.Type, active map[string]bool) bool {
	name := t.Name
	if bound := bindings[name]; bound != nil && len(t.Args) == 0 {
		if bound.Name == name && len(bound.Args) == 0 {
			// An unsubstituted type parameter is an unknown concrete type.
			// Treat it as owning so a move is not missed.
			return true
		}
		return o.owns(bound, bindings, active)
	}
	switch name {
	case "str", "Array", "Map", "Set":
		return true
	case "rc", "weak", "chan", "Task", "Duration", "Value", "Show", "Error", "TaskError":
		return false
	case "Option", "Result":
		for _, arg := range t.Args {
			if o.owns(arg, bindings, active) {
				return true
			}
		}
		return false
	}
	short := name
	if i := strings.LastIndexByte(short, '.'); i >= 0 {
		short = short[i+1:]
	}
	if active[short] {
		return false
	}
	if structs := o.structs[short]; len(structs) == 1 {
		active[short] = true
		defer delete(active, short)
		local := bindings
		if params := structs[0].TypeParams; len(params) > 0 && len(t.Args) == len(params) {
			local = make(map[string]*ast.Type, len(bindings)+len(params))
			for k, v := range bindings {
				local[k] = v
			}
			for i, p := range params {
				local[p.Name] = t.Args[i]
			}
		}
		for _, f := range structs[0].Fields {
			if o.owns(f.Type, local, active) {
				return true
			}
		}
		return false
	}
	if o.interfaces[short] {
		// A boxed interface owns the concrete value it points at.
		return true
	}
	if enums := o.enums[short]; len(enums) == 1 {
		active[short] = true
		defer delete(active, short)
		local := bindings
		if params := enums[0].TypeParams; len(params) > 0 && len(t.Args) == len(params) {
			local = make(map[string]*ast.Type, len(bindings)+len(params))
			for k, v := range bindings {
				local[k] = v
			}
			for i, p := range params {
				local[p.Name] = t.Args[i]
			}
		}
		for _, v := range enums[0].Variants {
			for _, arg := range v.Args {
				if o.owns(arg, local, active) {
					return true
				}
			}
		}
		return false
	}
	// An interface value borrows the concrete value; any other unknown name
	// is treated as owning so a miss cannot become a double release.
	if len(t.Args) == 0 && strings.HasPrefix(name, "interface ") {
		return false
	}
	return true
}
