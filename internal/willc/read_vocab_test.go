package willc

import "testing"

// TestReadRejectsRangePattern checks that a pattern kind the grammar cannot
// produce is a shape error: the two-dot range is not part of the language.
func TestReadRejectsRangePattern(t *testing.T) {
	doc := "#1 module path=\"m\"\n" +
		"#2 pattern parent=#1 pos=\"a.will:1:1\" text=\"1\" kind=range\n" +
		"#3 end\n"
	if _, err := Read([]byte(doc)); err == nil {
		t.Fatal("a range pattern was accepted")
	}
}

func TestReadRejectsUnknownLitKind(t *testing.T) {
	doc := "#1 module path=\"m\"\n" +
		"#2 lit parent=#1 pos=\"a.will:1:1\" kind=tuple text=\"()\" type=\"()\"\n" +
		"#3 end\n"
	if _, err := Read([]byte(doc)); err == nil {
		t.Fatal("an unknown literal kind was accepted")
	}
}

func TestReadAcceptsUnitLiteral(t *testing.T) {
	data := []byte("#1 module path=\"m\"\n" +
		"#2 lit parent=#1 pos=\"a.will:1:1\" kind=unit text=\"()\" type=\"()\"\n" +
		"#3 end\n")
	if _, err := Read(data); err != nil {
		t.Fatalf("read: %v", err)
	}
}
