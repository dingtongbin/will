package check

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

// RcSlots reports M0003 for a direct write to an rc<> field after the same
// handle has crossed into another task. The proof is deliberately local: the
// capture and the write must share one lexical block, and the block must not
// join the task or exit its region before the write. Shapes outside that proof
// stay unreported rather than guessing.
func RcSlots(files []*ast.File, own *Ownership) []diag.Diagnostic {
	c := &rcSlotChecker{own: own, seen: map[*ast.AssignStmt]bool{}}
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			c.funcDecl(fn)
		}
	}
	return c.bag.Diagnostics()
}

type rcSlotChecker struct {
	own  *Ownership
	bag  diag.Bag
	seen map[*ast.AssignStmt]bool
	// scopes maps a name to its declared type, innermost last.
	scopes []map[string]*ast.Type
	// shared counts captures in the current block. A second capture keeps the
	// root shared until every task that holds it is joined.
	shared map[string]int
	tasks  map[string][]string
}

func (c *rcSlotChecker) funcDecl(fn *ast.FuncDecl) {
	c.scopes = []map[string]*ast.Type{{}}
	if fn.Recv != nil {
		c.declare(fn.Recv.Name, fn.Recv.Type)
	}
	for _, p := range fn.Params {
		c.declare(p.Name, p.Type)
	}
	c.block(fn.Body)
}

func (c *rcSlotChecker) push() {
	c.scopes = append(c.scopes, map[string]*ast.Type{})
}

func (c *rcSlotChecker) pop() {
	c.scopes = c.scopes[:len(c.scopes)-1]
}

func (c *rcSlotChecker) declare(name string, typ *ast.Type) {
	if name == "" || name == "_" {
		return
	}
	c.scopes[len(c.scopes)-1][name] = typ
}

func (c *rcSlotChecker) typeOf(name string) *ast.Type {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if typ, ok := c.scopes[i][name]; ok {
			return typ
		}
	}
	return nil
}

func isRcType(t *ast.Type) bool {
	return t != nil && t.Kind == ast.NamedType && t.Name == "rc" && len(t.Args) == 1
}

// block visits one statement list with a fresh capture set. Captures inside a
// nested block do not escape it, so a later write in an outer block is not
// reported on evidence the proof does not have.
func (c *rcSlotChecker) block(b *ast.Block) {
	if b == nil {
		return
	}
	savedShared := c.shared
	savedTasks := c.tasks
	c.shared = map[string]int{}
	c.tasks = map[string][]string{}
	c.push()
	for _, st := range b.Stmts {
		c.stmt(st)
	}
	c.pop()
	c.shared = savedShared
	c.tasks = savedTasks
}

func (c *rcSlotChecker) stmt(st ast.Stmt) {
	switch s := st.(type) {
	case nil:
	case *ast.Block:
		c.block(s)
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			c.expr(d.Value)
			if d.Name != "" && d.Name != "_" {
				delete(c.shared, d.Name)
			}
			c.declare(d.Name, d.Type)
			if spawn, ok := d.Value.(*ast.SpawnExpr); ok && d.Name != "" && d.Name != "_" {
				if roots := c.captureRoots(spawn); len(roots) != 0 {
					c.tasks[d.Name] = append(c.tasks[d.Name], roots...)
				}
			}
		case *ast.ConstDecl:
			c.expr(d.Value)
			c.declare(d.Name, d.Type)
		}
	case *ast.AssignStmt:
		c.expr(s.Value)
		c.assign(s)
	case *ast.ExprStmt:
		c.expr(s.Expr)
	case *ast.DeferStmt:
		c.expr(s.Call)
	case *ast.ReturnStmt:
		c.expr(s.Value)
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
			c.stmt(arm.Body)
		}
	case *ast.IfExpr:
		c.expr(s.Cond)
		c.block(s.Then)
		c.stmt(s.Else)
	case *ast.MatchExpr:
		c.expr(s.Subject)
		for _, arm := range s.Arms {
			c.expr(arm.Guard)
			c.stmt(arm.Body)
		}
	case *ast.DeadlineExpr:
		c.expr(s.Duration)
		c.block(s.Body)
	case *ast.BreakStmt, *ast.ContinueStmt:
	}
}

func (c *rcSlotChecker) assign(s *ast.AssignStmt) {
	if target, ok := s.Target.(*ast.NameExpr); ok {
		delete(c.shared, target.Name)
		return
	}
	fields, root, pos, ok := c.fieldWrite(s.Target)
	if !ok || c.shared[root] == 0 {
		return
	}
	rootType := c.typeOf(root)
	if !isRcType(rootType) {
		return
	}
	current := rootType.Args[0]
	for _, name := range fields {
		current = c.own.FieldType(current, name)
		if current == nil {
			return
		}
	}
	if !isRcType(current) || c.seen[s] {
		return
	}
	c.seen[s] = true
	c.bag.Add(diag.Diagnostic{
		Pos: pos, End: pos, Severity: diag.Error,
		Code:    diag.CodeRcSlotUnguarded,
		Message: "this rc slot is shared and mutable without a guard or an atomic",
		Help:    "protect it with a guard or an atomic",
	})
}

