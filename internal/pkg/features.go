package pkg

import (
	"sort"
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/token"
)

// EnabledFeatures is the set of features --features opens. The command line
// sets it before loading, because pruning happens while the packages are read.
var EnabledFeatures = map[string]bool{}

// SetEnabledFeatures parses the comma-separated value of --features.
func SetEnabledFeatures(list string) {
	EnabledFeatures = map[string]bool{}
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			EnabledFeatures[name] = true
		}
	}
}

// pruneReport is one package's feature pruning plus its diagnostics.
type pruneReport struct {
	diags []diag.Diagnostic
}

// prunePackages removes the branches no enabled feature selects and drops the
// std/meta import, so the resolver and the checker never see the dead code.
func prunePackages(pkgs []*Package, declared map[string]bool) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, p := range pkgs {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			out = append(out, pruneFile(f.AST, declared, EnabledFeatures)...)
		}
	}
	return out
}

// pruneFile rewrites the feature conditions of one file. The std/meta import
// is removed after the conditions are gone, so it cannot be reported unused.
func pruneFile(f *ast.File, declared, enabled map[string]bool) []diag.Diagnostic {
	meta := false
	for _, imp := range f.Imports {
		if !imp.C && imp.Path == "std/meta" {
			meta = true
			break
		}
	}
	p := &pruner{declared: declared, enabled: enabled, meta: meta}
	if meta {
		for _, d := range f.Decls {
			p.pruneDecl(d)
		}
	} else {
		// Without the import, meta.Feature is an ordinary method call and the
		// resolver decides what it means.
		return nil
	}
	if meta {
		kept := f.Imports[:0]
		for _, imp := range f.Imports {
			if !imp.C && imp.Path == "std/meta" {
				continue
			}
			kept = append(kept, imp)
		}
		f.Imports = kept
	}
	return p.diags
}

// pruner walks one file and evaluates feature conditions.
type pruner struct {
	declared map[string]bool
	enabled  map[string]bool
	meta     bool
	diags    []diag.Diagnostic
}

func (p *pruner) pruneDecl(d ast.Decl) {
	switch x := d.(type) {
	case *ast.FuncDecl:
		if x.Body != nil {
			p.pruneBlock(x.Body)
		}
	case *ast.ConstDecl:
		p.walkExpr(x.Value, false)
	case *ast.VarDecl:
		p.walkExpr(x.Value, false)
	case *ast.StructDecl:
		for _, f := range x.Fields {
			p.walkExpr(f.Default, false)
		}
	case *ast.LaneDecl:
		p.pruneDecl(x.Decl)
	}
}

func (p *pruner) pruneBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		p.pruneStmt(st)
	}
}

func (p *pruner) pruneStmt(st ast.Stmt) {
	switch x := st.(type) {
	case nil:
		return
	case *ast.Block:
		p.pruneBlock(x)
	case *ast.IfExpr:
		p.pruneIf(x)
	case *ast.ForStmt:
		p.walkExpr(x.Iter, false)
		p.pruneBlock(x.Body)
	case *ast.RegionStmt:
		p.pruneBlock(x.Body)
	case *ast.ParallelStmt:
		p.pruneBlock(x.Body)
	case *ast.SelectStmt:
		for _, arm := range x.Arms {
			p.walkExpr(arm.Chan, false)
			p.walkExpr(arm.Timeout, false)
			p.pruneStmt(arm.Body)
		}
	case *ast.MatchExpr:
		p.walkExpr(x.Subject, false)
		for _, arm := range x.Arms {
			p.walkExpr(arm.Guard, false)
			p.pruneStmt(arm.Body)
		}
	case *ast.DeadlineExpr:
		p.walkExpr(x.Duration, false)
		p.pruneBlock(x.Body)
	case *ast.VarStmt:
		switch d := x.Decl.(type) {
		case *ast.ConstDecl:
			p.walkExpr(d.Value, false)
		case *ast.VarDecl:
			p.walkExpr(d.Value, false)
		}
	case *ast.AssignStmt:
		p.walkExpr(x.Target, false)
		p.walkExpr(x.Value, false)
	case *ast.ExprStmt:
		p.walkExpr(x.Expr, false)
	case *ast.DeferStmt:
		p.walkExpr(x.Call, false)
	case *ast.ReturnStmt:
		p.walkExpr(x.Value, false)
	}
}

