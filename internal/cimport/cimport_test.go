package cimport

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
)

func typeText(t *testing.T, typ *ast.Type) string {
	t.Helper()
	if typ == nil {
		return ""
	}
	return ast.TypeText(typ)
}

func TestParseScalarPrototypes(t *testing.T) {
	header := []byte(`
typedef unsigned long size_t;

int putchar(int c);
void* malloc(size_t n);
void free(void* p);
int add(int a, int b);
double scale(double x, int n);
int printf(const char* fmt, ...);
`)
	funcs, err := Parse("api.h", header)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Func{}
	for _, fn := range funcs {
		byName[fn.Name] = fn
	}
	if got := typeText(t, byName["putchar"].Result); got != "i32" {
		t.Errorf("putchar result = %q", got)
	}
	if got := typeText(t, byName["putchar"].Params[0]); got != "i32" {
		t.Errorf("putchar param = %q", got)
	}
	if got := typeText(t, byName["malloc"].Result); got != "raw" {
		t.Errorf("malloc result = %q", got)
	}
	if got := typeText(t, byName["malloc"].Params[0]); got != "usize" {
		t.Errorf("malloc size_t = %q", got)
	}
	if got := typeText(t, byName["free"].Result); got != "()" {
		t.Errorf("free result = %q", got)
	}
	if len(byName["printf"].Params) != 1 || !byName["printf"].Variadic {
		t.Errorf("printf = %+v, want one fixed param and variadic", byName["printf"])
	}
	if got := typeText(t, byName["scale"].Result); got != "f64" {
		t.Errorf("scale result = %q", got)
	}
}
