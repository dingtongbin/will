// Package token defines the lexical tokens of will and their source text.
package token

import "github.com/will-lang/will/internal/source"

// Kind identifies a token class.
type Kind uint8

const (
	// Invalid is the zero kind and never appears in a scanned stream.
	Invalid Kind = iota
	// EOF ends every token stream.
	EOF
	// Newline separates statements outside grouping delimiters.
	Newline
	// Ident is an identifier.
	Ident
	// Int is an integer literal, including any type suffix.
	Int
	// Float is a floating-point literal, including any type suffix.
	Float
	// Duration is a duration literal such as 250ms.
	Duration
	// Char is a character literal.
	Char
	// String is a text literal.
	String
	// Bytes is a byte string literal.
	Bytes
	// FString is an interpolated string literal; Text holds the raw body.
	FString
	// DocComment is a documentation comment without its delimiters.
	DocComment

	// KwFunc introduces a function or method declaration.
	KwFunc
	// KwStruct introduces a struct declaration.
	KwStruct
	// KwEnum introduces an enum declaration.
	KwEnum
	// KwInterface introduces an interface declaration.
	KwInterface
	// KwConst introduces a constant declaration.
	KwConst
	// KwVar introduces a variable declaration.
	KwVar
	// KwImport introduces an import declaration.
	KwImport
	// KwDerive introduces a derive declaration.
	KwDerive
	// KwLane marks a type as belonging to a lane, and starts the lane.Here builtin.
	KwLane
	// KwReturn returns from the current function.
	KwReturn
	// KwIf introduces a conditional expression.
	KwIf
	// KwElse introduces the alternative branch of a conditional expression.
	KwElse
	// KwFor introduces a loop.
	KwFor
	// KwIn separates the binding from the iterable in a for loop.
	KwIn
	// KwMatch introduces a match expression.
	KwMatch
	// KwSpawn starts a task.
	KwSpawn
	// KwRegion opens a structured task region.
	KwRegion
	// KwParallel opens a CPU-parallel block.
	KwParallel
	// KwDeadline opens a scoped deadline.
	KwDeadline
	// KwSelect waits on several channel operations.
	KwSelect
	// KwDefer schedules a call at the end of the nearest block.
	KwDefer
	// KwBreak exits the nearest loop.
	KwBreak
	// KwContinue starts the next iteration of the nearest loop.
	KwContinue
	// KwTrue is the boolean true literal.
	KwTrue
	// KwFalse is the boolean false literal.
	KwFalse

	// Plus is +.
	Plus
	// Minus is -.
	Minus
	// Star is *.
	Star
	// Slash is /.
	Slash
	// Percent is %.
	Percent
	// PlusWrap is +%.
	PlusWrap
	// MinusWrap is -%.
	MinusWrap
	// StarWrap is *%.
	StarWrap
	// PlusSat is +|.
	PlusSat
	// MinusSat is -|.
	MinusSat
	// StarSat is *|.
	StarSat
	// Shl is <<.
	Shl
	// Shr is >>.
	Shr
	// ShlWrap is <<%.
	ShlWrap
	// Amp is &.
	Amp
	// Pipe is |.
	Pipe
	// Caret is ^.
	Caret
	// Tilde is ~.
	Tilde
	// AndAnd is &&.
	AndAnd
	// OrOr is ||.
	OrOr
	// Bang is !.
	Bang
	// Assign is =.
	Assign
	// AddAssign is +=.
	AddAssign
	// SubAssign is -=.
	SubAssign
	// MulAssign is *=.
	MulAssign
	// DivAssign is /=.
	DivAssign
	// ModAssign is %=.
	ModAssign
	// AndAssign is &=.
	AndAssign
	// OrAssign is |=.
	OrAssign
	// XorAssign is ^=.
	XorAssign
	// ShlAssign is <<=.
	ShlAssign
	// ShrAssign is >>=.
	ShrAssign
	// PlusWrapAssign is +%=.
	PlusWrapAssign
	// MinusWrapAssign is -%=.
	MinusWrapAssign
	// StarWrapAssign is *%=.
	StarWrapAssign
	// PlusSatAssign is +|=.
	PlusSatAssign
	// MinusSatAssign is -|=.
	MinusSatAssign
	// StarSatAssign is *|=.
	StarSatAssign
	// ShlWrapAssign is <<%=.
	ShlWrapAssign
	// Eq is ==.
	Eq
	// Ne is !=.
	Ne
	// Lt is <.
	Lt
	// Le is <=.
	Le
	// Gt is >.
	Gt
	// Ge is >=.
	Ge
	// DotDot is the range operator ...
	DotDot
	// DotDotDot is the variadic expansion operator ...
	DotDotDot
	// Dot is the selector operator.
	Dot
	// Comma separates list elements.
	Comma
	// Colon separates a name from its type or a field from its value.
	Colon
	// Question is the error propagation suffix.
	Question
	// Arrow precedes a return type.
	Arrow
	// FatArrow separates a pattern from its arm body.
	FatArrow
	// LParen is (.
	LParen
	// RParen is ).
	RParen
	// LBracket is [.
	LBracket
	// RBracket is ].
	RBracket
	// LBrace is {.
	LBrace
	// RBrace is }.
	RBrace
)

