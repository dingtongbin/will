// Package gcmeta computes the GC type graph and descriptor layout that the
// compiler emits. A class has a payload descriptor; a struct is an inline value
// and has no header. The same descriptors are used by stack maps, allocation,
// field barriers, and the runtime collector.
package gcmeta

import (
	"fmt"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// Kind classifies one declaration or one value shape in the GC graph.
type Kind uint8

const (
	// KindScalar is a value with no references.
	KindScalar Kind = iota
	// KindClass is a GC object with an eight-byte header and a payload.
	KindClass
	// KindStruct is an inline value with no header.
	KindStruct
	// KindEnum is an inline tagged value.
	KindEnum
	// KindInterface is a two-word interface value.
	KindInterface
	// KindArray is a fixed-length inline array.
	KindArray
	// KindView is a slice view.
	KindView
	// KindStr is the two-word immutable text value.
	KindStr
	// KindBytes is the two-word immutable byte value.
	KindBytes
	// KindFunc is a code pointer and a closure environment.
	KindFunc
	// KindContainer is a GC-managed container handle.
	KindContainer
	// KindTask is a GC-managed task handle.
	KindTask
	// KindChan is a GC-managed channel handle.
	KindChan
	// KindWeak is a weak reference.
	KindWeak
	// KindPinned is a pinned handle.
	KindPinned
	// KindUnit is the empty value.
	KindUnit
)

// Field is one payload field with its final byte offset and size.
type Field struct {
	Name   string
	Type   string
	Offset int
	Size   int
}

// NestedField is one field of an inline value whose field type needs a
// descriptor scan. Weak marks a field whose type is a weak reference.
type NestedField struct {
	Offset uint32
	Type   string
	Weak   bool
}

// NestedVariant is one active enum variant. Tag is the payload tag; Offset is
// the payload start; Type names the inline payload descriptor, or is empty for
// a unit payload.
type NestedVariant struct {
	Tag    int32
	Offset uint32
	Type   string
}

// Type is one compiler-side GC type. A class type describes the payload, not
// the header; a struct type describes the inline value.
type Type struct {
	Name  string
	Kind  Kind
	ID    uint32
	Size  int
	Align int
	// Refs are payload offsets that hold a GC reference.
	Refs []uint32
	// Weaks are payload offsets that hold a weak reference.
	Weaks []uint32
	// Fields is the declaration order of a class, struct, or enum payload.
	Fields []Field
	// Elem is the element type of an array or view.
	Elem string
	// Len is the element count of a fixed array.
	Len int
	// Sequence marks a variable-length object whose payload is an array of
	// ElemSize-byte elements. ElemType names the descriptor for one element;
	// an empty name means the elements hold no references.
	Sequence bool
	ElemType string
	ElemSize int
	// Nested describes an inline value's fields when they need recursive
	// scanning, such as an enum or a struct containing an enum.
	Nested []NestedField
	// TagOffset is the byte offset of an enum tag; Variants describes the
	// active payload for each tag. Both are empty for non-enum values.
	TagOffset int
	Variants  []NestedVariant
}

// Program is the type graph of one compilation batch. Types contains only
// declarations; built-in value shapes are computed on demand.
type Program struct {
	types   map[string]*Type
	order   []string
	ids     map[string]uint32
	decls   map[string]*declaration
	records map[int]willc.Record
	inline  map[string]*Type
}

type declaration struct {
	name   string
	kind   Kind
	pkg    string
	fields []willc.Record
	// variants is set for an enum declaration.
	variants []willc.Record
}

// Build computes every declared type from a batch of willc documents. IDs are
// assigned in sorted qualified-name order, so the same input always produces
// the same descriptor bytes.
func Build(docs []*willc.Doc) (*Program, error) {
	p := &Program{
		types:   map[string]*Type{},
		ids:     map[string]uint32{},
		records: map[int]willc.Record{},
		inline:  map[string]*Type{},
	}
	decls := map[string]*declaration{}
	for _, doc := range docs {
		pkg := modulePath(doc)
		for i := range doc.Records {
			r := &doc.Records[i]
			p.records[r.ID] = *r
			var kind Kind
			switch r.Kind {
			case "class":
				kind = KindClass
			case "struct":
				kind = KindStruct
			case "enum":
				kind = KindEnum
			case "interface":
				kind = KindInterface
			default:
				continue
			}
			name := fieldText(*r, "text")
			q := qualify(pkg, name)
			if q == "" {
				return nil, fmt.Errorf("type declaration without a name at #%d", r.ID)
			}
			if _, exists := decls[q]; exists {
				return nil, fmt.Errorf("type %s is declared twice", q)
			}
			decl := &declaration{name: q, kind: kind, pkg: pkg}
			generic := false
			for _, child := range doc.Records {
				if refField(child, "parent") != r.ID {
					continue
				}
				switch child.Kind {
				case "field":
					decl.fields = append(decl.fields, child)
				case "variant":
					decl.variants = append(decl.variants, child)
				case "typeparam":
					generic = true
				}
			}
			// A generic declaration is a template, not a runtime layout. Its
			// monomorphized instances carry concrete fields and are registered
			// separately.
			if generic {
				continue
			}
			sortByID(decl.fields)
			sortByID(decl.variants)
			decls[q] = decl
		}
	}
	p.decls = decls
	names := make([]string, 0, len(decls))
	for name := range decls {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t, err := p.buildDecl(decls, decls[name])
		if err != nil {
			return nil, err
		}
		p.types[name] = t
		p.order = append(p.order, name)
	}
	for _, name := range names {
		if err := p.ensureNested(name); err != nil {
			return nil, err
		}
	}
	// IDs are part of the object header. Only classes are allocated with a
	// header; struct and enum descriptors are static scan metadata.
	var classNames []string
	for _, name := range p.order {
		if p.types[name].Kind == KindClass {
			classNames = append(classNames, name)
		}
	}
	if len(classNames) > TypeIDMax {
		return nil, fmt.Errorf("type count %d exceeds the object header limit %d",
			len(classNames), TypeIDMax)
	}
	for i, name := range classNames {
		id := uint32(i + 1)
		p.ids[name] = id
		p.types[name].ID = id
	}
	return p, nil
}

// TypeIDMax is the largest id the eight-byte object header can carry.
const TypeIDMax = 0x7fffff

// Lookup returns one declared type.
func (p *Program) Lookup(name string) *Type {
	return p.types[name]
}

// Types returns the declarations in sorted qualified-name order.
func (p *Program) Types() []*Type {
	out := make([]*Type, 0, len(p.order))
	for _, name := range p.order {
		out = append(out, p.types[name])
	}
	return out
}

// InlineTypes returns the nested or anonymous descriptors created on demand.
// They have no declaration ID until the caller assigns one.
func (p *Program) InlineTypes() []*Type {
	names := make([]string, 0, len(p.inline))
	for name := range p.inline {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*Type, 0, len(names))
	for _, name := range names {
		out = append(out, p.inline[name])
	}
	return out
}

// Declared reports whether a type text names a declaration in this program.
// The text is resolved against pkg first, then against one unique short name.
func (p *Program) Declared(pkg, text string) bool {
	_, ok := p.resolve(pkg, text)
	return ok
}

// TypeText resolves a short type name to its qualified declaration name. It is
// used by the emitter before it writes a descriptor symbol.
func (p *Program) TypeText(pkg, text string) (string, bool) {
	name, ok := p.resolve(pkg, text)
	return name, ok
}

func (p *Program) resolve(pkg, text string) (string, bool) {
	return resolveName(p.types, pkg, text)
}

func (p *Program) resolveDecl(pkg, text string) (string, bool) {
	return resolveName(p.decls, pkg, text)
}

func resolveName[T any](table map[string]T, pkg, text string) (string, bool) {
	if _, ok := table[text]; ok {
		return text, true
	}
	if q := qualify(pkg, text); q != "" {
		if _, ok := table[q]; ok {
			return q, true
		}
	}
	var found string
	for name := range table {
		if strings.HasSuffix(name, "."+text) {
			if found != "" {
				return "", false
			}
			found = name
		}
	}
	if found != "" {
		return found, true
	}
	return "", false
}

func (p *Program) buildDecl(decls map[string]*declaration, decl *declaration) (*Type, error) {
	t := &Type{Name: decl.name, Kind: decl.kind}
	switch decl.kind {
	case KindInterface:
		t.Size = 2 * WordSize
		t.Align = WordSize
		t.Refs = []uint32{0}
		return t, nil
	case KindClass, KindStruct:
		size, align, refs, weaks, fields, err := p.layoutFields(decl.pkg,
			decl.fields, map[string]bool{decl.name: true})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", decl.name, err)
		}
		t.Size = size
		t.Align = align
		t.Refs = refs
		t.Weaks = weaks
		t.Fields = *fields
		return t, nil
	case KindEnum:
		return p.buildEnum(decl)
	}
	return nil, fmt.Errorf("unknown declaration kind at %s", decl.name)
}

