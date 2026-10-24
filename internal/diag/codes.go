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
	// CodeDuplicateDerive reports the same derive twice for one type.
	CodeDuplicateDerive = "N0008"
	// CodeDuplicateImport reports the same import written twice.
	CodeDuplicateImport = "N0009"
	// CodeTopLevelDiscard reports a top-level declaration named _.
	CodeTopLevelDiscard = "N0010"
	// CodeNoField reports a field that the struct or enum does not declare.
	CodeNoField = "N0011"
	// CodeNoMethod reports a method the receiver type does not declare.
	CodeNoMethod = "N0012"
	// CodeBadDeriveName reports a derive name outside the generator list.
	CodeBadDeriveName = "N0013"

	// CodeTypeMismatch reports a value of the wrong type.
	CodeTypeMismatch = "T0001"
	// CodeInterfaceUnsatisfied reports a type that does not satisfy an interface.
	CodeInterfaceUnsatisfied = "T0002"
	// CodeBadArgument reports an argument count or type error.
	CodeBadArgument = "T0003"
	// CodeTypeArgCount reports a wrong number of type arguments.
	CodeTypeArgCount = "T0004"
	// CodeConstantRange reports an integer constant outside its type range.
	CodeConstantRange = "T0005"
	// CodeChainedComparison reports a chained comparison.
	CodeChainedComparison = "T0006"
	// CodeNewWithoutInit reports new on a type that has no init.
	CodeNewWithoutInit = "T0007"
	// CodeBuiltinConstraintAsInterface reports a built-in constraint used
	// where an interface value is needed.
	CodeBuiltinConstraintAsInterface = "T0008"
	// CodeShiftAmount reports a constant shift amount that is not less than
	// the width of the shifted type.
	CodeShiftAmount = "T0009"
	// CodeFeatureOutsideIf reports meta.Feature outside an if condition.
	CodeFeatureOutsideIf = "T0010"
	// CodeNoZero reports a local variable whose type has no zero value.
	CodeNoZero = "T0011"
	// CodeVariadicWiden reports a C variadic argument that needs an explicit
	// widening cast.
	CodeVariadicWiden = "T0012"

	// CodePackageNameMismatch reports a package name that differs from its directory.
	CodePackageNameMismatch = "B0001"
	// CodeInvalidWillMod reports a will.mod syntax error.
	CodeInvalidWillMod = "B0002"
	// CodeChecksumMismatch reports a dependency checksum mismatch.
	CodeChecksumMismatch = "B0003"
	// CodeFeatureNotDeclared reports a feature used without a will.mod declaration.
	CodeFeatureNotDeclared = "B0004"
	// CodeUnsupportedTarget reports a target triple outside the allowed list.
	CodeUnsupportedTarget = "B0005"
	// CodeNoEntry reports a missing entry function.
	CodeNoEntry = "B0006"
	// CodeFreestanding reports a runtime capability or a package that
	// --freestanding does not allow.
	CodeFreestanding = "B0007"
	// CodeUnknownImportPath reports an import path that matches no package.
	CodeUnknownImportPath = "B0008"
	// CodePackageTwoPaths reports one package reached through two import paths.
	CodePackageTwoPaths = "B0009"
	// CodeImportCycle reports an import cycle.
	CodeImportCycle = "B0010"

	// CodeCannotParseCHeader reports a C header the compiler cannot parse.
	CodeCannotParseCHeader = "X0001"
	// CodeLinkFailed reports an undefined symbol at link time.
	CodeLinkFailed = "X0002"
	// CodeExternalToolMissing reports a missing or too old external tool.
	CodeExternalToolMissing = "X0003"

	// Ownership and borrow codes.
	CodeMoved             = "O0001"
	CodeMissingClone      = "O0002"
	CodeBorrowConflict    = "O0003"
	CodeBorrowOutlives    = "O0004"
	CodeBorrowStored      = "O0005"
	CodeMoveWhileBorrowed = "O0006"
	CodeContainerChanged  = "O0007"
	CodeCopyNotBorrow     = "O0008"

	// Memory and release codes.
	CodeUnreleasableCycle = "M0001"
	CodePossibleCycle     = "M0002"
	CodeRcSlotUnguarded   = "M0003"
	CodeExplicitLeak      = "M0004"
	CodeSmallPlate        = "M0005"

	// Concurrency codes.
	CodeSuspendInParallel  = "K0001"
	CodeGuardAcrossSuspend = "K0002"
	CodeTaskEscapesRegion  = "K0003"
	CodeWrongLane          = "K0004"
	CodeSpawnNotCall       = "K0005"
	CodeDeadlock           = "K0006"
	CodeGuardCopied        = "K0007"
	CodeReadGuardWrite     = "K0008"
	CodeAwaitOutsideAsync  = "K0009"
	CodeAsyncNotAwaited    = "K0010"
	CodeAwaitNotAsync      = "K0011"
	CodeAsyncAsValue       = "K0012"
	CodeSelectOutsideAsync = "K0013"

	// Error propagation codes.
	CodeResultDropped  = "R0001"
	CodeUnwrapType     = "R0002"
	CodePanicContained = "R0003"

	// Input, output, and blocking codes.
	CodeBlockingNotAwaited = "I0001"
	CodeWaitInSync         = "I0002"
	CodeWaitInAsync        = "I0003"
	CodeBlocksThreadInTask = "I0004"

	// CodeCannotTranslate reports a willc node the mechanical translation
	// cannot handle. It is a compiler bug, not a program error.
	CodeCannotTranslate = "G0001"
)

