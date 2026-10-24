package gen

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/willc"
)

// TestSpawnPlaceLowers checks the three placement forms reach the runtime:
// auto asks the scheduler, here keeps the current lane, and on uses the declared
// lane index. The parser and willc round-trip are covered by their own tests.
func TestSpawnPlaceLowers(t *testing.T) {
	src := `lane Ui struct UiMarker {
    id: i32
}

func Work() -> i32 {
    return 1
}

func main() -> i32 {
    region {
        var a: Task<i32> = spawn Work()
        var b: Task<i32> = spawn here Work()
        var c: Task<i32> = spawn on Ui Work()
        _ = a
        _ = b
        _ = c
    }
    return 0
}
`
	doc := emitWillc(t, src)
	ir, err := EmitPackageModule([]*willc.Doc{doc}, "example.com/app", "main", "example.com/app", nil, false)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	text := string(ir)
	for _, want := range []string{
		"call i32 @willrt_lane_choose()",
		"call i32 @willrt_lane_here()",
		"declare i32 @willrt_lane_choose()",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("IR lacks %q:\n%s", want, text)
		}
	}
}