// pruneIf evaluates a feature condition. A live branch is pruned recursively; a
// dead branch is emptied (or the then branch is emptied for a false condition),
// so the node keeps its scope and the emitter sees a constant condition.
func (p *pruner) pruneIf(x *ast.IfExpr) {
	if value, ok := p.featureValue(x.Cond); ok {
		if value {
			x.Cond = boolLiteral(true)
			x.Else = nil
			p.pruneBlock(x.Then)
			return
		}
		x.Cond = boolLiteral(false)
		x.Then = &ast.Block{}
		if x.Else != nil {
			p.pruneElse(x.Else)
		}
		return
	}
	p.walkExpr(x.Cond, false)
	p.pruneBlock(x.Then)
	if x.Else != nil {
		p.pruneElse(x.Else)
	}
}

func (p *pruner) pruneElse(st ast.Stmt) {
	switch x := st.(type) {
	case nil:
		return
	case *ast.Block:
		p.pruneBlock(x)
	case *ast.IfExpr:
		p.pruneIf(x)
	default:
		p.pruneStmt(st)
	}
}

// featureValue evaluates a condition made only of Feature calls, !, &&, and
// ||. ok is false when the condition is not such an expression.
func (p *pruner) featureValue(e ast.Expr) (value, ok bool) {
	switch x := e.(type) {
	case *ast.MethodCallExpr:
		if !p.isFeatureCall(x) {
			return false, false
		}
		if len(x.Args) != 1 {
			p.outsideIf(x.Pos())
			return false, true
		}
		lit, isLit := x.Args[0].Expr.(*ast.LiteralExpr)
		if !isLit || lit.Kind != token.String {
			p.outsideIf(x.Pos())
			return false, true
		}
		name := lit.Value
		if !p.declared[name] {
			p.undeclared(x.Pos(), name)
			return false, true
		}
		return p.enabled[name], true
	case *ast.UnaryExpr:
		if x.Op != token.Bang {
			return false, false
		}
		v, ok := p.featureValue(x.X)
		return !v, ok
	case *ast.BinaryExpr:
		if x.Op != token.AndAnd && x.Op != token.OrOr {
			return false, false
		}
		l, lok := p.featureValue(x.L)
		r, rok := p.featureValue(x.R)
		if !lok || !rok {
			return false, false
		}
		if x.Op == token.AndAnd {
			return l && r, true
		}
		return l || r, true
	}
	return false, false
}

// isFeatureCall reports whether e is meta.Feature(...) in a file that imports
// std/meta.
func (p *pruner) isFeatureCall(x *ast.MethodCallExpr) bool {
	if !p.meta || x.Name != "Feature" {
		return false
	}
	recv, ok := x.Recv.(*ast.NameExpr)
	return ok && recv.Name == "meta"
}

// outsideIf reports a Feature call that is not a whole if condition.
func (p *pruner) outsideIf(pos source.Pos) {
	p.diags = append(p.diags, diag.Diagnostic{
		Pos:      pos,
		End:      pos,
		Severity: diag.Error,
		Code:     diag.CodeFeatureOutsideIf,
		Message:  "meta.Feature is only allowed in an if condition",
		Help:     `write if meta.Feature("x") { ... }`,
	})
}

func (p *pruner) undeclared(pos source.Pos, name string) {
	p.diags = append(p.diags, diag.Diagnostic{
		Pos:      pos,
		End:      pos,
		Severity: diag.Error,
		Code:     diag.CodeFeatureNotDeclared,
		Message:  "feature " + name + " is not declared in will.mod",
		Help:     "add feature " + name + ", or remove the condition",
	})
}

// featureMentions reports whether an expression contains a Feature call. It is
// used to reject a condition that mixes features with run-time values.
func (p *pruner) featureMentions(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.MethodCallExpr:
		if p.isFeatureCall(x) {
			return true
		}
		return p.featureMentions(x.Recv) || anyCallArg(x.Args, p.featureMentions)
	case *ast.UnaryExpr:
		return p.featureMentions(x.X)
	case *ast.BinaryExpr:
		return p.featureMentions(x.L) || p.featureMentions(x.R)
	}
	return false
}

