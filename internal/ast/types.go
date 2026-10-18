package ast

// TypeKind classifies a type expression.
type TypeKind uint8

const (
	// ScalarType is a built-in scalar type such as i32 or str.
	ScalarType TypeKind = iota
	// NamedType is a named type, optionally with type arguments.
	NamedType
	// ArrayType is a fixed-length array [N]T.
	ArrayType
	// ViewType is a read-only view []T.
	ViewType
	// FuncType is a function type func(T, U) -> V.
	FuncType
	// UnitType is the empty type ().
	UnitType
)

// Type is a type expression.
type Type struct {
	Span
	Kind   TypeKind
	Name   string
	Args   []*Type
	Elem   *Type
	Len    string
	Params []*Type
	Result *Type
}
