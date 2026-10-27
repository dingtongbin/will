// This file owns the task-boundary rules of the concurrency volume: a task
// handle must not escape its region (K0003), and the wait bridge must not
// appear inside a function that suspends (I0003, warning). K0005 for a spawn
// whose operand is not one call is reported by the parser at the spawn. The
// marks package supplies the shapes; this file turns them into diagnostics.
package check

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/marks"
)

// Concurrency reports the task-boundary diagnostics for one file.
func Concurrency(f *ast.File) []diag.Diagnostic {
	c := &concurrencyChecker{bag: diag.Bag{}}
	m := marks.Analyze(f)
	c.regionEscapes(f)
	c.guardAcrossSuspend(f)
	c.waitBridges(m)
	return c.bag.Diagnostics()
}

type concurrencyChecker struct {
	bag diag.Bag
}

/*
regionEscapes reports K0003 when a task handle is used outside the region

	that produced it, or when an assignment carries a handle into a binding
	that outlives that region. A task handle is only produced by a spawn, so
	tracking spawn bindings by name through a scope chain is exact for the
	shapes the grammar allows: a use in the creating region is fine, an
	assignment into an outer binding or a use after the region escapes.
*/
func (c *concurrencyChecker) regionEscapes(f *ast.File) {
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		e := &escapeWalker{checker: c}
		e.scopes = []*map[string]int{{}}
		e.block(fn.Body, 0)
	}
}

type escapeWalker struct {
	checker *concurrencyChecker
	scopes  []*map[string]int
}

/*
bind records a name in the innermost scope; assign updates the scope that

	already owns the name, so an inner assignment to an outer binding is seen
	by uses after the block. lookup finds the innermost binding.
*/
func (e *escapeWalker) bind(name string, depth int) {
	(*e.scopes[len(e.scopes)-1])[name] = depth
}

func (e *escapeWalker) assign(name string, depth int) {
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if _, ok := (*e.scopes[i])[name]; ok {
			(*e.scopes[i])[name] = depth
			return
		}
	}
	e.bind(name, depth)
}

func (e *escapeWalker) lookup(name string) (int, bool) {
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if d, ok := (*e.scopes[i])[name]; ok {
			return d, true
		}
	}
	return 0, false
}

func (e *escapeWalker) block(b *ast.Block, region int) {
	if b == nil {
		return
	}
	e.scopes = append(e.scopes, &map[string]int{})
	for _, st := range b.Stmts {
		e.stmt(st, region)
	}
	e.scopes = e.scopes[:len(e.scopes)-1]
}

func (e *escapeWalker) stmt(st ast.Stmt, region int) {
	switch s := st.(type) {
	case *ast.VarStmt:
		if d, ok := s.Decl.(*ast.VarDecl); ok {
			e.use(d.Value, region)
			switch v := d.Value.(type) {
			case *ast.SpawnExpr:
				e.bind(d.Name, region)
			case *ast.NameExpr:
				if from, ok := e.lookup(v.Name); ok {
					e.bind(d.Name, from)
				} else {
					e.bind(d.Name, -1)
				}
			default:
				e.bind(d.Name, -1)
			}
		}
	case *ast.AssignStmt:
		e.use(s.Value, region)
		if name, ok := s.Target.(*ast.NameExpr); ok {
			if v, ok := s.Value.(*ast.NameExpr); ok {
				if from, ok := e.lookup(v.Name); ok {
					e.assign(name.Name, from)
				} else {
					e.assign(name.Name, -1)
				}
			} else {
				e.assign(name.Name, -1)
			}
		} else {
			e.use(s.Target, region)
		}
	case *ast.ExprStmt:
		e.use(s.Expr, region)
	case *ast.DeferStmt:
		e.use(s.Call, region)
	case *ast.ReturnStmt:
		e.use(s.Value, region)
	case *ast.ForStmt:
		e.use(s.Iter, region)
		e.block(s.Body, region)
	case *ast.RegionStmt:
		e.block(s.Body, region+1)
	case *ast.ParallelStmt:
		e.block(s.Body, region)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			e.use(arm.Chan, region)
			e.use(arm.Timeout, region)
			e.stmt(arm.Body, region)
		}
	}
}

func (e *escapeWalker) use(expr ast.Expr, region int) {
	switch x := expr.(type) {
	case nil:
	case *ast.NameExpr:
		if from, ok := e.lookup(x.Name); ok && from > region {
			e.checker.bag.Error(x.Span.Pos(), diag.CodeTaskEscapesRegion,
				"this task handle escapes its region",
				"join it inside the region")
		}
	case *ast.CallExpr:
		e.use(x.Callee, region)
		for _, a := range x.Args {
			e.use(a.Expr, region)
		}
	case *ast.MethodCallExpr:
		e.use(x.Recv, region)
		for _, a := range x.Args {
			e.use(a.Expr, region)
		}
	case *ast.FieldExpr:
		e.use(x.Recv, region)
	case *ast.IndexExpr:
		e.use(x.Recv, region)
		e.use(x.Index, region)
	case *ast.SliceExpr:
		e.use(x.Recv, region)
		e.use(x.Lo, region)
		e.use(x.Hi, region)
	case *ast.BorrowExpr:
		e.use(x.X, region)
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			e.use(f.Value, region)
		}
	case *ast.ArrayLitExpr:
		for _, elem := range x.Elems {
			e.use(elem, region)
		}
	case *ast.NewExpr:
		for _, a := range x.Args {
			e.use(a.Expr, region)
		}
	case *ast.UnaryExpr:
		e.use(x.X, region)
	case *ast.BinaryExpr:
		e.use(x.L, region)
		e.use(x.R, region)
	case *ast.RangeExpr:
		e.use(x.Lo, region)
		e.use(x.Hi, region)
	case *ast.TryExpr:
		e.use(x.X, region)
	case *ast.AwaitExpr:
		e.use(x.X, region)
	case *ast.UnwrapExpr:
		e.use(x.X, region)
	case *ast.SpawnExpr:
		e.use(x.Call, region)
	case *ast.DeadlineExpr:
		e.use(x.Duration, region)
		e.block(x.Body, region)
	case *ast.IfExpr:
		e.use(x.Cond, region)
		e.block(x.Then, region)
		e.stmt(x.Else, region)
	case *ast.MatchExpr:
		e.use(x.Subject, region)
		for _, arm := range x.Arms {
			e.use(arm.Guard, region)
			e.stmt(arm.Body, region)
		}
	case *ast.InterpExpr:
		for _, part := range x.Parts {
			e.use(part.Expr, region)
		}
	}
}

// waitBridges reports I0003 for a wait inside a function that has its own
// suspension points: the bridge turns an async call back into a blocking
// one, which inside an async function is a mistake the caller should not
// pay for. A function whose only suspensions are its waits is a synchronous
// function using the bridge as intended.
func (c *concurrencyChecker) waitBridges(m *marks.File) {
	for _, fn := range m.Funcs {
		if !fn.Async {
			continue
		}
		if len(fn.Suspends) <= len(fn.Waits) {
			continue
		}
		for _, w := range fn.Waits {
			c.bag.Warning(w.Pos(), diag.CodeWaitInAsync,
				"wait in an async function",
				"call the async function directly")
		}
	}
}
