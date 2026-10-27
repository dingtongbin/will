package types

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/resolve"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/token"
)

// DispatchPoint records one call through an interface value. The index lists
// every type that can be the target.
type DispatchPoint struct {
	Pos       source.Pos
	Interface string
}

// Result carries what the checker learned beyond diagnostics.
// TypeOf returns the checked type of one expression, or nil when the checker
// did not visit it.
func (r *Result) TypeOf(e ast.Expr) *Type {
	if r == nil || e == nil {
		return nil
	}
	return r.ExprTypes[e]
}

type Result struct {
	Dispatch []DispatchPoint
	// LiteralTypes is the resolved type text of every literal, by node. The
	// intermediate format records it, so the backend never infers a width.
	LiteralTypes map[*ast.LiteralExpr]string
	// CallTypes is the resolved type text of a predeclared constructor call
	// (Some, Ok, Err), by node. The expected type supplies the side the call
	// itself cannot infer, such as the error type of Ok.
	CallTypes map[*ast.CallExpr]string
	// NameTypes is the resolved type text of a predeclared value such as None.
	NameTypes map[*ast.NameExpr]string
	// ExprTypes is the checked type of every expression the checker visited.
	// The ownership and task-boundary passes use it to ask Send/Sync.
	ExprTypes map[ast.Expr]*Type
	env       *env
}

// Check checks one resolved file and returns its diagnostics.
func Check(f *ast.File, res *resolve.Result) []diag.Diagnostic {
	return CheckWith(f, res, nil)
}

// CheckWith checks one file with the rest of its package visible. The other
// files supply declarations only; their own bodies are not checked here.
func CheckWith(f *ast.File, res *resolve.Result, others []*ast.File) []diag.Diagnostic {
	_, ds := checkWithResult(f, res, others)
	return ds
}

// CheckFull checks one resolved file and also returns the dispatch points.
func CheckFull(f *ast.File, res *resolve.Result) (*Result, []diag.Diagnostic) {
	return checkWithResult(f, res, nil)
}

// CheckWithFull checks one file with the rest of its package visible and also
// returns what the checker recorded.
func CheckWithFull(f *ast.File, res *resolve.Result, others []*ast.File) (*Result, []diag.Diagnostic) {
	return checkWithResult(f, res, others)
}

// CheckPackage checks every file of one package. The declaration environment is
// built from all of them, so a type, a method, or an interface declared in one
// file is visible in the others.
func CheckPackage(files []*ast.File, results []*resolve.Result) []diag.Diagnostic {
	_, ds := CheckPackageFull(files, results)
	return ds
}

// CheckPackageFull checks every file of one package and also returns the
// checker's result: dispatch points and resolved literal types.
func CheckPackageFull(files []*ast.File, results []*resolve.Result) (*Result, []diag.Diagnostic) {
	return CheckPackageFullWithForeign(files, results, nil)
}

// CheckPackageWithForeign checks one package with the init declarations of
// imported packages available, so new on an imported generic type sees the
// Result its init returns.
func CheckPackageWithForeign(files []*ast.File, results []*resolve.Result, foreign map[string]map[string]*ast.FuncDecl) []diag.Diagnostic {
	_, ds := CheckPackageFullWithForeign(files, results, foreign)
	return ds
}

// CheckPackageWithImported also checks calls to imported free functions, so a
// literal passed across a package boundary gets the declared parameter type.
func CheckPackageWithImported(files []*ast.File, results []*resolve.Result, inits, funcs map[string]map[string]*ast.FuncDecl) []diag.Diagnostic {
	_, ds := CheckPackageFullWithImported(files, results, inits, funcs)
	return ds
}

// CheckPackageFullWithImported returns the checker result as well, so the
// front end can emit the literal types it resolved across packages.
func CheckPackageFullWithImported(files []*ast.File, results []*resolve.Result, inits, funcs map[string]map[string]*ast.FuncDecl) (*Result, []diag.Diagnostic) {
	c := &checker{in: newInterner(), foreignInits: inits, foreignFuncs: funcs,
		exprTypes: map[ast.Expr]*Type{}}
	c.env = buildEnvAll(c, files)
	c.checkGuardDecls(files)
	c.checkPackage(files, results)
	return c.result(), c.bag.Diagnostics()
}

// CheckPackageFullWithForeign is CheckPackageFull plus imported init
// declarations keyed by package name and type name.
func CheckPackageFullWithForeign(files []*ast.File, results []*resolve.Result, foreign map[string]map[string]*ast.FuncDecl) (*Result, []diag.Diagnostic) {
	c := &checker{in: newInterner(), foreignInits: foreign,
		exprTypes: map[ast.Expr]*Type{}}
	c.env = buildEnvAll(c, files)
	c.checkGuardDecls(files)
	c.checkPackage(files, results)
	return c.result(), c.bag.Diagnostics()
}

