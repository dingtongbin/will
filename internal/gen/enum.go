package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// variantInfo is one lowered variant of an enum. args holds the will type
// texts in declaration order; payload is the LLVM type of the whole payload, or
// "" when the variant carries nothing.
type variantInfo struct {
	id      int
	name    string
	tag     uint32
	args    []string
	argLLVM []string
	payload string
	size    int
	align   int
}

// enumInfo is the lowered form of one enum declaration. The type starts with a
// u32 tag; the payload area is sized and aligned like a C union over the
// variants.
type enumInfo struct {
	id       int
	name     string
	llvm     string
	text     string
	variants []variantInfo
	byName   map[string]int
	// payloadType is the LLVM field that holds the payload, and payloadIndex
	// is its field number in the enum type. A payload of size zero has no
	// field and payloadIndex is zero.
	payloadType  string
	payloadIndex int
	payloadSize  int
	payloadAlign int
	llvmType     string
	size         int
	align        int
	// done and finishing keep a nested enum from being finished twice and stop
	// a payload that contains the enum itself.
	done      bool
	finishing bool
}

// externalEnum is the module-wide shape of one enum: its qualified will name,
// its LLVM type, and the tag of every variant. A variant that carries a
// payload is marked so a cross-document construction of it is refused instead
// of laid out without its payload.
type externalEnum struct {
	name    string
	llvm    string
	tags    map[string]uint32
	payload map[string]bool
	size    int
	align   int
	laid    bool
}

// collectExternalEnums records every enum of the module before any document is
// lowered, so a variant declared in another document still has a tag and a
// type.
func (g *generator) collectExternalEnums() {
	g.externalEnums = map[string]*externalEnum{}
	for _, d := range g.docs {
		var enums []willc.Record
		for _, r := range d.recs {
			if r.Kind == "enum" {
				enums = append(enums, r)
			}
		}
		sortByID(enums)
		for _, r := range enums {
			q := qualify(d.path, fieldText(r, "text"))
			ext := &externalEnum{name: q, llvm: qualifiedEnumName(q), tags: map[string]uint32{}, payload: map[string]bool{}}
			var variants []willc.Record
			for _, c := range d.recs {
				if c.Kind == "variant" && refField(c, "parent") == r.ID {
					variants = append(variants, c)
				}
			}
			sortByID(variants)
			maxSize, maxAlign := 0, 1
			known := true
			for i, v := range variants {
				name := fieldText(v, "text")
				ext.tags[name] = uint32(i)
				args := refsOf(v, "arg")
				if len(args) != 0 {
					ext.payload[name] = true
				}
				for _, id := range args {
					arg, ok := d.recs[id]
					if !ok {
						known = false
						continue
					}
					typ, ok := llvmTypeText(fieldText(arg, "text"))
					if !ok {
						typ, ok = g.typeLLVMOf(fieldText(arg, "text"))
					}
					if !ok {
						known = false
						continue
					}
					size, align, ok := g.sizeAlign(typ)
					if !ok {
						known = false
						continue
					}
					if size > maxSize {
						maxSize = size
					}
					if align > maxAlign {
						maxAlign = align
					}
				}
			}
			ext.align = maxAlign
			if ext.align < 4 {
				ext.align = 4
			}
			ext.size = alignUp(4, ext.align) + maxSize
			ext.laid = known
			g.externalEnums[q] = ext
		}
	}
}

