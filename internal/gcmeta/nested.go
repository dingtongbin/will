package gcmeta

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// ensureNested fills the recursive scan metadata of one declaration after all
// declaration layouts exist. Class values are references, so only structs and
// enums recurse into their payload.
func (p *Program) ensureNested(name string) error {
	t, ok := p.types[name]
	if !ok || t == nil || t.Kind == KindInterface {
		return nil
	}
	if t.Nested != nil || t.Variants != nil {
		return nil
	}
	decl := p.decls[name]
	if decl == nil {
		return nil
	}
	switch t.Kind {
	case KindClass, KindStruct:
		seen := map[string]bool{name: true}
		type fieldPlan struct {
			index int
			typ   *Type
		}
		var plans []fieldPlan
		useTable := false
		for i, f := range decl.fields {
			if i >= len(t.Fields) {
				break
			}
			ft, err := p.nestedType(decl.pkg, fieldText(f, "type"), seen)
			if err != nil {
				return err
			}
			plans = append(plans, fieldPlan{index: i, typ: ft})
			if hasNestedTable(ft) {
				useTable = true
			}
		}
		if useTable {
			for _, plan := range plans {
				ft := plan.typ
				if !hasNestedScan(ft) {
					continue
				}
				t.Nested = append(t.Nested, NestedField{
					Offset: uint32(t.Fields[plan.index].Offset),
					Type:   ft.Name,
					Weak: len(ft.Weaks) != 0 && len(ft.Refs) == 0 &&
						len(ft.Nested) == 0 && len(ft.Variants) == 0,
				})
			}
		}
	case KindEnum:
		if err := p.fillEnumNested(t, decl, map[string]bool{name: true}); err != nil {
			return err
		}
	}
	return nil
}

// fillEnumNested builds the tag-aware variant table of one enum declaration.
func (p *Program) fillEnumNested(t *Type, decl *declaration, seen map[string]bool) error {
	payloadAlign := 1
	for _, v := range decl.variants {
		args, err := p.variantArgs(decl.pkg, v, seen)
		if err != nil {
			return err
		}
		if args.align > payloadAlign {
			payloadAlign = args.align
		}
	}
	payloadOffset := alignUp(4, max(4, payloadAlign))
	t.TagOffset = 0
	for index, v := range decl.variants {
		args, err := p.variantArgs(decl.pkg, v, seen)
		if err != nil {
			return err
		}
		typeName := ""
		if args.size != 0 {
			payload, err := p.variantPayload(decl, index, v, seen)
			if err != nil {
				return err
			}
			typeName = payload.Name
		}
		t.Variants = append(t.Variants, NestedVariant{
			Tag:    int32(index),
			Offset: uint32(payloadOffset),
			Type:   typeName,
		})
	}
	return nil
}

// variantPayload returns the descriptor of one variant's payload. A multi-arg
// variant is an inline tuple whose fields are scanned recursively.
func (p *Program) variantPayload(decl *declaration, index int, variant willc.Record, seen map[string]bool) (*Type, error) {
	var args []willc.Record
	for _, f := range variant.Fields {
		if f.Name != "arg" || f.Value.Kind != willc.RefValue {
			continue
		}
		if rec, ok := p.records[f.Value.Ref]; ok {
			args = append(args, rec)
		}
	}
	if len(args) == 1 {
		return p.nestedType(decl.pkg, fieldText(args[0], "text"), seen)
	}
	name := fmt.Sprintf("value.%s.variant.%d", decl.name, index)
	if existing := p.inline[name]; existing != nil {
		return existing, nil
	}
	t := &Type{Name: name, Kind: KindStruct}
	type argPlan struct {
		offset int
		typ    *Type
	}
	var plans []argPlan
	useTable := false
	offset := 0
	align := 1
	for _, arg := range args {
		ft, err := p.nestedType(decl.pkg, fieldText(arg, "text"), seen)
		if err != nil {
			return nil, err
		}
		fieldOffset := alignUp(offset, ft.Align)
		plans = append(plans, argPlan{offset: fieldOffset, typ: ft})
		if hasNestedTable(ft) {
			useTable = true
		}
		offset = fieldOffset + ft.Size
		if ft.Align > align {
			align = ft.Align
		}
	}
	for _, plan := range plans {
		ft := plan.typ
		if useTable {
			if hasNestedScan(ft) {
				t.Nested = append(t.Nested, NestedField{
					Offset: uint32(plan.offset),
					Type:   ft.Name,
					Weak: len(ft.Weaks) != 0 && len(ft.Refs) == 0 &&
						len(ft.Nested) == 0 && len(ft.Variants) == 0,
				})
			}
			continue
		}
		t.Refs = append(t.Refs, shiftOffsets(ft.Refs, plan.offset)...)
		t.Weaks = append(t.Weaks, shiftOffsets(ft.Weaks, plan.offset)...)
	}
	t.Size = alignUp(offset, align)
	t.Align = align
	p.inline[name] = t
	return t, nil
}

