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

// OwnsHeap reports whether a declared type carries a heap buffer that its owner
// must release: str, Array, Map, and Set. A struct or enum type is not included
// yet; that needs the field layout.
func OwnsHeap(t *Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ScalarType:
		return t.Name == "str"
	case NamedType:
		switch t.Name {
		case "Array", "Map", "Set":
			return true
		}
	}
	return false
}