// buildEnums records every enum of the current document. The payload type of a
// variant is its single argument's type, or a struct of the argument types in
// declaration order. The union field is the payload type with the greatest
// alignment, padded with bytes to the largest payload size, so the LLVM type
// keeps the alignment the layout volume fixes.
func (g *generator) buildEnums() {
	var enums []willc.Record
	for _, r := range g.recs {
		if r.Kind == "enum" {
			enums = append(enums, r)
		}
	}
	sortByID(enums)
	for _, r := range enums {
		info := &enumInfo{id: r.ID, name: fieldText(r, "text"), text: fieldText(r, "text"), byName: map[string]int{}}
		info.llvm = g.enumLLVM(info.name)
		g.enums[r.ID] = info
		g.enumByLLVM[info.llvm] = info
		g.enumByName[info.name] = info
		var variants []willc.Record
		for _, c := range g.recs {
			if c.Kind == "variant" && refField(c, "parent") == r.ID {
				variants = append(variants, c)
			}
		}
		sortByID(variants)
		for i, v := range variants {
			vi := variantInfo{id: v.ID, name: fieldText(v, "text"), tag: uint32(i)}
			for _, id := range refsOf(v, "arg") {
				t, ok := g.recs[id]
				if !ok {
					continue
				}
				vi.args = append(vi.args, fieldText(t, "text"))
				if typ, ok := g.argLLVM(t); ok {
					vi.argLLVM = append(vi.argLLVM, typ)
				}
			}
			if len(vi.argLLVM) == len(vi.args) {
				switch len(vi.argLLVM) {
				case 0:
				case 1:
					vi.payload = vi.argLLVM[0]
				default:
					vi.payload = "{ " + strings.Join(vi.argLLVM, ", ") + " }"
				}
			}
			info.variants = append(info.variants, vi)
			info.byName[vi.name] = i
		}
		for i := range info.variants {
			g.variantByID[info.variants[i].id] = &info.variants[i]
		}
	}
	for _, r := range enums {
		info := g.enums[r.ID]
		if info == nil {
			continue
		}
		g.finishEnum(info)
	}
	for _, r := range enums {
		info := g.enums[r.ID]
		if info == nil || info.llvmType == "" || g.emittedTypes[info.llvm] {
			continue
		}
		g.emittedTypes[info.llvm] = true
		fmt.Fprintf(g.out, "%s = type %s\n", info.llvm, info.llvmType)
	}
}

// finishEnum computes the payload union of one enum and its LLVM type.
func (g *generator) finishEnum(info *enumInfo) {
	if info.done || info.finishing {
		return
	}
	info.finishing = true
	defer func() { info.finishing = false; info.done = true }()
	for i := range info.variants {
		v := &info.variants[i]
		if v.payload == "" {
			continue
		}
		size, align, ok := g.sizeAlign(v.payload)
		if !ok {
			v.payload = ""
			continue
		}
		v.size, v.align = size, align
		if size > info.payloadSize {
			info.payloadSize = size
		}
		if align > info.payloadAlign {
			info.payloadAlign = align
		}
	}
	if info.payloadAlign == 0 {
		info.payloadAlign = 1
	}
	offset := alignUp(4, info.payloadAlign)
	if info.payloadSize == 0 {
		info.llvmType = "{ i32 }"
		info.size = 4
		info.align = 4
		return
	}
	chosen := ""
	for i := range info.variants {
		v := &info.variants[i]
		if v.payload == "" {
			continue
		}
		if chosen == "" {
			chosen = v.payload
			continue
		}
		bestSize, bestAlign, _ := g.sizeAlign(chosen)
		if v.align > bestAlign || (v.align == bestAlign && v.size > bestSize) {
			chosen = v.payload
		}
	}
	if chosen == "" {
		info.llvmType = ""
		return
	}
	gap := offset - 4
	fields := []string{"i32"}
	if gap > 0 {
		fields = append(fields, fmt.Sprintf("[%d x i8]", gap))
	}
	info.payloadIndex = len(fields)
	fields = append(fields, chosen)
	if size, _, ok := g.sizeAlign(chosen); ok && size < info.payloadSize {
		fields = append(fields, fmt.Sprintf("[%d x i8]", info.payloadSize-size))
	}
	info.payloadType = chosen
	info.llvmType = "{ " + strings.Join(fields, ", ") + " }"
	info.align = info.payloadAlign
	if info.align < 4 {
		info.align = 4
	}
	info.size = alignUp(offset+info.payloadSize, info.align)
}

// enumLLVM names one enum type. The package path keeps two packages' enums
// apart, like a struct.
func (g *generator) enumLLVM(name string) string {
	body := "enum." + name
	if g.pkg != "" {
		body = "enum." + g.pkg + "." + name
	}
	return "%" + llvmSymbol(body)
}

// qualifiedEnumName renders a package-qualified enum name as its LLVM type.
func qualifiedEnumName(name string) string {
	return "%" + llvmSymbol("enum."+name)
}

// isEnumType reports whether an LLVM type names an enum.
func isEnumType(typ string) bool {
	return strings.HasPrefix(typ, "%enum.") || strings.HasPrefix(typ, `%"enum.`)
}

// refsOf returns the node references a record repeats under one field name.
func refsOf(r willc.Record, name string) []int {
	var out []int
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == willc.RefValue {
			out = append(out, f.Value.Ref)
		}
	}
	return out
}

