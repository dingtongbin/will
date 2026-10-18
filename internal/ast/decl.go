package ast

// Import is one import declaration. C selects import c "header"; SideEffect
// selects import _ "path".
type Import struct {
	Span
	Path       string
	SideEffect bool
	C          bool
	Links      []*Link
}

// Link is one name in an import c block.
type Link struct {
	Span
	Name     string
	Fast     bool
	Blocking bool
}

// ConstDecl declares a constant.
type ConstDecl struct {
	Span
	Doc   string
	Name  string
	Type  *Type
	Value Expr
}

func (*ConstDecl) declNode() {}

// VarDecl declares a variable. The same node is used for local declarations.
type VarDecl struct {
	Span
	Doc   string
	Name  string
	Type  *Type
	Value Expr
}

func (*VarDecl) declNode() {}

// FuncDecl declares a function or, when Recv is set, a method.
type FuncDecl struct {
	Span
	Doc        string
	Recv       *Receiver
	Name       string
	TypeParams []*TypeParam
	Params     []*Param
	Result     *Type
	Body       *Block
}

func (*FuncDecl) declNode() {}

// Receiver is the receiver of a method declaration.
type Receiver struct {
	Span
	Name string
	Type *Type
	Mut  bool
}

// Param is one function parameter.
type Param struct {
	Span
	Name     string
	Type     *Type
	Mut      bool
	Variadic bool
}

// TypeParam is one type parameter with its interface constraints.
type TypeParam struct {
	Span
	Name        string
	Constraints []*Type
}

// StructDecl declares a struct.
type StructDecl struct {
	Span
	Doc        string
	Name       string
	TypeParams []*TypeParam
	Fields     []*Field
}

func (*StructDecl) declNode() {}

// Field is one struct field.
type Field struct {
	Span
	Name    string
	Type    *Type
	Default Expr
}

// EnumDecl declares an enum.
type EnumDecl struct {
	Span
	Doc        string
	Name       string
	TypeParams []*TypeParam
	Variants   []*Variant
}

func (*EnumDecl) declNode() {}

// Variant is one enum variant with optional payload types.
type Variant struct {
	Span
	Name string
	Args []*Type
}

// InterfaceDecl declares an interface.
type InterfaceDecl struct {
	Span
	Doc        string
	Name       string
	TypeParams []*TypeParam
	Methods    []*MethodSig
}

func (*InterfaceDecl) declNode() {}

// MethodSig is one method signature inside an interface.
type MethodSig struct {
	Span
	Name   string
	Params []*Param
	Result *Type
}

// DeriveDecl asks the compiler to generate Json, Show, or Equal for a type.
type DeriveDecl struct {
	Span
	Doc  string
	Name string
	Type *Type
}

func (*DeriveDecl) declNode() {}

// LaneDecl marks one type as belonging to a lane.
type LaneDecl struct {
	Span
	Name string
	Decl Decl
}

func (*LaneDecl) declNode() {}
