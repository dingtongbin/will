package mod

import (
	"strings"
	"testing"

	"github.com/will-lang/will/internal/diag"
)

const example = `module   example.com/myapp
will     1
require  std@bundled
require  example.com/lib@v1.4.2
replace  example.com/lib@v1.4.2 => ../lib
feature  tls
`

func TestParseLink(t *testing.T) {
	m, ds := Parse("will.mod", []byte("module m\nwill 1\nlink m\nlink ../lib/libfoo.a\n"))
	if len(ds) != 0 {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
	if len(m.Links) != 2 || m.Links[0] != "m" || m.Links[1] != "../lib/libfoo.a" {
		t.Fatalf("links = %v", m.Links)
	}
}

func TestParseExample(t *testing.T) {
	m, ds := Parse("will.mod", []byte(example))
	if len(ds) != 0 {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
	if m.Path != "example.com/myapp" || m.Will != 1 {
		t.Fatalf("module = %q will = %d", m.Path, m.Will)
	}
	if len(m.Requires) != 2 {
		t.Fatalf("requires = %d, want 2", len(m.Requires))
	}
	if !m.Requires[0].Bundled || m.Requires[0].Path != "std" {
		t.Errorf("require 0 = %+v", m.Requires[0])
	}
	if m.Requires[1].Version != (Version{1, 4, 2}) {
		t.Errorf("require 1 = %+v", m.Requires[1])
	}
	if len(m.Replaces) != 1 || m.Replaces[0].Target != "../lib" || m.Replaces[0].IsVersioned {
		t.Fatalf("replaces = %+v", m.Replaces)
	}
	if len(m.Features) != 1 || m.Features[0] != "tls" {
		t.Fatalf("features = %v", m.Features)
	}

	deps := m.Select()
	if len(deps) != 2 {
		t.Fatalf("deps = %d, want 2", len(deps))
	}
	if deps[0].Path != "example.com/lib" || deps[0].Local != "../lib" || deps[0].Version != (Version{1, 4, 2}) {
		t.Errorf("dep 0 = %+v", deps[0])
	}
	if deps[1].Path != "std" || !deps[1].Bundled {
		t.Errorf("dep 1 = %+v", deps[1])
	}
}

func TestSelectHighestVersion(t *testing.T) {
	src := "module m\nwill 1\nrequire a@v1.2.0\nrequire a@v1.10.0\nrequire a@v1.4.2\n"
	m, ds := Parse("will.mod", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
	deps := m.Select()
	if len(deps) != 1 || deps[0].Version != (Version{1, 10, 0}) {
		t.Fatalf("deps = %+v, want a@v1.10.0", deps)
	}
}

func TestSelectVersionedReplace(t *testing.T) {
	src := "module m\nwill 1\nrequire a@v1.0.0\nreplace a@v1.0.0 => b@v2.0.0\n"
	m, ds := Parse("will.mod", []byte(src))
	if len(ds) != 0 {
		t.Fatalf("diagnostics:\n%s", diag.FormatAll(ds))
	}
	deps := m.Select()
	if len(deps) != 1 || deps[0].Path != "a" || deps[0].Version != (Version{2, 0, 0}) || deps[0].TargetPath != "b" {
		t.Fatalf("deps = %+v, want a resolved to b@v2.0.0", deps)
	}
}

func TestVersionParse(t *testing.T) {
	cases := []struct {
		text string
		ok   bool
	}{
		{"v1.4.2", true},
		{"v0.0.0", true},
		{"v1.4", false},
		{"1.4.2", false},
		{"v1.4.2.3", false},
		{"v01.4.2", true},
		{"v-1.4.2", false},
	}
	for _, c := range cases {
		if _, ok := ParseVersion(c.text); ok != c.ok {
			t.Errorf("ParseVersion(%q) ok = %v, want %v", c.text, ok, c.ok)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		help string
	}{
		{"missing module", "will 1\n", "module <import path>"},
		{"missing will", "module m\n", "will <integer>"},
		{"unknown field", "module m\nwill 1\nnope x\n", "unknown field nope"},
		{"bad require", "module m\nwill 1\nrequire a\n", "require <path>@<version>"},
		{"bad version", "module m\nwill 1\nrequire a@1.2\n", "v<major>.<minor>.<patch>"},
		{"bad replace", "module m\nwill 1\nreplace a@v1.0.0 ../lib\n", "replace is path@version => target"},
		{"duplicate module", "module a\nmodule b\nwill 1\n", "module appears once"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ds := Parse("will.mod", []byte(c.src))
			if len(ds) == 0 {
				t.Fatalf("Parse(%q) succeeded, want a diagnostic", c.src)
			}
			d := ds[0]
			if d.Code != diag.CodeInvalidWillMod || d.Message != "invalid will.mod" {
				t.Fatalf("diagnostic = %+v", d)
			}
			if !strings.Contains(d.Help, c.help) {
				t.Errorf("help = %q, want it to contain %q", d.Help, c.help)
			}
		})
	}
}