func (p *Program) buildEnum(decl *declaration) (*Type, error) {
	t := &Type{Name: decl.name, Kind: KindEnum}
	size, align, refs, weaks, err := p.layoutEnum(decl,
		map[string]bool{decl.name: true})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", decl.name, err)
	}
	t.Size = size
	t.Align = align
	t.Refs = refs
	t.Weaks = weaks
	return t, nil
}

// WordSize is the size of a pointer on every supported target.
const WordSize = 8

// InlineType returns the descriptor for one value stored outside an object
// header: an array element, a channel element, or a task frame slot. A class
// value is an eight-byte reference; a struct value carries its nested offsets.
func (p *Program) InlineType(pkg, text string) (*Type, error) {
	return p.nestedType(pkg, text, map[string]bool{})
}

func modulePath(doc *willc.Doc) string {
	for _, r := range doc.Records {
		if r.Kind == "module" {
			return fieldText(r, "path")
		}
	}
	return ""
}

func qualify(pkg, name string) string {
	if pkg == "" || strings.Contains(name, ".") {
		return name
	}
	return pkg + "." + name
}

func fieldText(r willc.Record, name string) string {
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == willc.TextValue {
			return f.Value.Text
		}
	}
	return ""
}

func refField(r willc.Record, name string) int {
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == willc.RefValue {
			return f.Value.Ref
		}
	}
	return 0
}

func sortByID(records []willc.Record) {
	sort.SliceStable(records, func(i, j int) bool { return records[i].ID < records[j].ID })
}

func alignUp(value, align int) int {
	if align <= 1 {
		return value
	}
	return (value + align - 1) / align * align
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
