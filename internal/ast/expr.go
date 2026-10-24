package ast

import (
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/token"
)

// NameExpr is an identifier.
type NameExpr struct {
	Span
	Name string
}

func (*NameExpr) exprNode() {}

// LiteralExpr is a literal. Text is the source spelling and Value is the
// decoded value for quoted literals.
type LiteralExpr struct {
	Span
	Kind  token.Kind
	Text  string
	Value string
}

func (*LiteralExpr) exprNode() {}

// CallArg is one call argument. Spread marks trailing variadic expansion.
type CallArg struct {
	Span
	Expr   Expr
	Spread bool
}

// CallExpr calls a function or converts a value with T(x).
type CallExpr struct {
	Span
	Callee   Expr
	TypeArgs []*Type
	Args     []*CallArg
}

func (*CallExpr) exprNode() {}

// MethodCallExpr calls a method.
type MethodCallExpr struct {
	Span
	Recv     Expr
	Name     string
	NamePos  source.Pos
	TypeArgs []*Type
	Args     []*CallArg
	// Link is the import c declaration this call names, when the receiver is
	// the c qualifier. The checker uses its prototype.
	Link *Link
}

func (*MethodCallExpr) exprNode() {}

// FieldExpr reads a field.
type FieldExpr struct {
	Span
	Recv    Expr
	Name    string
	NamePos source.Pos
}

func (*FieldExpr) exprNode() {}

// IndexExpr reads one element.
type IndexExpr struct {
	Span
	Recv  Expr
	Index Expr
}

func (*IndexExpr) exprNode() {}

// SliceExpr reads a half-open range of elements.
type SliceExpr struct {
	Span
	Recv Expr
	Lo   Expr
	Hi   Expr
}

func (*SliceExpr) exprNode() {}

// AggregateField is one name: value pair in an aggregate literal.
type AggregateField struct {
	Span
	Name  string
	Value Expr
}

// AggregateExpr constructs a named type with an aggregate literal.
type AggregateExpr struct {
	Span
	TypeName string
	TypeArgs []*Type
	Fields   []*AggregateField
}

func (*AggregateExpr) exprNode() {}

// NewExpr constructs a heap value.
type NewExpr struct {
	Span
	Type *Type
	Args []*CallArg
}

func (*NewExpr) exprNode() {}

// UnaryExpr applies a prefix operator.
type UnaryExpr struct {
	Span
	Op token.Kind
	X  Expr
}

func (*UnaryExpr) exprNode() {}

// BinaryExpr applies a binary operator.
type BinaryExpr struct {
	Span
	Op token.Kind
	L  Expr
	R  Expr
}

func (*BinaryExpr) exprNode() {}

// RangeExpr is a half-open range.
type RangeExpr struct {
	Span
	Lo Expr
	Hi Expr
}

func (*RangeExpr) exprNode() {}

// LambdaExpr is a function literal.
type LambdaExpr struct {
	Span
	Params []*Param
	Result *Type
	Body   *Block
}

func (*LambdaExpr) exprNode() {}

// TryExpr propagates an error with the ? suffix.
type TryExpr struct {
	Span
	X Expr
}

func (*TryExpr) exprNode() {}

// UnwrapExpr unwraps a Result or Option with the ! suffix.
type UnwrapExpr struct {
	Span
	X Expr
}

func (*UnwrapExpr) exprNode() {}

// AwaitExpr explicitly waits for one async call or built-in wait operation.
type AwaitExpr struct {
	Span
	X Expr
}

func (*AwaitExpr) exprNode() {}

// SpawnExpr starts one task. Place is "auto", "here", or "on"; Lane is set
// only when Place is "on".
type SpawnExpr struct {
	Span
	Place string
	Lane  string
	Call  Expr
}

func (*SpawnExpr) exprNode() {}

// DeadlineExpr runs a block under a duration deadline. It is also a statement.
type DeadlineExpr struct {
	Span
	Duration Expr
	Body     *Block
}

func (*DeadlineExpr) exprNode() {}
func (*DeadlineExpr) stmtNode() {}

// IfExpr is a conditional expression. It is also a statement.
type IfExpr struct {
	Span
	Cond Expr
	Then *Block
	Else Stmt
}

func (*IfExpr) exprNode() {}
func (*IfExpr) stmtNode() {}

// MatchExpr is a match expression. It is also a statement.
type MatchExpr struct {
	Span
	Subject Expr
	Arms    []*MatchArm
}

func (*MatchExpr) exprNode() {}
func (*MatchExpr) stmtNode() {}

// UnitExpr is the empty value ().
type UnitExpr struct {
	Span
}

func (*UnitExpr) exprNode() {}

// InterpPart is one part of an interpolated string. Exactly one of Lit and
// Expr is set.
type InterpPart struct {
	Span
	Lit  string
	Expr Expr
}

// InterpExpr is an interpolated string literal.
type InterpExpr struct {
	Span
	Parts []InterpPart
}

func (*InterpExpr) exprNode() {}

// LaneHereExpr is the built-in lane.Here() expression.
type LaneHereExpr struct {
	Span
}

func (*LaneHereExpr) exprNode() {}
