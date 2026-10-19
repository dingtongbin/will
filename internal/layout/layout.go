// Package layout computes the byte layout of a type. The rules follow the
// target ABI: a scalar aligns to its own size up to four bytes and to eight
// beyond that, a struct's alignment is the largest alignment of its fields, and
// its size is padded to that alignment. A field type that is not a scalar yet
// is reported instead of guessed.
package layout

import "fmt"

// Field is one declared field.
type Field struct {
	Name string
	Type string
}

// Struct is one type's layout.
type Struct struct {
	Name    string
	Size    int
	Align   int
	Fields  []Field
	Offsets []int
}

// Of computes the layout of a struct from its fields, in declaration order.
func Of(name string, fields []Field) (Struct, error) {
	s := Struct{Name: name, Align: 1, Fields: fields}
	offset := 0
	for _, f := range fields {
		size, align, ok := scalar(f.Type)
		if !ok {
			return Struct{}, fmt.Errorf("the layout of field %s is not computed yet: %s is not a scalar", f.Name, f.Type)
		}
		offset = roundUp(offset, align)
		s.Offsets = append(s.Offsets, offset)
		offset += size
		if align > s.Align {
			s.Align = align
		}
	}
	s.Size = roundUp(offset, s.Align)
	return s, nil
}

// Scalar returns the size and alignment of one scalar type; ok is false when
// the type is not a scalar yet.
func Scalar(name string) (size, align int, ok bool) {
	return scalar(name)
}

// scalar returns the size and alignment of one scalar type.
func scalar(name string) (size, align int, ok bool) {
	switch name {
	case "bool", "i8", "u8":
		return 1, 1, true
	case "i16", "u16":
		return 2, 2, true
	case "i32", "u32", "f32", "char":
		return 4, 4, true
	case "i64", "u64", "f64", "usize", "isize", "raw", "ptr":
		return 8, 8, true
	}
	return 0, 0, false
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
