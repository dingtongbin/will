// This file owns T0016: a value that the structural Send rule forbids moving
// to another task. The type checker supplies the type of every expression;
// this pass finds the syntactic places where a value actually crosses.
package check

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/types"
)

// CrossTask reports values that cannot move through spawn or a channel send.
func CrossTask(f *ast.File, checked *types.Result) []diag.Diagnostic {
	if f == nil || checked == nil {
		return nil
	}
	c := &crossTaskChecker{types: checked}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		c.lambdas = map[string]*ast.LambdaExpr{}
		collectLambdas(fn.Body, c.lambdas)
		c.block(fn.Body)
	}
	return c.bag.Diagnostics()
}

type crossTaskChecker struct {
	types   *types.Result
	bag     diag.Bag
	lambdas map[string]*ast.LambdaExpr
}

func (c *crossTaskChecker) block(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		c.stmt(st)
	}
}

func (c *crossTaskChecker) stmt(st ast.Stmt) {
	switch s := st.(type) {
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			c.expr(d.Value)
		case *ast.ConstDecl:
			c.expr(d.Value)
		}
	case *ast.AssignStmt:
		c.expr(s.Target)
		c.expr(s.Value)
	case *ast.ExprStmt:
		c.expr(s.Expr)
	case *ast.DeferStmt:
		c.expr(s.Call)
	case *ast.ReturnStmt:
		c.expr(s.Value)
	case *ast.Block:
		c.block(s)
	case *ast.ForStmt:
		c.expr(s.Iter)
		c.block(s.Body)
	case *ast.RegionStmt:
		c.block(s.Body)
	case *ast.ParallelStmt:
		c.block(s.Body)
	case *ast.SelectStmt:
		for _, arm := range s.Arms {
			c.expr(arm.Chan)
			c.expr(arm.Timeout)
			c.blockStmt(arm.Body)
		}
	case *ast.IfExpr:
		c.expr(s.Cond)
		c.block(s.Then)
		c.stmt(s.Else)
	case *ast.MatchExpr:
		c.expr(s.Subject)
		for _, arm := range s.Arms {
			c.expr(arm.Guard)
			c.blockStmt(arm.Body)
		}
	case *ast.DeadlineExpr:
		c.expr(s.Duration)
		c.block(s.Body)
	}
}

func (c *crossTaskChecker) blockStmt(st ast.Stmt) {
	if b, ok := st.(*ast.Block); ok {
		c.block(b)
		return
	}
	c.stmt(st)
}

func (c *crossTaskChecker) expr(e ast.Expr) {
	switch x := e.(type) {
	case nil:
	case *ast.SpawnExpr:
		c.spawn(x)
		c.expr(x.Call)
	case *ast.MethodCallExpr:
		if x.Name == "send" && len(x.Args) == 1 {
			c.sendArg(x.Args[0].Expr)
		}
		c.expr(x.Recv)
		for _, a := range x.Args {
			c.expr(a.Expr)
		}
	case *ast.CallExpr:
		c.expr(x.Callee)
		for _, a := range x.Args {
			c.expr(a.Expr)
		}
	case *ast.FieldExpr:
		c.expr(x.Recv)
	case *ast.IndexExpr:
		c.expr(x.Recv)
		c.expr(x.Index)
	case *ast.SliceExpr:
		c.expr(x.Recv)
		c.expr(x.Lo)
		c.expr(x.Hi)
	case *ast.BorrowExpr:
		c.expr(x.X)
	case *ast.UnaryExpr:
		c.expr(x.X)
	case *ast.BinaryExpr:
		c.expr(x.L)
		c.expr(x.R)
	case *ast.NewExpr:
		for _, a := range x.Args {
			c.expr(a.Expr)
		}
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			c.expr(f.Value)
		}
	case *ast.ArrayLitExpr:
		for _, elem := range x.Elems {
			c.expr(elem)
		}
	case *ast.InterpExpr:
		for _, p := range x.Parts {
			c.expr(p.Expr)
		}
	case *ast.TryExpr:
		c.expr(x.X)
	case *ast.AwaitExpr:
		c.expr(x.X)
	case *ast.UnwrapExpr:
		c.expr(x.X)
	case *ast.LambdaExpr:
		c.block(x.Body)
	case *ast.IfExpr:
		c.stmt(x)
	case *ast.MatchExpr:
		c.stmt(x)
	case *ast.DeadlineExpr:
		c.stmt(x)
	}
}

// spawn checks the task result and every value the call captures. A call is
// the only spawn operand, so walking its callee and arguments is exact.
func (c *crossTaskChecker) spawn(x *ast.SpawnExpr) {
	if t := c.types.TypeOf(x); t != nil && !c.types.Send(t) {
		c.bag.Error(x.Pos(), diag.CodeCrossTask,
			"task result type cannot cross a task boundary",
			"use a sendable result type")
	}
	c.rejectBorrowArgs(x.Call)
	for _, name := range spawnNames(x.Call) {
		t := c.types.TypeOf(name)
		if t == nil {
			continue
		}
		if t.Kind() == types.FuncKind {
			if lam := c.lambdas[name.Name]; lam != nil {
				c.lambda(lam)
			}
			continue
		}
		if c.types.Send(t) {
			continue
		}
		c.bag.Error(name.Pos(), diag.CodeCrossTask,
			"this value cannot cross a task boundary",
			"keep it in this task, or move it into an arc<T>")
	}
	if call, ok := x.Call.(*ast.CallExpr); ok {
		switch callee := call.Callee.(type) {
		case *ast.LambdaExpr:
			c.lambda(callee)
		case *ast.NameExpr:
			if lam := c.lambdas[callee.Name]; lam != nil {
				c.lambda(lam)
			}
		}
	}
}

