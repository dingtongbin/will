package ast

// PatternKind classifies a match pattern.
type PatternKind uint8

const (
	// WildPattern is _.
	WildPattern PatternKind = iota
	// BindPattern introduces one name.
	BindPattern
	// LiteralPattern matches one literal.
	LiteralPattern
	// RangePattern matches a half-open literal range.
	RangePattern
	// VariantPattern matches an enum variant and its payload.
	VariantPattern
	// OrPattern matches any of its alternatives.
	OrPattern
)

// Pattern is one match pattern.
type Pattern struct {
	Span
	Kind    PatternKind
	Name    string
	Literal *LiteralExpr
	Lo      *LiteralExpr
	Hi      *LiteralExpr
	Args    []*Pattern
	Alts    []*Pattern
}
