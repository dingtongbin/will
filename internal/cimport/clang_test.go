package cimport

import (
	"testing"

	"github.com/will-lang/will/internal/ast"
)

func TestClangTypeMapping(t *testing.T) {
	cases := []struct {
		text   string
		target string
		want   string
	}{
		{"int", "", "i32"},
		{"unsigned int", "", "u32"},
		{"long", "x86_64-win-msvc", "i32"},
		{"unsigned long", "x86_64-win-msvc", "u32"},
		{"long", "x86_64-linux-gnu", "i64"},
		{"unsigned long", "x86_64-linux-gnu", "u64"},
		{"long long", "", "i64"},
		{"char", "", "i8"},
		{"unsigned char", "", "u8"},
		{"_Bool", "", "bool"},
		{"double", "", "f64"},
		{"size_t", "", "usize"},
		{"ssize_t", "", "isize"},
		{"const char *", "", "raw"},
		{"Box *", "", "raw"},
		{"int (*)(int)", "", "raw"},
		{"void", "", "()"},
	}
	for _, tc := range cases {
		got, ok := clangType(tc.text, tc.target)
		if !ok {
			t.Errorf("%q (%s): not mapped", tc.text, tc.target)
			continue
		}
		if ast.TypeText(got) != tc.want {
			t.Errorf("%q (%s) = %s, want %s", tc.text, tc.target, ast.TypeText(got), tc.want)
		}
	}
	for _, text := range []string{"struct Box", "union U", "long double"} {
		if _, ok := clangType(text, ""); ok {
			t.Errorf("%q was mapped but has no table entry", text)
		}
	}
}