func (c *crossTaskChecker) rejectBorrowArgs(e ast.Expr) {
	if e == nil {
		return
	}
	if b, ok := e.(*ast.BorrowExpr); ok {
		if t := c.types.TypeOf(b); t != nil && !c.types.Send(t) {
			c.bag.Error(b.Pos(), diag.CodeCrossTask,
				"this borrow cannot cross a task boundary",
				"move an owned value into the task instead")
		}
		c.rejectBorrowArgs(b.X)
		return
	}
	switch x := e.(type) {
	case *ast.CallExpr:
		for _, a := range x.Args {
			c.rejectBorrowArgs(a.Expr)
		}
	case *ast.MethodCallExpr:
		c.rejectBorrowArgs(x.Recv)
		for _, a := range x.Args {
			c.rejectBorrowArgs(a.Expr)
		}
	case *ast.AggregateExpr:
		for _, f := range x.Fields {
			c.rejectBorrowArgs(f.Value)
		}
	case *ast.ArrayLitExpr:
		for _, elem := range x.Elems {
			c.rejectBorrowArgs(elem)
		}
	}
}

// lambda reports a closure value whose captures include a value that cannot
// move to another task. A capture is every name read but not declared locally.
func (c *crossTaskChecker) lambda(lam *ast.LambdaExpr) {
	declared := map[string]bool{}
	for _, p := range lam.Params {
		declared[p.Name] = true
	}
	collectDeclared(lam.Body, declared)
	seen := map[string]bool{}
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch x := e.(type) {
		case *ast.NameExpr:
			if declared[x.Name] || seen[x.Name] {
				return
			}
			t := c.types.TypeOf(x)
			if t == nil || c.types.Send(t) {
				return
			}
			seen[x.Name] = true
			c.bag.Error(x.Pos(), diag.CodeCrossTask,
				"this value cannot cross a task boundary",
				"keep it in this task, or move it into an arc<T>")
		case *ast.CallExpr:
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a.Expr)
			}
		case *ast.MethodCallExpr:
			walk(x.Recv)
			for _, a := range x.Args {
				walk(a.Expr)
			}
		case *ast.FieldExpr:
			walk(x.Recv)
		case *ast.IndexExpr:
			walk(x.Recv)
			walk(x.Index)
		case *ast.SliceExpr:
			walk(x.Recv)
			walk(x.Lo)
			walk(x.Hi)
		case *ast.BorrowExpr:
			walk(x.X)
		case *ast.UnaryExpr:
			walk(x.X)
		case *ast.BinaryExpr:
			walk(x.L)
			walk(x.R)
		case *ast.NewExpr:
			for _, a := range x.Args {
				walk(a.Expr)
			}
		case *ast.AggregateExpr:
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ast.ArrayLitExpr:
			for _, elem := range x.Elems {
				walk(elem)
			}
		case *ast.TryExpr:
			walk(x.X)
		case *ast.AwaitExpr:
			walk(x.X)
		case *ast.UnwrapExpr:
			walk(x.X)
		case *ast.InterpExpr:
			for _, p := range x.Parts {
				walk(p.Expr)
			}
		}
	}
	var walkStmt func(ast.Stmt)
	var walkBlock func(*ast.Block)
	walkBlock = func(b *ast.Block) {
		if b == nil {
			return
		}
		for _, st := range b.Stmts {
			walkStmt(st)
		}
	}
	walkStmt = func(st ast.Stmt) {
		switch s := st.(type) {
		case *ast.VarStmt:
			switch d := s.Decl.(type) {
			case *ast.VarDecl:
				walk(d.Value)
			case *ast.ConstDecl:
				walk(d.Value)
			}
		case *ast.AssignStmt:
			walk(s.Target)
			walk(s.Value)
		case *ast.ExprStmt:
			walk(s.Expr)
		case *ast.DeferStmt:
			walk(s.Call)
		case *ast.ReturnStmt:
			walk(s.Value)
		case *ast.ForStmt:
			walk(s.Iter)
			walkBlock(s.Body)
		case *ast.Block:
			walkBlock(s)
		case *ast.RegionStmt:
			walkBlock(s.Body)
		case *ast.ParallelStmt:
			walkBlock(s.Body)
		case *ast.SelectStmt:
			for _, arm := range s.Arms {
				walk(arm.Chan)
				walk(arm.Timeout)
				if b, ok := arm.Body.(*ast.Block); ok {
					walkBlock(b)
				} else {
					walkStmt(arm.Body)
				}
			}
		case *ast.IfExpr:
			walk(s.Cond)
			walkBlock(s.Then)
			walkStmt(s.Else)
		case *ast.MatchExpr:
			walk(s.Subject)
			for _, arm := range s.Arms {
				walk(arm.Guard)
				if b, ok := arm.Body.(*ast.Block); ok {
					walkBlock(b)
				} else {
					walkStmt(arm.Body)
				}
			}
		case *ast.DeadlineExpr:
			walk(s.Duration)
			walkBlock(s.Body)
		}
	}
	walkBlock(lam.Body)
}

