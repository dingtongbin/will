package cli

import (
	"io"

	"github.com/will-lang/will/internal/diag"
)

// supportedTargets is the list of target triples the build volume fixes. It
// lives here once; a triple outside it is reported by will itself and never
// handed to clang.
var supportedTargets = []string{
	"x86_64-linux-gnu",
	"aarch64-linux-gnu",
	"x86_64-mac-darwin",
	"aarch64-mac-darwin",
	"x86_64-win-msvc",
	"aarch64-android",
	"aarch64-ios",
}

// SupportedTarget reports whether triple is one of the allowed targets.
func SupportedTarget(triple string) bool {
	for _, t := range supportedTargets {
		if t == triple {
			return true
		}
	}
	return false
}

// targetDiagnostic returns the B0005 diagnostic for an unsupported target, or
// zero when the target is fine or was not written.
func targetDiagnostic(triple string, set bool) (diag.Diagnostic, bool) {
	if !set || SupportedTarget(triple) {
		return diag.Diagnostic{}, false
	}
	return diag.Diagnostic{
		Severity: diag.Error,
		Code:     diag.CodeUnsupportedTarget,
		Message:  "unsupported target " + triple,
		Help:     "use one of the supported platforms: linux, mac, win, android, ios",
	}, true
}

// reportNoPos prints one diagnostic that has no source position.
func reportNoPos(d diag.Diagnostic, jsonOut bool, build diag.Build, stdout io.Writer) {
	if jsonOut {
		io.WriteString(stdout, diag.JSONAll([]diag.Diagnostic{d}, build))
		return
	}
	io.WriteString(stdout, diag.FormatAll([]diag.Diagnostic{d}))
}

// entryDiagnostic returns the B0006 diagnostic for a missing entry function.
func entryDiagnostic(name string) diag.Diagnostic {
	return diag.Diagnostic{
		Severity: diag.Error,
		Code:     diag.CodeNoEntry,
		Message:  "no entry function " + name,
		Help:     "declare it, or pass --entry <name>",
	}
}
