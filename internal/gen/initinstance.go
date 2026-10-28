package gen

import (
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// initDeclForNew resolves the generic init declaration a new expression needs,
// including a declaration from an imported package. It returns the declaration
// document, the init function, the unqualified struct name, the receiver type
// parameters, and the concrete type arguments the call writes.
func (g *generator) initDeclForNew(d *docState, r willc.Record) (*docState, willc.Record, string, []string, []string, bool) {
	text := g.canonicalTypeText(fieldText(r, "type"))
	name, args, ok := splitGenericType(text)
	if !ok {
		return nil, willc.Record{}, "", nil, nil, false
	}
	def, local, ok := g.structDeclDoc(d, name)
	if !ok {
		return nil, willc.Record{}, "", nil, nil, false
	}
	var decl willc.Record
	for _, rec := range def.recs {
		if rec.Kind == "struct" && fieldText(rec, "text") == local {
			decl = rec
			break
		}
	}
	if decl.ID == 0 {
		return nil, willc.Record{}, "", nil, nil, false
	}
	params := g.typeParamNamesIn(def, decl)
	if len(params) != len(args) {
		return nil, willc.Record{}, "", nil, nil, false
	}
	for _, fn := range def.recs {
		if fn.Kind != "func" || fieldText(fn, "text") != "init" {
			continue
		}
		recv := receiverOfIn(def, fn.ID)
		if recv == nil {
			continue
		}
		recvName, recvArgs, ok := splitGenericType(baseBorrowText(fieldText(*recv, "type")))
		if !ok || recvName != local || len(recvArgs) != len(params) {
			continue
		}
		generic := false
		for _, a := range recvArgs {
			if contains(params, a) {
				generic = true
			}
		}
		if generic {
			return def, fn, local, recvArgs, args, true
		}
	}
	return nil, willc.Record{}, "", nil, nil, false
}

// structDeclDoc finds the document that declares a struct by a type text that
// may be local or package-qualified.
func (g *generator) structDeclDoc(d *docState, name string) (*docState, string, bool) {
	if !strings.Contains(name, ".") {
		for _, rec := range d.recs {
			if rec.Kind == "struct" && fieldText(rec, "text") == name {
				return d, name, true
			}
		}
		return nil, "", false
	}
	return g.genericStructDoc(name)
}
