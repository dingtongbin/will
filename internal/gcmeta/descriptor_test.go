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

func TestDescriptorTextForSequence(t *testing.T) {
	sequence := &Type{
		Name:     "container.sequence.test",
		Kind:     KindStruct,
		ID:       9,
		Align:    8,
		Sequence: true,
		ElemType: "value.Node",
		ElemSize: 16,
	}
	text, err := DescriptorText(sequence)
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	for _, want := range []string{
		`i32 9, i32 0, i32 8, i32 8, i32 0, ptr null, i32 0, ptr null, i32 16, ptr @"willrt.gc.type.value.Node"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sequence descriptor lacks %q:\n%s", want, text)
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
