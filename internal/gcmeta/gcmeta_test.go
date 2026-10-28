package gcmeta

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/decisions"
	"github.com/will-lang/will/internal/willc"
)

func readDoc(t *testing.T, body string) *willc.Doc {
	t.Helper()
	doc, err := willc.Read([]byte(body))
	if err != nil {
		t.Fatalf("read willc: %v", err)
	}
	return doc
}

func TestClassPayloadLayout(t *testing.T) {
	doc := readDoc(t, "#1 module path=\"m\"\n"+
		"#2 class text=\"Node\"\n"+
		"#3 field parent=#2 text=\"next\" type=\"Node\"\n"+
		"#4 field parent=#2 text=\"value\" type=\"i64\"\n"+
		"#5 end\n")
	program, err := Build([]*willc.Doc{doc})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	node := program.Lookup("m.Node")
	if node == nil {
		t.Fatal("class m.Node is missing")
	}
	if node.Size != 16 || node.Align != 8 {
		t.Fatalf("Node layout = %d/%d, want 16/8", node.Size, node.Align)
	}
	if node.ID != 1 {
		t.Fatalf("Node id = %d, want 1", node.ID)
	}
	if len(node.Refs) != 1 || node.Refs[0] != 0 {
		t.Fatalf("Node refs = %v, want [0]", node.Refs)
	}
}

func TestStructAndNestedValueRefs(t *testing.T) {
	doc := readDoc(t, "#1 module path=\"m\"\n"+
		"#2 class text=\"Node\"\n"+
		"#3 field parent=#2 text=\"next\" type=\"Node\"\n"+
		"#4 struct text=\"Pair\"\n"+
		"#5 field parent=#4 text=\"owner\" type=\"Node\"\n"+
		"#6 field parent=#4 text=\"label\" type=\"str\"\n"+
		"#7 field parent=#4 text=\"count\" type=\"i64\"\n"+
		"#8 end\n")
	program, err := Build([]*willc.Doc{doc})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	pair := program.Lookup("m.Pair")
	if pair == nil {
		t.Fatal("struct m.Pair is missing")
	}
	if pair.Size != 32 || pair.Align != 8 {
		t.Fatalf("Pair layout = %d/%d, want 32/8", pair.Size, pair.Align)
	}
	want := []uint32{0, 8}
	if len(pair.Refs) != len(want) || pair.Refs[0] != want[0] ||
		pair.Refs[1] != want[1] {
		t.Fatalf("Pair refs = %v, want %v", pair.Refs, want)
	}
}

func TestOptionAndWeakLayout(t *testing.T) {
	doc := readDoc(t, "#1 module path=\"m\"\n"+
		"#2 class text=\"Node\"\n"+
		"#3 field parent=#2 text=\"next\" type=\"Node\"\n"+
		"#4 struct text=\"Holder\"\n"+
		"#5 field parent=#4 text=\"maybe\" type=\"Option<Node>\"\n"+
		"#6 field parent=#4 text=\"weak\" type=\"Weak<Node>\"\n"+
		"#7 end\n")
	program, err := Build([]*willc.Doc{doc})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	holder := program.Lookup("m.Holder")
	if holder == nil {
		t.Fatal("struct m.Holder is missing")
	}
	// Option<Node> is tag + padding + pointer at offset 8. Weak<Node> follows
	// at offset 16.
	if len(holder.Refs) != 1 || holder.Refs[0] != 8 {
		t.Fatalf("Holder refs = %v, want [8]", holder.Refs)
	}
	if len(holder.Weaks) != 1 || holder.Weaks[0] != 16 {
		t.Fatalf("Holder weaks = %v, want [16]", holder.Weaks)
	}
}

func TestClassIDsAreSorted(t *testing.T) {
	doc := readDoc(t, "#1 module path=\"m\"\n"+
		"#2 class text=\"Zed\"\n"+
		"#3 field parent=#2 text=\"v\" type=\"i64\"\n"+
		"#4 class text=\"Alpha\"\n"+
		"#5 field parent=#4 text=\"v\" type=\"i64\"\n"+
		"#6 end\n")
	program, err := Build([]*willc.Doc{doc})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if program.Lookup("m.Alpha").ID != 1 || program.Lookup("m.Zed").ID != 2 {
		t.Fatalf("ids = Alpha:%d Zed:%d, want 1/2",
			program.Lookup("m.Alpha").ID, program.Lookup("m.Zed").ID)
	}
}

func TestDecisionRecordsCarryClassLayout(t *testing.T) {
	doc := readDoc(t, "#1 module path=\"m\"\n"+
		"#2 class text=\"Node\"\n"+
		"#3 field parent=#2 text=\"next\" type=\"Node\"\n"+
		"#4 field parent=#2 text=\"value\" type=\"i64\"\n"+
		"#5 end\n")
	records, err := DecisionRecords([]*willc.Doc{doc})
	if err != nil {
		t.Fatalf("decision records: %v", err)
	}
	text := string(decisions.Write(records))
	for _, want := range []string{
		"layout type=\"m.Node\" size=16 align=8",
		"field type=\"m.Node\" name=\"next\" offset=0 size=8",
		"field type=\"m.Node\" name=\"value\" offset=8 size=8",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("record lacks %q:\n%s", want, text)
		}
	}
}

func TestVariantDependentRefsAreRejected(t *testing.T) {
	doc := readDoc(t, "#1 module path=\"m\"\n"+
		"#2 class text=\"Node\"\n"+
		"#3 field parent=#2 text=\"next\" type=\"Node\"\n"+
		"#4 enum text=\"Choice\"\n"+
		"#5 variant parent=#4 text=\"Ref\" arg=#6\n"+
		"#6 type parent=#5 text=\"Node\" kind=class\n"+
		"#7 variant parent=#4 text=\"Scalar\" arg=#8\n"+
		"#8 type parent=#7 text=\"i64\" kind=scalar\n"+
		"#9 end\n")
	if _, err := Build([]*willc.Doc{doc}); err == nil {
		t.Fatal("a variant-dependent reference layout was accepted")
	}
}