// checkPackage walks every file after the environment is built.
func (c *checker) checkPackage(files []*ast.File, results []*resolve.Result) {
	c.checkCrossTypes(files)
	for i, f := range files {
		if f == nil || i >= len(results) || results[i] == nil {
			continue
		}
		c.res = results[i]
		c.walkFile(f)
		c.inferFile(f)
	}
}

// result packages what the checker recorded.
func (c *checker) result() *Result {
	return &Result{Dispatch: c.dispatch, LiteralTypes: c.litTypes, CallTypes: c.callTypes,
		NameTypes: c.nameTypes, ExprTypes: c.exprTypes, env: c.env}
}

func checkWithResult(f *ast.File, res *resolve.Result, others []*ast.File) (*Result, []diag.Diagnostic) {
	c := &checker{res: res, in: newInterner(),
		exprTypes: map[ast.Expr]*Type{}}
	files := make([]*ast.File, 0, len(others)+1)
	files = append(files, others...)
	files = append(files, f)
	c.env = buildEnvAll(c, files)
	c.checkGuardDecls(files)
	c.checkCrossTypes(files)
	c.walkFile(f)
	c.inferFile(f)
	return c.result(), c.bag.Diagnostics()
}

type checker struct {
	bag      diag.Bag
	res      *resolve.Result
	in       *interner
	env      *env
	dispatch []DispatchPoint
	// litTypes is the resolved type text of every literal, by node.
	litTypes map[*ast.LiteralExpr]string
	// callTypes and nameTypes are the resolved type texts of the predeclared
	// constructors and values, by node.
	callTypes map[*ast.CallExpr]string
	nameTypes map[*ast.NameExpr]string
	// foreignInits holds init declarations of imported packages, keyed by
	// package name and type name. A qualified new needs the result type the
	// defining package gave that init.
	foreignInits map[string]map[string]*ast.FuncDecl
	// foreignFuncs holds imported free functions by package name and function
	// name, so a call checks its arguments against the declaring signature.
	foreignFuncs map[string]map[string]*ast.FuncDecl
	// exprTypes caches the inferred type of each expression for post-passes.
	// inferExpr fills it.
	exprTypes map[ast.Expr]*Type
	// aliasResolved caches expanded aliases. aliasActive and aliasStack detect
	// a cycle while aliases are expanded.
	aliasResolved map[string]*Type
	aliasActive   map[string]bool
	aliasStack    []string
	aliasReported map[string]bool
}

func (c *checker) walkFile(f *ast.File) {
	for _, d := range f.Decls {
		c.walkDecl(d)
	}
}

func (c *checker) walkDecl(d ast.Decl) {
	switch decl := d.(type) {
	case *ast.ConstDecl:
		c.walkExpr(decl.Value)
	case *ast.VarDecl:
		c.walkExpr(decl.Value)
	case *ast.FuncDecl:
		c.walkBlock(decl.Body)
	case *ast.StructDecl:
		for _, f := range decl.Fields {
			c.walkExpr(f.Default)
		}
	case *ast.LaneDecl:
		if decl.Decl != nil {
			c.walkDecl(decl.Decl)
		}
	}
}

func (c *checker) walkBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		c.walkStmt(s)
	}
}

func (c *checker) walkStmt(s ast.Stmt) {
	switch st := s.(type) {
	case *ast.Block:
		c.walkBlock(st)
	case *ast.VarStmt:
		switch d := st.Decl.(type) {
		case *ast.ConstDecl:
			c.walkExpr(d.Value)
		case *ast.VarDecl:
			c.walkExpr(d.Value)
		}
	case *ast.AssignStmt:
		c.walkExpr(st.Target)
		c.walkExpr(st.Value)
	case *ast.ExprStmt:
		c.walkExpr(st.Expr)
	case *ast.DeferStmt:
		c.walkExpr(st.Call)
	case *ast.ReturnStmt:
		c.walkExpr(st.Value)
	case *ast.ForStmt:
		c.walkExpr(st.LoopExpr())
		c.walkBlock(st.Body)
	case *ast.RegionStmt:
		c.walkBlock(st.Body)
	case *ast.ParallelStmt:
		c.walkBlock(st.Body)
	case *ast.SelectStmt:
		for _, arm := range st.Arms {
			c.walkExpr(arm.Chan)
			c.walkExpr(arm.Timeout)
			c.walkStmt(arm.Body)
		}
	case *ast.IfExpr:
		c.walkIf(st)
	case *ast.MatchExpr:
		c.walkMatch(st)
	case *ast.DeadlineExpr:
		c.walkExpr(st.Duration)
		c.walkBlock(st.Body)
	}
}