// argLLVM returns the LLVM type of one variant argument type record.
func (g *generator) argLLVM(t willc.Record) (string, bool) {
	text := fieldText(t, "text")
	if typ, _, ok := llvmScalar(text); ok {
		return typ, true
	}
	if typ, ok := llvmTypeText(text); ok {
		return typ, true
	}
	if to := refField(t, "to"); to != 0 {
		if r, ok := g.recs[to]; ok {
			switch r.Kind {
			case "struct":
				if info, ok := g.ensureStruct(to); ok {
					return info.llvm, true
				}
			case "enum":
				return g.enumLLVM(fieldText(r, "text")), true
			case "interface":
				return g.ifaceLLVM(fieldText(r, "text")), true
			}
		}
	}
	if g.structTypes[text] {
		return qualifiedTypeName(text), true
	}
	if g.enumTypes[text] || g.declKinds[text] == "enum" {
		return qualifiedEnumName(text), true
	}
	if g.ifaceTypes[text] || g.declKinds[text] == "interface" {
		return qualifiedIfaceName(text), true
	}
	return "", false
}

// enumVariantCall returns the enum and variant a call, name, or method call
// constructs, or nil when the expression is not a variant constructor.
func (g *generator) enumVariantCall(r willc.Record) (*enumInfo, *variantInfo) {
	if r.Kind == "call" || r.Kind == "name" {
		decl, ok := g.recs[refField(r, "to")]
		if !ok || decl.Kind != "variant" {
			return nil, nil
		}
		info := g.enums[refField(decl, "parent")]
		if info == nil {
			return nil, nil
		}
		name := fieldText(decl, "text")
		i, ok := info.byName[name]
		if !ok {
			return nil, nil
		}
		return info, &info.variants[i]
	}
	if r.Kind != "mcall" {
		return nil, nil
	}
	recv, ok := g.recs[refField(r, "recv")]
	if !ok {
		return nil, nil
	}
	if recv.Kind == "field" {
		if info, v := g.externalVariantCall(r, recv); info != nil {
			return info, v
		}
		// The same package may declare the enum in another file.
		if ext := g.externalEnums[qualify(g.pkg, fieldText(recv, "text"))]; ext != nil {
			return g.syntheticVariant(ext, fieldText(r, "text"))
		}
		return nil, nil
	}
	if recv.Kind != "name" {
		return nil, nil
	}
	decl := refField(recv, "to")
	if decl == 0 {
		// A type that lives in another file of this package.
		if ext := g.externalEnums[qualify(g.pkg, fieldText(recv, "text"))]; ext != nil {
			return g.syntheticVariant(ext, fieldText(r, "text"))
		}
		return nil, nil
	}
	info := g.enums[decl]
	if info == nil {
		return nil, nil
	}
	i, ok := info.byName[fieldText(r, "text")]
	if !ok {
		return nil, nil
	}
	return info, &info.variants[i]
}

// externalVariantCall resolves `pkg.Enum.Variant()`: the receiver is the
// qualified enum name, and the module table supplies its tag. A payload
// variant is not constructed across documents yet, because its layout belongs
// to the defining document.
func (g *generator) externalVariantCall(r willc.Record, field willc.Record) (*enumInfo, *variantInfo) {
	recvName, ok := g.recs[refField(field, "recv")]
	if !ok || recvName.Kind != "name" {
		return nil, nil
	}
	imp, ok := g.recs[refField(recvName, "to")]
	if !ok || imp.Kind != "import" {
		return nil, nil
	}
	path := fieldText(imp, "path")
	if canonical := g.aliases[path]; canonical != "" {
		path = canonical
	}
	q := path + "." + fieldText(field, "text")
	return g.syntheticVariant(g.externalEnums[q], fieldText(r, "text"))
}

// syntheticVariant builds the enum shape of a variant whose declaration is in
// another document. A payload variant is refused because its layout belongs to
// the defining document.
func (g *generator) syntheticVariant(ext *externalEnum, name string) (*enumInfo, *variantInfo) {
	if ext == nil || ext.payload[name] {
		return nil, nil
	}
	tag, ok := ext.tags[name]
	if !ok {
		return nil, nil
	}
	info := &enumInfo{name: ext.name, text: ext.name, llvm: ext.llvm, byName: map[string]int{name: 0}}
	return info, &variantInfo{name: name, tag: tag}
}

