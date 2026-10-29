package gcmeta

import (
	"fmt"
	"sort"

	"github.com/will-lang/will/internal/willc"
)

type valueLayout struct {
	size      int
	align     int
	refs      []uint32
	weaks     []uint32
	reference bool
	weak      bool
}

func (p *Program) layoutFields(pkg string, fields []willc.Record, seen map[string]bool) (int, int, []uint32, []uint32, *[]Field, error) {
	offset := 0
	align := 1
	var refs, weaks []uint32
	var placed []Field
	for _, f := range fields {
		text := fieldText(f, "type")
		value, err := p.layoutValue(pkg, text, seen)
		if err != nil {
			return 0, 0, nil, nil, nil, fmt.Errorf("field %s: %w", fieldText(f, "text"), err)
		}
		offset = alignUp(offset, value.align)
		entry := Field{Name: fieldText(f, "text"), Type: text, Offset: offset,
			Size: value.size}
		placed = append(placed, entry)
		if value.reference {
			refs = append(refs, uint32(offset))
		} else if value.weak {
			weaks = append(weaks, uint32(offset))
		} else {
			refs = append(refs, shiftOffsets(value.refs, offset)...)
			weaks = append(weaks, shiftOffsets(value.weaks, offset)...)
		}
		offset += value.size
		if value.align > align {
			align = value.align
		}
	}
	size := alignUp(offset, align)
	sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
	sort.Slice(weaks, func(i, j int) bool { return weaks[i] < weaks[j] })
	return size, align, refs, weaks, &placed, nil
}

func (p *Program) layoutValue(pkg, text string, seen map[string]bool) (valueLayout, error) {
	canon, err := p.canonicalText(pkg, text, 0)
	if err != nil {
		return valueLayout{}, err
	}
	text = canon
	if decl, ok := p.decls[text]; ok {
		return p.declLayout(decl, seen)
	}
	node, err := parseTypeText(text)
	if err != nil {
		return valueLayout{}, err
	}
	switch node.kind {
	case typeUnit:
		return valueLayout{align: 1}, nil
	case typeView:
		if _, err := p.layoutValue(pkg, typeTextOf(node.elem), seen); err != nil {
			return valueLayout{}, err
		}
		// A view borrows its source. The source handle owns the storage for the
		// whole borrow, so the view itself is not a GC root; treating an
		// interior pointer as an object reference would corrupt relocation.
		return valueLayout{size: 2 * WordSize, align: WordSize}, nil
	case typeArray:
		elem, err := p.layoutValue(pkg, typeTextOf(node.elem), seen)
		if err != nil {
			return valueLayout{}, err
		}
		stride := alignUp(elem.size, elem.align)
		value := valueLayout{size: node.length * stride, align: elem.align}
		for i := 0; i < node.length; i++ {
			base := i * stride
			if elem.reference {
				value.refs = append(value.refs, uint32(base))
			} else if elem.weak {
				value.weaks = append(value.weaks, uint32(base))
			} else {
				value.refs = append(value.refs, shiftOffsets(elem.refs, base)...)
				value.weaks = append(value.weaks, shiftOffsets(elem.weaks, base)...)
			}
		}
		return value, nil
	case typeFunc:
		return valueLayout{size: 2 * WordSize, align: WordSize,
			refs: []uint32{WordSize}}, nil
	}
	name := node.name
	switch name {
	case "bool", "i8", "u8":
		return valueLayout{size: 1, align: 1}, nil
	case "i16", "u16":
		return valueLayout{size: 2, align: 2}, nil
	case "i32", "u32", "f32":
		return valueLayout{size: 4, align: 4}, nil
	case "i64", "u64", "f64", "usize", "isize", "raw":
		return valueLayout{size: 8, align: 8}, nil
	case "str", "bytes":
		// Text storage is an immutable GC byte sequence. The data word is a
		// reference; the length stays a plain word.
		return valueLayout{size: 2 * WordSize, align: WordSize,
			refs: []uint32{0}}, nil
	case "Error", "Value", "Show", "interface {}":
		return valueLayout{size: 2 * WordSize, align: WordSize,
			refs: []uint32{0}}, nil
	case "ref", "mut":
		return valueLayout{size: WordSize, align: WordSize}, nil
	case "Array", "Map", "Set":
		// These containers are three-word handles: data, length, capacity.
		// The data pointer is the only GC reference in the handle.
		return valueLayout{size: 3 * WordSize, align: WordSize, refs: []uint32{0}}, nil
	case "list", "dict", "set":
		return valueLayout{size: WordSize, align: WordSize, reference: true}, nil
	case "chan", "Task":
		if len(node.args) != 1 {
			return valueLayout{}, fmt.Errorf("%s needs one type argument", name)
		}
		if _, err := p.layoutValue(pkg, typeTextOf(node.args[0]), seen); err != nil {
			return valueLayout{}, err
		}
		return valueLayout{size: WordSize, align: WordSize, reference: true}, nil
	case "Weak":
		if len(node.args) != 1 {
			return valueLayout{}, fmt.Errorf("Weak needs one type argument")
		}
		return valueLayout{size: WordSize, align: WordSize, weak: true}, nil
	case "Pinned", "Handle":
		return valueLayout{size: WordSize, align: WordSize}, nil
	case "Option":
		if len(node.args) != 1 {
			return valueLayout{}, fmt.Errorf("Option needs one type argument")
		}
		payload, err := p.layoutValue(pkg, typeTextOf(node.args[0]), seen)
		if err != nil {
			return valueLayout{}, err
		}
		return taggedLayout(payload, valueLayout{align: 1})
	case "Result":
		if len(node.args) != 2 {
			return valueLayout{}, fmt.Errorf("Result needs two type arguments")
		}
		ok, err := p.layoutValue(pkg, typeTextOf(node.args[0]), seen)
		if err != nil {
			return valueLayout{}, err
		}
		fail, err := p.layoutValue(pkg, typeTextOf(node.args[1]), seen)
		if err != nil {
			return valueLayout{}, err
		}
		return taggedLayout(ok, fail)
	case "rc", "arc", "weak_rc", "weak_arc":
		// Legacy reference-counted handles are integers, not GC references.
		// The final surface removes them; the layout keeps old corpus programs
		// translating while their migration is still in progress.
		return valueLayout{size: WordSize, align: WordSize}, nil
	}
	if stringsHasBorrowPrefix(text) {
		return valueLayout{size: WordSize, align: WordSize}, nil
	}
	if decl, ok := p.resolveDeclExact(pkg, name); ok {
		return p.declLayout(decl, seen)
	}
	return valueLayout{}, fmt.Errorf("type %s has no GC layout", text)
}

