// Package layout computes the byte layout of will's own types.
//
// The rules are the target ABI's rules for C: fields keep their declaration
// order, a field starts at the next multiple of its alignment, a struct's
// alignment is the largest alignment of its fields, and its size is padded to
// that alignment. A fixed-length array uses the element's stride. An enum puts
// a u32 discriminant first and lays the payload area out as a union of the
// variants. The unit type and a struct without fields take no bytes and align
// to one. Nothing is reordered and nothing is packed. An extern type is
// reported instead of guessed because its layout comes from its header.
package layout

import "fmt"

// Target is what the target triple decides about layout: the width of a word
// in bytes. Every supported triple is 64 bit.
type Target struct {
	Word int
}

// Type is one computed layout.
type Type struct {
	Size  int
	Align int
}

// Kind classifies a type specification.
type Kind uint8

const (
	// UnitKind is the empty type ().
	UnitKind Kind = iota
	// ScalarKind is a built-in scalar type.
	ScalarKind
	// NamedKind is a struct or enum declared in the program.
	NamedKind
	// ArrayKind is a fixed-length array [N]T.
	ArrayKind
	// SliceKind is a read-only view []T.
	SliceKind
	// RefKind is a ref T or mut T pointer.
	RefKind
	// GenericKind is a predeclared generic type with arguments.
	GenericKind
	// FuncKind is a function value: a code pointer and an environment.
	FuncKind
)

// Spec is one type to lay out.
type Spec struct {
	Kind Kind
	Name string
	Elem *Spec
	Len  int
	Args []*Spec
}

// Field is one declared field.
type Field struct {
	Name string
	Type *Spec
}

// Variant is one enum variant with its payload types in order.
type Variant struct {
	Name string
	Args []*Spec
}

// Placed is one field or variant placed inside a named type.
type Placed struct {
	Name   string
	Offset int
	Size   int
}

// Named is the layout of one struct or enum.
type Named struct {
	Name   string
	Type   Type
	Fields []Placed
}

// Resolver lays out the named types of one program.
type Resolver struct {
	word       int
	structs    map[string][]Field
	enums      map[string][]Variant
	interfaces map[string]bool
	order      []string
	computed   map[string]Named
	busy       map[string]bool
}

// New returns a resolver for one target.
func New(t Target) *Resolver {
	word := t.Word
	if word == 0 {
		word = 8
	}
	return &Resolver{
		word:       word,
		structs:    make(map[string][]Field),
		enums:      make(map[string][]Variant),
		interfaces: make(map[string]bool),
		computed:   make(map[string]Named),
		busy:       make(map[string]bool),
	}
}

// AddStruct registers one struct. Fields keep declaration order.
func (r *Resolver) AddStruct(name string, fields []Field) error {
	if r.known(name) {
		return fmt.Errorf("type %s is declared twice", name)
	}
	r.structs[name] = fields
	r.order = append(r.order, name)
	return nil
}

// AddInterface registers one interface. An interface value is a data pointer
// and a method-table pointer, so its layout is two words and never needs the
// method set.
func (r *Resolver) AddInterface(name string) error {
	if r.known(name) {
		return fmt.Errorf("type %s is declared twice", name)
	}
	r.interfaces[name] = true
	r.order = append(r.order, name)
	return nil
}

// AddEnum registers one enum. Variants keep declaration order.
func (r *Resolver) AddEnum(name string, variants []Variant) error {
	if r.known(name) {
		return fmt.Errorf("type %s is declared twice", name)
	}
	r.enums[name] = variants
	r.order = append(r.order, name)
	return nil
}

// Names returns the registered names in declaration order.
func (r *Resolver) Names() []string {
	return append([]string(nil), r.order...)
}

func (r *Resolver) known(name string) bool {
	_, isStruct := r.structs[name]
	_, isEnum := r.enums[name]
	return isStruct || isEnum || r.interfaces[name]
}