// Codes lists every code of the code table, in table order. The table in the
// diagnostics volume is the source of truth; this list exists so a test can
// see that no code is missing or duplicated.
var Codes = []string{
	CodeNonASCII, CodeBadEncoding, CodeUnterminated, CodeBadEscape, CodeBadNumber,
	CodeBadSymbol, CodeBadChar,
	CodeUnexpectedToken, CodeKeywordName, CodeTypeArgs,
	CodeUndefinedName, CodePrivateName, CodeShadow, CodeUnusedImport, CodeUnusedVar,
	CodeMethodsTwoPackages, CodeDuplicateName, CodeDuplicateDerive, CodeDuplicateImport,
	CodeTopLevelDiscard, CodeNoField, CodeNoMethod, CodeBadDeriveName,
	CodeTypeMismatch, CodeInterfaceUnsatisfied, CodeBadArgument, CodeTypeArgCount,
	CodeConstantRange, CodeChainedComparison, CodeNewWithoutInit,
	CodeBuiltinConstraintAsInterface,
	CodeShiftAmount,
	CodeFeatureOutsideIf,
	CodeNoZero,
	CodeVariadicWiden,
	CodeMoved, CodeMissingClone, CodeBorrowConflict, CodeBorrowOutlives, CodeBorrowStored,
	CodeMoveWhileBorrowed, CodeContainerChanged, CodeCopyNotBorrow,
	CodeUnreleasableCycle, CodePossibleCycle, CodeRcSlotUnguarded, CodeExplicitLeak, CodeSmallPlate,
	CodeSuspendInParallel, CodeGuardAcrossSuspend, CodeTaskEscapesRegion, CodeWrongLane,
	CodeSpawnNotCall, CodeDeadlock,
	CodeGuardCopied, CodeReadGuardWrite,
	CodeAwaitOutsideAsync, CodeAsyncNotAwaited, CodeAwaitNotAsync, CodeAsyncAsValue,
	CodeSelectOutsideAsync,
	CodeResultDropped, CodeUnwrapType, CodePanicContained,
	CodeBlockingNotAwaited, CodeWaitInSync, CodeWaitInAsync, CodeBlocksThreadInTask,
	CodePackageNameMismatch, CodeInvalidWillMod, CodeChecksumMismatch, CodeFeatureNotDeclared,
	CodeUnsupportedTarget, CodeNoEntry, CodeFreestanding, CodeUnknownImportPath,
	CodePackageTwoPaths, CodeImportCycle,
	CodeCannotTranslate,
	CodeCannotParseCHeader, CodeLinkFailed, CodeExternalToolMissing,
}

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
