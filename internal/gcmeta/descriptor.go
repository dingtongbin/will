package gcmeta

import (
	"fmt"
	"strconv"
	"strings"
)

// DescriptorSymbol is the stable LLVM global that holds one type descriptor.
// The source name is quoted because package paths contain characters LLVM does
// not allow in an unquoted identifier.
func DescriptorSymbol(name string) string {
	return `@"willrt.gc.type.` + escapeName(name) + `"`
}

// RefSymbol is the LLVM global that holds one descriptor's reference offsets.
func RefSymbol(name string) string {
	return `@"willrt.gc.refs.` + escapeName(name) + `"`
}

// WeakSymbol is the LLVM global that holds one descriptor's weak offsets.
func WeakSymbol(name string) string {
	return `@"willrt.gc.weak.` + escapeName(name) + `"`
}

// FieldSymbol is the LLVM global that holds one nested field table.
func FieldSymbol(name string) string {
	return `@"willrt.gc.fields.` + escapeName(name) + `"`
}

// VariantSymbol is the LLVM global that holds one enum variant table.
func VariantSymbol(name string) string {
	return `@"willrt.gc.variants.` + escapeName(name) + `"`
}

func escapeName(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, `\`, `\\`), `"`, `\"`)
}

// DescriptorText renders the descriptor and its offset arrays. The runtime
// layout is the one in willrt_gc.h: five u32 fields, a reference-offset pointer,
// a weak count, and a weak-offset pointer.
func DescriptorText(t *Type) (string, error) {
	if t == nil {
		return "", fmt.Errorf("nil GC type")
	}
	if t.ID > TypeIDMax {
		return "", fmt.Errorf("type %s id %d exceeds the header limit", t.Name, t.ID)
	}
	if t.Sequence {
		if t.ElemSize <= 0 {
			return "", fmt.Errorf("sequence %s has no element size", t.Name)
		}
		if len(t.Refs) != 0 || len(t.Weaks) != 0 {
			return "", fmt.Errorf("sequence %s must not carry fixed offsets", t.Name)
		}
	}
	if len(t.Refs) > 0xffffffff || len(t.Weaks) > 0xffffffff {
		return "", fmt.Errorf("type %s has too many roots", t.Name)
	}
	var b strings.Builder
	refName := "null"
	if len(t.Refs) > 0 {
		refName = RefSymbol(t.Name)
		fmt.Fprintf(&b, "%s = private constant [%d x i32] [", refName, len(t.Refs))
		for i, offset := range t.Refs {
			if i != 0 {
				b.WriteString(", ")
			}
			b.WriteString("i32 ")
			b.WriteString(strconv.FormatUint(uint64(offset), 10))
		}
		b.WriteString("]\n")
	}
	weakName := "null"
	if len(t.Weaks) > 0 {
		weakName = WeakSymbol(t.Name)
		fmt.Fprintf(&b, "%s = private constant [%d x i32] [", weakName, len(t.Weaks))
		for i, offset := range t.Weaks {
			if i != 0 {
				b.WriteString(", ")
			}
			b.WriteString("i32 ")
			b.WriteString(strconv.FormatUint(uint64(offset), 10))
		}
		b.WriteString("]\n")
	}
	flags := 0
	if len(t.Refs) > 0 {
		flags |= 1
	}
	if t.Sequence {
		flags |= 8
	}
	nestedCount := len(t.Refs)
	if len(t.Nested) != 0 {
		flags |= 16
		nestedCount = len(t.Nested)
		refName = FieldSymbol(t.Name)
		fmt.Fprintf(&b, "%s = private constant [%d x { i32, i32, ptr }] [",
			refName, len(t.Nested))
		for i, field := range t.Nested {
			if i != 0 {
				b.WriteString(", ")
			}
			weakFlag := 0
			if field.Weak {
				weakFlag = 1
			}
			fmt.Fprintf(&b, "{ i32, i32, ptr } { i32 %d, i32 %d, ptr %s }",
				field.Offset, weakFlag, DescriptorSymbol(field.Type))
		}
		b.WriteString("]\n")
	}
	if len(t.Variants) != 0 {
		flags |= 32
		nestedCount = len(t.Variants)
		refName = VariantSymbol(t.Name)
		fmt.Fprintf(&b, "%s = private constant [%d x { i32, i32, ptr }] [",
			refName, len(t.Variants))
		for i, variant := range t.Variants {
			if i != 0 {
				b.WriteString(", ")
			}
			payload := "null"
			if variant.Type != "" {
				payload = DescriptorSymbol(variant.Type)
			}
			fmt.Fprintf(&b, "{ i32, i32, ptr } { i32 %d, i32 %d, ptr %s }",
				variant.Tag, variant.Offset, payload)
		}
		b.WriteString("]\n")
	}
	elemType := "null"
	if t.ElemType != "" {
		elemType = DescriptorSymbol(t.ElemType)
	}
	elemSize := 0
	if t.Sequence {
		elemSize = t.ElemSize
	}
	fmt.Fprintf(&b, "%s = private constant { i32, i32, i32, i32, i32, ptr, i32, ptr, i32, ptr } "+
		"{ i32 %d, i32 %d, i32 %d, i32 %d, i32 %d, ptr %s, i32 %d, ptr %s, i32 %d, ptr %s }\n",
		DescriptorSymbol(t.Name), t.ID, t.Size, t.Align, flags, nestedCount,
		refName, len(t.Weaks), weakName, elemSize, elemType)
	return b.String(), nil
}

// DescriptorBlock renders every declared type descriptor in sorted order. A
// body with no descriptor is valid; the caller decides whether the runtime
// needs the type-table declaration.
func DescriptorBlock(types []*Type) (string, error) {
	var b strings.Builder
	for _, t := range types {
		if t == nil || (t.Kind != KindClass && t.Kind != KindStruct &&
			t.Kind != KindEnum && t.Kind != KindInterface) {
			continue
		}
		text, err := DescriptorText(t)
		if err != nil {
			return "", err
		}
		b.WriteString(text)
	}
	return b.String(), nil
}
