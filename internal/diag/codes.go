package diag

import "github.com/will-lang/will/internal/source"

// Lexical diagnostic codes. The code table is the source of truth and these
// values never change meaning once published.
const (
	// CodeNonASCII reports a byte that is not printable ASCII, space, tab, or LF.
	CodeNonASCII = "L0001"
	// CodeBadEncoding reports a byte order mark, a CR byte, or a missing final LF.
	CodeBadEncoding = "L0002"
	// CodeUnterminated reports a block comment or quoted literal without an end.
	CodeUnterminated = "L0003"
	// CodeBadEscape reports an escape sequence the language does not define.
	CodeBadEscape = "L0004"
	// CodeBadNumber reports a malformed number or duration literal.
	CodeBadNumber = "L0005"
	// CodeBadSymbol reports an ASCII symbol that is not part of the language.
	CodeBadSymbol = "L0006"
	// CodeBadChar reports a character literal that is not exactly one scalar.
	CodeBadChar = "L0007"

	// CodeUnexpectedToken reports a token where a production was required.
	CodeUnexpectedToken = "S0001"
	// CodeKeywordName reports a strict keyword used as a name.
	CodeKeywordName = "S0002"
	// CodeTypeArgs reports a type argument list that cannot be parsed.
	CodeTypeArgs = "S0003"

	// CodeUndefinedName reports a name with no declaration.
	CodeUndefinedName = "N0001"
	// CodePrivateName reports a private name used outside its package.
	CodePrivateName = "N0002"
	// CodeShadow reports a name that shadows an outer name.
	CodeShadow = "N0003"
	// CodeUnusedImport reports an import that is never used.
	CodeUnusedImport = "N0004"
	// CodeUnusedVar reports a variable that is never read.
	CodeUnusedVar = "N0005"
	// CodeMethodsTwoPackages reports a type with methods in two packages.
	CodeMethodsTwoPackages = "N0006"
	// CodeDuplicateName reports a duplicate top-level name.
	CodeDuplicateName = "N0007"
)

// Message is one fixed diagnostic text from the code table.
type Message struct {
	Code string
	Text string
	Help string
}

// Lexical messages, fixed by the code table.
var (
	// NonASCII reports a character outside printable ASCII, space, tab, and LF.
	NonASCII = Message{CodeNonASCII, "this character is not ASCII", "source files must be ASCII; replace it"}
	// ByteOrderMark reports a BOM at the start of a file.
	ByteOrderMark = Message{CodeBadEncoding, "the file has a byte order mark", "run will fmt to rewrite it"}
	// CRLineEnding reports a CR byte anywhere in a file.
	CRLineEnding = Message{CodeBadEncoding, "the file uses CR line endings", "run will fmt to rewrite it"}
	// MissingFinalLF reports a final line without a terminating LF.
	MissingFinalLF = Message{CodeBadEncoding, "the file does not end with a newline", "run will fmt to rewrite it"}
	// UnterminatedBlockComment reports a block comment without a closing marker.
	UnterminatedBlockComment = Message{CodeUnterminated, "unterminated block comment", "close it"}
	// UnterminatedString reports a quoted literal without a closing quote.
	UnterminatedString = Message{CodeUnterminated, "unterminated string", "close it"}
	// UnknownEscape reports an escape sequence outside the defined set.
	UnknownEscape = Message{CodeBadEscape, "unknown escape sequence", `allowed: \n \r \t \0 \\ \' \" \xNN \u{...}`}
	// InvalidNumber reports a malformed numeric or duration literal.
	InvalidNumber = Message{CodeBadNumber, "invalid number literal", "separate digits with _ between two digits; 1. and .5 are not allowed"}
	// BadSymbol reports a symbol that is not part of the language.
	BadSymbol = Message{CodeBadSymbol, "this symbol is not part of the language", "remove it"}
	// BadChar reports a character literal that does not hold exactly one scalar.
	BadChar = Message{CodeBadChar, "character literal must hold exactly one scalar", "write one character between the quotes"}
	// KeywordName reports a strict keyword used as a name.
	KeywordName = Message{CodeKeywordName, "a keyword cannot be used as a name", "rename it"}
	// TypeArguments reports a type argument list that cannot be parsed.
	TypeArguments = Message{CodeTypeArgs, "cannot parse type arguments here", "comparisons cannot be chained, so this reads as a comparison; write the type arguments where a type is expected"}
)

// Report records m as an error at pos.
func (b *Bag) Report(pos source.Pos, m Message) {
	b.Error(pos, m.Code, m.Text, m.Help)
}
