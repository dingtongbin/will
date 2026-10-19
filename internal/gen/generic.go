package gen

import (
	"sort"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// hasTypeParams reports whether a function declares type parameters.
func (g *generator) hasTypeParams(fn willc.Record) bool {
	for _, r := range g.recs {
		if r.Kind == "typeparam" && refField(r, "parent") == fn.ID {
			return true
		}
	}
	return false
}

// typeParamNames returns the declared type parameter names in order.
func (g *generator) typeParamNames(fn willc.Record) []string {
	var names []string
	for _, r := range g.recs {
		if r.Kind == "typeparam" && refField(r, "parent") == fn.ID {
			names = append(names, fieldText(r, "text"))
		}
	}
	return names
}

// typeArgTexts returns the type argument texts a call writes.
func (g *generator) typeArgTexts(r willc.Record) []string {
	var texts []string
	for _, id := range refsOf(r, "typearg") {
		if t, ok := g.recs[id]; ok {
			texts = append(texts, fieldText(t, "text"))
		}
	}
	return texts
}

// instanceName mangles a generic function with its type arguments.
func instanceName(name string, args []string) string {
	return name + "$" + strings.Join(args, "$")
}

// substituteText replaces a bare type parameter in a type text.
func substituteText(text string, names, args []string) string {
	for i, name := range names {
		if text == name && i < len(args) {
			return args[i]
		}
	}
	return text
}

// planInstances creates one specialized document for every distinct generic
// call. The instance is emitted after the ordinary documents; calls may forward
// reference it.
func (g *generator) planInstances() {
	seen := map[string]bool{}
	base := len(g.docs)
	for i := 0; i < base; i++ {
		d := g.docs[i]
		for _, r := range d.recs {
			if (r.Kind != "call" && r.Kind != "mcall") || len(refsOf(r, "typearg")) == 0 {
				continue
			}
			calleeDoc := d
			var fn willc.Record
			if target := refField(r, "to"); target != 0 {
				fn = d.recs[target]
			} else if sym := textField(r, "to"); sym != "" {
				foundDoc, found, ok := g.findGenericFunc(sym)
				if !ok {
					continue
				}
				calleeDoc, fn = foundDoc, found
			} else {
				continue
			}
			if fn.Kind != "func" || !g.hasTypeParamsIn(calleeDoc, fn) {
				continue
			}
			args := typeArgTextsIn(d, r)
			if len(args) == 0 {
				continue
			}
			key := calleeDoc.path + "|" + fieldText(fn, "text") + "|" + strings.Join(args, ",")
			if seen[key] {
				continue
			}
			seen[key] = true
			doc, ok := g.cloneInstance(calleeDoc, fn, args)
			if !ok {
				continue
			}
			g.docs = append(g.docs, newDocState(doc))
		}
	}
	g.planMethodInstances(base, seen)
}

// findGenericFunc finds the free generic function one qualified symbol names.
func (g *generator) findGenericFunc(sym string) (*docState, willc.Record, bool) {
	for _, d := range g.docs {
		for _, f := range d.recs {
			if f.Kind != "func" || receiverOfIn(d, f.ID) != nil {
				continue
			}
			if qualify(d.path, fieldText(f, "text")) == sym && g.hasTypeParamsIn(d, f) {
				return d, f, true
			}
		}
	}
	return nil, willc.Record{}, false
}

func (g *generator) hasTypeParamsIn(d *docState, fn willc.Record) bool {
	for _, r := range d.recs {
		if r.Kind == "typeparam" && refField(r, "parent") == fn.ID {
			return true
		}
	}
	return false
}

func typeArgTextsIn(d *docState, r willc.Record) []string {
	var texts []string
	for _, id := range refsOf(r, "typearg") {
		if t, ok := d.recs[id]; ok {
			texts = append(texts, fieldText(t, "text"))
		}
	}
	return texts
}

// cloneInstance copies one generic function and its records into a new
// document, substituting each type parameter with the call's type argument.
func (g *generator) cloneInstance(d *docState, fn willc.Record, args []string) (*willc.Doc, bool) {
	names := make([]string, 0, len(args))
	for _, r := range d.recs {
		if r.Kind == "typeparam" && refField(r, "parent") == fn.ID {
			names = append(names, fieldText(r, "text"))
		}
	}
	if len(names) != len(args) {
		return nil, false
	}
	return g.cloneFuncInstance(d, fn, names, args, instanceName(fieldText(fn, "text"), args))
}

// cloneFuncInstance copies one function subtree into a fresh document with
// every type parameter replaced. A non-empty mangled name renames the function;
// a method instance keeps its declared name because its receiver type text
// already tells the two instantiations apart.
func (g *generator) cloneFuncInstance(d *docState, fn willc.Record, names, args []string, mangled string) (*willc.Doc, bool) {
	included := map[int]bool{fn.ID: true}
	changed := true
	for changed {
		changed = false
		for _, r := range d.recs {
			if included[r.ID] {
				// A type the signature names comes along with its declaration,
				// so the instance can lay out a generic receiver or field.
				if r.Kind == "type" {
					to := refField(r, "to")
					if to != 0 && !included[to] {
						included[to] = true
						changed = true
					}
				}
				continue
			}
			if included[refField(r, "parent")] {
				included[r.ID] = true
				changed = true
			}
		}
	}
	ids := make([]int, 0, len(included))
	for id := range included {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	remap := map[int]int{}
	doc := &willc.Doc{}
	module := willc.Record{ID: 1, Kind: "module", Fields: []willc.Field{
		{Name: "path", Value: willc.Text(d.path)},
	}}
	doc.Records = append(doc.Records, module)
	for i, id := range ids {
		remap[id] = i + 2
	}
	subst := make(map[string]string, len(names))
	for i, name := range names {
		if i < len(args) {
			subst[name] = args[i]
		}
	}
	for _, id := range ids {
		src := d.recs[id]
		if src.Kind == "typeparam" {
			parent := d.recs[refField(src, "parent")]
			if parent.Kind != "struct" {
				continue
			}
		}
		rec := willc.Record{ID: remap[id], Kind: src.Kind}
		isParamType := src.Kind == "type" && contains(names, fieldText(src, "text"))
		for _, f := range src.Fields {
			v := f.Value
			if f.Name == "parent" && v.Kind == willc.RefValue {
				if v.Ref == d.moduleID {
					v.Ref = 1
				} else if next, ok := remap[v.Ref]; ok {
					v.Ref = next
				} else {
					return nil, false
				}
			} else if v.Kind == willc.RefValue {
				if next, ok := remap[v.Ref]; ok {
					v.Ref = next
				} else if src.Kind == "type" && f.Name == "to" {
					// The type text is substituted, so the declaration of the
					// generic type itself does not have to be copied.
					v.Ref = 0
				} else {
					return nil, false
				}
			}
			isTypeText := f.Name == "type" || (f.Name == "text" && src.Kind == "type")
			if isTypeText && (v.Kind == willc.TextValue || v.Kind == willc.WordValue) {
				v.Text = substituteTypeText(v.Text, subst)
			}
			rec.Fields = append(rec.Fields, willc.Field{Name: f.Name, Value: v})
		}
		if src.ID == fn.ID && mangled != "" {
			for i := range rec.Fields {
				if rec.Fields[i].Name == "text" {
					rec.Fields[i].Value = willc.Text(mangled)
				}
			}
		}
		if isParamType {
			rec.Fields = rec.Fields[:0]
			rec.Fields = append(rec.Fields,
				willc.Field{Name: "parent", Value: willc.Ref(remap[refField(src, "parent")])},
				willc.Field{Name: "pos", Value: willc.Text(fieldText(src, "pos"))},
				willc.Field{Name: "text", Value: willc.Text(substituteTypeText(fieldText(src, "text"), subst))},
				willc.Field{Name: "kind", Value: willc.Word("scalar")},
			)
		}
		doc.Records = append(doc.Records, rec)
	}
	return doc, true
}

func contains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// genericMethodCall describes a method call on an instantiated generic
// receiver: the declaration, the receiver's parameter names, and the concrete
// receiver arguments.
type genericMethodCall struct {
	fn       willc.Record
	recvName string
	names    []string
	args     []string
}

// methodInstance resolves a method call whose receiver is a concrete instance
// of a generic struct declared in this document.
func (g *generator) methodInstance(r willc.Record) (genericMethodCall, bool) {
	if r.Kind != "mcall" {
		return genericMethodCall{}, false
	}
	recvText, err := g.willTypeText(refField(r, "recv"))
	if err != nil {
		return genericMethodCall{}, false
	}
	recvName, recvArgs, ok := splitGenericType(recvText)
	if !ok {
		return genericMethodCall{}, false
	}
	id, ok := g.structDecl[recvName]
	if !ok {
		return genericMethodCall{}, false
	}
	params := g.typeParamNames(g.recs[id])
	if len(params) != len(recvArgs) {
		return genericMethodCall{}, false
	}
	name := fieldText(r, "text")
	for _, f := range g.recs {
		if f.Kind != "func" || fieldText(f, "text") != name {
			continue
		}
		recv := g.receiverOf(f.ID)
		if recv == nil {
			continue
		}
		declName, declArgs, ok := splitGenericType(fieldText(*recv, "type"))
		if !ok || declName != recvName || len(declArgs) != len(params) {
			continue
		}
		generic := false
		for _, a := range declArgs {
			if contains(params, a) {
				generic = true
			}
		}
		if !generic {
			continue
		}
		return genericMethodCall{fn: f, recvName: recvName, names: declArgs, args: recvArgs}, true
	}
	return genericMethodCall{}, false
}

// genericMethodName returns the symbol of a generic method instance, or "".
func (g *generator) genericMethodName(r willc.Record) string {
	call, ok := g.methodInstance(r)
	if !ok {
		return ""
	}
	return qualify(g.pkg, structInstanceKey(call.recvName, call.args)+"."+fieldText(r, "text"))
}

// genericMethodResult returns the substituted result text of a generic method
// call, or "".
func (g *generator) genericMethodResult(r willc.Record) string {
	call, ok := g.methodInstance(r)
	if !ok {
		return ""
	}
	subst := make(map[string]string, len(call.names))
	for i, name := range call.names {
		subst[name] = call.args[i]
	}
	return substituteTypeText(fieldText(call.fn, "type"), subst)
}

// genericMethodDecl reports whether a method declaration has a generic
// receiver and therefore needs one instance per receiver type.
func (g *generator) genericMethodDecl(fn willc.Record) bool {
	recv := g.receiverOf(fn.ID)
	if recv == nil {
		return false
	}
	name, args, ok := splitGenericType(fieldText(*recv, "type"))
	if !ok {
		return false
	}
	id, ok := g.structDecl[name]
	if !ok {
		return false
	}
	params := g.typeParamNames(g.recs[id])
	for _, a := range args {
		if contains(params, a) {
			return true
		}
	}
	return false
}

// planMethodInstances creates one document per generic method instance.
func (g *generator) planMethodInstances(base int, seen map[string]bool) {
	for i := 0; i < base; i++ {
		d := g.docs[i]
		for _, r := range d.recs {
			if r.Kind != "mcall" {
				continue
			}
			g.use(d)
			for _, decl := range d.recs {
				switch decl.Kind {
				case "struct":
					g.structDecl[fieldText(decl, "text")] = decl.ID
				case "enum":
					g.enumDecl[fieldText(decl, "text")] = decl.ID
				case "interface":
					g.ifaceDecl[fieldText(decl, "text")] = decl.ID
				}
			}
			call, ok := g.methodInstance(r)
			if !ok {
				continue
			}
			key := d.path + "|" + call.recvName + "|" + fieldText(call.fn, "text") + "|" + strings.Join(call.args, ",")
			if seen[key] {
				continue
			}
			seen[key] = true
			doc, ok := g.cloneFuncInstance(d, call.fn, call.names, call.args, "")
			if !ok {
				continue
			}
			g.docs = append(g.docs, newDocState(doc))
		}
	}
}

// genericTarget resolves a generic function call to its declaration, whether
// the callee is a node of the current document or a qualified symbol of
// another one.
func (g *generator) genericTarget(r willc.Record) (*docState, willc.Record, []string, bool) {
	args := g.typeArgTexts(r)
	if len(args) == 0 {
		return nil, willc.Record{}, nil, false
	}
	if id := refField(r, "to"); id != 0 {
		fn, ok := g.recs[id]
		if ok && fn.Kind == "func" && g.hasTypeParams(fn) {
			return g.cur, fn, args, true
		}
		return nil, willc.Record{}, nil, false
	}
	sym := textField(r, "to")
	if sym == "" {
		return nil, willc.Record{}, nil, false
	}
	for _, d := range g.docs {
		for _, f := range d.recs {
			if f.Kind != "func" || receiverOfIn(d, f.ID) != nil {
				continue
			}
			if qualify(d.path, fieldText(f, "text")) == sym && g.hasTypeParamsIn(d, f) {
				return d, f, args, true
			}
		}
	}
	return nil, willc.Record{}, nil, false
}

// genericCallName returns the mangled symbol of a generic call, or "" when the
// call is not generic.
func (g *generator) genericCallName(r willc.Record) string {
	_, fn, args, ok := g.genericTarget(r)
	if !ok {
		return ""
	}
	if id := refField(r, "to"); id != 0 {
		return qualify(g.pkg, instanceName(fieldText(fn, "text"), args))
	}
	return instanceName(textField(r, "to"), args)
}

// genericCallResult returns the substituted result text of a generic call, or
// "" when the call is not generic.
func (g *generator) genericCallResult(r willc.Record) string {
	d, fn, args, ok := g.genericTarget(r)
	if !ok {
		return ""
	}
	return substituteText(fieldText(fn, "type"), typeParamNamesIn(d, fn), args)
}

// receiverOfIn returns the receiver record of a function in one document.
func receiverOfIn(d *docState, fn int) *willc.Record {
	for _, r := range d.recs {
		if r.Kind == "receiver" && refField(r, "parent") == fn {
			rec := r
			return &rec
		}
	}
	return nil
}

// typeParamNamesIn returns the declared type parameter names of a function.
func typeParamNamesIn(d *docState, fn willc.Record) []string {
	var names []string
	for _, r := range d.recs {
		if r.Kind == "typeparam" && refField(r, "parent") == fn.ID {
			names = append(names, fieldText(r, "text"))
		}
	}
	return names
}
