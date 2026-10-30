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
	name, args, ok := splitGenericType(strings.TrimSpace(fieldText(r, "type")))
	if !ok {
		return nil, willc.Record{}, "", nil, nil, false
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	args = g.canonicalTypeArgs(args)
	// The construction may live in a cloned instance document whose subtree
	// has no declaration; search the declaring document by the short name.
	var def *docState
	local := name
	search := func(cand *docState) bool {
		if cand == nil {
			return false
		}
		for _, rec := range cand.recs {
			if (rec.Kind == "struct" || rec.Kind == "class") && fieldText(rec, "text") == name {
				def = cand
				return true
			}
		}
		return false
	}
	if !search(d) {
		for _, cand := range g.docs {
			if search(cand) {
				break
			}
		}
	}
	if def == nil {
		return nil, willc.Record{}, "", nil, nil, false
	}
	var decl willc.Record
	for _, rec := range def.recs {
		if (rec.Kind == "struct" || rec.Kind == "class") && fieldText(rec, "text") == local {
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
	for _, a := range args {
		if contains(params, a) {
			// A construction inside the generic declaration still names the
			// type parameter: it is not an instance, and an instance of it
			// would keep the parameter unsubstituted.
			return nil, willc.Record{}, "", nil, nil, false
		}
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

// structDeclDoc finds the document that declares a struct or class by a type
// text that may be local or package-qualified.
func (g *generator) structDeclDoc(d *docState, name string) (*docState, string, bool) {
	if !strings.Contains(name, ".") {
		for _, rec := range d.recs {
			if (rec.Kind == "struct" || rec.Kind == "class") && fieldText(rec, "text") == name {
				return d, name, true
			}
		}
		return nil, "", false
	}
	return g.genericStructDoc(name)
}
