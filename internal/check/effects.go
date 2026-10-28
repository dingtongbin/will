package check

import (
	"strings"

	"github.com/will-lang/will/internal/ast"
)

// MutEffects summarizes the declarations that can write through a parameter or
// receiver. The borrow pass uses it across files in one package because syntax
// in a caller file does not show the callee's parameter modes.
type MutEffects struct {
	// Methods is keyed by "<short type>.<method>".
	Methods map[string]bool
	// Funcs maps a function name to the mut flag of each parameter.
	Funcs map[string][]bool
}

// NewMutEffects collects the mut receiver and mut parameter modes of every
// loaded file. Short type names are enough for a package-level decision; a
// same-named method on a different type remains a distinct key.
func NewMutEffects(files []*ast.File) *MutEffects {
	e := &MutEffects{Methods: map[string]bool{}, Funcs: map[string][]bool{}}
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fn.Recv != nil && fn.Recv.Mut {
				e.Methods[methodEffectKey(fn.Recv.Type, fn.Name)] = true
			}
			modes := make([]bool, len(fn.Params))
			for i, p := range fn.Params {
				modes[i] = p.Mut
			}
			e.Funcs[fn.Name] = modes
		}
	}
	return e
}

// MethodMut reports whether the named method writes its receiver.
func (e *MutEffects) MethodMut(recv *ast.Type, name string) bool {
	if e == nil {
		return false
	}
	return e.Methods[methodEffectKey(recv, name)]
}

// FuncMut reports the mut flags of one free function.
func (e *MutEffects) FuncMut(name string) []bool {
	if e == nil {
		return nil
	}
	return e.Funcs[name]
}

func methodEffectKey(recv *ast.Type, name string) string {
	if recv == nil || recv.Kind != ast.NamedType || name == "" {
		return ""
	}
	short := recv.Name
	if i := strings.LastIndexByte(short, '.'); i >= 0 {
		short = short[i+1:]
	}
	return short + "." + name
}