// declLayout returns the storage shape of one declaration. A class is a GC
// reference; structs and enums are inline values whose fields are scanned
// recursively.
func (p *Program) declLayout(decl *declaration, seen map[string]bool) (valueLayout, error) {
	switch decl.kind {
	case KindClass:
		return valueLayout{size: WordSize, align: WordSize, reference: true}, nil
	case KindInterface:
		return valueLayout{size: 2 * WordSize, align: WordSize,
			refs: []uint32{0}}, nil
	case KindStruct, KindEnum:
		if seen[decl.name] {
			return valueLayout{}, fmt.Errorf("inline type %s contains itself", decl.name)
		}
		seen[decl.name] = true
		defer delete(seen, decl.name)
		var size, align int
		var refs, weaks []uint32
		if decl.kind == KindStruct {
			var err error
			size, align, refs, weaks, _, err = p.layoutFields(decl.pkg, decl.fields, seen)
			if err != nil {
				return valueLayout{}, err
			}
		} else {
			var err error
			size, align, refs, weaks, err = p.layoutEnum(decl, seen)
			if err != nil {
				return valueLayout{}, err
			}
		}
		return valueLayout{size: size, align: align, refs: refs, weaks: weaks}, nil
	}
	return valueLayout{}, fmt.Errorf("type %s has no GC layout", decl.name)
}

// taggedLayout lays out a tag word followed by the union of two payloads. A
// unit payload is zero-filled, so its absent references do not constrain the
// other variant. Two non-unit payloads with different reference offsets need a
// tag-aware descriptor, which this descriptor cannot express yet.
func taggedLayout(first, second valueLayout) (valueLayout, error) {
	align := max(4, max(first.align, second.align))
	offset := alignUp(4, align)
	size := offset + max(first.size, second.size)
	value := valueLayout{size: alignUp(size, align), align: align}
	refs, weaks, err := taggedOffsets(first, second)
	if err != nil {
		return valueLayout{}, err
	}
	value.refs = shiftOffsets(refs, offset)
	value.weaks = shiftOffsets(weaks, offset)
	return value, nil
}

