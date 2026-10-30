package check

import "github.com/will-lang/will/internal/ast"

// rootName returns the local name at the root of a place expression.
func rootName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.NameExpr:
		return x.Name
	case *ast.FieldExpr:
		return rootName(x.Recv)
	case *ast.IndexExpr:
		return rootName(x.Recv)
	case *ast.SliceExpr:
		return rootName(x.Recv)
	}
	return ""
}
