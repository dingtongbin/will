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
	// The class and the str data word are both GC references. The label bytes
	// are immutable and live in a byte sequence.
	want := []uint32{0, 8}
	if len(pair.Refs) != len(want) || pair.Refs[0] != want[0] {
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

func TestVariantDependentRefsUseVariantTable(t *testing.T) {
	doc := readDoc(t, "#1 module path=\"m\"\n"+
		"#2 class text=\"Node\"\n"+
		"#3 field parent=#2 text=\"next\" type=\"Node\"\n"+
		"#4 enum text=\"Choice\"\n"+
		"#5 variant parent=#4 text=\"Ref\" arg=#6\n"+
		"#6 type parent=#5 text=\"Node\" kind=class\n"+
		"#7 variant parent=#4 text=\"Scalar\" arg=#8\n"+
		"#8 type parent=#7 text=\"i64\" kind=scalar\n"+
		"#9 end\n")
	program, err := Build([]*willc.Doc{doc})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	choice := program.Lookup("m.Choice")
	if choice == nil || len(choice.Variants) != 2 {
		t.Fatalf("Choice variants = %#v", choice)
	}
	if choice.Variants[0].Type != "value.m.Node" ||
		choice.Variants[1].Type != "value.i64" {
		t.Fatalf("Choice variant types = %#v", choice.Variants)
	}
}

func TestGenericClassInstanceLayout(t *testing.T) {
	doc := readDoc(t, "#1 module path=\"m\"\n"+
		"#2 class parent=#1 text=\"Box\"\n"+
		"#3 typeparam parent=#2 text=\"T\"\n"+
		"#4 field parent=#2 text=\"value\" type=\"T\"\n"+
		"#5 class parent=#1 text=\"Node\"\n"+
		"#6 field parent=#5 text=\"value\" type=\"i32\"\n"+
		"#7 func parent=#1 text=\"main\" type=\"i32\"\n"+
		"#8 type parent=#7 text=\"Box<Node>\" kind=class to=#2 arg=#9\n"+
		"#9 type parent=#8 text=\"Node\" kind=named to=#5\n"+
		"#10 end\n")
	program, err := Build([]*willc.Doc{doc})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if program.Lookup("m.Box") != nil {
		t.Fatalf("generic template was registered as a runtime layout")
	}
	box := program.Lookup("m.Box<m.Node>")
	if box == nil || box.Kind != KindClass {
		t.Fatalf("Box<Node> instance = %#v", box)
	}
	if box.Size != 8 || len(box.Refs) != 1 || box.Refs[0] != 0 {
		t.Fatalf("Box<Node> layout = size %d refs %v", box.Size, box.Refs)
	}
	if name, ok := program.TypeText("m", "Box<Node>"); !ok || name != "m.Box<m.Node>" {
		t.Fatalf("TypeText(Box<Node>) = %q, %v", name, ok)
	}
}

func TestContainerPayloadType(t *testing.T) {
	doc := readDoc(t, "#1 module path=\"m\"\n"+
		"#2 func parent=#1 text=\"main\" type=\"i32\"\n"+
		"#3 var parent=#2 text=\"xs\" type=\"list[i32]\"\n"+
		"#4 type parent=#3 text=\"list[i32]\" kind=named arg=#5\n"+
		"#5 type parent=#4 text=\"i32\" kind=scalar\n"+
		"#6 var parent=#2 text=\"ys\" type=\"dict[str,i32]\"\n"+
		"#7 type parent=#6 text=\"dict[str,i32]\" kind=named arg=#8 arg=#9\n"+
		"#8 type parent=#7 text=\"str\" kind=scalar\n"+
		"#9 type parent=#7 text=\"i32\" kind=scalar\n"+
		"#10 end\n")
	program, err := Build([]*willc.Doc{doc})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, want := range []string{"list[i32]", "dict[str,i32]"} {
		container := program.Lookup(want)
		if container == nil || container.Kind != KindClass {
			t.Fatalf("%s type = %#v", want, container)
		}
		if container.Size != 24 || len(container.Refs) != 1 || container.Refs[0] != 0 {
			t.Fatalf("%s layout = size %d refs %v", want, container.Size, container.Refs)
		}
		if container.ID == 0 {
			t.Fatalf("%s has no class id", want)
		}
		if name, ok := program.TypeText("m", want); !ok || name != want {
			t.Fatalf("TypeText(%s) = %q, %v", want, name, ok)
		}
	}
}
