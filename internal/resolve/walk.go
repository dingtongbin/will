package resolve

import (
	"strings"

	"github.com/will-lang/will/internal/ast"
)

func (r *resolver) walkBlock(b *ast.Block) {
	if b == nil {
		return
	}
	r.push()
	for _, s := range b.Stmts {
		r.walkStmt(s)
	}
	r.pop()
}

func (r *resolver) walkStmt(s ast.Stmt) {
	switch st := s.(type) {
	case *ast.Block:
		r.walkBlock(st)
	case *ast.VarStmt:
		switch d := st.Decl.(type) {
		case *ast.ConstDecl:
			r.walkType(d.Type)
			r.walkExpr(d.Value, true)
			r.declareLocal(&Symbol{Name: d.Name, Kind: LocalKind, Pos: d.NamePos, Node: d, Exported: isExported(d.Name)})
		case *ast.VarDecl:
			r.walkType(d.Type)
			r.walkExpr(d.Value, true)
			r.declareLocal(&Symbol{Name: d.Name, Kind: LocalKind, Pos: d.NamePos, Node: d, Exported: isExported(d.Name)})
		}
	case *ast.AssignStmt:
		if st.Op == "=" {
			r.walkPlace(st.Target)
		} else {
			r.walkExpr(st.Target, true)
		}
		r.walkExpr(st.Value, true)
	case *ast.ExprStmt:
		r.walkExpr(st.Expr, true)
	case *ast.DeferStmt:
		r.walkExpr(st.Call, true)
	case *ast.ReturnStmt:
		r.walkExpr(st.Value, true)
	case *ast.BreakStmt, *ast.ContinueStmt:
	case *ast.ForStmt:
		r.push()
		r.walkExpr(st.Iter, true)
		for _, name := range st.Names {
			r.declareLocal(&Symbol{Name: name, Kind: LocalKind, Pos: st.Pos(), Node: st, Exported: isExported(name)})
		}
		r.walkBlock(st.Body)
		r.pop()
	case *ast.RegionStmt:
		r.walkBlock(st.Body)
	case *ast.ParallelStmt:
		r.walkBlock(st.Body)
	case *ast.SelectStmt:
		r.walkSelect(st)
	case *ast.IfExpr:
		r.walkIf(st)
	case *ast.MatchExpr:
		r.walkMatch(st)
	case *ast.DeadlineExpr:
		r.walkExpr(st.Duration, true)
		r.walkBlock(st.Body)
	}
}

func (r *resolver) walkIf(ifx *ast.IfExpr) {
	r.walkExpr(ifx.Cond, true)
	r.walkBlock(ifx.Then)
	if ifx.Else != nil {
		r.walkStmt(ifx.Else)
	}
}

func (r *resolver) walkMatch(m *ast.MatchExpr) {
	r.walkExpr(m.Subject, true)
	for _, arm := range m.Arms {
		r.push()
		r.walkPattern(arm.Pattern)
		r.walkExpr(arm.Guard, true)
		r.walkStmt(arm.Body)
		r.pop()
	}
}

func (r *resolver) walkSelect(sel *ast.SelectStmt) {
	for _, arm := range sel.Arms {
		r.push()
		if arm.Bind != "" && arm.Bind != "_" {
			r.declareLocal(&Symbol{Name: arm.Bind, Kind: LocalKind, Pos: arm.Pos(), Node: arm, Exported: isExported(arm.Bind)})
		}
		r.walkExpr(arm.Chan, true)
		r.walkExpr(arm.Timeout, true)
		r.walkStmt(arm.Body)
		r.pop()
	}
}

func (r *resolver) walkPattern(p *ast.Pattern) {
	if p == nil {
		return
	}
	switch p.Kind {
	case ast.WildPattern, ast.LiteralPattern, ast.RangePattern:
	case ast.BindPattern:
		r.declareLocal(&Symbol{Name: p.Name, Kind: LocalKind, Pos: p.Pos(), Node: p, Exported: isExported(p.Name)})
	case ast.VariantPattern:
		if sym, ok := r.current().lookup(p.Name); ok && (sym.Kind == VariantKind || sym.Kind == PredeclaredValueKind) {
			r.refs[p] = sym
			sym.Used = true
		} else {
			r.undefined(p.Pos(), p.Name)
		}
		for _, a := range p.Args {
			r.walkPattern(a)
		}
	case ast.OrPattern:
		for _, a := range p.Alts {
			r.walkPattern(a)
		}
	}
}

func (r *resolver) walkPlace(e ast.Expr) {
	switch x := e.(type) {
	case *ast.NameExpr:
		if x.Name == "_" {
			return
		}
		sym, ok := r.current().lookup(x.Name)
		if !ok {
			r.undefined(x.Pos(), x.Name)
			return
		}
		r.refs[x] = sym
	case *ast.FieldExpr:
		r.walkExpr(x.Recv, true)
	case *ast.IndexExpr:
		r.walkExpr(x.Recv, true)
		r.walkExpr(x.Index, true)
	default:
		r.walkExpr(e, true)
	}
}

