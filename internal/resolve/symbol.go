// Package resolve binds names in one file to their declarations and reports
// the name diagnostics from the code table. Package-wide rules wait for the
// package layer.
package resolve

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/source"
)

// Kind classifies a declaration.
type Kind uint8

const (
	// PredeclaredTypeKind is a predeclared type name.
	PredeclaredTypeKind Kind = iota
	// PredeclaredValueKind is a predeclared value name.
	PredeclaredValueKind
	// ImportKind is one imported package name.
	ImportKind
	// CLinkKind is one name declared by import c.
	CLinkKind
	// ConstKind is a constant.
	ConstKind
	// VarKind is a variable.
	VarKind
	// FuncKind is a free function.
	FuncKind
	// MethodKind is a method; RecvType holds the receiver type name.
	MethodKind
	// StructKind is a struct type.
	StructKind
	// EnumKind is an enum type.
	EnumKind
	// VariantKind is one enum variant.
	VariantKind
	// InterfaceKind is an interface type.
	InterfaceKind
	// TypeParamKind is one type parameter.
	TypeParamKind
	// ParamKind is a function parameter.
	ParamKind
	// ReceiverKind is a method receiver.
	ReceiverKind
	// LocalKind is a local binding.
	LocalKind
	// LaneKind is a lane name.
	LaneKind
)

func (k Kind) isType() bool {
	switch k {
	case PredeclaredTypeKind, StructKind, EnumKind, InterfaceKind, TypeParamKind:
		return true
	}
	return false
}

// IsType reports whether the symbol denotes a type.
func (s *Symbol) IsType() bool { return s.Kind.isType() }

// Symbol is one declared name.
type Symbol struct {
	Name        string
	Kind        Kind
	Pos         source.Pos
	Node        ast.Node
	RecvType    string
	Used        bool
	Exported    bool
	Predeclared bool
}

// Scope is one lexical scope. A nil parent is the package scope.
type scope struct {
	parent *scope
	names  map[string]*Symbol
	order  []*Symbol
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, names: map[string]*Symbol{}}
}

func (s *scope) lookup(name string) (*Symbol, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if sym, ok := cur.names[name]; ok {
			return sym, true
		}
	}
	return nil, false
}

// define inserts sym and returns the symbol it replaced, if any.
func (s *scope) define(sym *Symbol) *Symbol {
	prev := s.names[sym.Name]
	s.names[sym.Name] = sym
	if prev == nil {
		s.order = append(s.order, sym)
	}
	return prev
}

// Result carries the resolved names of one file.
type Result struct {
	File    *ast.File
	Package *scope
	// Refs maps a name occurrence to the symbol it denotes.
	Refs    map[ast.Node]*Symbol
	Symbols []*Symbol
}
