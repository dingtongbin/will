package layout

import "testing"

// TestStructLayout checks the struct rule: declaration order, each field at the
// next multiple of its alignment, and the size padded to the type's alignment.
func TestStructLayout(t *testing.T) {
	cases := []struct {
		name    string
		fields  []Field
		size    int
		align   int
		offsets []int
	}{
		{"two i64", []Field{{"x", scalar("i64")}, {"y", scalar("i64")}}, 16, 8, []int{0, 8}},
		{"two i32", []Field{{"x", scalar("i32")}, {"y", scalar("i32")}}, 8, 4, []int{0, 4}},
		{"i8 then i32 pads", []Field{{"a", scalar("i8")}, {"b", scalar("i32")}}, 8, 4, []int{0, 4}},
		{"i32 then i8 pads to align", []Field{{"a", scalar("i32")}, {"b", scalar("i8")}}, 8, 4, []int{0, 4}},
		{"i8 then i64 pads", []Field{{"a", scalar("i8")}, {"b", scalar("i64")}}, 16, 8, []int{0, 8}},
		{"bool", []Field{{"a", scalar("bool")}}, 1, 1, []int{0}},
		{"unit field takes no bytes", []Field{{"a", scalar("i32")}, {"u", &Spec{Kind: UnitKind}}, {"b", scalar("i8")}}, 8, 4, []int{0, 4, 4}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := New(Target{Word: 8})
			if err := res.AddStruct("T", c.fields); err != nil {
				t.Fatalf("add: %v", err)
			}
			got, err := res.Layout("T")
			if err != nil {
				t.Fatalf("layout: %v", err)
			}
			if got.Type.Size != c.size || got.Type.Align != c.align {
				t.Errorf("size = %d, align = %d, want %d and %d", got.Type.Size, got.Type.Align, c.size, c.align)
			}
			for i, want := range c.offsets {
				if got.Fields[i].Offset != want {
					t.Errorf("offset %d = %d, want %d", i, got.Fields[i].Offset, want)
				}
			}
		})
	}
}

// TestEmptyStruct checks that a struct without fields takes no bytes and
// aligns to one.
func TestEmptyStruct(t *testing.T) {
	res := New(Target{Word: 8})
	if err := res.AddStruct("Empty", nil); err != nil {
		t.Fatal(err)
	}
	got, err := res.Layout("Empty")
	if err != nil {
		t.Fatal(err)
	}
	if got.Type.Size != 0 || got.Type.Align != 1 {
		t.Fatalf("size = %d, align = %d, want 0 and 1", got.Type.Size, got.Type.Align)
	}
}

// TestNestedStruct checks that a struct field is placed by the nested type's
// own size and alignment.
func TestNestedStruct(t *testing.T) {
	res := New(Target{Word: 8})
	if err := res.AddStruct("Inner", []Field{{"a", scalar("i8")}, {"b", scalar("i32")}}); err != nil {
		t.Fatal(err)
	}
	if err := res.AddStruct("Outer", []Field{{"tag", scalar("i8")}, {"inner", named("Inner")}}); err != nil {
		t.Fatal(err)
	}
	got, err := res.Layout("Outer")
	if err != nil {
		t.Fatal(err)
	}
	if got.Type.Size != 12 || got.Type.Align != 4 {
		t.Fatalf("size = %d, align = %d, want 12 and 4", got.Type.Size, got.Type.Align)
	}
	if got.Fields[1].Offset != 4 {
		t.Fatalf("inner offset = %d, want 4", got.Fields[1].Offset)
	}
	if off, err := res.OffsetOf("Outer", "inner"); err != nil || off != 4 {
		t.Fatalf("OffsetOf = %d, %v, want 4", off, err)
	}
}

// TestArrayLayout checks the stride rule: the element size rounded up to the
// element alignment.
func TestArrayLayout(t *testing.T) {
	res := New(Target{Word: 8})
	if err := res.AddStruct("Inner", []Field{{"a", scalar("i8")}, {"b", scalar("i32")}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		spec  *Spec
		size  int
		align int
	}{
		{"three i32", array(3, scalar("i32")), 12, 4},
		{"two i64", array(2, scalar("i64")), 16, 8},
		{"three i8", array(3, scalar("i8")), 3, 1},
		{"two nested", array(2, named("Inner")), 16, 4},
		{"zero length", array(0, scalar("i64")), 0, 8},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := res.Size(c.spec)
			if err != nil {
				t.Fatalf("size: %v", err)
			}
			if got.Size != c.size || got.Align != c.align {
				t.Fatalf("size = %d, align = %d, want %d and %d", got.Size, got.Align, c.size, c.align)
			}
		})
	}
}