func taggedOffsets(first, second valueLayout) ([]uint32, []uint32, error) {
	firstUnit := first.size == 0
	secondUnit := second.size == 0
	if firstUnit && secondUnit {
		return nil, nil, nil
	}
	firstRefs, firstWeaks := payloadOffsets(first)
	secondRefs, secondWeaks := payloadOffsets(second)
	if firstUnit {
		return secondRefs, secondWeaks, nil
	}
	if secondUnit {
		return firstRefs, firstWeaks, nil
	}
	refs, err := commonOffsets([][]uint32{firstRefs, secondRefs})
	if err != nil {
		refs = nil
	}
	weaks, err := commonOffsets([][]uint32{firstWeaks, secondWeaks})
	if err != nil {
		weaks = nil
	}
	return refs, weaks, nil
}

func payloadOffsets(value valueLayout) ([]uint32, []uint32) {
	if value.reference {
		return []uint32{0}, nil
	}
	if value.weak {
		return nil, []uint32{0}
	}
	return value.refs, value.weaks
}

func (p *Program) layoutEnum(decl *declaration, seen map[string]bool) (int, int, []uint32, []uint32, error) {
	var variants []valueLayout
	for _, v := range decl.variants {
		args, err := p.variantArgs(decl.pkg, v, seen)
		if err != nil {
			return 0, 0, nil, nil, err
		}
		variants = append(variants, args)
	}
	if len(variants) == 0 {
		return 4, 4, nil, nil, nil
	}
	payloadAlign := 1
	payloadSize := 0
	for _, v := range variants {
		if v.size > payloadSize {
			payloadSize = v.size
		}
		if v.align > payloadAlign {
			payloadAlign = v.align
		}
	}
	align := max(4, payloadAlign)
	offset := alignUp(4, align)
	var refGroups, weakGroups [][]uint32
	for _, v := range variants {
		if v.size != 0 {
			refGroups = append(refGroups, v.refs)
			weakGroups = append(weakGroups, v.weaks)
		}
	}
	refs, err := commonOffsets(refGroups)
	if err != nil {
		// The nested descriptor carries the variant table; the flat list is
		// only an optimization for enums whose variants share offsets.
		refs = nil
	}
	weaks, err := commonOffsets(weakGroups)
	if err != nil {
		weaks = nil
	}
	return alignUp(offset+payloadSize, align), align, shiftOffsets(refs, offset),
		shiftOffsets(weaks, offset), nil
}

func (p *Program) variantArgs(pkg string, variant willc.Record, seen map[string]bool) (valueLayout, error) {
	offset := 0
	align := 1
	var refs, weaks []uint32
	for _, f := range variant.Fields {
		if f.Name != "arg" || f.Value.Kind != willc.RefValue {
			continue
		}
		rec, ok := p.records[f.Value.Ref]
		if !ok {
			return valueLayout{}, fmt.Errorf("variant argument #%d is missing", f.Value.Ref)
		}
		text := fieldText(rec, "text")
		value, err := p.layoutValue(pkg, text, seen)
		if err != nil {
			return valueLayout{}, err
		}
		offset = alignUp(offset, value.align)
		if value.reference {
			refs = append(refs, uint32(offset))
		} else if value.weak {
			weaks = append(weaks, uint32(offset))
		} else {
			refs = append(refs, shiftOffsets(value.refs, offset)...)
			weaks = append(weaks, shiftOffsets(value.weaks, offset)...)
		}
		offset += value.size
		if value.align > align {
			align = value.align
		}
	}
	return valueLayout{size: alignUp(offset, align), align: align,
		refs: refs, weaks: weaks}, nil
}

func commonOffsets(groups [][]uint32) ([]uint32, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	first := append([]uint32(nil), groups[0]...)
	sort.Slice(first, func(i, j int) bool { return first[i] < first[j] })
	for _, group := range groups[1:] {
		set := map[uint32]bool{}
		for _, offset := range group {
			set[offset] = true
		}
		var next []uint32
		for _, offset := range first {
			if set[offset] {
				next = append(next, offset)
			}
		}
		first = next
	}
	if len(first) == 0 && hasAny(groups) {
		return nil, fmt.Errorf("no common offset")
	}
	return first, nil
}

func hasAny(groups [][]uint32) bool {
	for _, g := range groups {
		if len(g) != 0 {
			return true
		}
	}
	return false
}

func shiftOffsets(offsets []uint32, base int) []uint32 {
	if len(offsets) == 0 {
		return nil
	}
	out := make([]uint32, len(offsets))
	for i, offset := range offsets {
		out[i] = uint32(base) + offset
	}
	return out
}

func stringsHasBorrowPrefix(text string) bool {
	return len(text) > 4 && (text[:4] == "ref " || text[:4] == "mut ")
}
