package willc

import (
	"bytes"
	"testing"
)

// TestGCShapeRecordsRoundTrip covers the records the compiler writes for a
// class layout, an allocation site, a barrier, a safepoint, and a stack map.
func TestGCShapeRecordsRoundTrip(t *testing.T) {
	data := []byte("#1 module path=\"m\"\n" +
		"#2 class parent=#1 text=\"Node\"\n" +
		"#3 field parent=#2 text=\"next\" type=\"Node\"\n" +
		"#4 type parent=#3 text=\"Node\" kind=class\n" +
		"#5 safepoint parent=#4 pos=\"a.will:2:1\" kind=alloc\n" +
		"#6 alloc parent=#5 pos=\"a.will:2:1\" type=\"Node\" site=tlab\n" +
		"#7 barrier parent=#5 pos=\"a.will:2:1\" kind=write\n" +
		"#8 stackmap parent=#1 func=\"m.run\" slot=0 type=\"Node\"\n" +
		"#9 count parent=#1 name=safepoints n=1\n" +
		"#10 end\n")
	doc, err := Read(data)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := Write(doc); !bytes.Equal(got, data) {
		t.Fatalf("round trip =\n%s\nwant\n%s", got, data)
	}
}

// TestGCShapeRecordsRejectUnknownBarrier ensures the record vocabulary is
// enforced at read time, not discovered by the lowering.
func TestGCShapeRecordsRejectUnknownBarrier(t *testing.T) {
	data := []byte("#1 module path=\"m\"\n" +
		"#2 barrier parent=#1 pos=\"a.will:1:1\" kind=scan\n" +
		"#3 end\n")
	if _, err := Read(data); err == nil {
		t.Fatal("an unknown barrier kind was accepted")
	}
}
