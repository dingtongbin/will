package pkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFeatureModule(t *testing.T, src string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "feat")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "will.mod"), []byte("module example.com/feat\nwill 1\nfeature tls\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.will"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestFeaturePrunesDeadBranch checks that the disabled branch is gone before
// resolution, so an undefined name in it is not reported.
func TestFeaturePrunesDeadBranch(t *testing.T) {
	old := EnabledFeatures
	defer func() { EnabledFeatures = old }()
	EnabledFeatures = map[string]bool{}

	dir := writeFeatureModule(t, `import "std/meta"

def F() -> i32 {
    if meta.Feature("tls") {
        return Missing()
    }
    return 0
}
`)
	b := Load(dir)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	for _, d := range b.CheckAll() {
		if d.Code == "N0001" {
			t.Fatalf("the dead branch was resolved: %+v", d)
		}
	}

	SetEnabledFeatures("tls")
	b = Load(dir)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	found := false
	for _, d := range b.CheckAll() {
		if d.Code == "N0001" && strings.Contains(d.Message, "undefined name") {
			found = true
		}
	}
	if !found {
		t.Fatal("the enabled branch was not resolved")
	}
}

func TestFeatureOutsideIfReportsT0010(t *testing.T) {
	old := EnabledFeatures
	defer func() { EnabledFeatures = old }()
	SetEnabledFeatures("tls")

	dir := writeFeatureModule(t, `import "std/meta"

def F() -> bool {
    var on: bool = meta.Feature("tls")
    return on
}
`)
	b := Load(dir)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	ds := b.CheckAll()
	var t0010, n0001 bool
	for _, d := range ds {
		if d.Code == "T0010" {
			t0010 = true
		}
		if d.Code == "N0001" {
			n0001 = true
		}
	}
	if !t0010 {
		t.Fatalf("no T0010 in %+v", ds)
	}
	if n0001 {
		t.Fatalf("the T0010 cascade was not suppressed: %+v", ds)
	}
}

func TestFeatureNotDeclaredReportsB0004(t *testing.T) {
	old := EnabledFeatures
	defer func() { EnabledFeatures = old }()
	EnabledFeatures = map[string]bool{}

	dir := writeFeatureModule(t, `import "std/meta"

def F() -> i32 {
    if meta.Feature("nope") {
        return 1
    }
    return 0
}
`)
	b := Load(dir)
	if b.Err != nil {
		t.Fatalf("load: %v", b.Err)
	}
	found := false
	for _, d := range b.CheckAll() {
		if d.Code == "B0004" && strings.Contains(d.Message, "nope") {
			found = true
		}
	}
	if !found {
		t.Fatal("an undeclared feature was accepted")
	}
}
