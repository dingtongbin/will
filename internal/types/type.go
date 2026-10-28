// Package types checks the types of one resolved file and reports the type
// diagnostics from the code table.
package types

import (
	"strings"

	"github.com/will-lang/will/internal/ast"
)

// Kind classifies a checked type.
type Kind uint8

const (
	// InvalidKind is an unknown type; unknown matches anything so that one
	// error does not cascade.
	InvalidKind Kind = iota
	// ScalarKind is a built-in scalar type.
	ScalarKind
	// NamedKind is a declared or predeclared named type.
	NamedKind
	// ArrayKind is a fixed-length array.
	ArrayKind
	// ViewKind is a read-only view.
	ViewKind
	// FuncKind is a function type.
	FuncKind
	// UnitKind is the empty type.
	UnitKind
	// RangeKind is the type of a Range(lo, hi) expression.
	RangeKind
)

// Type is one interned checked type. Two types are equal exactly when they
// have the same id, so equality is a pointer-sized comparison and the
// canonical text is computed once.
type Type struct {
	id      int
	text    string
	kind    Kind
	borrow  ast.BorrowKind
	viewMut bool
	class   bool
	name    string
	args    []*Type
	elem    *Type
	length  string
	params  []*Type
	result  *Type
}

// ID returns the interning id.
func (t *Type) ID() int {
	if t == nil {
		return 0
	}
	return t.id
}

// String returns the canonical type text.
func (t *Type) String() string {
	if t == nil {
		return "?"
	}
	return t.text
}

// Kind returns the type kind.
func (t *Type) Kind() Kind {
	if t == nil {
		return InvalidKind
	}
	return t.kind
}

// IsClass reports whether the named type is a GC class reference.
func (t *Type) IsClass() bool {
	return t != nil && t.class
}

// Name returns the scalar or named type name.
func (t *Type) Name() string {
	if t == nil {
		return ""
	}
	return t.name
}

// Borrow returns the explicit borrow wrapper of the type.
func (t *Type) Borrow() ast.BorrowKind {
	if t == nil {
		return ast.OwnedBorrow
	}
	return t.borrow
}

// IsRef reports whether the type is ref T.
func (t *Type) IsRef() bool {
	return t != nil && t.borrow == ast.RefBorrow
}

// IsMutRef reports whether the type is mut T.
func (t *Type) IsMutRef() bool {
	return t != nil && t.borrow == ast.MutBorrow
}

// ViewMut reports whether a view type is mut []T.
func (t *Type) ViewMut() bool {
	return t != nil && t.kind == ViewKind && t.viewMut
}

// Unref returns the type without a ref or mut wrapper.
func (t *Type) Unref() *Type {
	if t == nil || t.borrow == ast.OwnedBorrow {
		return t
	}
	u := *t
	u.borrow = ast.OwnedBorrow
	u.text = strings.TrimPrefix(strings.TrimPrefix(t.text, "ref "), "mut ")
	return &u
}

// Args returns the type arguments.
func (t *Type) Args() []*Type { return t.args }

// Elem returns the element type of an array or view.
func (t *Type) Elem() *Type { return t.elem }

// Length returns the declared length text of a fixed array.
func (t *Type) Length() string { return t.length }

// Params returns the parameter types of a function type.
func (t *Type) Params() []*Type { return t.params }

// Result returns the result type of a function type.
func (t *Type) Result() *Type { return t.result }

// Equal reports whether a and b are the same type. Interned types compare by
// id; a partially known type (a nil argument, or one with no arguments
// recorded) matches anything in that position, so one unknown type does not
// produce a second diagnostic.
func Equal(a, b *Type) bool {
	if a == nil || b == nil || a.kind == InvalidKind || b.kind == InvalidKind {
		return true
	}
	if a == b {
		return true
	}
	if a.kind != b.kind || a.borrow != b.borrow || a.viewMut != b.viewMut ||
		a.name != b.name || a.length != b.length {
		return false
	}
	if len(a.args) > 0 && len(b.args) > 0 {
		if len(a.args) != len(b.args) {
			return false
		}
		for i := range a.args {
			if !Equal(a.args[i], b.args[i]) {
				return false
			}
		}
	}
	if !Equal(a.elem, b.elem) {
		return false
	}
	if len(a.params) > 0 && len(b.params) > 0 {
		if len(a.params) != len(b.params) {
			return false
		}
		for i := range a.params {
			if !Equal(a.params[i], b.params[i]) {
				return false
			}
		}
	}
	return Equal(a.result, b.result)
}