// enumConstruct stores one constructed variant in the slot reserved for it and
// returns the slot with the enum type.
func (g *generator) enumConstruct(r willc.Record, info *enumInfo, v *variantInfo) (string, string, error) {
	slot := g.allocSlot(g.enumSlots, "enum", r.ID, info.llvm)
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tagPtr, info.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 %d, ptr %s\n", v.tag, tagPtr)
	args := g.callArgs(r)
	if len(args) != len(v.args) {
		return "", "", g.errf(r.ID, "cannot translate node #%d: variant %s takes %d arguments", r.ID, v.name, len(v.args))
	}
	if len(args) == 0 {
		return slot, info.llvm, nil
	}
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, info.llvm, slot, info.payloadIndex)
	for i, argID := range args {
		dst := payload
		if len(args) > 1 {
			dst = g.newTmp()
			fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", dst, v.payload, payload, i)
		}
		typ, err := g.typeOf(argID)
		if err != nil {
			return "", "", err
		}
		if isAggregateType(typ) {
			src, srcType, err := g.place(argID)
			if err != nil {
				return "", "", err
			}
			if srcType != typ {
				return "", "", g.errf(argID, "cannot translate node #%d: the argument needs %s", argID, typ)
			}
			g.copyValue(src, dst, typ)
			continue
		}
		value, err := g.value(argID)
		if err != nil {
			return "", "", err
		}
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, dst)
	}
	return slot, info.llvm, nil
}

// sizeAlign returns the size and alignment of an LLVM type under the target
// ABI. Named structs and enums are resolved through the current document.
func (g *generator) sizeAlign(typ string) (int, int, bool) {
	if typ == "ptr" {
		return 8, 8, true
	}
	if typ == "float" {
		return 4, 4, true
	}
	if typ == "double" {
		return 8, 8, true
	}
	if typ == "%willrt_error" {
		return 32, 8, true
	}
	// The join keeps the error copy and the status word after it.
	if typ == "%willrt.task_error" {
		return 40, 8, true
	}
	if typ == "%willrt_arm" {
		return 40, 8, true
	}
	if strings.HasPrefix(typ, "%interface.") || strings.HasPrefix(typ, `%"interface.`) {
		return 16, 8, true
	}
	if strings.HasPrefix(typ, "i") {
		w := intWidth(typ)
		if w < 1 {
			return 0, 0, false
		}
		return (w + 7) / 8, (w + 7) / 8, true
	}
	if strings.HasPrefix(typ, "[") && strings.HasSuffix(typ, "]") {
		inner := typ[1 : len(typ)-1]
		sep := strings.Index(inner, " x ")
		if sep < 0 {
			return 0, 0, false
		}
		n, err := strconv.Atoi(inner[:sep])
		if err != nil {
			return 0, 0, false
		}
		size, align, ok := g.sizeAlign(inner[sep+3:])
		if !ok {
			return 0, 0, false
		}
		return n * alignUp(size, align), align, true
	}
	if strings.HasPrefix(typ, "{") && strings.HasSuffix(typ, "}") {
		fields := splitTypeList(typ[1 : len(typ)-1])
		size, align := 0, 1
		for _, f := range fields {
			fs, fa, ok := g.sizeAlign(strings.TrimSpace(f))
			if !ok {
				return 0, 0, false
			}
			size = alignUp(size, fa) + fs
			if fa > align {
				align = fa
			}
		}
		return alignUp(size, align), align, true
	}
	info := g.byType[typ]
	if info == nil && isStructType(typ) {
		info = g.structInfoByLLVM(typ)
	}
	if info != nil {
		size, align := 0, 1
		for _, f := range info.types {
			fs, fa, ok := g.sizeAlign(f)
			if !ok {
				return 0, 0, false
			}
			size = alignUp(size, fa) + fs
			if fa > align {
				align = fa
			}
		}
		return alignUp(size, align), align, true
	}
	if info := g.enumByLLVM[typ]; info != nil {
		if !info.done {
			g.finishEnum(info)
		}
		if info.llvmType == "" {
			return 0, 0, false
		}
		return info.size, info.align, true
	}
	for _, ext := range g.externalEnums {
		if ext.llvm == typ && ext.laid {
			return ext.size, ext.align, true
		}
	}
	if isIfaceType(typ) {
		return 16, 8, true
	}
	return 0, 0, false
}

// splitTypeList splits a literal struct body at the commas that are not inside
// a nested type.
func splitTypeList(body string) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{', '[', '<':
			depth++
		case '}', ']', '>':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, body[start:i])
				start = i + 1
			}
		}
	}
	if tail := body[start:]; strings.TrimSpace(tail) != "" {
		out = append(out, tail)
	}
	return out
}

func alignUp(n, align int) int {
	if align <= 1 {
		return n
	}
	return (n + align - 1) / align * align
}
