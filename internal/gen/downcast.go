package gen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// downcast is one Is or As call on an interface value.
type downcast struct {
	source  *ifaceInfo
	target  string
	toIface *ifaceInfo
	isAs    bool
}

// downcastOf recognizes the two explicit downcasts. A method with the same
// name but no type argument stays an ordinary interface method.
func (g *generator) downcastOf(r willc.Record) (*downcast, bool) {
	if r.Kind != "mcall" {
		return nil, false
	}
	name := fieldText(r, "text")
	if name != "Is" && name != "As" {
		return nil, false
	}
	args := g.typeArgTexts(r)
	if len(args) != 1 {
		return nil, false
	}
	recvText, err := g.willTypeText(refField(r, "recv"))
	if err != nil {
		return nil, false
	}
	source, ok := g.ifaceOfText(recvText)
	if !ok {
		return nil, false
	}
	target := g.typeIdentityText(args[0])
	if target == "" {
		return nil, false
	}
	toIface, _ := g.ifaceOfText(target)
	return &downcast{source: source, target: target, toIface: toIface, isAs: name == "As"}, true
}

// downcastTypeText returns the result type of a downcast. Is gives bool and As
// gives Option of the target type.
func (g *generator) downcastTypeText(r willc.Record) (string, bool) {
	d, ok := g.downcastOf(r)
	if !ok {
		return "", false
	}
	if !d.isAs {
		return "bool", true
	}
	return "Option<" + d.target + ">", true
}

// downcastSatisfies reports whether one concrete type has every method of an
// interface. The check uses the same method lookup as vtable.
func (g *generator) downcastSatisfies(iface *ifaceInfo, concrete string) bool {
	for _, m := range iface.methods {
		if g.methodsQualified[concrete+"."+m.name] != "" {
			continue
		}
		if g.cur != nil && g.cur.methods[concrete+"."+m.name] != "" {
			continue
		}
		if iface == g.showIface {
			if _, ok := g.showThunkName(concrete); ok {
				continue
			}
		}
		return false
	}
	return true
}

// genericTypeDecl reports whether a name is a generic declaration rather than
// one instantiated type. A generic declaration has no runtime layout, so it
// cannot be a downcast target.
func (g *generator) genericTypeDecl(name string) bool {
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind != "struct" && r.Kind != "enum" {
				continue
			}
			if qualify(d.path, fieldText(r, "text")) != name {
				continue
			}
			for _, c := range d.recs {
				if c.Kind == "typeparam" && refField(c, "parent") == r.ID {
					return true
				}
			}
		}
	}
	return false
}

// downcastCandidates returns every concrete type this module knows that
// satisfies both interfaces. The list is a compile-time closed set: Will has no
// runtime type registry, so a type that is not in the program cannot be
// recovered by a downcast.
func (g *generator) downcastCandidates(source, target *ifaceInfo) []string {
	var out []string
	seen := map[string]bool{}
	add := func(text string) {
		if text == "" {
			return
		}
		text = g.typeIdentityText(text)
		if seen[text] {
			return
		}
		seen[text] = true
		if g.genericTypeDecl(text) {
			return
		}
		if strings.Contains(text, "<") {
			if _, ok := g.structInstances[text]; !ok {
				return
			}
		}
		if !g.downcastSatisfies(source, text) || !g.downcastSatisfies(target, text) {
			return
		}
		out = append(out, text)
	}
	for name := range g.structDecls {
		add(name)
	}
	for name, kind := range g.declKinds {
		if kind == "enum" {
			add(name)
		}
	}
	for name := range g.structInstances {
		add(name)
	}
	for name := range g.enumTypes {
		add(name)
	}
	for key := range g.methodsQualified {
		if i := strings.LastIndexByte(key, '.'); i > 0 {
			add(key[:i])
		}
	}
	for _, name := range downcastScalars {
		add(name)
	}
	sort.Strings(out)
	return out
}

// downcastScalars are the predeclared types that can appear behind Show or an
// empty interface. A type with no methods is filtered out by the interface
// check before it reaches the table.
var downcastScalars = []string{
	"bool", "i8", "i16", "i32", "i64", "isize",
	"u8", "u16", "u32", "u64", "usize",
	"f32", "f64", "str",
}

