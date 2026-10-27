package types

import (
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

// typeHasGuard reports whether a written type mentions the guard of std/sync,
// including through Option and other generic arguments.
func typeHasGuard(t *ast.Type) bool {
	if t == nil {
		return false
	}
	if t.Kind == ast.NamedType {
		name := t.Name
		if name == "Guard" || name == "ReadGuard" ||
			strings.HasSuffix(name, "sync.Guard") || strings.HasSuffix(name, "sync.ReadGuard") {
			return true
		}
	}
	for _, arg := range t.Args {
		if typeHasGuard(arg) {
			return true
		}
	}
	if typeHasGuard(t.Elem) || typeHasGuard(t.Result) {
		return true
	}
	for _, param := range t.Params {
		if typeHasGuard(param) {
			return true
		}
	}
	return false
}

// guardFactory reports whether a method is one of the two lock types that
// creates a guard. Only these declarations may name a guard in their result.
func guardFactory(recv, name string) bool {
	if recv != "Mutex" && recv != "RwLock" {
		return false
	}
	switch name {
	case "lock", "try_lock", "r_lock", "try_rlock", "w_lock", "try_wlock":
		return true
	}
	return false
}

// checkGuardDecls rejects a guard outside the one place that creates it. A
// guard is affine: it cannot be a parameter, a field, a global, or the result
// of a user function, because every one of those would copy it or let it
// outlive the scope that releases it.
func (c *checker) checkGuardDecls(files []*ast.File) {
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, d := range f.Decls {
			switch decl := d.(type) {
			case *ast.StructDecl:
				if decl.Name == "Guard" || decl.Name == "ReadGuard" {
					continue
				}
				for _, field := range decl.Fields {
					if typeHasGuard(field.Type) {
						c.bag.Error(field.Pos(), diag.CodeGuardCopied,
							"a guard cannot be stored in a field",
							"keep it in the local that lock returned")
					}
				}
			case *ast.FuncDecl:
				recv := ""
				if decl.Recv != nil && decl.Recv.Type != nil {
					recv = decl.Recv.Type.Name
				}
				if typeHasGuard(decl.Result) && !guardFactory(recv, decl.Name) {
					c.bag.Error(decl.Pos(), diag.CodeGuardCopied,
						"a guard cannot be returned from this function",
						"keep it in the local that lock returned")
				}
				for _, param := range decl.Params {
					if typeHasGuard(param.Type) {
						c.bag.Error(param.Pos(), diag.CodeGuardCopied,
							"a guard cannot be passed as a parameter",
							"keep it in the local that lock returned")
					}
				}
			case *ast.VarDecl:
				if typeHasGuard(decl.Type) {
					c.bag.Error(decl.Pos(), diag.CodeGuardCopied,
						"a guard cannot be a package variable",
						"keep it in the local that lock returned")
				}
			}
		}
	}
}
