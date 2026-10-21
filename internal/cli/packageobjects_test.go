package cli

import "testing"

// TestPackageObjectKeyScopesSources checks the ownership boundary that makes
// incremental builds useful: a non-entry object ignores unrelated packages,
// while the entry object owns the generic instances and sees every source.
func TestPackageObjectKeyScopesSources(t *testing.T) {
	fe := frontEnd{modulePath: "example.com/app"}
	opts := buildOptions{entry: "main", opt: "-O2"}
	texts := map[string]string{
		"example.com/app":     "app source",
		"example.com/app/lib": "library source",
		"example.com/other":   "unrelated source",
	}
	libKey := packageObjectKey(fe, "example.com/app/lib", texts, opts)
	entryKey := packageObjectKey(fe, "example.com/app", texts, opts)

	texts["example.com/other"] = "changed unrelated source"
	if got := packageObjectKey(fe, "example.com/app/lib", texts, opts); got != libKey {
		t.Fatal("a non-entry object key changed when an unrelated package changed")
	}
	if got := packageObjectKey(fe, "example.com/app", texts, opts); got == entryKey {
		t.Fatal("the entry object key ignored an unrelated source even though it owns generic instances")
	}
}

// TestPackageObjectKeyIsStable checks that map insertion order cannot change a
// key, because the entry key walks every source path.
func TestPackageObjectKeyIsStable(t *testing.T) {
	fe := frontEnd{modulePath: "example.com/app"}
	opts := buildOptions{entry: "main", opt: "-O2"}
	a := map[string]string{
		"example.com/app": "app",
		"example.com/a":   "a",
		"example.com/b":   "b",
	}
	b := map[string]string{
		"example.com/b":   "b",
		"example.com/app": "app",
		"example.com/a":   "a",
	}
	if got, want := packageObjectKey(fe, "example.com/app", a, opts), packageObjectKey(fe, "example.com/app", b, opts); got != want {
		t.Fatalf("key depends on map order: %s != %s", got, want)
	}
}