// walkExpr visits an expression outside a feature condition. Any Feature call
// there is T0010.
func (p *pruner) walkExpr(e ast.Expr, inCondition bool) {
	if e == nil {
		return
	}
	if x, ok := e.(*ast.MethodCallExpr); ok && p.isFeatureCall(x) {
		if !inCondition {
			p.outsideIf(x.Pos())
		}
		for _, a := range x.Args {
			p.walkExpr(a.Expr, false)
		}
		return
	}
	switch x := e.(type) {
	case *ast.CallExpr:
		p.walkExpr(x.Callee, false)
		for _, a := range x.Args {
			p.walkExpr(a.Expr, false)
		}
	case *ast.MethodCallExpr:
		p.walkExpr(x.Recv, false)
		for _, a := range x.Args {
			p.walkExpr(a.Expr, false)
		}
	case *ast.FieldExpr:
		p.walkExpr(x.Recv, false)
	case *ast.IndexExpr:
		p.walkExpr(x.Recv, false)
		p.walkExpr(x.Index, false)
	case *ast.SliceExpr:
		p.walkExpr(x.Recv, false)
		p.walkExpr(x.Lo, false)
		p.walkExpr(x.Hi, false)
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			p.walkExpr(f.Value, false)
		}
	case *ast.NewExpr:
		for _, a := range x.Args {
			p.walkExpr(a.Expr, false)
		}
	case *ast.UnaryExpr:
		p.walkExpr(x.X, false)
	case *ast.BinaryExpr:
		p.walkExpr(x.L, false)
		p.walkExpr(x.R, false)
	case *ast.RangeExpr:
		p.walkExpr(x.Lo, false)
		p.walkExpr(x.Hi, false)
	case *ast.LambdaExpr:
		p.pruneBlock(x.Body)
	case *ast.TryExpr:
		p.walkExpr(x.X, false)
	case *ast.UnwrapExpr:
		p.walkExpr(x.X, false)
	case *ast.SpawnExpr:
		p.walkExpr(x.Call, false)
	case *ast.DeadlineExpr:
		p.walkExpr(x.Duration, false)
		p.pruneBlock(x.Body)
	case *ast.IfExpr:
		if p.featureMentions(x.Cond) {
			p.outsideIf(x.Pos())
			return
		}
		p.walkExpr(x.Cond, false)
		p.pruneBlock(x.Then)
		if x.Else != nil {
			p.pruneElse(x.Else)
		}
	case *ast.MatchExpr:
		p.walkExpr(x.Subject, false)
		for _, arm := range x.Arms {
			p.walkExpr(arm.Guard, false)
			p.pruneStmt(arm.Body)
		}
	case *ast.InterpExpr:
		for _, part := range x.Parts {
			p.walkExpr(part.Expr, false)
		}
	}
}

func boolLiteral(value bool) *ast.LiteralExpr {
	kind, text := token.KwFalse, "false"
	if value {
		kind, text = token.KwTrue, "true"
	}
	return &ast.LiteralExpr{Kind: kind, Text: text, Value: text}
}

func anyCallArg(args []*ast.CallArg, f func(ast.Expr) bool) bool {
	for _, a := range args {
		if f(a.Expr) {
			return true
		}
	}
	return false
}

// declaredFeatures unions the feature declarations of every module in the
// graph, so a dependency may guard its own code with its own features.
func (g *graph) declaredFeatures() map[string]bool {
	out := map[string]bool{}
	for _, n := range g.nodes {
		if n.Mod == nil {
			continue
		}
		for _, f := range n.Mod.Features {
			out[f] = true
		}
	}
	return out
}

// enabledUndeclared reports the --features names no will.mod declares, in name
// order so the output does not depend on map iteration.
func enabledUndeclared(declared map[string]bool) []diag.Diagnostic {
	var names []string
	for name := range EnabledFeatures {
		if !declared[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var out []diag.Diagnostic
	for _, name := range names {
		out = append(out, diag.Diagnostic{
			Pos:      source.Pos{File: "will.mod", Line: 1, Col: 1},
			End:      source.Pos{File: "will.mod", Line: 1, Col: 1},
			Severity: diag.Error,
			Code:     diag.CodeFeatureNotDeclared,
			Message:  "feature " + name + " is not declared in will.mod",
			Help:     "add feature " + name + ", or remove it from --features",
		})
	}
	return out
}