// nestedType returns a descriptor for one inline value. Named structs and enums
// use their declaration descriptor; class values and anonymous shapes get an
// inline descriptor.
func (p *Program) nestedType(pkg, text string, seen map[string]bool) (*Type, error) {
	text = strings.TrimSpace(text)
	canon, err := p.canonicalText(pkg, text, 0)
	if err != nil {
		return nil, err
	}
	text = canon
	node, err := parseTypeText(text)
	if err != nil {
		return nil, err
	}
	switch node.kind {
	case typeUnit:
		return p.inlineValue("value.()", 0, 1, nil, nil), nil
	case typeView:
		return p.inlineValue("value."+text, 2*WordSize, WordSize, nil, nil), nil
	case typeArray:
		elem, err := p.nestedType(pkg, typeTextOf(node.elem), seen)
		if err != nil {
			return nil, err
		}
		value, err := p.layoutValue(pkg, text, seen)
		if err != nil {
			return nil, err
		}
		t := &Type{Name: "value." + text, Kind: KindStruct, Size: value.size, Align: value.align}
		if hasNestedScan(elem) {
			stride := alignUp(elem.Size, elem.Align)
			for i := 0; i < node.length; i++ {
				t.Nested = append(t.Nested, NestedField{
					Offset: uint32(i * stride),
					Type:   elem.Name,
					Weak: len(elem.Weaks) != 0 && len(elem.Refs) == 0 &&
						len(elem.Nested) == 0 && len(elem.Variants) == 0,
				})
			}
		}
		p.inline[t.Name] = t
		return t, nil
	case typeFunc:
		return p.inlineValue("value."+text, 2*WordSize, WordSize, []uint32{WordSize}, nil), nil
	}
	// A canonical generic instance names one declaration in the table; its
	// descriptor is the declaration descriptor, except for a class, whose
	// value is one reference.
	if decl, ok := p.decls[text]; ok {
		if decl.kind == KindClass {
			return p.inlineValue("value."+text, WordSize, WordSize, []uint32{0}, nil), nil
		}
		if decl.kind == KindInterface {
			return p.inlineValue("value."+text, 2*WordSize, WordSize, []uint32{0}, nil), nil
		}
		if err := p.ensureNested(decl.name); err != nil {
			return nil, err
		}
		if t := p.types[decl.name]; t != nil {
			return t, nil
		}
		return nil, fmt.Errorf("type %s has no GC layout", text)
	}
	name := node.name
	switch name {
	case "bool", "i8", "u8":
		return p.inlineValue("value."+text, 1, 1, nil, nil), nil
	case "i16", "u16":
		return p.inlineValue("value."+text, 2, 2, nil, nil), nil
	case "i32", "u32", "f32":
		return p.inlineValue("value."+text, 4, 4, nil, nil), nil
	case "i64", "u64", "f64", "usize", "isize", "raw":
		return p.inlineValue("value."+text, 8, 8, nil, nil), nil
	case "str", "bytes":
		return p.inlineValue("value."+text, 2*WordSize, WordSize, []uint32{0}, nil), nil
	case "Error", "Value", "Show", "interface {}":
		return p.inlineValue("value."+text, 2*WordSize, WordSize, []uint32{0}, nil), nil
	case "ref", "mut":
		return p.inlineValue("value."+text, WordSize, WordSize, nil, nil), nil
	case "Array", "Map", "Set":
		return p.inlineValue("value."+text, 3*WordSize, WordSize, []uint32{0}, nil), nil
	case "list", "dict", "set":
		return p.inlineValue("value."+text, WordSize, WordSize, []uint32{0}, nil), nil
	case "chan", "Task":
		return p.inlineValue("value."+text, WordSize, WordSize, []uint32{0}, nil), nil
	case "Weak":
		return p.inlineValue("value."+text, WordSize, WordSize, nil, []uint32{0}), nil
	case "Pinned", "Handle":
		return p.inlineValue("value."+text, WordSize, WordSize, nil, nil), nil
	case "Option", "Result":
		return p.taggedNested(pkg, text, node, seen)
	case "rc", "arc", "weak_rc", "weak_arc":
		return p.inlineValue("value."+text, WordSize, WordSize, nil, nil), nil
	}
	if strings.HasPrefix(text, "[]") {
		return p.inlineValue("value."+text, 2*WordSize, WordSize, nil, nil), nil
	}
	resolved, ok := p.resolveDecl(pkg, name)
	if !ok {
		return nil, fmt.Errorf("type %s is not declared", text)
	}
	decl := p.decls[resolved]
	if decl == nil {
		return nil, fmt.Errorf("type %s has no declaration", resolved)
	}
	if decl.kind == KindClass {
		return p.inlineValue("value."+text, WordSize, WordSize, []uint32{0}, nil), nil
	}
	if err := p.ensureNested(resolved); err != nil {
		return nil, err
	}
	if t := p.types[resolved]; t != nil {
		return t, nil
	}
	return nil, fmt.Errorf("type %s has no GC layout", text)
}

