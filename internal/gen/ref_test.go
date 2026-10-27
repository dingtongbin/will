package gen

import (
	"strings"
	"testing"
)

// TestRefLocalDereferencesForFieldAccess pins the zero-cost lowering of a
// local ref: the slot holds a pointer and a field read dereferences it instead
// of copying the pointee.
func TestRefLocalDereferencesForFieldAccess(t *testing.T) {
	src := "struct User {\n    score: i32\n}\n\n" +
		"def read() -> i32 {\n" +
		"    var user: User = User { score: 7 }\n" +
		"    var borrowed: ref User = ref user\n" +
		"    return borrowed.score\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		"store ptr",
		"load ptr",
		`getelementptr inbounds %"struct.`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

// TestReturnedRefKeepsPointerABI checks that from is compile-time only and a
// returned ref is a pointer, not a copied User.
func TestReturnedRefKeepsPointerABI(t *testing.T) {
	src := "struct User {\n    score: i32\n}\n\n" +
		"def better(a: ref User, b: ref User) -> ref User from a, b {\n" +
		"    if a.score >= b.score {\n" +
		"        return a\n" +
		"    }\n" +
		"    return b\n" +
		"}\n\n" +
		"def use() -> i32 {\n" +
		"    var a: User = User { score: 1 }\n" +
		"    var b: User = User { score: 2 }\n" +
		"    var best: ref User = better(ref a, ref b)\n" +
		"    return best.score\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v\n%s", err, willcText(t, doc))
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define ptr @"example.com/app.better"`,
		"ret ptr",
		"store ptr",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

// TestRefScalarDereferences loads through a ref parameter on a read.
func TestRefScalarDereferences(t *testing.T) {
	src := "def read(x: ref i32) -> i32 {\n" +
		"    return x\n" +
		"}\n\n" +
		"def use() -> i32 {\n" +
		"    var x: i32 = 4\n" +
		"    return read(ref x)\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v\n%s", err, willcText(t, doc))
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`define i32 @"example.com/app.read"(ptr %x)`,
		"load i32, ptr %x",
		`call i32 @"example.com/app.read"(ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}

// TestRefFieldDereferences stores one pointer field and reads through it.
func TestRefFieldDereferences(t *testing.T) {
	src := "struct User {\n    score: i32\n}\n\n" +
		"struct Holder {\n    user: ref User\n}\n\n" +
		"def read() -> i32 {\n" +
		"    var user: User = User { score: 9 }\n" +
		"    var holder: Holder = Holder { user: ref user }\n" +
		"    return holder.user.score\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v\n%s", err, willcText(t, doc))
	}
	text := normalizeIR(string(ir))
	if !strings.Contains(text, `%"struct.example.com/app.Holder" = type { ptr }`) {
		t.Errorf("ref field is not one pointer:\n%s", text)
	}
	if !strings.Contains(text, "load ptr") {
		t.Errorf("ref field is not loaded:\n%s", text)
	}
}

// TestMethodRefFromSelfReturnsPointer covers from self naming the receiver.
func TestMethodRefFromSelfReturnsPointer(t *testing.T) {
	src := "struct Box {\n    n: i32\n}\n\n" +
		"def (b: ref Box) number() -> ref i32 from self {\n" +
		"    return ref b.n\n" +
		"}\n\n" +
		"def use() -> i32 {\n" +
		"    var b: Box = Box { n: 5 }\n" +
		"    var n: ref i32 = b.number()\n" +
		"    return n\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v\n%s", err, willcText(t, doc))
	}
	text := normalizeIR(string(ir))
	if !strings.Contains(text, `define ptr @"example.com/app.Box.number"(ptr %b)`) {
		t.Errorf("method does not return a pointer:\n%s", text)
	}
}

// TestMutViewWritesThroughDescriptor keeps mut []T a value-sized descriptor,
// not a pointer to a descriptor.
func TestMutViewWritesThroughDescriptor(t *testing.T) {
	src := "def bump(values: mut []i32) {\n" +
		"    values[0] += 1\n" +
		"}\n\n" +
		"def use() -> i32 {\n" +
		"    var xs: Array<i32> = new Array<i32>(1)\n" +
		"    xs.push(1)\n" +
		"    bump(mut xs[Range(0, xs.len())])\n" +
		"    return xs[0]\n" +
		"}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	if !strings.Contains(text, `define void @"example.com/app.bump"(ptr %values)`) {
		t.Errorf("bump does not take a view descriptor by pointer:\n%s", text)
	}
	if !strings.Contains(text, "getelementptr inbounds { ptr, i64 }") {
		t.Errorf("bump does not read the view descriptor:\n%s", text)
	}
}
