package types

import (
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/token"
)

// rejectRawAddress reports a raw(x) address that is used as an ordinary will
// value. The result is only valid directly as a C argument or as the operand
// of usize(...); raw(n: usize) is the integer form and is not rejected.
func (c *checker) rejectRawAddress(e ast.Expr) {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return
	}
	name, ok := call.Callee.(*ast.NameExpr)
	if !ok || name.Name != "raw" || len(call.Args) != 1 {
		return
	}
	arg := call.Args[0].Expr
	if c.rawInteger(arg) {
		return
	}
	switch arg.(type) {
	case *ast.NameExpr, *ast.FieldExpr, *ast.IndexExpr, *ast.SliceExpr:
		c.bag.Error(call.Pos(), diag.CodeTypeMismatch,
			"raw(x) is only allowed as a C argument or inside usize(...)",
			"take the address at the call site, or convert it with usize(raw(x))")
	}
}

// rawInteger reports whether the argument is the raw(n: usize) form rather
// than a place whose address is taken.
func (c *checker) rawInteger(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.LiteralExpr:
		if x.Kind != token.Int {
			return false
		}
		return x.Text == "0" || strings.HasSuffix(x.Text, "usize")
	case *ast.CallExpr:
		name, ok := x.Callee.(*ast.NameExpr)
		return ok && name.Name == "usize"
	case *ast.NameExpr:
		if c.res == nil {
			return false
		}
		sym, ok := c.res.Refs[x]
		if !ok || sym == nil || sym.Node == nil {
			return false
		}
		switch n := sym.Node.(type) {
		case *ast.VarDecl:
			return ast.TypeText(n.Type) == "usize"
		case *ast.ConstDecl:
			return ast.TypeText(n.Type) == "usize"
		case *ast.Param:
			return ast.TypeText(n.Type) == "usize"
		}
	}
	return false
}