// fieldWrite returns the field names from the root through target, the root
// name, and the earliest field position. Indexing or a method receiver is not a
// direct slot write and stays outside the proof.
func (c *rcSlotChecker) fieldWrite(target ast.Expr) ([]string, string, source.Pos, bool) {
	var fields []string
	pos := target.Pos()
	for {
		switch x := target.(type) {
		case *ast.FieldExpr:
			fields = append(fields, x.Name)
			target = x.Recv
		case *ast.NameExpr:
			for i, j := 0, len(fields)-1; i < j; i, j = i+1, j-1 {
				fields[i], fields[j] = fields[j], fields[i]
			}
			return fields, x.Name, pos, len(fields) != 0
		default:
			return nil, "", source.Pos{}, false
		}
	}
}

// captureRoots lists the rc handles a spawn hands to another task. A Clone on
// the argument still shares the same object, so the source handle is the root.
func (c *rcSlotChecker) captureRoots(spawn *ast.SpawnExpr) []string {
	var roots []string
	if spawn == nil || spawn.Call == nil {
		return nil
	}
	call, ok := spawn.Call.(*ast.CallExpr)
	if !ok {
		return nil
	}
	for _, arg := range call.Args {
		if name, ok := c.captureRoot(arg.Expr); ok {
			roots = append(roots, name)
		}
	}
	return roots
}

func (c *rcSlotChecker) captureRoot(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.NameExpr:
		return x.Name, isRcType(c.typeOf(x.Name))
	case *ast.MethodCallExpr:
		if x.Name == "Clone" {
			return c.captureRoot(x.Recv)
		}
	}
	return "", false
}

func (c *rcSlotChecker) share(name string) {
	if name == "" || name == "_" {
		return
	}
	c.shared[name]++
}

func (c *rcSlotChecker) unshare(names []string) {
	for _, name := range names {
		if c.shared[name] <= 1 {
			delete(c.shared, name)
			continue
		}
		c.shared[name]--
	}
}

// expr records a send and a join in source order. Other expressions are walked
// so a spawn or send inside a call argument still contributes.
func (c *rcSlotChecker) expr(e ast.Expr) {
	switch x := e.(type) {
	case nil, *ast.NameExpr, *ast.LiteralExpr, *ast.UnitExpr, *ast.LaneHereExpr:
	case *ast.CallExpr:
		c.expr(x.Callee)
		for _, a := range x.Args {
			c.expr(a.Expr)
		}
	case *ast.MethodCallExpr:
		// A channel send only proves sharing when the channel itself reaches
		// another task. That escape proof is not available here, so a send
		// does not make the slot shared for M0003.
		if x.Name == "Join" {
			if task, ok := x.Recv.(*ast.NameExpr); ok {
				c.unshare(c.tasks[task.Name])
				delete(c.tasks, task.Name)
			}
		}
		c.expr(x.Recv)
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
	case *ast.RangeExpr:
		c.expr(x.Lo)
		c.expr(x.Hi)
	case *ast.LambdaExpr:
		c.block(x.Body)
	case *ast.TryExpr:
		c.expr(x.X)
	case *ast.AwaitExpr:
		c.expr(x.X)
	case *ast.UnwrapExpr:
		c.expr(x.X)
	case *ast.SpawnExpr:
		for _, root := range c.captureRoots(x) {
			c.share(root)
		}
		c.expr(x.Call)
	case *ast.IfExpr:
		c.expr(x.Cond)
		c.block(x.Then)
		c.stmt(x.Else)
	case *ast.MatchExpr:
		c.expr(x.Subject)
		for _, arm := range x.Arms {
			c.expr(arm.Guard)
			c.stmt(arm.Body)
		}
	case *ast.DeadlineExpr:
		c.expr(x.Duration)
		c.block(x.Body)
	case *ast.InterpExpr:
		for _, part := range x.Parts {
			c.expr(part.Expr)
		}
	}
}

func (c *rcSlotChecker) exprBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		c.exprStmt(st)
	}
}

func (c *rcSlotChecker) exprStmt(st ast.Stmt) {
	switch s := st.(type) {
	case *ast.ExprStmt:
		c.expr(s.Expr)
	case *ast.ReturnStmt:
		c.expr(s.Value)
	case *ast.VarStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			c.expr(d.Value)
		case *ast.ConstDecl:
			c.expr(d.Value)
		}
	case *ast.AssignStmt:
		c.expr(s.Value)
	case *ast.Block:
		c.exprBlock(s)
	case *ast.IfExpr:
		c.expr(s)
	}
}
