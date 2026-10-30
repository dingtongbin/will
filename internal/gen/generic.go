package gen

import (
	"sort"
	"strconv"
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

// typeArgTexts returns the type argument texts a call writes. A cross-package
// type record carries its qualified name in to; the short spelling is not
// resolvable from the defining package.
func (g *generator) typeArgTexts(r willc.Record) []string {
	var texts []string
	for _, id := range refsOf(r, "typearg") {
		if t, ok := g.recs[id]; ok {
			texts = append(texts, typeRecordText(t))
		}
	}
	return texts
}

// typeRecordText is the canonical text of one type record: the qualified to
// spelling when the record has one, otherwise the source text.
func typeRecordText(t willc.Record) string {
	if to := textField(t, "to"); to != "" {
		return to
	}
	return fieldText(t, "text")
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
// call. A method instance may itself call another generic method or build a
// container with a parameterized init, so planning repeats until no document
// adds a new instance.
func (g *generator) planInstances() {
	seen := map[string]bool{}
	for {
		base := len(g.docs)
		g.planCallInstances(base, seen)
		g.planMethodInstances(base, seen)
		g.planInitInstances(base, seen)
		if len(g.docs) == base {
			return
		}
	}
}

// planCallInstances creates the free generic function instances named by the
// current documents.
func (g *generator) planCallInstances(base int, seen map[string]bool) {
	for i := 0; i < base; i++ {
		d := g.docs[i]
		g.use(d)
		for _, r := range sortedRecords(d.recs) {
			if r.Kind != "call" && r.Kind != "mcall" {
				continue
			}
			calleeDoc, fn, args, ok := g.genericTarget(r)
			if !ok {
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
			state := newDocState(doc)
			state.clone = true
			g.docs = append(g.docs, state)
		}
	}
}

// planInitInstances creates the init method of a generic struct for every
// concrete type that a new expression constructs. A generic init is not a
// method call, so the method-call scan cannot find it. The declaration may live
// in the imported package, so the helper resolves both sides of the type text.
func (g *generator) planInitInstances(base int, seen map[string]bool) {
	for i := 0; i < base; i++ {
		d := g.docs[i]
		for _, r := range sortedRecords(d.recs) {
			if r.Kind != "new" {
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
			var def *docState
			var fn willc.Record
			var local string
			var names, args []string
			found := false
			for _, cand := range g.docs {
				def, fn, local, names, args, found = g.initDeclForNew(cand, r)
				if found {
					break
				}
			}
			if !found {
				continue
			}
			key := def.path + "|" + local + "|init|" + strings.Join(args, ",")
			if seen[key] {
				continue
			}
			seen[key] = true
			// The receiver text carries the instance (MutexState<Counter>), so
			// the init keeps its declared name and the symbol is generated from
			// the receiver; mangling the text here would repeat the receiver.
			doc, ok := g.cloneFuncInstance(def, fn, names, args, "")
			if !ok {
				continue
			}
			state := newDocState(doc)
			state.clone = true
			g.docs = append(g.docs, state)
		}
	}
}

// initForNew predicts the symbol and substituted parameter types of the init
// instance a new expression calls. The instance document itself is planned
// separately, before lowering. The declaration may be in another package.
func (g *generator) initForNew(d *docState, r willc.Record) (string, []string, string, bool) {
	def, fn, local, names, args, ok := g.initDeclForNew(d, r)
	if !ok {
		return "", nil, "", false
	}
	subst := make(map[string]string, len(names))
	for i, n := range names {
		subst[n] = args[i]
	}
	var params []string
	for _, p := range def.recs {
		if p.Kind == "param" && refField(p, "parent") == fn.ID {
			text := substituteTypeText(fieldText(p, "type"), subst)
			params = append(params, g.qualifyImportedType(def.path, text))
		}
	}
	result := substituteTypeText(fieldText(fn, "type"), subst)
	if result == "()" {
		result = ""
	}
	if result != "" {
		result = g.qualifyImportedType(def.path, result)
	}
	return qualify(def.path, structInstanceKey(local, args)+".init"), params, result, true
}

// typeParamNamesIn returns the type parameter names declared by one struct.
func (g *generator) typeParamNamesIn(d *docState, decl willc.Record) []string {
	var names []string
	for _, r := range d.recs {
		if r.Kind == "typeparam" && refField(r, "parent") == decl.ID {
			names = append(names, fieldText(r, "text"))
		}
	}
	return names
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
			texts = append(texts, typeRecordText(t))
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
// every type parameter replaced. Records keep their original ids: the subtree
// is a subset of one document, so its ids are already unique, and references
// that reach outside the subtree stay valid once those records are pulled in.
// A non-empty mangled name renames the function; a method instance keeps its
// declared name because its receiver type text already tells the two
// instantiations apart.
func (g *generator) cloneFuncInstance(d *docState, fn willc.Record, names, args []string, mangled string) (*willc.Doc, bool) {
	included := map[int]bool{fn.ID: true}
	changed := true
	for changed {
		changed = false
		for _, r := range d.recs {
			if included[r.ID] {
				// A type the signature names comes along with its declaration,
				// so the instance can lay out a generic receiver or field. An
				// aggregate literal likewise needs the struct it builds.
				if r.Kind == "type" || r.Kind == "agg" {
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
		// Names the body qualifies through a package reach their import, and
		// a cimport is the only way to reach its link table, so both travel
		// with the instance.
		for _, r := range d.recs {
			if !included[r.ID] {
				continue
			}
			if r.Kind != "name" {
				continue
			}
			to := refField(r, "to")
			if to == 0 || included[to] {
				continue
			}
			if target, ok := d.recs[to]; ok &&
				(target.Kind == "import" || target.Kind == "cimport") {
				included[to] = true
				changed = true
			}
		}
	}
	remap := map[int]int{}
	for id := range included {
		remap[id] = id
	}
	subst := make(map[string]string, len(names))
	for i, name := range names {
		if i < len(args) {
			subst[name] = args[i]
		}
	}
	doc := &willc.Doc{}
	module := d.recs[d.moduleID]
	doc.Records = append(doc.Records, module)
	ids := make([]int, 0, len(included))
	for id := range included {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		src := d.recs[id]
		if src.Kind == "typeparam" {
			parent := d.recs[refField(src, "parent")]
			if parent.Kind != "struct" {
				continue
			}
		}
		rec := willc.Record{ID: id, Kind: src.Kind}
		// A type the signature spells as a type parameter becomes a scalar of
		// the concrete argument; its declaration (the typeparam) does not
		// travel with the instance.
		isParamType := src.Kind == "type" && contains(names, fieldText(src, "text"))
		if isParamType {
			rec.Fields = append(rec.Fields,
				willc.Field{Name: "parent", Value: willc.Ref(refField(src, "parent"))},
				willc.Field{Name: "pos", Value: willc.Text(fieldText(src, "pos"))},
				willc.Field{Name: "text", Value: willc.Text(substituteTypeText(fieldText(src, "text"), subst))},
				willc.Field{Name: "kind", Value: willc.Word("scalar")},
			)
			doc.Records = append(doc.Records, rec)
			continue
		}
		for _, f := range src.Fields {
			v := f.Value
			if v.Kind == willc.RefValue && f.Name == "to" && remap[v.Ref] == 0 {
				// A call may name a function outside the subtree; the text
				// spelling resolves it through the symbol table instead. A
				// type text is substituted, so its stale ref simply drops.
				if src.Kind == "call" {
					if target, ok := d.recs[v.Ref]; ok && target.Kind == "func" {
						v = willc.Text(qualify(d.path, fieldText(target, "text")))
					} else {
						return nil, false
					}
				} else if src.Kind != "type" {
					return nil, false
				} else {
					continue
				}
			}
			isTypeText := f.Name == "type" || (f.Name == "text" && src.Kind == "type")
			if isTypeText && (v.Kind == willc.TextValue || v.Kind == willc.WordValue) {
				v.Text = substituteTypeText(v.Text, subst)
			}
			if f.Name == "field" && v.Kind == willc.TextValue {
				// Aggregate literals keep their field value as name=#id inside
				// one text field; the id names a record of the same document.
				if fieldName, id, ok := splitAggregateField(v.Text); ok {
					v.Text = fieldName + "=#" + strconv.Itoa(id)
				}
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
	doc      *docState
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
	recvText = g.canonicalTypeText(recvText)
	recvName, recvArgs, ok := splitGenericType(recvText)
	if !ok {
		return genericMethodCall{}, false
	}
	recvArgs = g.canonicalTypeArgs(recvArgs)
	if decl, ok := typeDeclIn(g.recs, recvName); ok {
		params := g.typeParamNames(decl)
		if len(params) != len(recvArgs) {
			return genericMethodCall{}, false
		}
		if call, ok := g.genericMethodIn(g.cur, g.recs, recvName, params, recvArgs, fieldText(r, "text")); ok {
			call.doc = g.cur
			return call, true
		}
	}
	// The receiver may be written without a package prefix inside a cloned
	// instance document. Qualify it before looking for the declaration.
	if !strings.Contains(recvName, ".") {
		recvName = qualify(g.pkg, recvName)
	}
	doc, local, ok := g.genericStructDoc(recvName)
	if !ok {
		return genericMethodCall{}, false
	}
	var decl willc.Record
	for _, rec := range doc.recs {
		if (rec.Kind == "struct" || rec.Kind == "class") && fieldText(rec, "text") == local {
			decl = rec
			break
		}
	}
	if decl.ID == 0 {
		return genericMethodCall{}, false
	}
	params := g.typeParamNamesIn(doc, decl)
	if len(params) != len(recvArgs) {
		return genericMethodCall{}, false
	}
	call, ok := g.genericMethodIn(doc, doc.recs, local, params, recvArgs, fieldText(r, "text"))
	if !ok {
		return genericMethodCall{}, false
	}
	call.doc = doc
	return call, true
}

// genericMethodIn finds one method declaration with a generic receiver in a
// document's records.
func (g *generator) genericMethodIn(d *docState, recs map[int]willc.Record, recvName string, params, recvArgs []string, name string) (genericMethodCall, bool) {
	for _, f := range recs {
		if f.Kind != "func" || fieldText(f, "text") != name {
			continue
		}
		recv := receiverOfIn(d, f.ID)
		if recv == nil {
			continue
		}
		declName, declArgs, ok := splitGenericType(baseBorrowText(fieldText(*recv, "type")))
		if !ok || declName != recvName || len(declArgs) != len(params) {
			continue
		}
		generic := false
		for _, a := range declArgs {
			if contains(params, a) {
				generic = true
			}
		}
		if generic {
			return genericMethodCall{fn: f, recvName: recvName, names: declArgs, args: recvArgs}, true
		}
	}
	return genericMethodCall{}, false
}

// genericMethodName returns the symbol of a generic method instance, or "".
func (g *generator) genericMethodName(r willc.Record) string {
	call, ok := g.methodInstance(r)
	if !ok {
		return ""
	}
	pkg := g.pkg
	if call.doc != nil {
		pkg = call.doc.path
	}
	return qualify(pkg, structInstanceKey(call.recvName, call.args)+"."+fieldText(r, "text"))
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
	text := substituteTypeText(fieldText(call.fn, "type"), subst)
	if call.doc != nil && call.doc != g.cur {
		text = g.qualifyImportedType(call.doc.path, text)
	}
	return text
}

// qualifyImportedType puts the defining package on a type text returned by a
// method of another document. A plain named type and a user generic both need
// the prefix; the predeclared generics keep their unqualified spelling.
func (g *generator) qualifyImportedType(pkg, text string) string {
	if name, args, ok := splitGenericType(text); ok {
		if q := qualify(pkg, name); g.declKinds[q] != "" {
			return q + "<" + strings.Join(args, ",") + ">"
		}
	}
	return g.qualifyTypeText(pkg, text)
}

// genericMethodDecl reports whether a method declaration has a generic
// receiver and therefore needs one instance per receiver type.
func (g *generator) genericMethodDecl(fn willc.Record) bool {
	recv := g.receiverOf(fn.ID)
	if recv == nil {
		return false
	}
	name, args, ok := splitGenericType(baseBorrowText(fieldText(*recv, "type")))
	if !ok {
		return false
	}
	id, ok := typeDeclIn(g.recs, name)
	if !ok {
		return false
	}
	params := g.typeParamNames(id)
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
		for _, r := range sortedRecords(d.recs) {
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
			source := d
			if call.doc != nil {
				source = call.doc
			}
			key := source.path + "|" + call.recvName + "|" + fieldText(call.fn, "text") + "|" + strings.Join(call.args, ",")
			if seen[key] {
				continue
			}
			seen[key] = true
			doc, ok := g.cloneFuncInstance(source, call.fn, call.names, call.args, "")
			if !ok {
				continue
			}
			state := newDocState(doc)
			state.clone = true
			g.docs = append(g.docs, state)
		}
	}
}

// genericTarget resolves a generic function call to its declaration, whether
// the callee is a node of the current document or a qualified symbol of
// another one.
func (g *generator) genericTarget(r willc.Record) (*docState, willc.Record, []string, bool) {
	args := g.typeArgTexts(r)
	if id := refField(r, "to"); id != 0 {
		fn, ok := g.recs[id]
		if ok && fn.Kind == "func" && g.hasTypeParams(fn) {
			if len(args) == 0 {
				args = g.inferGenericArgs(g.cur, fn, r)
			}
			if len(args) == 0 {
				return nil, willc.Record{}, nil, false
			}
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
				if len(args) == 0 {
					args = g.inferGenericArgs(d, f, r)
				}
				if len(args) == 0 {
					return nil, willc.Record{}, nil, false
				}
				return d, f, args, true
			}
		}
	}
	return nil, willc.Record{}, nil, false
}

// inferGenericArgs derives the type arguments of a call from the argument
// types. It only accepts direct, syntactic matches; an ambiguous call keeps
// the explicit type arguments requirement and is reported by the checker.
func (g *generator) inferGenericArgs(d *docState, fn willc.Record, call willc.Record) []string {
	names := typeParamNamesIn(d, fn)
	if len(names) == 0 {
		return nil
	}
	var params []willc.Record
	for _, r := range d.recs {
		if r.Kind == "param" && refField(r, "parent") == fn.ID {
			params = append(params, r)
		}
	}
	sortByID(params)
	args := g.callArgs(call)
	if len(args) > len(params) {
		return nil
	}
	subst := map[string]string{}
	for i, arg := range args {
		actual := ""
		if r, ok := g.recs[arg]; ok && (r.Kind == "func" || r.Kind == "lambda") {
			actual = g.lambdaTypeText(r)
		}
		if actual == "" {
			actual, _ = g.willTypeText(arg)
		}
		if actual == "" {
			return nil
		}
		if !unifyTypeText(fieldText(params[i], "type"), actual, names, subst) {
			return nil
		}
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		value := subst[name]
		if value == "" {
			return nil
		}
		out = append(out, value)
	}
	return out
}

// unifyTypeText binds generic type parameter names in want from one argument
// type text. It is deliberately small: it understands the container and
// function shapes the language spells in a signature and nothing else.
func unifyTypeText(want, actual string, names []string, subst map[string]string) bool {
	want = strings.TrimSpace(want)
	actual = strings.TrimSpace(actual)
	if want == "" || actual == "" {
		return true
	}
	for _, name := range names {
		if want == name {
			if prev, ok := subst[name]; ok {
				return prev == actual
			}
			subst[name] = actual
			return true
		}
	}
	if strings.HasPrefix(want, "[]") {
		if !strings.HasPrefix(actual, "[]") {
			return false
		}
		return unifyTypeText(want[2:], actual[2:], names, subst)
	}
	if strings.HasPrefix(want, "func(") {
		wp, wr, ok := funcTypeParts(want)
		if !ok {
			return false
		}
		ap, ar, ok := funcTypeParts(actual)
		if !ok || len(wp) != len(ap) {
			return false
		}
		for i := range wp {
			if !unifyTypeText(wp[i], ap[i], names, subst) {
				return false
			}
		}
		return unifyTypeText(wr, ar, names, subst)
	}
	if name, wargs, ok := splitGenericType(want); ok {
		aname, aargs, ok := splitGenericType(actual)
		if !ok || name != aname || len(wargs) != len(aargs) {
			return false
		}
		for i := range wargs {
			if !unifyTypeText(wargs[i], aargs[i], names, subst) {
				return false
			}
		}
		return true
	}
	return true
}

// isGenericCallee reports whether a call names a generic function or a method
// on an instantiated generic receiver. Such a call resolves to an instance
// document, never to the declaration the records name.
func (g *generator) isGenericCallee(call willc.Record) bool {
	if g.genericCallName(call) != "" {
		return true
	}
	return call.Kind == "mcall" && g.genericMethodName(call) != ""
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
	names := typeParamNamesIn(d, fn)
	subst := make(map[string]string, len(names))
	for i, name := range names {
		if i < len(args) {
			subst[name] = args[i]
		}
	}
	return substituteTypeText(fieldText(fn, "type"), subst)
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
