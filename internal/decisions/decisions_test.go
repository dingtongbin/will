package decisions

import (
	"strings"
	"testing"
)

// TestWriteMatchesTheFixedGrammar checks the line grammar of the decision
// record against the examples the format volume gives.
func TestWriteMatchesTheFixedGrammar(t *testing.T) {
	records := []Record{
		Layout("Point", 16, 8),
		FieldOffset("Point", "x", 0, 8),
		FieldOffset("Point", "y", 8, 8),
		Obligation("src/a.will:7:9", "index", "proved", "range"),
	}
	want := "layout type=\"Point\" size=16 align=8\n" +
		"field type=\"Point\" name=\"x\" offset=0 size=8\n" +
		"field type=\"Point\" name=\"y\" offset=8 size=8\n" +
		"obligation pos=\"src/a.will:7:9\" check=\"index\" verdict=\"proved\" by=\"range\"\n"
	got := string(Write(records))
	if got != want {
		t.Fatalf("write =\n%q\nwant\n%q", got, want)
	}
	if !strings.HasSuffix(got, "\n") || strings.Contains(got, "\n\n") {
		t.Fatalf("the record must end with one newline and hold no blank line: %q", got)
	}
}

func TestWriteEmpty(t *testing.T) {
	if got := Write(nil); len(got) != 0 {
		t.Fatalf("write(nil) = %q, want empty", got)
	}
}