// IsInteger reports whether t is an integer scalar.
func (t *Type) IsInteger() bool {
	if t == nil || t.kind != ScalarKind {
		return false
	}
	_, _, ok := intWidth(t.name)
	return ok
}

// IsNumeric reports whether t is an integer or floating-point scalar.
func (t *Type) IsNumeric() bool {
	if t == nil {
		return false
	}
	return t.IsInteger() || t.kind == ScalarKind && (t.name == "f32" || t.name == "f64")
}

func isBool(t *Type) bool {
	return t != nil && t.kind == ScalarKind && t.name == "bool"
}

// builtinArity maps the predeclared generic type names to their arity.
var builtinArity = map[string]int{
	"Array": 1, "Set": 1, "Map": 2,
	"chan": 1, "rc": 1, "arc": 1, "weak_rc": 1, "weak_arc": 1, "Task": 1,
	"Option": 1, "Result": 2,
}

var scalarNames = []string{
	"bool", "str", "raw",
	"i8", "i16", "i32", "i64", "isize",
	"u8", "u16", "u32", "u64", "usize",
	"f32", "f64",
}

func isScalarName(name string) bool {
	for _, s := range scalarNames {
		if s == name {
			return true
		}
	}
	return false
}

// interner dedupes types by canonical text and assigns ids.
type interner struct {
	byText map[string]*Type
}

func newInterner() *interner {
	return &interner{byText: map[string]*Type{}}
}

func (in *interner) intern(t *Type) *Type {
	if prev, ok := in.byText[t.text]; ok {
		return prev
	}
	t.id = len(in.byText) + 1
	in.byText[t.text] = t
	return t
}

func (in *interner) scalar(name string) *Type {
	return in.intern(&Type{text: name, kind: ScalarKind, name: name})
}

func (in *interner) named(name string, args []*Type) *Type {
	return in.namedWithClass(name, args, false)
}

func (in *interner) namedClass(name string, args []*Type) *Type {
	return in.namedWithClass(name, args, true)
}

func (in *interner) namedWithClass(name string, args []*Type, class bool) *Type {
	text := name
	if len(args) > 0 {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.String()
		}
		text = name + "<" + strings.Join(parts, ",") + ">"
	}
	return in.intern(&Type{text: text, kind: NamedKind, class: class, name: name, args: args})
}

func (in *interner) array(length string, elem *Type) *Type {
	return in.intern(&Type{text: "[" + length + "]" + elem.String(), kind: ArrayKind, elem: elem, length: length})
}

func (in *interner) view(elem *Type, mut ...bool) *Type {
	isMut := len(mut) > 0 && mut[0]
	text := "[]" + elem.String()
	if isMut {
		text = "mut " + text
	}
	return in.intern(&Type{text: text, kind: ViewKind, viewMut: isMut, elem: elem})
}

func (in *interner) borrowed(t *Type, kind ast.BorrowKind) *Type {
	if t == nil || kind == ast.OwnedBorrow {
		return t
	}
	prefix := "ref "
	if kind == ast.MutBorrow {
		prefix = "mut "
	}
	return in.intern(&Type{
		text:   prefix + t.text,
		kind:   t.kind,
		borrow: kind,
		class:  t.class,
		name:   t.name,
		args:   t.args,
		elem:   t.elem,
		length: t.length,
		params: t.params,
		result: t.result,
	})
}

func (in *interner) funcT(params []*Type, result *Type) *Type {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p.String()
	}
	text := "func(" + strings.Join(parts, ",") + ")"
	if result != nil {
		text += "->" + result.String()
	}
	return in.intern(&Type{text: text, kind: FuncKind, params: params, result: result})
}

func (in *interner) unit() *Type {
	return in.intern(&Type{text: "()", kind: UnitKind})
}

func (in *interner) rangeT() *Type {
	return in.intern(&Type{text: "range", kind: RangeKind})
}

// builtin reports the checked type of a predeclared type name.
func (in *interner) builtin(name string) *Type {
	if isScalarName(name) {
		return in.scalar(name)
	}
	switch name {
	case "Value", "Duration", "Show", "Error", "TaskError":
		return in.named(name, nil)
	}
	return nil
}

// receiverName returns the simple name of a receiver type.
func receiverName(t *ast.Type) string {
	if t == nil {
		return ""
	}
	return t.Name
}
