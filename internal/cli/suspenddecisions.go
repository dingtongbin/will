package cli

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/decisions"
)

// collectFrames writes the frame record for the narrow loop + Recv shape the
// state-machine lowerer accepts. The state word is always at offset zero; the
// remaining fields follow the frame layout used by gen.statemachine.
func collectFrames(u sourceUnit, out *[]decisions.Record) {
	for _, d := range u.file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !hasRecvLoop(fn.Body) {
			continue
		}
		name := qualifiedName(u.prefix, fn.Name)
		*out = append(*out, decisions.Frame(name, 24))
		*out = append(*out, decisions.FrameField(name, "state", 0, "u32"))
		*out = append(*out, decisions.FrameField(name, "channel", 8, "ptr"))
		*out = append(*out, decisions.FrameField(name, "accumulator", 16, "i32"))
		*out = append(*out, decisions.FrameField(name, "payload", 20, "i32"))
	}
}

func hasRecvLoop(b *ast.Block) bool {
	if b == nil {
		return false
	}
	for _, st := range b.Stmts {
		if hasRecvStmt(st) {
			return true
		}
	}
	return false
}

func hasRecvStmt(st ast.Stmt) bool {
	switch s := st.(type) {
	case *ast.ForStmt:
		if blockHasRecv(s.Body) {
			return true
		}
		return hasRecvStmt(s.Body)
	case *ast.IfExpr:
		if blockHasRecv(s.Then) || hasRecvStmt(s.Else) {
			return true
		}
	case *ast.MatchExpr:
		for _, arm := range s.Arms {
			if hasRecvStmt(arm.Body) {
				return true
			}
		}
	case *ast.RegionStmt:
		return hasRecvLoop(s.Body)
	case *ast.ParallelStmt:
		return hasRecvLoop(s.Body)
	}
	return false
}

func blockHasRecv(b *ast.Block) bool {
	if b == nil {
		return false
	}
	for _, st := range b.Stmts {
		if recvExprInStmt(st) {
			return true
		}
	}
	return false
}

func recvExprInStmt(st ast.Stmt) bool {
	switch s := st.(type) {
	case *ast.ExprStmt:
		return recvInExpr(s.Expr)
	case *ast.VarStmt:
		if v, ok := s.Decl.(*ast.VarDecl); ok {
			return recvInExpr(v.Value)
		}
		if c, ok := s.Decl.(*ast.ConstDecl); ok {
			return recvInExpr(c.Value)
		}
	case *ast.AssignStmt:
		return recvInExpr(s.Value)
	case *ast.MatchExpr:
		for _, arm := range s.Arms {
			if recvExprInStmt(arm.Body) {
				return true
			}
		}
	case *ast.IfExpr:
		return blockHasRecv(s.Then) || recvExprInStmt(s.Else)
	}
	return false
}

func recvInExpr(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ast.MethodCallExpr:
		if x.Name == "Recv" {
			return true
		}
		if recvInExpr(x.Recv) {
			return true
		}
		for _, a := range x.Args {
			if recvInExpr(a.Expr) {
				return true
			}
		}
	case *ast.CallExpr:
		for _, a := range x.Args {
			if recvInExpr(a.Expr) {
				return true
			}
		}
	case *ast.AwaitExpr:
		return recvInExpr(x.X)
	case *ast.UnwrapExpr:
		return recvInExpr(x.X)
	case *ast.TryExpr:
		return recvInExpr(x.X)
	}
	return false
}
