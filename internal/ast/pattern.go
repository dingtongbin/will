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
	Args    []*Pattern
	Alts    []*Pattern
}
