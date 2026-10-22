package gen

import (
	"strings"
	"testing"
)

// TestEntryResultWrapperReportsError checks the only non-integer entry shape:
// Err is written through the report target and exits 1.
func TestEntryResultWrapperReportsError(t *testing.T) {
	src := `func main() -> Result<(), Error> {
    return Err(Canceled)
}
`
	doc := emitWillc(t, src)
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		"call void @willrt_report_write_n",
		`@.str.0 = private unnamed_addr constant [8 x i8] c"error: \00"`,
		"ret i32 1",
		"@will.error.data.8",
		"define internal { ptr, i64 } @will.error.message.8",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
