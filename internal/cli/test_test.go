package cli

import (
	"strings"
	"testing"
)

func TestTestEntryRunsCasesSerially(t *testing.T) {
	src := testEntry("example.com/app", []string{"TestAlpha", "TestBeta"}, []string{"BenchmarkOne"})
	for _, want := range []string{
		"import \"std/test\"",
		"def WillTestMain() -> i32",
		"test.NewT(\"example.com/app/TestAlpha\")",
		"TestBeta(t_Beta)",
		"test.NewB(\"example.com/app/BenchmarkOne\")",
		"var total: i32 = 3",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("entry lacks %q:\n%s", want, src)
		}
	}
}

func TestTestHandleSpellings(t *testing.T) {
	if !isTestHandle("T", "T") || !isTestHandle("test.T", "T") {
		t.Fatal("a local or qualified T was rejected")
	}
	if isTestHandle("B", "T") || isTestHandle("test.B", "T") {
		t.Fatal("B was accepted as T")
	}
}
