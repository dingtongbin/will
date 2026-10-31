package gen

import (
	"strings"
	"testing"
)

// TestEmitInterfaceDowncasts pins the type identity word and the two forms of
// explicit downcast: Is gives a branch-free bool chain and As gives an Option
// whose Some payload is the concrete value or another interface table.
func TestEmitInterfaceDowncasts(t *testing.T) {
	src := "interface Shape {\n    def area() -> i32\n}\n\ninterface Labeled {\n    def label() -> str\n}\n\nstruct Square {\n    n: i32\n\n    def area(self) -> i32 {\n        return self.n\n    }\n\n    def label(self) -> str {\n        return \"square\"\n    }\n}\n\n\n\ndef use(v: Shape) -> i32 {\n    if not v.is<Square>() {\n        return -1\n    }\n    var labeled: Option<Labeled> = v.as<Labeled>()\n    match labeled {\n        Some(x) => {\n            if x.label() == \"square\" {\n                return 1\n            }\n            return 2\n        }\n        None => {\n            return -2\n        }\n    }\n}\n\ndef main() -> i32 {\n    return 0\n}\n"
	doc := emitWillc(t, src)
	ir, err := Emit(doc)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := normalizeIR(string(ir))
	for _, want := range []string{
		`@"will.type.example.com/app.Square" = linkonce_odr constant i8 0`,
		`vtable.example.com/app.Labeled`,
		`cast.check.`,
		`cast.hit.`,
		`is.check.`,
		`getelementptr inbounds [4 x ptr], ptr`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `@will.type.Square`) {
		t.Errorf("IR used an unqualified type identity:\n%s", text)
	}
}