func (r *resolver) walkType(t *ast.Type) {
	if t == nil {
		return
	}
	switch t.Kind {
	case ast.ScalarType, ast.NamedType:
		if t.Name == "" {
			return
		}
		if sym, ok := r.qualifier(t.Name); ok {
			r.refs[t] = sym
			sym.Used = true
		} else if sym, ok := r.current().lookup(t.Name); ok && sym.Kind.isType() {
			r.refs[t] = sym
			sym.Used = true
		} else {
			r.undefined(t.Pos(), t.Name)
		}
		for _, a := range t.Args {
			r.walkType(a)
		}
	case ast.ArrayType, ast.ViewType:
		r.walkType(t.Elem)
	case ast.FuncType:
		for _, p := range t.Params {
			r.walkType(p)
		}
		r.walkType(t.Result)
	case ast.UnitType:
	}
}

func (r *resolver) walkExpr(e ast.Expr, markUsed bool) {
	switch x := e.(type) {
	case nil:
	case *ast.NameExpr:
		if x.Name == "_" {
			return
		}
		sym, ok := r.current().lookup(x.Name)
		if !ok {
			r.undefined(x.Pos(), x.Name)
			return
		}
		r.refs[x] = sym
		if markUsed {
			sym.Used = true
		}
	case *ast.LiteralExpr, *ast.LaneHereExpr, *ast.UnitExpr:
	case *ast.CallExpr:
		r.walkCallee(x.Callee)
		r.walkTypes(x.TypeArgs)
		r.walkArgs(x.Args)
	case *ast.MethodCallExpr:
		r.walkQualifier(x.Recv)
		r.walkTypes(x.TypeArgs)
		r.walkArgs(x.Args)
	case *ast.FieldExpr:
		r.walkQualifier(x.Recv)
	case *ast.IndexExpr:
		r.walkExpr(x.Recv, true)
		r.walkExpr(x.Index, true)
	case *ast.SliceExpr:
		r.walkExpr(x.Recv, true)
		r.walkExpr(x.Lo, true)
		r.walkExpr(x.Hi, true)
	case *ast.AggregateExpr:
		if sym, ok := r.qualifier(x.TypeName); ok {
			r.refs[x] = sym
			sym.Used = true
		} else if sym, ok := r.current().lookup(x.TypeName); ok && sym.Kind.isType() {
			r.refs[x] = sym
			sym.Used = true
		} else {
			r.undefined(x.Pos(), x.TypeName)
		}
		r.walkTypes(x.TypeArgs)
		for _, f := range x.Fields {
			r.walkExpr(f.Value, true)
		}
	case *ast.NewExpr:
		r.walkType(x.Type)
		r.walkArgs(x.Args)
	case *ast.UnaryExpr:
		r.walkExpr(x.X, true)
	case *ast.BinaryExpr:
		r.walkExpr(x.L, true)
		r.walkExpr(x.R, true)
	case *ast.RangeExpr:
		r.walkExpr(x.Lo, true)
		r.walkExpr(x.Hi, true)
	case *ast.LambdaExpr:
		r.push()
		for _, pm := range x.Params {
			r.declareLocal(&Symbol{Name: pm.Name, Kind: ParamKind, Pos: pm.NamePos, Node: pm, Exported: isExported(pm.Name)})
			r.walkType(pm.Type)
		}
		r.walkType(x.Result)
		r.walkBlock(x.Body)
		r.pop()
	case *ast.TryExpr:
		r.walkExpr(x.X, true)
	case *ast.UnwrapExpr:
		r.walkExpr(x.X, true)
	case *ast.SpawnExpr:
		if x.Lane != "" {
			if sym, ok := r.current().lookup(x.Lane); ok && sym.Kind == LaneKind {
				r.refs[x] = sym
				sym.Used = true
			} else {
				r.undefined(x.Pos(), x.Lane)
			}
		}
		r.walkExpr(x.Call, true)
	case *ast.DeadlineExpr:
		r.walkExpr(x.Duration, true)
		r.walkBlock(x.Body)
	case *ast.IfExpr:
		r.walkIf(x)
	case *ast.MatchExpr:
		r.walkMatch(x)
	case *ast.InterpExpr:
		for _, part := range x.Parts {
			r.walkExpr(part.Expr, true)
		}
	}
}

func (r *resolver) walkTypes(types []*ast.Type) {
	for _, t := range types {
		r.walkType(t)
	}
}

func (r *resolver) walkArgs(args []*ast.CallArg) {
	for _, a := range args {
		r.walkExpr(a.Expr, true)
	}
}

func (r *resolver) walkCallee(callee ast.Expr) {
	r.walkExpr(callee, true)
}

// qualifier reports the import symbol in a package-qualified name such as
// http.Ctx.
func (r *resolver) qualifier(name string) (*Symbol, bool) {
	i := strings.IndexByte(name, '.')
	if i <= 0 {
		return nil, false
	}
	sym, ok := r.current().lookup(name[:i])
	if !ok || sym.Kind != ImportKind {
		return nil, false
	}
	return sym, true
}

// walkQualifier handles the left side of a selector. A name that denotes an
// imported package is used but is not a value.
func (r *resolver) walkQualifier(recv ast.Expr) {
	if name, ok := recv.(*ast.NameExpr); ok {
		if sym, found := r.current().lookup(name.Name); found && sym.Kind == ImportKind {
			r.refs[name] = sym
			sym.Used = true
			return
		}
	}
	r.walkExpr(recv, true)
}
