package willc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTripGolden(t *testing.T) {
	path := filepath.Join("..", "..", "tests", "willc", "roundtrip", "willc.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	doc, err := Read(data)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := Write(doc)
	if !bytes.Equal(out, data) {
		t.Fatalf("round trip differs:\n--- want\n%s\n--- got\n%s", data, out)
	}
}

func TestWriteMinimalDoc(t *testing.T) {
	doc := &Doc{Records: []Record{
		{ID: 1, Kind: "module", Fields: []Field{{Name: "path", Value: Text("x")}}},
		{ID: 2, Kind: "end"},
	}}
	got := string(Write(doc))
	want := "#1 module path=\"x\"\n#2 end\n"
	if got != want {
		t.Fatalf("write = %q, want %q", got, want)
	}
}

func TestReadErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		msg  string
	}{
		{"no final newline", `#1 module path="x"`, "does not end with a newline"},
		{"CR line ending", "#1 module path=\"x\"\r\n", "CR line endings"},
		{"non ASCII", "#1 module path=\"\xc3\xa9\"\n", "not ASCII"},
		{"unknown kind", "#1 bogus\n", "unknown node kind"},
		{"missing required field", "#1 module\n#2 end\n", "needs field path"},
		{"disallowed field", "#1 module path=\"x\" nope=1\n#2 end\n", "not allowed"},
		{"repeated field", "#1 module path=\"x\" path=\"y\"\n#2 end\n", "repeats"},
		{"dangling reference", "#1 module path=\"x\"\n#2 func text=\"f\" type=#9\n#3 end\n", "dangling"},
		{"ids out of order", "#2 module path=\"x\"\n#3 end\n", "run from #1"},
		{"leading zero id", "#01 module path=\"x\"\n#2 end\n", "leading zeros"},
		{"blank line", "#1 module path=\"x\"\n\n#2 end\n", "must start with #"},
		{"first not module", "#1 func text=\"f\"\n#2 end\n", "first record must be module"},
		{"last not end", "#1 module path=\"x\"\n#2 func text=\"f\"\n", "last record must be end"},
		{"missing kind", "#1\n", "needs a kind"},
		{"unknown value", "#1 module path=+\n#2 end\n", "unknown field value"},
		{"unterminated quote", "#1 module path=\"x\n#2 end\n", "unterminated quoted text"},
		{"invalid escape", "#1 module path=\"\\q\"\n#2 end\n", "invalid quoted text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Read([]byte(c.src))
			if err == nil {
				t.Fatalf("Read(%q) succeeded, want failure", c.src)
			}
			if !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("error = %q, want it to contain %q", err, c.msg)
			}
		})
	}
}

func TestReadAcceptsUniversalFields(t *testing.T) {
	src := "#1 module path=\"x\"\n#2 func text=\"f\" pos=\"a.will:1:1\" parent=#1\n#3 end\n"
	doc, err := Read([]byte(src))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if doc.Records[1].Fields[1].Name != "pos" {
		t.Fatalf("second field = %+v, want pos", doc.Records[1].Fields[1])
	}
}