// Layout returns the layout of one named type and the place of each of its
// fields or variants.
func (r *Resolver) Layout(name string) (Named, error) {
	if got, ok := r.computed[name]; ok {
		return got, nil
	}
	if r.busy[name] {
		return Named{}, fmt.Errorf("type %s contains itself", name)
	}
	r.busy[name] = true
	defer delete(r.busy, name)

	var got Named
	var err error
	if fields, ok := r.structs[name]; ok {
		got, err = r.structLayout(name, fields)
	} else if variants, ok := r.enums[name]; ok {
		got, err = r.enumLayout(name, variants)
	} else if r.interfaces[name] {
		got = Named{Name: name, Type: Type{Size: 2 * r.word, Align: r.word}}
	} else if name == "Error" {
		// Error is predeclared and has no source declaration.
		got = Named{Name: name, Type: Type{Size: 2 * r.word, Align: r.word}}
	} else {
		return Named{}, fmt.Errorf("the layout of type %s is not known", name)
	}
	if err != nil {
		return Named{}, err
	}
	r.computed[name] = got
	return got, nil
}

// Size returns the layout of one type specification.
func (r *Resolver) Size(s *Spec) (Type, error) {
	if s == nil {
		return Type{}, fmt.Errorf("the layout of a missing type is not known")
	}
	switch s.Kind {
	case UnitKind:
		return Type{Size: 0, Align: 1}, nil
	case ScalarKind:
		return r.scalar(s.Name)
	case NamedKind:
		got, err := r.Layout(s.Name)
		if err != nil {
			return Type{}, err
		}
		return got.Type, nil
	case ArrayKind:
		elem, err := r.Size(s.Elem)
		if err != nil {
			return Type{}, err
		}
		stride := roundUp(elem.Size, elem.Align)
		return Type{Size: s.Len * stride, Align: elem.Align}, nil
	case SliceKind:
		return Type{Size: 2 * r.word, Align: r.word}, nil
	case RefKind:
		return Type{Size: r.word, Align: r.word}, nil
	case GenericKind:
		return r.generic(s)
	case FuncKind:
		return Type{Size: 2 * r.word, Align: r.word}, nil
	}
	return Type{}, fmt.Errorf("the layout of this type is not known")
}

// SizeOf returns the size of one named type.
func (r *Resolver) SizeOf(name string) (int, error) {
	got, err := r.Layout(name)
	if err != nil {
		return 0, err
	}
	return got.Type.Size, nil
}

// AlignOf returns the alignment of one named type.
func (r *Resolver) AlignOf(name string) (int, error) {
	got, err := r.Layout(name)
	if err != nil {
		return 0, err
	}
	return got.Type.Align, nil
}

// OffsetOf returns the offset of one field or variant inside a named type.
func (r *Resolver) OffsetOf(name, field string) (int, error) {
	got, err := r.Layout(name)
	if err != nil {
		return 0, err
	}
	for _, f := range got.Fields {
		if f.Name == field {
			return f.Offset, nil
		}
	}
	return 0, fmt.Errorf("type %s has no field %s", name, field)
}

// structLayout places the fields of one struct in declaration order.
func (r *Resolver) structLayout(name string, fields []Field) (Named, error) {
	got := Named{Name: name, Type: Type{Align: 1}}
	offset := 0
	for _, f := range fields {
		t, err := r.Size(f.Type)
		if err != nil {
			return Named{}, fmt.Errorf("field %s of type %s: %w", f.Name, name, err)
		}
		offset = roundUp(offset, t.Align)
		got.Fields = append(got.Fields, Placed{Name: f.Name, Offset: offset, Size: t.Size})
		offset += t.Size
		if t.Align > got.Type.Align {
			got.Type.Align = t.Align
		}
	}
	got.Type.Size = roundUp(offset, got.Type.Align)
	return got, nil
}

// enumLayout places the u32 discriminant first and the payload area as a union
// of the variants.
func (r *Resolver) enumLayout(name string, variants []Variant) (Named, error) {
	payloadAlign := 1
	payloadSize := 0
	payloads := make([]Type, len(variants))
	for i, v := range variants {
		t, err := r.fieldsSize(v.Args)
		if err != nil {
			return Named{}, fmt.Errorf("variant %s of type %s: %w", v.Name, name, err)
		}
		payloads[i] = t
		if t.Align > payloadAlign {
			payloadAlign = t.Align
		}
		if t.Size > payloadSize {
			payloadSize = t.Size
		}
	}
	offset := roundUp(4, payloadAlign)
	got := Named{Name: name, Type: Type{Align: payloadAlign}}
	if got.Type.Align < 4 {
		got.Type.Align = 4
	}
	got.Type.Size = roundUp(offset+payloadSize, got.Type.Align)
	got.Fields = append(got.Fields, Placed{Name: "tag", Offset: 0, Size: 4})
	for i, v := range variants {
		got.Fields = append(got.Fields, Placed{Name: v.Name, Offset: offset, Size: payloads[i].Size})
	}
	return got, nil
}

