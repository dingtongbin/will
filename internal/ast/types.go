package ast

import "strings"

// TypeKind classifies a type expression.
type TypeKind uint8

const (
	// ScalarType is a built-in scalar type such as i32 or str.
	ScalarType TypeKind = iota
	// NamedType is a named type, optionally with type arguments.
	NamedType
	// ArrayType is a fixed-length array [N]T.
	ArrayType
	// ViewType is a view []T; ViewMut distinguishes mut []T.
	ViewType
	// FuncType is a function type func(T, U) -> V.
	FuncType
	// UnitType is the empty type ().
	UnitType
)

// BorrowKind records the explicit borrow wrapper of a type.
type BorrowKind uint8

const (
	// OwnedBorrow is a value that carries its own storage.
	OwnedBorrow BorrowKind = iota
	// RefBorrow is a shared borrow ref T.
	RefBorrow
	// MutBorrow is an exclusive borrow mut T.
	MutBorrow
)

// Type is a type expression. Borrow wraps the underlying Kind: ref T and
// mut T keep the kind of T and record the borrow in Borrow.
type Type struct {
	Span
	Kind    TypeKind
	Borrow  BorrowKind
	ViewMut bool
	Name    string
	Args    []*Type
	Elem    *Type
	Len     string
	Params  []*Type
	Result  *Type
}

// IsRef reports whether the type is ref T.
func (t *Type) IsRef() bool {
	return t != nil && t.Borrow == RefBorrow
}

// IsMutRef reports whether the type is mut T.
func (t *Type) IsMutRef() bool {
	return t != nil && t.Borrow == MutBorrow
}

// Unref returns the type without its borrow wrapper. The borrow itself stays
// in the returned type's ViewMut flag when the wrapped type is a view.
func Unref(t *Type) *Type {
	if t == nil || t.Borrow == OwnedBorrow {
		return t
	}
	u := *t
	u.Borrow = OwnedBorrow
	return &u
}

// TypeText renders a type in the canonical spelling the intermediate format
// uses: no space after a comma.
func TypeText(t *Type) string {
	if t == nil {
		return ""
	}
	base := typeBaseText(t)
	switch t.Borrow {
	case RefBorrow:
		return "ref " + base
	case MutBorrow:
		return "mut " + base
	}
	return base
}

func typeBaseText(t *Type) string {
	switch t.Kind {
	case ScalarType:
		return t.Name
	case NamedType:
		if len(t.Args) == 0 {
			return t.Name
		}
		parts := make([]string, len(t.Args))
		for i, a := range t.Args {
			parts[i] = TypeText(a)
		}
		return t.Name + "<" + strings.Join(parts, ",") + ">"
	case ArrayType:
		return "[" + t.Len + "]" + TypeText(t.Elem)
	case ViewType:
		if t.ViewMut {
			return "mut []" + TypeText(t.Elem)
		}
		return "[]" + TypeText(t.Elem)
	case FuncType:
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = TypeText(p)
		}
		out := "func(" + strings.Join(parts, ",") + ")"
		if t.Result != nil {
			out += "->" + TypeText(t.Result)
		}
		return out
	case UnitType:
		return "()"
	}
	return ""
}

// OwnsHeap reports whether a declared type carries a heap buffer that its owner
// must release: str, Array, Map, and Set. A struct or enum type is not included
// yet; that needs the field layout.
// NeedsRelease reports whether a declared type owns something the compiler must
// release at the end of its scope. An rc handle owns one reference; a weak
// handle owns nothing.
func NeedsRelease(t *Type) bool {
	if t == nil {
		return false
	}
	if t.Borrow != OwnedBorrow || t.Kind == ViewType {
		return false
	}
	if OwnsHeap(t) {
		return true
	}
	if t.Kind == FuncType {
		// A closure value owns its heap environment; a plain function value has
		// an empty environment and the same release is a no-op.
		return true
	}
	if t.Kind != NamedType {
		return false
	}
	switch t.Name {
	case "weak_rc", "weak_arc", "Duration", "Value", "Show", "Task":
		return false
	}
	// A named struct, enum, Option, Result, or rc may hold an rc field; the
	// release lowering walks the value and ignores the ones that hold none.
	return true
}

func OwnsHeap(t *Type) bool {
	if t == nil || t.Borrow != OwnedBorrow || t.Kind == ViewType {
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