// taggedNested builds the tag-aware descriptor of Option<T> or Result<T,E>.
func (p *Program) taggedNested(pkg, text string, node *typeNode, seen map[string]bool) (*Type, error) {
	name := "value." + text
	if existing := p.inline[name]; existing != nil {
		return existing, nil
	}
	var payloads []*Type
	for _, arg := range node.args {
		payload, err := p.nestedType(pkg, typeTextOf(arg), seen)
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, payload)
	}
	payloadAlign := 4
	payloadSize := 0
	for _, payload := range payloads {
		if payload.Align > payloadAlign {
			payloadAlign = payload.Align
		}
		if payload.Size > payloadSize {
			payloadSize = payload.Size
		}
	}
	offset := alignUp(4, payloadAlign)
	size := alignUp(offset+payloadSize, payloadAlign)
	t := &Type{Name: name, Kind: KindEnum, Size: size, Align: payloadAlign}
	if node.name == "Option" {
		t.Variants = []NestedVariant{
			{Tag: 0, Offset: uint32(offset)},
			{Tag: 1, Offset: uint32(offset), Type: payloads[0].Name},
		}
	} else {
		t.Variants = []NestedVariant{
			{Tag: 0, Offset: uint32(offset), Type: payloads[0].Name},
			{Tag: 1, Offset: uint32(offset), Type: payloads[1].Name},
		}
	}
	p.inline[name] = t
	return t, nil
}

func (p *Program) inlineValue(name string, size, align int, refs, weaks []uint32) *Type {
	if existing := p.inline[name]; existing != nil {
		return existing
	}
	t := &Type{Name: name, Kind: KindStruct, Size: size, Align: align,
		Refs: refs, Weaks: weaks}
	p.inline[name] = t
	return t
}

func hasNestedScan(t *Type) bool {
	return t != nil && (len(t.Refs) != 0 || len(t.Weaks) != 0 ||
		len(t.Nested) != 0 || len(t.Variants) != 0)
}

func hasNestedTable(t *Type) bool {
	return t != nil && (len(t.Nested) != 0 || len(t.Variants) != 0)
}
