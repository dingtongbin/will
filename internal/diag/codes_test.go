package diag

import (
	"strings"
	"testing"
)

// TestCodeTableIsComplete checks the shape of every code: one section letter
// and four digits, listed once, with a known section letter.
func TestCodeTableIsComplete(t *testing.T) {
	sections := map[byte]bool{'L': true, 'S': true, 'N': true, 'T': true, 'O': true,
		'M': true, 'K': true, 'R': true, 'I': true, 'B': true, 'G': true, 'X': true}
	seen := map[string]bool{}
	for _, code := range Codes {
		if len(code) != 5 {
			t.Errorf("code %q is not one letter and four digits", code)
		}
		if !sections[code[0]] {
			t.Errorf("code %q uses an unknown section letter", code)
		}
		if strings.Trim(code[1:], "0123456789") != "" {
			t.Errorf("code %q does not end in four digits", code)
		}
		if seen[code] {
			t.Errorf("code %q is listed twice", code)
		}
		seen[code] = true
	}
}

// TestCodesMatchConstants guards against a constant that was renamed or
// dropped from the list.
func TestCodesMatchConstants(t *testing.T) {
	all := []string{
		CodeNonASCII, CodeBadEncoding, CodeUnterminated, CodeBadEscape, CodeBadNumber,
		CodeBadSymbol, CodeBadChar,
		CodeUnexpectedToken, CodeKeywordName, CodeTypeArgs,
		CodeUndefinedName, CodePrivateName, CodeShadow, CodeUnusedImport, CodeUnusedVar,
		CodeDuplicateName, CodeDuplicateDerive, CodeDuplicateImport,
		CodeTopLevelDiscard, CodeNoField, CodeNoMethod, CodeBadDeriveName,
		CodeTypeMismatch, CodeInterfaceUnsatisfied, CodeBadArgument, CodeTypeArgCount,
		CodeConstantRange, CodeChainedComparison, CodeBadConstruct, CodeInitReturn,
		CodeBuiltinConstraintAsInterface,
		CodeShiftAmount,
		CodeFeatureOutsideIf,
		CodeNoZero,
		CodeVariadicWiden, CodeTypeAliasCycle, CodeCrossTask,
		CodeMoved, CodeMissingClone, CodeContainerChanged,
		CodeExplicitLeak,
		CodeGuardAcrossSuspend, CodeTaskEscapesRegion, CodeTaskNotAwaited,
		CodeSpawnNotCall, CodeDeadlock,
		CodeGuardCopied, CodeReadGuardWrite,
		CodeAwaitOutsideAsync, CodeAsyncNotAwaited, CodeAwaitNotAsync, CodeAsyncAsValue,
		CodeSelectOutsideAsync, CodeDeferSuspend,
		CodeResultDropped, CodeUnwrapType, CodePanicContained,
		CodeBlockingNotAwaited, CodeBlockingSyncInAsync,
		CodePackageNameMismatch, CodeInvalidWillMod, CodeChecksumMismatch, CodeFeatureNotDeclared,
		CodeUnsupportedTarget, CodeNoEntry, CodeFreestanding, CodeUnknownImportPath,
		CodePackageTwoPaths, CodeImportCycle,
		CodeCannotTranslate,
		CodeCannotParseCHeader, CodeLinkFailed, CodeExternalToolMissing,
	}
	if len(all) != len(Codes) {
		t.Fatalf("constants = %d, Codes = %d", len(all), len(Codes))
	}
	listed := map[string]bool{}
	for _, code := range Codes {
		listed[code] = true
	}
	for _, code := range all {
		if !listed[code] {
			t.Errorf("code %q is not in Codes", code)
		}
	}
}