func (c *checker) walkIf(ifx *ast.IfExpr) {
	c.walkExpr(ifx.Cond)
	c.walkBlock(ifx.Then)
	if ifx.Else != nil {
		c.walkStmt(ifx.Else)
	}
}

func (c *checker) walkMatch(m *ast.MatchExpr) {
	c.walkExpr(m.Subject)
	for _, arm := range m.Arms {
		c.walkExpr(arm.Guard)
		c.walkStmt(arm.Body)
	}
}

func (c *checker) walkExpr(e ast.Expr) {
	switch x := e.(type) {
	case nil:
	case *ast.LiteralExpr:
		c.checkLiteral(x)
	case *ast.NameExpr, *ast.LaneHereExpr, *ast.UnitExpr:
	case *ast.CallExpr:
		c.walkExpr(x.Callee)
		c.walkArgs(x.Args)
	case *ast.MethodCallExpr:
		c.walkExpr(x.Recv)
		c.walkArgs(x.Args)
	case *ast.FieldExpr:
		c.walkExpr(x.Recv)
	case *ast.IndexExpr:
		c.walkExpr(x.Recv)
		c.walkExpr(x.Index)
	case *ast.SliceExpr:
		c.walkExpr(x.Recv)
		c.walkExpr(x.Lo)
		c.walkExpr(x.Hi)
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			c.walkExpr(f.Value)
		}
	case *ast.ArrayLitExpr:
		for _, e := range x.Elems {
			c.walkExpr(e)
		}
	case *ast.NewExpr:
		c.walkArgs(x.Args)
	case *ast.UnaryExpr:
		c.walkExpr(x.X)
	case *ast.BinaryExpr:
		c.checkComparison(x)
		c.walkExpr(x.L)
		c.walkExpr(x.R)
	case *ast.RangeExpr:
		c.walkExpr(x.Lo)
		c.walkExpr(x.Hi)
	case *ast.LambdaExpr:
		c.walkBlock(x.Body)
	case *ast.TryExpr:
		c.walkExpr(x.X)
	case *ast.AwaitExpr:
		c.walkExpr(x.X)
	case *ast.UnwrapExpr:
		c.walkExpr(x.X)
	case *ast.SpawnExpr:
		c.walkExpr(x.Call)
	case *ast.DeadlineExpr:
		c.walkExpr(x.Duration)
		c.walkBlock(x.Body)
	case *ast.IfExpr:
		c.walkIf(x)
	case *ast.MatchExpr:
		c.walkMatch(x)
	case *ast.InterpExpr:
		for _, part := range x.Parts {
			c.walkExpr(part.Expr)
		}
	}
}

func (c *checker) walkArgs(args []*ast.CallArg) {
	for _, a := range args {
		c.walkExpr(a.Expr)
	}
}

func isComparison(k token.Kind) bool {
	switch k {
	case token.Eq, token.Ne, token.Lt, token.Le, token.Gt, token.Ge:
		return true
	}
	return false
}

// checkComparison reports T0006 when a comparison has a comparison operand.
func (c *checker) checkComparison(x *ast.BinaryExpr) {
	if !isComparison(x.Op) {
		return
	}
	if l, ok := x.L.(*ast.BinaryExpr); ok && isComparison(l.Op) {
		c.chained(x)
		return
	}
	if r, ok := x.R.(*ast.BinaryExpr); ok && isComparison(r.Op) {
		c.chained(x)
	}
}

func (c *checker) chained(x *ast.BinaryExpr) {
	c.bag.Error(x.Pos(), diag.CodeChainedComparison, "comparisons cannot be chained",
		"split it into two comparisons")
}

// checkLiteral reports T0005 for an integer literal whose own suffix cannot
// hold the value. An untyped literal is checked when it gets an expected type.
func (c *checker) checkLiteral(lit *ast.LiteralExpr) {
	if lit.Kind != token.Int {
		return
	}
	value, suffix, ok := parseIntLiteral(lit.Text)
	if !ok || suffix == "" {
		return
	}
	if !intTypeName(suffix) {
		return
	}
	if !fitsInt(value, suffix) {
		c.checkIntConstant(lit.Pos(), value, lit.Text, suffix)
	}
}
