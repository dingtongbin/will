package decisions

import (
	"bytes"
	"testing"
)

// TestGCWritersMatchTheDecisionGrammar checks the field order of the GC
// decision records and their round trip through Parse.
func TestGCWritersMatchTheDecisionGrammar(t *testing.T) {
	records := []Record{
		StackMap("example.com/app.run", 8, "example.com/app.Node"),
		Barrier("example.com/app.run", "src/app.will:12:9", "satb"),
		Alloc("example.com/app.run", "src/app.will:14:5", "example.com/app.Node", "tlab"),
	}
	want := "stackmap func=\"example.com/app.run\" slot=8 type=\"example.com/app.Node\"\n" +
		"barrier func=\"example.com/app.run\" pos=\"src/app.will:12:9\" kind=\"satb\"\n" +
		"alloc func=\"example.com/app.run\" pos=\"src/app.will:14:5\" type=\"example.com/app.Node\" site=\"tlab\"\n"
	got := Write(records)
	if string(got) != want {
		t.Fatalf("write =\n%s\nwant\n%s", got, want)
	}
	parsed, err := Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if again := Write(parsed); !bytes.Equal(again, got) {
		t.Fatalf("round trip =\n%s\nwant\n%s", again, got)
	}
}
