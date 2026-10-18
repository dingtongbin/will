// Package ast defines the syntax tree shared by the parser and every stage
// after it. Node shapes follow the node table in the intermediate format.
package ast

import "github.com/will-lang/will/internal/source"

// Span is the source range of a node.
type Span struct {
	Start source.Pos
	End   source.Pos
}

// Pos returns the first position of the node.
func (s Span) Pos() source.Pos { return s.Start }

// EndPos returns the position just past the node.
func (s Span) EndPos() source.Pos { return s.End }

// Node is any syntax tree node with a source range.
type Node interface {
	Pos() source.Pos
	EndPos() source.Pos
}

// Decl is a top-level declaration.
type Decl interface {
	Node
	declNode()
}

// Stmt is a statement or a construct usable as a statement.
type Stmt interface {
	Node
	stmtNode()
}

// Expr is an expression.
type Expr interface {
	Node
	exprNode()
}

// File is one parsed source file.
type File struct {
	Span
	Imports []*Import
	Decls   []Decl
}
