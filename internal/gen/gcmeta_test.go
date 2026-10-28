package gen

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/willc"
)

// TestEmitGCDescriptors checks that a class record reaches the GC metadata
// block even before a function uses it. The descriptor is the one the runtime
// registers, so this is the place a layout mistake first becomes visible.
func TestEmitGCDescriptors(t *testing.T) {
	doc, err := willc.Read([]byte("#1 module path=\"m\"\n" +
		"#2 class text=\"Node\"\n" +
		"#3 field parent=#2 text=\"next\" type=\"Node\"\n" +
		"#4 field parent=#2 text=\"value\" type=\"i64\"\n" +
		"#5 end\n"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ir, err := EmitModule([]*willc.Doc{doc}, "")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		`@"willrt.gc.type.m.Node"`,
		`@"willrt.gc.refs.m.Node" = private constant [1 x i32] [i32 0]`,
		`i32 1, i32 16, i32 8, i32 1, i32 1`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("IR lacks %q:\n%s", want, text)
		}
	}
}
