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

// TestMutViewWritesThroughDescriptor keeps mut []T a value-sized descriptor,
// not a pointer to a descriptor.
func TestMutViewWritesThroughDescriptor(t *testing.T) {
	src := "def bump(values: mut []i32) {\n" +
		"    values[0] += 1\n" +
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