func collectLambdas(b *ast.Block, out map[string]*ast.LambdaExpr) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		switch s := st.(type) {
		case *ast.VarStmt:
			if d, ok := s.Decl.(*ast.VarDecl); ok {
				if lam, ok := d.Value.(*ast.LambdaExpr); ok && d.Name != "" {
					out[d.Name] = lam
				}
			}
		case *ast.Block:
			collectLambdas(s, out)
		case *ast.IfExpr:
			collectLambdas(s.Then, out)
			if inner, ok := s.Else.(*ast.Block); ok {
				collectLambdas(inner, out)
			}
		case *ast.ForStmt:
			collectLambdas(s.Body, out)
		case *ast.RegionStmt:
			collectLambdas(s.Body, out)
		case *ast.ParallelStmt:
			collectLambdas(s.Body, out)
		case *ast.DeadlineExpr:
			collectLambdas(s.Body, out)
		}
	}
}

func collectDeclared(b *ast.Block, out map[string]bool) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		switch s := st.(type) {
		case *ast.VarStmt:
			switch d := s.Decl.(type) {
			case *ast.VarDecl:
				out[d.Name] = true
			case *ast.ConstDecl:
				out[d.Name] = true
			}
		case *ast.ForStmt:
			for _, name := range s.Names {
				out[name] = true
			}
			collectDeclared(s.Body, out)
		case *ast.Block:
			collectDeclared(s, out)
		case *ast.IfExpr:
			collectDeclared(s.Then, out)
			if inner, ok := s.Else.(*ast.Block); ok {
				collectDeclared(inner, out)
			}
		case *ast.MatchExpr:
			for _, arm := range s.Arms {
				if inner, ok := arm.Body.(*ast.Block); ok {
					collectDeclared(inner, out)
				}
			}
		case *ast.RegionStmt:
			collectDeclared(s.Body, out)
		case *ast.DeadlineExpr:
			collectDeclared(s.Body, out)
		}
	}
}

func (c *crossTaskChecker) sendArg(e ast.Expr) {
	switch x := e.(type) {
	case *ast.LambdaExpr:
		c.lambda(x)
		return
	case *ast.NameExpr:
		if lam := c.lambdas[x.Name]; lam != nil {
			c.lambda(lam)
			return
		}
	}
	t := c.types.TypeOf(e)
	if t == nil || c.types.Send(t) {
		return
	}
	c.bag.Error(e.Pos(), diag.CodeCrossTask,
		"this value cannot cross a task boundary",
		"keep it in this task, or move it into an arc<T>")
}

// spawnNames lists the names read by a spawn call. A method receiver and every
// argument are included; a nested call contributes its own names.
func spawnNames(e ast.Expr) []*ast.NameExpr {
	var out []*ast.NameExpr
	var walk func(ast.Expr, bool)
	walk = func(e ast.Expr, callee bool) {
		switch x := e.(type) {
		case *ast.NameExpr:
			// The function being called is code, not a captured value. Its
			// receiver, if any, still crosses through the FieldExpr case.
			if !callee {
				out = append(out, x)
			}
		case *ast.FieldExpr:
			walk(x.Recv, false)
		case *ast.IndexExpr:
			walk(x.Recv, false)
			walk(x.Index, false)
		case *ast.SliceExpr:
			walk(x.Recv, false)
			walk(x.Lo, false)
			walk(x.Hi, false)
		case *ast.BorrowExpr:
			walk(x.X, false)
		case *ast.CallExpr:
			walk(x.Callee, true)
			for _, a := range x.Args {
				walk(a.Expr, false)
			}
		case *ast.MethodCallExpr:
			walk(x.Recv, false)
			for _, a := range x.Args {
				walk(a.Expr, false)
			}
		case *ast.UnaryExpr:
			walk(x.X, false)
		case *ast.BinaryExpr:
			walk(x.L, false)
			walk(x.R, false)
		case *ast.NewExpr:
			for _, a := range x.Args {
				walk(a.Expr, false)
			}
		case *ast.AggregateExpr:
			for _, f := range x.Fields {
				walk(f.Value, false)
			}
		case *ast.ArrayLitExpr:
			for _, elem := range x.Elems {
				walk(elem, false)
			}
		case *ast.TryExpr:
			walk(x.X, false)
		case *ast.AwaitExpr:
			walk(x.X, false)
		case *ast.UnwrapExpr:
			walk(x.X, false)
		case *ast.InterpExpr:
			for _, p := range x.Parts {
				walk(p.Expr, false)
			}
		}
	}
	walk(e, true)
	return out
}
