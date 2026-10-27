package check

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

// checkRegionTasks reports K0015 when a task handle created inside a region is
// still live at region exit or at an early return. The check is deliberately
// local: only named task locals are tracked; a handle hidden in an aggregate is
// rejected by the type checker's Send rule when it crosses a boundary.
func (c *concurrencyChecker) regionTasks(f *ast.File) {
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		c.regionBlock(fn.Body)
	}
}

func (c *concurrencyChecker) regionBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		switch s := st.(type) {
		case *ast.RegionStmt:
			c.checkRegion(s.Body)
			c.regionBlock(s.Body)
		case *ast.IfExpr:
			c.regionBlock(s.Then)
			c.regionStmt(s.Else)
		case *ast.MatchExpr:
			for _, arm := range s.Arms {
				c.regionStmt(arm.Body)
			}
		case *ast.ForStmt:
			c.regionBlock(s.Body)
		case *ast.SelectStmt:
			for _, arm := range s.Arms {
				c.regionStmt(arm.Body)
			}
		case *ast.DeadlineExpr:
			c.regionBlock(s.Body)
		}
	}
}

func (c *concurrencyChecker) regionStmt(st ast.Stmt) {
	if b, ok := st.(*ast.Block); ok {
		c.regionBlock(b)
		return
	}
	if st == nil {
		return
	}
	c.regionBlock(&ast.Block{Stmts: []ast.Stmt{st}})
}

func (c *concurrencyChecker) checkRegion(body *ast.Block) {
	tasks := map[string]bool{}
	c.collectTasks(body, tasks)
	c.consumeTasks(body, tasks)
	for name, done := range tasks {
		if done {
			continue
		}
		c.bag.Error(body.Pos(), diag.CodeTaskNotAwaited,
			"task handle "+name+" is not awaited before region exit",
			"await it, or write _ = await "+name+" to discard the result")
	}
}

func (c *concurrencyChecker) collectTasks(st ast.Stmt, tasks map[string]bool) {
	switch s := st.(type) {
	case *ast.Block:
		for _, inner := range s.Stmts {
			c.collectTasks(inner, tasks)
		}
	case *ast.VarStmt:
		if d, ok := s.Decl.(*ast.VarDecl); ok && d.Name != "" && d.Name != "_" {
			if isTaskType(d.Type) || isSpawn(d.Value) {
				tasks[d.Name] = false
			}
		}
	case *ast.IfExpr:
		c.collectTasks(s.Then, tasks)
		if s.Else != nil {
			c.collectTasks(s.Else, tasks)
		}
	case *ast.MatchExpr:
		for _, arm := range s.Arms {
			c.collectTasks(arm.Body, tasks)
		}
	case *ast.ForStmt:
		c.collectTasks(s.Body, tasks)
	case *ast.RegionStmt:
		// A nested region owns its own handles.
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			c.collectTasks(arm.Body, tasks)
		}
	case *ast.DeadlineExpr:
		c.collectTasks(s.Body, tasks)
	}
}

func (c *concurrencyChecker) consumeTasks(st ast.Stmt, tasks map[string]bool) {
	switch s := st.(type) {
	case *ast.Block:
		for _, inner := range s.Stmts {
			c.consumeTasks(inner, tasks)
		}
	case *ast.ExprStmt:
		c.consumeExpr(s.Expr, tasks)
	case *ast.AssignStmt:
		c.consumeExpr(s.Value, tasks)
	case *ast.ReturnStmt:
		c.consumeExpr(s.Value, tasks)
	case *ast.VarStmt:
		if d, ok := s.Decl.(*ast.VarDecl); ok {
			c.consumeExpr(d.Value, tasks)
		}
	case *ast.IfExpr:
		c.consumeExpr(s.Cond, tasks)
		c.consumeTasks(s.Then, tasks)
		if s.Else != nil {
			c.consumeTasks(s.Else, tasks)
		}
	case *ast.MatchExpr:
		c.consumeExpr(s.Subject, tasks)
		for _, arm := range s.Arms {
			c.consumeExpr(arm.Guard, tasks)
			c.consumeTasks(arm.Body, tasks)
		}
	case *ast.ForStmt:
		c.consumeExpr(s.Iter, tasks)
		c.consumeTasks(s.Body, tasks)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			c.consumeExpr(arm.Chan, tasks)
			c.consumeExpr(arm.Timeout, tasks)
			c.consumeTasks(arm.Body, tasks)
		}
	case *ast.DeadlineExpr:
		c.consumeExpr(s.Duration, tasks)
		c.consumeTasks(s.Body, tasks)
	}
}

func (c *concurrencyChecker) consumeExpr(e ast.Expr, tasks map[string]bool) {
	switch x := e.(type) {
	case *ast.AwaitExpr:
		if name, ok := x.X.(*ast.NameExpr); ok {
			if _, tracked := tasks[name.Name]; tracked {
				tasks[name.Name] = true
			}
		}
		c.consumeExpr(x.X, tasks)
	case *ast.UnwrapExpr:
		c.consumeExpr(x.X, tasks)
	case *ast.TryExpr:
		c.consumeExpr(x.X, tasks)
	case *ast.BinaryExpr:
		c.consumeExpr(x.L, tasks)
		c.consumeExpr(x.R, tasks)
	case *ast.UnaryExpr:
		c.consumeExpr(x.X, tasks)
	case *ast.FieldExpr:
		c.consumeExpr(x.Recv, tasks)
	case *ast.IndexExpr:
		c.consumeExpr(x.Recv, tasks)
		c.consumeExpr(x.Index, tasks)
	case *ast.SliceExpr:
		c.consumeExpr(x.Recv, tasks)
		c.consumeExpr(x.Lo, tasks)
		c.consumeExpr(x.Hi, tasks)
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			c.consumeExpr(f.Value, tasks)
		}
	case *ast.ArrayLitExpr:
		for _, elem := range x.Elems {
			c.consumeExpr(elem, tasks)
		}
	case *ast.CallExpr:
		for _, a := range x.Args {
			c.consumeExpr(a.Expr, tasks)
		}
	case *ast.MethodCallExpr:
		c.consumeExpr(x.Recv, tasks)
		for _, a := range x.Args {
			c.consumeExpr(a.Expr, tasks)
		}
	}
}

func isTaskType(t *ast.Type) bool {
	return t != nil && t.Kind == ast.NamedType && t.Name == "Task"
}

func isSpawn(e ast.Expr) bool {
	_, ok := e.(*ast.SpawnExpr)
	return ok
}