// fieldsSize lays out a list of types as a struct.
func (r *Resolver) fieldsSize(specs []*Spec) (Type, error) {
	got := Type{Align: 1}
	offset := 0
	for _, s := range specs {
		t, err := r.Size(s)
		if err != nil {
			return Type{}, err
		}
		offset = roundUp(offset, t.Align)
		offset += t.Size
		if t.Align > got.Align {
			got.Align = t.Align
		}
	}
	got.Size = roundUp(offset, got.Align)
	return got, nil
}

// scalar returns the layout of one built-in scalar type.
func (r *Resolver) scalar(name string) (Type, error) {
	switch name {
	case "bool", "i8", "u8":
		return Type{Size: 1, Align: 1}, nil
	case "i16", "u16":
		return Type{Size: 2, Align: 2}, nil
	case "i32", "u32", "f32":
		return Type{Size: 4, Align: 4}, nil
	case "i64", "u64", "f64":
		return Type{Size: 8, Align: 8}, nil
	case "usize", "isize", "raw":
		return Type{Size: r.word, Align: r.word}, nil
	case "str":
		return Type{Size: 2 * r.word, Align: r.word}, nil
	}
	return Type{}, fmt.Errorf("%s is not a scalar type", name)
}

// generic returns the layout of one predeclared generic type.
func (r *Resolver) generic(s *Spec) (Type, error) {
	switch s.Name {
	case "Array", "Set":
		if len(s.Args) != 1 {
			return Type{}, fmt.Errorf("%s needs one type argument", s.Name)
		}
		return Type{Size: 3 * r.word, Align: r.word}, nil
	case "Map":
		if len(s.Args) != 2 {
			return Type{}, fmt.Errorf("Map needs two type arguments")
		}
		return Type{Size: 3 * r.word, Align: r.word}, nil
	case "rc", "arc", "weak_rc", "weak_arc":
		if len(s.Args) != 1 {
			return Type{}, fmt.Errorf("%s needs one type argument", s.Name)
		}
		return Type{Size: r.word, Align: r.word}, nil
	case "chan", "Task":
		if len(s.Args) != 1 {
			return Type{}, fmt.Errorf("%s needs one type argument", s.Name)
		}
		return Type{Size: r.word, Align: r.word}, nil
	case "Option":
		if len(s.Args) != 1 {
			return Type{}, fmt.Errorf("Option needs one type argument")
		}
		return r.enumOf([][]*Spec{{s.Args[0]}, nil})
	case "Result":
		if len(s.Args) != 2 {
			return Type{}, fmt.Errorf("Result needs two type arguments")
		}
		return r.enumOf([][]*Spec{{s.Args[0]}, {s.Args[1]}})
	}
	return Type{}, fmt.Errorf("the layout of type %s is not known", s.Name)
}

// enumOf lays out a predeclared enum from its payload lists.
func (r *Resolver) enumOf(payloads [][]*Spec) (Type, error) {
	payloadAlign := 1
	payloadSize := 0
	for _, args := range payloads {
		t, err := r.fieldsSize(args)
		if err != nil {
			return Type{}, err
		}
		if t.Align > payloadAlign {
			payloadAlign = t.Align
		}
		if t.Size > payloadSize {
			payloadSize = t.Size
		}
	}
	align := payloadAlign
	if align < 4 {
		align = 4
	}
	offset := roundUp(4, payloadAlign)
	return Type{Size: roundUp(offset+payloadSize, align), Align: align}, nil
}

// roundUp rounds n up to the next multiple of align.
func roundUp(n, align int) int {
	if align <= 1 {
		return n
	}
	if rest := n % align; rest != 0 {
		n += align - rest
	}
	return n
}
