package layout

import "testing"

func TestLayoutOfScalarStructs(t *testing.T) {
	cases := []struct {
		name    string
		fields  []Field
		size    int
		align   int
		offsets []int
	}{
		{"two i64", []Field{{"x", "i64"}, {"y", "i64"}}, 16, 8, []int{0, 8}},
		{"two i32", []Field{{"x", "i32"}, {"y", "i32"}}, 8, 4, []int{0, 4}},
		{"i8 then i32 pads", []Field{{"a", "i8"}, {"b", "i32"}}, 8, 4, []int{0, 4}},
		{"i32 then i8 pads to align", []Field{{"a", "i32"}, {"b", "i8"}}, 8, 4, []int{0, 4}},
		{"i8 then i64 pads", []Field{{"a", "i8"}, {"b", "i64"}}, 16, 8, []int{0, 8}},
		{"bool", []Field{{"a", "bool"}}, 1, 1, []int{0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Of("T", c.fields)
			if err != nil {
				t.Fatalf("layout: %v", err)
			}
			if got.Size != c.size || got.Align != c.align {
				t.Errorf("size = %d, align = %d, want %d and %d", got.Size, got.Align, c.size, c.align)
			}
			for i, want := range c.offsets {
				if got.Offsets[i] != want {
					t.Errorf("offset %d = %d, want %d", i, got.Offsets[i], want)
				}
			}
		})
	}
}

func TestLayoutRejectsUnsupportedFields(t *testing.T) {
	if _, err := Of("T", []Field{{"a", "Point"}}); err == nil {
		t.Fatal("a struct field was laid out although its own layout is not computed")
	}
}
