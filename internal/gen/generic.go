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
			if r.Kind != "call" || len(refsOf(r, "typearg")) == 0 {
				continue
			}
			target := refField(r, "to")
			fn, ok := d.recs[target]
			if !ok || fn.Kind != "func" {
				continue
			}
			if !g.hasTypeParamsIn(d, fn) {
				continue
			}
			args := typeArgTextsIn(d, r)
			if len(args) == 0 {
				continue
			}
			key := d.path + "|" + fieldText(fn, "text") + "|" + strings.Join(args, ",")
			if seen[key] {
				continue
			}
			seen[key] = true
			doc, ok := g.cloneInstance(d, fn, args)
			if !ok {
				continue
			}
			g.docs = append(g.docs, newDocState(doc))
		}
	}
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
	included := map[int]bool{fn.ID: true}
	changed := true
	for changed {
		changed = false
		for _, r := range d.recs {
			if included[r.ID] {
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
	mangled := instanceName(fieldText(fn, "text"), args)
	for _, id := range ids {
		src := d.recs[id]
		if src.Kind == "typeparam" {
			continue
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
				} else {
					return nil, false
				}
			}
			if (f.Name == "type" || f.Name == "text") && (v.Kind == willc.TextValue || v.Kind == willc.WordValue) {
				v.Text = substituteText(v.Text, names, args)
			}
			rec.Fields = append(rec.Fields, willc.Field{Name: f.Name, Value: v})
		}
		if src.ID == fn.ID {
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
				willc.Field{Name: "text", Value: willc.Text(substituteText(fieldText(src, "text"), names, args))},
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

// genericCallName returns the mangled symbol of a generic call in the current
// document, or "" when the call is not generic.
func (g *generator) genericCallName(r willc.Record) string {
	target := refField(r, "to")
	fn, ok := g.recs[target]
	if !ok || fn.Kind != "func" || !g.hasTypeParams(fn) {
		return ""
	}
	args := g.typeArgTexts(r)
	if len(args) == 0 {
		return ""
	}
	return qualify(g.pkg, instanceName(fieldText(fn, "text"), args))
}

// genericCallResult returns the substituted result text of a generic call, or
// "" when the call is not generic.
func (g *generator) genericCallResult(r willc.Record) string {
	target := refField(r, "to")
	fn, ok := g.recs[target]
	if !ok || fn.Kind != "func" || !g.hasTypeParams(fn) {
		return ""
	}
	args := g.typeArgTexts(r)
	if len(args) == 0 {
		return ""
	}
	return substituteText(fieldText(fn, "type"), g.typeParamNames(fn), args)
}
