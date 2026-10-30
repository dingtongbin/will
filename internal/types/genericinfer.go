package types

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/resolve"
)

// inferCallTypeArgs fills the type parameters of a generic call from the
// argument types when the caller wrote no explicit type arguments. A parameter
// is bound once; a conflicting second binding leaves the signature unchanged so
// the ordinary argument check reports the mismatch.
func (c *checker) inferCallTypeArgs(s *inferScope, args []*ast.CallArg, typeArgs []*ast.Type, sig *signature) *signature {
	if sig == nil || sig.typeParams == 0 || len(typeArgs) != 0 {
		return sig
	}
	names := map[string]bool{}
	for _, name := range sig.typeNames {
		names[name] = true
	}
	subst := map[string]*Type{}
	for i, arg := range args {
		if i >= len(sig.params) {
			break
		}
		actual := c.inferExpr(s, arg.Expr)
		if actual == nil {
			// A bare function name in value position has no inferred type;
			// resolve it from its declaration so func(T) -> U unifies.
			if name, ok := arg.Expr.(*ast.NameExpr); ok {
				if sym := c.symbolOf(name); sym != nil && sym.Kind == resolve.FuncKind {
					if decl, ok := sym.Node.(*ast.FuncDecl); ok {
						fn := c.buildSig(decl)
						actual = c.in.funcT(fn.params, fn.result)
					}
				}
			}
		}
		if !unifyType(sig.params[i], actual, names, subst) {
			return sig
		}
	}
	if len(subst) != sig.typeParams {
		return sig
	}
	out := c.instantiate(sig, subst)
	out.typeParams = 0
	out.typeNames = nil
	return out
}

// unifyType binds the type parameters in want from one actual type. It returns
// false when a parameter already has a different binding.
func unifyType(want, actual *Type, names map[string]bool, subst map[string]*Type) bool {
	if want == nil || actual == nil {
		return true
	}
	if want.Kind() == NamedKind && names[want.Name()] {
		if prev, ok := subst[want.Name()]; ok {
			return Equal(prev, actual)
		}
		subst[want.Name()] = actual
		return true
	}
	if want.Kind() == ViewKind {
		if actual.Kind() == ViewKind {
			return unifyType(want.Elem(), actual.Elem(), names, subst)
		}
		if actual.Kind() == NamedKind && actual.Name() == "list" && len(actual.Args()) == 1 {
			return unifyType(want.Elem(), actual.Args()[0], names, subst)
		}
	}
	if want.Kind() == FuncKind && actual.Kind() == FuncKind {
		wp, ap := want.Params(), actual.Params()
		if len(wp) != len(ap) {
			return false
		}
		for i := range wp {
			if !unifyType(wp[i], ap[i], names, subst) {
				return false
			}
		}
		return unifyType(want.Result(), actual.Result(), names, subst)
	}
	if len(want.Args()) != 0 && len(actual.Args()) != 0 {
		if len(want.Args()) != len(actual.Args()) {
			return false
		}
		for i := range want.Args() {
			if !unifyType(want.Args()[i], actual.Args()[i], names, subst) {
				return false
			}
		}
	}
	return true
}
