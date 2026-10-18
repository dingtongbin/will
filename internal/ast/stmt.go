package ast

// Block is a brace-delimited statement list. When the last statement is an
// ExprStmt, that expression is the block's value.
type Block struct {
	Span
	Stmts []Stmt
}

func (*Block) stmtNode() {}

// VarStmt is a local const or var declaration.
type VarStmt struct {
	Span
	Decl Decl
}

func (*VarStmt) stmtNode() {}

// AssignStmt is an assignment. Op is the source spelling of the operator.
type AssignStmt struct {
	Span
	Target Expr
	Op     string
	Value  Expr
}

func (*AssignStmt) stmtNode() {}

// ExprStmt is a call or spawn used as a statement.
type ExprStmt struct {
	Span
	Expr Expr
}

func (*ExprStmt) stmtNode() {}

// DeferStmt schedules one call at the end of the nearest block.
type DeferStmt struct {
	Span
	Call Expr
}

func (*DeferStmt) stmtNode() {}

// ReturnStmt returns from the current function. Value is nil for a bare return.
type ReturnStmt struct {
	Span
	Value Expr
}

func (*ReturnStmt) stmtNode() {}

// BreakStmt exits the nearest loop.
type BreakStmt struct {
	Span
}

func (*BreakStmt) stmtNode() {}

// ContinueStmt starts the next iteration of the nearest loop.
type ContinueStmt struct {
	Span
}

func (*ContinueStmt) stmtNode() {}

// ForStmt is a counted, infinite, or iterator loop.
type ForStmt struct {
	Span
	Names []string
	Iter  Expr
	Body  *Block
}

func (*ForStmt) stmtNode() {}

// RegionStmt opens a structured task region.
type RegionStmt struct {
	Span
	Body *Block
}

func (*RegionStmt) stmtNode() {}

// ParallelStmt opens a CPU-parallel block.
type ParallelStmt struct {
	Span
	Body *Block
}

func (*ParallelStmt) stmtNode() {}

// SelectStmt waits on several channel operations.
type SelectStmt struct {
	Span
	Arms []*SelectArm
}

func (*SelectStmt) stmtNode() {}

// SelectArm is one arm of a select statement.
type SelectArm struct {
	Span
	Bind    string
	Chan    Expr
	Timeout Expr
	Default bool
	Body    Stmt
}

// MatchArm is one arm of a match expression.
type MatchArm struct {
	Span
	Pattern *Pattern
	Guard   Expr
	Body    Stmt
}
