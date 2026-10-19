package decisions

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestParseAndWriteRoundTrip(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "build", "fields", "decisions.txt"))
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	records, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := Write(records); !bytes.Equal(got, data) {
		t.Fatalf("round trip differs:\n--- want\n%s\n--- got\n%s", data, got)
	}
}

func TestParseKeepsAQuotedTextWithSpaces(t *testing.T) {
	src := "diagnostic code=\"T0001\" message=\"type mismatch: got str, want i32\"\n"
	records, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if got := records[0].Fields[1].Value.Text; got != "type mismatch: got str, want i32" {
		t.Fatalf("message = %q", got)
	}
	if got := string(Write(records)); got != src {
		t.Fatalf("write = %q, want %q", got, src)
	}
}

func TestParseRejectsBadLines(t *testing.T) {
	cases := []string{
		"layout type=\"T\" size=8 align=4",              // no final newline
		"\n",                                            // empty line
		" layout type=\"T\" size=8 align=4\n",           // leading space
		"layout type=\"T\" size=8 align=4 \n",           // trailing space
		"layout type=\"T\" type=\"U\" size=8 align=4\n", // repeated field
		"layout type=\"T\" size=8 align=#0\n",           // bad reference
		"layout type=\"unclosed size=8 align=4\n",       // unclosed text
	}
	for _, src := range cases {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", src)
		}
	}
}

func TestUnionIndexesLayoutsAndFields(t *testing.T) {
	src := "layout type=\"Point\" size=16 align=8\n" +
		"field type=\"Point\" name=\"x\" offset=0 size=8\n" +
		"field type=\"Point\" name=\"y\" offset=8 size=8\n" +
		"obligation pos=\"a.will:1:2\" check=\"index\" verdict=\"proved\" by=\"range\"\n" +
		"merge pos=\"a.will:3:4\" members=\"#12,#13\" count=2 reason=\"same-dominator\"\n"
	records, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ix, err := Union(records)
	if err != nil {
		t.Fatalf("union: %v", err)
	}
	got, ok := ix.Layout("Point")
	if !ok {
		t.Fatal("Point has no layout")
	}
	if got.Size != 16 || got.Align != 8 || len(got.Fields) != 2 {
		t.Fatalf("layout = %+v", got)
	}
	if got.Fields[1].Name != "y" || got.Fields[1].Offset != 8 || got.Fields[1].Size != 8 {
		t.Fatalf("second field = %+v", got.Fields[1])
	}
	if names := ix.Names(); len(names) != 1 || names[0] != "Point" {
		t.Fatalf("names = %v", names)
	}
}

func TestUnionRejectsTwoLayoutsAndOrphanFields(t *testing.T) {
	dup := "layout type=\"T\" size=8 align=4\nlayout type=\"T\" size=8 align=4\n"
	records, err := Parse([]byte(dup))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Union(records); err == nil {
		t.Fatal("two layouts for one type were accepted")
	}

	orphan := "field type=\"T\" name=\"x\" offset=0 size=4\n"
	records, err = Parse([]byte(orphan))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Union(records); err == nil {
		t.Fatal("a field without a layout was accepted")
	}
}

func TestUnionRejectsAFieldOfTheWrongValueKind(t *testing.T) {
	src := "layout type=\"T\" size=x align=4\n"
	records, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Union(records); err == nil {
		t.Fatal("a layout whose size is not an integer was accepted")
	}
}

func TestUnionRejectsTwoFieldsWithOneName(t *testing.T) {
	src := "layout type=\"T\" size=8 align=4\n" +
		"field type=\"T\" name=\"x\" offset=0 size=4\n" +
		"field type=\"T\" name=\"x\" offset=4 size=4\n"
	records, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Union(records); err == nil {
		t.Fatal("two fields with one name were accepted")
	}
}