// ifaceParts loads the data and method table words of one interface place.
func (g *generator) ifaceParts(base string, iface *ifaceInfo) (string, string) {
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", dataField, iface.llvm, base)
	data := g.load("ptr", dataField)
	tableField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", tableField, iface.llvm, base)
	table := g.load("ptr", tableField)
	return data, table
}

// ifaceReceiver places the receiver of a downcast and checks its type.
func (g *generator) ifaceReceiver(r willc.Record, iface *ifaceInfo) (string, error) {
	base, typ, err := g.place(refField(r, "recv"))
	if err != nil {
		return "", err
	}
	if typ != iface.llvm {
		return "", g.errf(r.ID, "cannot translate node #%d: the receiver needs %s", r.ID, iface.llvm)
	}
	return base, nil
}

// ifaceDowncastIs lowers Is<T>. Every candidate compares its type identity
// word, so the result is one branch-free chain of or instructions.
func (g *generator) ifaceDowncastIs(r willc.Record) (string, error) {
	d, ok := g.downcastOf(r)
	if !ok {
		return "", g.errf(r.ID, "cannot translate node #%d: the downcast is malformed", r.ID)
	}
	base, err := g.ifaceReceiver(r, d.source)
	if err != nil {
		return "", err
	}
	data, table := g.ifaceParts(base, d.source)
	present := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ne ptr %s, null\n", present, data)
	if d.toIface != nil && d.toIface == d.source {
		return present, nil
	}

	var candidates []string
	if d.toIface == nil {
		if !g.downcastSatisfies(d.source, d.target) {
			return "", g.errf(r.ID, "cannot translate node #%d: %s does not satisfy %s", r.ID, d.target, d.source.name)
		}
		candidates = []string{d.target}
	} else {
		candidates = g.downcastCandidates(d.source, d.toIface)
	}
	slot := g.callSlots[r.ID]
	if slot == "" {
		slot = g.allocSlot(g.callSlots, "is", r.ID, "i1")
	}
	fmt.Fprintf(g.out, "  store i1 false, ptr %s\n", slot)
	if len(candidates) == 0 {
		return g.load("i1", slot), nil
	}
	g.labels++
	n := g.labels
	check := fmt.Sprintf("is.check.%d", n)
	done := fmt.Sprintf("is.done.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", present, check, done)
	g.out.WriteString(check + ":\n")
	identity := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds [%d x ptr], ptr %s, i64 0, i64 %d\n",
		identity, ifaceTableSize(d.source), table, ifaceIdentityIndex(d.source))
	id := g.load("ptr", identity)
	result := ""
	for _, candidate := range candidates {
		equal := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq ptr %s, @%s\n", equal, id, llvmSymbol(g.typeIdentity(candidate)))
		if result == "" {
			result = equal
			continue
		}
		next := g.newTmp()
		fmt.Fprintf(g.out, "  %s = or i1 %s, %s\n", next, result, equal)
		result = next
	}
	if result == "" {
		result = "false"
	}
	fmt.Fprintf(g.out, "  store i1 %s, ptr %s\n", result, slot)
	fmt.Fprintf(g.out, "  br label %%%s\n", done)
	g.out.WriteString(done + ":\n")
	return g.load("i1", slot), nil
}