// TestEnumLayout checks the discriminant first and the payload area as a union
// of the variants.
func TestEnumLayout(t *testing.T) {
	res := New(Target{Word: 8})
	err := res.AddEnum("Value", []Variant{
		{Name: "None"},
		{Name: "Number", Args: []*Spec{scalar("i64")}},
		{Name: "Pair", Args: []*Spec{scalar("i32"), scalar("i8")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := res.Layout("Value")
	if err != nil {
		t.Fatal(err)
	}
	if got.Type.Size != 16 || got.Type.Align != 8 {
		t.Fatalf("size = %d, align = %d, want 16 and 8", got.Type.Size, got.Type.Align)
	}
	want := []Placed{{"tag", 0, 4}, {"None", 8, 0}, {"Number", 8, 8}, {"Pair", 8, 8}}
	for i, w := range want {
		if got.Fields[i] != w {
			t.Errorf("field %d = %+v, want %+v", i, got.Fields[i], w)
		}
	}
}

// TestEnumWithoutPayload checks that an enum of unit variants is one u32.
func TestEnumWithoutPayload(t *testing.T) {
	res := New(Target{Word: 8})
	if err := res.AddEnum("Color", []Variant{{Name: "Red"}, {Name: "Green"}}); err != nil {
		t.Fatal(err)
	}
	got, err := res.Layout("Color")
	if err != nil {
		t.Fatal(err)
	}
	if got.Type.Size != 4 || got.Type.Align != 4 {
		t.Fatalf("size = %d, align = %d, want 4 and 4", got.Type.Size, got.Type.Align)
	}
}

// TestPredeclaredLayout checks the types the language predeclares.
func TestPredeclaredLayout(t *testing.T) {
	res := New(Target{Word: 8})
	cases := []struct {
		name  string
		spec  *Spec
		size  int
		align int
	}{
		{"str", scalar("str"), 16, 8},
		{"view", &Spec{Kind: SliceKind, Elem: scalar("i32")}, 16, 8},
		{"array", &Spec{Kind: GenericKind, Name: "Array", Args: []*Spec{scalar("i32")}}, 24, 8},
		{"rc", &Spec{Kind: GenericKind, Name: "rc", Args: []*Spec{scalar("i64")}}, 8, 8},
		{"chan", &Spec{Kind: GenericKind, Name: "chan", Args: []*Spec{scalar("i32")}}, 8, 8},
		{"option", &Spec{Kind: GenericKind, Name: "Option", Args: []*Spec{scalar("i64")}}, 16, 8},
		{"result", &Spec{Kind: GenericKind, Name: "Result", Args: []*Spec{scalar("i32"), scalar("u64")}}, 16, 8},
		{"usize", scalar("usize"), 8, 8},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := res.Size(c.spec)
			if err != nil {
				t.Fatalf("size: %v", err)
			}
			if got.Size != c.size || got.Align != c.align {
				t.Fatalf("size = %d, align = %d, want %d and %d", got.Size, got.Align, c.size, c.align)
			}
		})
	}
}

// TestUnknownTypesAreReported checks that the resolver reports a type it cannot
// lay out instead of guessing.
func TestUnknownTypesAreReported(t *testing.T) {
	res := New(Target{Word: 8})
	cases := []*Spec{
		named("Missing"),
		&Spec{Kind: GenericKind, Name: "Map", Args: []*Spec{scalar("i32"), scalar("i32")}},
		scalar("nonsense"),
	}
	for _, c := range cases {
		if _, err := res.Size(c); err == nil {
			t.Errorf("Size(%+v) succeeded, want an error", c)
		}
	}
}

// TestCycleIsReported checks that a type that contains itself is reported.
func TestCycleIsReported(t *testing.T) {
	res := New(Target{Word: 8})
	if err := res.AddStruct("A", []Field{{"b", named("B")}}); err != nil {
		t.Fatal(err)
	}
	if err := res.AddStruct("B", []Field{{"a", named("A")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := res.Layout("A"); err == nil {
		t.Fatal("a cycle was laid out")
	}
}

// TestDuplicateNameIsReported checks that one name is declared once.
func TestDuplicateNameIsReported(t *testing.T) {
	res := New(Target{Word: 8})
	if err := res.AddStruct("T", nil); err != nil {
		t.Fatal(err)
	}
	if err := res.AddEnum("T", nil); err == nil {
		t.Fatal("a second declaration of T was accepted")
	}
}

// scalar is a scalar specification.
func scalar(name string) *Spec {
	return &Spec{Kind: ScalarKind, Name: name}
}

// named is a named specification.
func named(name string) *Spec {
	return &Spec{Kind: NamedKind, Name: name}
}

// array is a fixed-length array specification.
func array(n int, elem *Spec) *Spec {
	return &Spec{Kind: ArrayKind, Len: n, Elem: elem}
}
