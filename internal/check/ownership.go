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
}

// NewOwnership collects the struct and enum declarations of every loaded file.
// A short name declared by more than one package is ambiguous; the conservative
// answer for it is "owns", so the move analysis never misses a release.
func NewOwnership(files []*ast.File) *Ownership {
	o := &Ownership{
		structs:    map[string][]*ast.StructDecl{},
		enums:      map[string][]*ast.EnumDecl{},
		interfaces: map[string]bool{},
	}
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, d := range f.Decls {
			switch decl := d.(type) {
			case *ast.StructDecl:
				o.structs[decl.Name] = append(o.structs[decl.Name], decl)
			case *ast.EnumDecl:
				o.enums[decl.Name] = append(o.enums[decl.Name], decl)
			case *ast.InterfaceDecl:
				o.interfaces[decl.Name] = true
			}
		}
	}
	return o
}

// Owns reports whether one type carries a value that a scope must release.
func (o *Ownership) Owns(t *ast.Type) bool {
	if o == nil {
		return ast.OwnsHeap(t)
	}
	return o.owns(t, nil, map[string]bool{})
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
		return false
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
