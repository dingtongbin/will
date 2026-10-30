package gen

import (
	"strings"
	"testing"
)

// TestTryUploadsConcreteErrorAndMatchesUnit covers a ? statement that returns a
// concrete error type and a Result<(), E> whose success payload is the unit.
// The unit payload has no LLVM value, so binding it must not emit a load.
func TestTryUploadsConcreteErrorAndMatchesUnit(t *testing.T) {
	src := `struct E {
    msg: str

    def message(self) -> str {
        return self.msg
    }

    def status(self) -> i32 {
        return 7
    }
}



def fail() -> Result<(), E> {
    return Err(E { msg: "bad" })
}

def run() -> Result<(), E> {
    fail()?
    return Ok(())
}

def main() -> i32 {
    match run() {
        Ok(_) => {
            return 0
        }
        Err(e) => {
            return e.status()
        }
    }
}
`
	doc := emitWillc(t, src)
	ir, err := EmitWithEntry(doc, "main")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := string(ir)
	if strings.Contains(text, "load , ptr") {
		t.Fatalf("unit payload emitted an empty load:\n%s", text)
	}
	for _, want := range []string{"try.err.1:", "try.ok.1:", "ret i32 7"} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
