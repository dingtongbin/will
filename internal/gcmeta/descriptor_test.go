package gcmeta

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/willc"
)

func TestDescriptorTextCarriesReferenceOffsets(t *testing.T) {
	doc := readDoc(t, "#1 module path=\"m\"\n"+
		"#2 class text=\"Node\"\n"+
		"#3 field parent=#2 text=\"next\" type=\"Node\"\n"+
		"#4 field parent=#2 text=\"value\" type=\"i64\"\n"+
		"#5 end\n")
	program, err := Build([]*willc.Doc{doc})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	text, err := DescriptorBlock(program.Types())
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	for _, want := range []string{
		`@"willrt.gc.refs.m.Node" = private constant [1 x i32] [i32 0]`,
		`i32 1, i32 16, i32 8, i32 1, i32 1, ptr @"willrt.gc.refs.m.Node", i32 0, ptr null`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("descriptor lacks %q:\n%s", want, text)
		}
	}
}

func TestDescriptorSymbolQuotesPackageNames(t *testing.T) {
	got := DescriptorSymbol(`example.com/app.Node`)
	want := `@"willrt.gc.type.example.com/app.Node"`
	if got != want {
		t.Fatalf("symbol = %s, want %s", got, want)
	}
}