// ifaceDowncastAs lowers As<T>. The option starts as None, then each candidate
// identity opens one hit block that fills the payload.
func (g *generator) ifaceDowncastAs(r willc.Record) (string, string, error) {
	d, ok := g.downcastOf(r)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the downcast is malformed", r.ID)
	}
	base, err := g.ifaceReceiver(r, d.source)
	if err != nil {
		return "", "", err
	}
	adt, ok := g.adtType("Option<" + d.target + ">")
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: Option<%s> is not lowered yet", r.ID, d.target)
	}
	slot := g.allocSlot(g.adtSlots, "cast", r.ID, adt.llvm)
	tag := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tag, adt.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", tag)

	data, table := g.ifaceParts(base, d.source)
	present := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ne ptr %s, null\n", present, data)
	g.labels++
	n := g.labels
	check := fmt.Sprintf("cast.check.%d", n)
	done := fmt.Sprintf("cast.done.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", present, check, done)
	g.out.WriteString(check + ":\n")

	if d.toIface == nil {
		if !g.downcastSatisfies(d.source, d.target) {
			return "", "", g.errf(r.ID, "cannot translate node %d: %s does not satisfy %s", r.ID, d.target, d.source.name)
		}
		if err := g.downcastStoreConcrete(r.ID, d, data, adt, slot); err != nil {
			return "", "", err
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
	} else if d.toIface == d.source {
		payload, err := g.downcastPayload(adt, slot)
		if err != nil {
			return "", "", err
		}
		if _, err := g.cloneIfaceAt(base, payload, d.source); err != nil {
			return "", "", err
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
	} else {
		candidates := g.downcastCandidates(d.source, d.toIface)
		if len(candidates) == 0 {
			fmt.Fprintf(g.out, "  br label %%%s\n", done)
		} else {
			identity := g.newTmp()
			fmt.Fprintf(g.out, "  %s = getelementptr inbounds [%d x ptr], ptr %s, i64 0, i64 %d\n",
				identity, ifaceTableSize(d.source), table, ifaceIdentityIndex(d.source))
			id := g.load("ptr", identity)
			for i, candidate := range candidates {
				equal := g.newTmp()
				fmt.Fprintf(g.out, "  %s = icmp eq ptr %s, @%s\n", equal, id, llvmSymbol(g.typeIdentity(candidate)))
				hit := fmt.Sprintf("cast.hit.%d.%d", n, i)
				miss := fmt.Sprintf("cast.miss.%d.%d", n, i)
				fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", equal, hit, miss)
				g.out.WriteString(hit + ":\n")
				tableSym, err := g.vtable(d.toIface, candidate)
				if err != nil {
					return "", "", err
				}
				payload, err := g.downcastPayload(adt, slot)
				if err != nil {
					return "", "", err
				}
				clonePtr := g.newTmp()
				fmt.Fprintf(g.out, "  %s = getelementptr inbounds [%d x ptr], ptr %s, i64 0, i64 %d\n",
					clonePtr, ifaceTableSize(d.source), table, ifaceCloneIndex(d.source))
				cloneFn := g.load("ptr", clonePtr)
				newData := g.newTmp()
				fmt.Fprintf(g.out, "  %s = call ptr %s(ptr %s)\n", newData, cloneFn, data)
				dataField := g.newTmp()
				fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", dataField, d.toIface.llvm, payload)
				fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", newData, dataField)
				tableField := g.newTmp()
				fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", tableField, d.toIface.llvm, payload)
				fmt.Fprintf(g.out, "  store ptr @%s, ptr %s\n", llvmSymbol(tableSym), tableField)
				fmt.Fprintf(g.out, "  br label %%%s\n", done)
				g.out.WriteString(miss + ":\n")
			}
			fmt.Fprintf(g.out, "  br label %%%s\n", done)
		}
	}
	g.out.WriteString(done + ":\n")
	return slot, adt.llvm, nil
}

// downcastPayload writes the Some tag and returns the payload address. A unit
// payload has no field, so it returns an empty address.
func (g *generator) downcastPayload(adt *adtInfo, slot string) (string, error) {
	tag := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n", tag, adt.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", tag)
	if adt.payloadIndex == 0 {
		return "", nil
	}
	payload := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", payload, adt.llvm, slot, adt.payloadIndex)
	return payload, nil
}

// downcastStoreConcrete copies one concrete value out of the interface data
// pointer into the Some payload.
func (g *generator) downcastStoreConcrete(id int, d *downcast, data string, adt *adtInfo, slot string) error {
	payload, err := g.downcastPayload(adt, slot)
	if err != nil {
		return err
	}
	typ, ok := g.typeLLVMOf(d.target)
	if !ok {
		return g.errf(id, "cannot translate node #%d: %s is not lowered yet", id, d.target)
	}
	if typ == "" || payload == "" {
		return nil
	}
	return g.cloneOwnedValue(data, payload, d.target)
}
