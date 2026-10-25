package types

import (
	"fmt"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

// collectLaneTypes records the types marked by a lane declaration. The map is
// per package; a lane-marked value cannot cross to an auto spawn in another
// lane without an explicit here/on placement.
func (c *checker) collectLaneTypes(files []*ast.File) {
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, d := range f.Decls {
			lane, ok := d.(*ast.LaneDecl)
			if !ok {
				continue
			}
			if name := laneTypeName(lane.Decl); name != "" {
				c.laneTypes[name] = lane.Name
			}
		}
	}
}

// laneTypeName returns the declared type name wrapped by lane, or an empty
// string when the declaration is not a type.
func laneTypeName(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.StructDecl:
		return d.Name
	case *ast.EnumDecl:
		return d.Name
	case *ast.InterfaceDecl:
		return d.Name
	}
	return ""
}

// laneOfType returns the lane named by a marked type, including a marked type
// nested in a generic argument such as rc<UiMarker>.
func (c *checker) laneOfType(t *Type) (string, bool) {
	if t == nil {
		return "", false
	}
	if lane, ok := c.laneTypes[t.Name()]; ok {
		return lane, true
	}
	for _, arg := range t.Args() {
		if lane, ok := c.laneOfType(arg); ok {
			return lane, true
		}
	}
	return "", false
}

// checkSpawnLane rejects an auto spawn that captures a lane-marked value. The
// caller must choose here or on, because auto deliberately does not promise the
// creator lane.
func (c *checker) checkSpawnLane(x *ast.SpawnExpr) {
	if x == nil || x.Place != "auto" {
		return
	}
	var args []*ast.CallArg
	switch call := x.Call.(type) {
	case *ast.CallExpr:
		args = call.Args
	case *ast.MethodCallExpr:
		args = call.Args
		if call.Recv != nil {
			args = append([]*ast.CallArg{{Expr: call.Recv}}, args...)
		}
	}
	for _, arg := range args {
		if arg == nil || arg.Expr == nil {
			continue
		}
		lane, ok := c.laneOfType(c.exprTypes[arg.Expr])
		if !ok {
			continue
		}
		typ := c.exprTypes[arg.Expr]
		c.bag.Error(arg.Expr.Pos(), diag.CodeWrongLane,
			fmt.Sprintf("%s belongs to lane %s", typ.Name(), lane),
			"use it only on that lane")
	}
}