var names = map[Kind]string{
	Invalid:         "invalid",
	EOF:             "end of file",
	Newline:         "newline",
	Ident:           "identifier",
	Int:             "integer literal",
	Float:           "float literal",
	Duration:        "duration literal",
	Char:            "character literal",
	String:          "string literal",
	Bytes:           "byte string literal",
	FString:         "interpolated string literal",
	DocComment:      "documentation comment",
	KwFunc:          "func",
	KwStruct:        "struct",
	KwEnum:          "enum",
	KwInterface:     "interface",
	KwConst:         "const",
	KwVar:           "var",
	KwImport:        "import",
	KwDerive:        "derive",
	KwLane:          "lane",
	KwReturn:        "return",
	KwIf:            "if",
	KwElse:          "else",
	KwFor:           "for",
	KwIn:            "in",
	KwMatch:         "match",
	KwSpawn:         "spawn",
	KwRegion:        "region",
	KwParallel:      "parallel",
	KwDeadline:      "deadline",
	KwSelect:        "select",
	KwDefer:         "defer",
	KwBreak:         "break",
	KwContinue:      "continue",
	KwTrue:          "true",
	KwFalse:         "false",
	Plus:            "+",
	Minus:           "-",
	Star:            "*",
	Slash:           "/",
	Percent:         "%",
	PlusWrap:        "+%",
	MinusWrap:       "-%",
	StarWrap:        "*%",
	PlusSat:         "+|",
	MinusSat:        "-|",
	StarSat:         "*|",
	Shl:             "<<",
	Shr:             ">>",
	ShlWrap:         "<<%",
	Amp:             "&",
	Pipe:            "|",
	Caret:           "^",
	Tilde:           "~",
	AndAnd:          "&&",
	OrOr:            "||",
	Bang:            "!",
	Assign:          "=",
	AddAssign:       "+=",
	SubAssign:       "-=",
	MulAssign:       "*=",
	DivAssign:       "/=",
	ModAssign:       "%=",
	AndAssign:       "&=",
	OrAssign:        "|=",
	XorAssign:       "^=",
	ShlAssign:       "<<=",
	ShrAssign:       ">>=",
	PlusWrapAssign:  "+%=",
	MinusWrapAssign: "-%=",
	StarWrapAssign:  "*%=",
	PlusSatAssign:   "+|=",
	MinusSatAssign:  "-|=",
	StarSatAssign:   "*|=",
	ShlWrapAssign:   "<<%=",
	Eq:              "==",
	Ne:              "!=",
	Lt:              "<",
	Le:              "<=",
	Gt:              ">",
	Ge:              ">=",
	DotDot:          "..",
	DotDotDot:       "...",
	Dot:             ".",
	Comma:           ",",
	Colon:           ":",
	Question:        "?",
	Arrow:           "->",
	FatArrow:        "=>",
	LParen:          "(",
	RParen:          ")",
	LBracket:        "[",
	RBracket:        "]",
	LBrace:          "{",
	RBrace:          "}",
}

// String returns the spelling used in diagnostics.
func (k Kind) String() string {
	if s, ok := names[k]; ok {
		return s
	}
	return "token"
}

// Keywords maps reserved words to their token kind.
var Keywords = map[string]Kind{
	"func":      KwFunc,
	"struct":    KwStruct,
	"enum":      KwEnum,
	"interface": KwInterface,
	"const":     KwConst,
	"var":       KwVar,
	"import":    KwImport,
	"derive":    KwDerive,
	"lane":      KwLane,
	"return":    KwReturn,
	"if":        KwIf,
	"else":      KwElse,
	"for":       KwFor,
	"in":        KwIn,
	"match":     KwMatch,
	"spawn":     KwSpawn,
	"region":    KwRegion,
	"parallel":  KwParallel,
	"deadline":  KwDeadline,
	"select":    KwSelect,
	"defer":     KwDefer,
	"break":     KwBreak,
	"continue":  KwContinue,
	"true":      KwTrue,
	"false":     KwFalse,
}

// Token is one lexical token with its original spelling and decoded value.
type Token struct {
	Kind  Kind
	Text  string
	Value string
	Pos   source.Pos
	End   source.Pos
}

// Is reports whether t has kind k.
func (t Token) Is(k Kind) bool {
	return t.Kind == k
}

// IsKeyword reports whether k is a strict keyword.
func (k Kind) IsKeyword() bool {
	return k >= KwFunc && k <= KwFalse
}

// Describe returns the token spelling for an unexpected-token message.
func (t Token) Describe() string {
	if t.Kind == Ident || t.Kind == Int || t.Kind == Float || t.Kind == Duration {
		return t.Text
	}
	if t.Kind == EOF {
		return "end of file"
	}
	return t.Kind.String()
}
