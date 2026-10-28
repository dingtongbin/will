package gen

import (
	"strings"
	"testing"
)

// TestLiteralBoolTextLowering checks that source boolean literals use the
// spelling LLVM accepts.
func TestLiteralBoolTextLowering(t *testing.T) {
	cases := map[string]string{
		"True":  "true",
		"False": "false",
		"true":  "true",
		"false": "false",
	}
	for text, want := range cases {
		if got := literalBoolText(text); got != want {
			t.Errorf("literalBoolText(%q) = %q, want %q", text, got, want)
		}
	}
}

// TestEmitBoolLiterals checks that both literal returns and a constant
// reference lower to LLVM boolean constants.
func TestEmitBoolLiterals(t *testing.T) {
	src := `const Flag: bool = True

def choose(v: bool) -> bool {
    if v {
        return True
    }
    return False
}

def constant() -> bool {
    return Flag
}
`
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, unwanted := range []string{"ret i1 True", "ret i1 False", "ret i1 Flag"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("IR contains source spelling %q:\n%s", unwanted, text)
		}
	}
	for _, want := range []string{"ret i1 true", "ret i1 false"} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
