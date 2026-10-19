// Package gen translates willc into LLVM text. The translation is
// mechanical: it never analyzes, and a node it does not know is reported with
// G0001 instead of being guessed.
package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// Emit translates one willc document into LLVM text. Only the node subset
// needed by the first lowering experiment is implemented: integer functions
// with parameters, return, integer literals, names, and + - *.
func Emit(doc *willc.Doc) ([]byte, error) {
	return EmitModule([]*willc.Doc{doc}, "")
}

// EmitWithEntry translates one willc document and, when entry names a function
// that returns no value, wraps it in the i32-returning main the C runtime
// calls, so a program whose entry returns nothing exits with 0.
func EmitWithEntry(doc *willc.Doc, entry string) ([]byte, error) {
	return EmitModule([]*willc.Doc{doc}, entry)
}

// EmitModule translates the documents of one module into a single LLVM module.
// Each document is one source file, and its module path is the package path:
// every function symbol is <package path>.<name>, so a call written as
// to="<package path>.<name>" names the same symbol as the definition.
func EmitModule(docs []*willc.Doc, entry string) ([]byte, error) {
	g := &generator{
		scope:    map[int]string{},
		declared: map[string]bool{},
		out:      &strings.Builder{},
	}
	for _, doc := range docs {
		g.docs = append(g.docs, newDocState(doc))
	}
	// A void entry cannot be the C main itself, so it keeps a mangled symbol
	// and the generated wrapper calls it.
	g.findEntry(entry)
	for _, d := range g.docs {
		g.use(d)
		if err := g.emitDoc(); err != nil {
			return nil, err
		}
	}
	if g.wrappedEntry != "" {
		fmt.Fprintf(g.out, "define i32 @main() {\nentry:\n  call void @%s()\n  ret i32 0\n}\n",
			llvmSymbol(g.wrappedEntry))
	}
	return []byte(g.out.String()), nil
}

// docState is the per-file part of the translation: record ids and the callable
// names mean something only inside one document.
type docState struct {
	path  string
	recs  map[int]willc.Record
	funcs map[int]string
	links map[int]string
}

func newDocState(doc *willc.Doc) *docState {
	d := &docState{
		recs:  map[int]willc.Record{},
		funcs: map[int]string{},
		links: map[int]string{},
	}
	for _, r := range doc.Records {
		d.recs[r.ID] = r
		if r.Kind == "module" {
			d.path = fieldText(r, "path")
		}
	}
	return d
}

// findEntry records the package and name of the entry function. A void entry
// also gets a mangled symbol, because the generated i32 main calls it.
func (g *generator) findEntry(entry string) {
	if entry == "" {
		return
	}
	for _, d := range g.docs {
		for _, r := range d.recs {
			if r.Kind != "func" || fieldText(r, "text") != entry {
				continue
			}
			if hasChild(d.recs, r.ID, "receiver") {
				continue
			}
			g.entryName = entry
			g.entryPkg = d.path
			if fieldText(r, "type") == "" {
				g.wrappedEntry = qualify(d.path, "will$"+entry)
			}
			return
		}
	}
}

// use makes one document current. The record ids, callable names, temporaries,
// and parameter scopes are per document; declarations are module-wide.
func (g *generator) use(d *docState) {
	g.cur = d
	g.pkg = d.path
	g.recs = d.recs
	g.scope = map[int]string{}
	g.tmp = 0
}

type generator struct {
	// docs is every document of the module, and cur is the one being lowered.
	docs []*docState
	cur  *docState

	recs     map[int]willc.Record
	scope    map[int]string
	declared map[string]bool
	out      *strings.Builder
	tmp      int

	// pkg is the package path of the current document.
	pkg string
	// entryName, entryPkg, and wrappedEntry describe a void entry: it keeps a
	// mangled symbol because the generated main calls it.
	entryName    string
	entryPkg     string
	wrappedEntry string
}

// emitDoc lowers every function of the current document.
func (g *generator) emitDoc() error {
	g.collect()
	for _, r := range g.recs {
		if r.Kind == "func" {
			if err := g.funcDecl(r); err != nil {
				return err
			}
		}
	}
	return nil
}

// collect records the callable names of the current document: file functions
// and import c links.
func (g *generator) collect() {
	for _, r := range g.recs {
		switch r.Kind {
		case "func":
			g.cur.funcs[r.ID] = g.symbolName(fieldText(r, "text"))
		case "link":
			g.cur.links[r.ID] = fieldText(r, "text")
		}
	}
}

// symbolName returns the IR symbol of one function of the current document.
// The entry keeps the C symbol when it returns a value (only main is callable
// there), and uses its mangled symbol when the generated main wraps it.
func (g *generator) symbolName(name string) string {
	if g.entryName != "" && g.entryPkg == g.pkg && g.entryName == name {
		if g.wrappedEntry != "" {
			return g.wrappedEntry
		}
		return name
	}
	return qualify(g.pkg, name)
}

// qualify puts a package path in front of a name. A document without a package
// path keeps the plain name.
func qualify(pkg, name string) string {
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}

// llvmSymbol quotes a symbol whose text is not a plain LLVM identifier.
func llvmSymbol(name string) string {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '_' || c == '.' || c == '-' || c == '$' ||
			c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		return `"` + name + `"`
	}
	return name
}

func (g *generator) errf(id int, format string, args ...any) error {
	return &willc.Error{ID: id, Message: fmt.Sprintf(format, args...)}
}

func (g *generator) funcDecl(fn willc.Record) error {
	name := fieldText(fn, "text")
	if hasChild(g.recs, fn.ID, "receiver") {
		return g.errf(fn.ID, "cannot translate node #%d: methods are not lowered yet", fn.ID)
	}
	result := ""
	var params []willc.Record
	for _, r := range g.recs {
		if refField(r, "parent") != fn.ID {
			continue
		}
		switch r.Kind {
		case "param":
			params = append(params, r)
		case "type":
			result = fieldText(r, "text")
		}
	}
	sortByID(params)
	var sig []string
	for _, p := range params {
		if fieldText(p, "type") != "i32" {
			return g.errf(p.ID, "cannot translate node #%d: only i32 parameters are lowered yet", p.ID)
		}
		pname := fieldText(p, "text")
		g.scope[p.ID] = "%" + pname
		sig = append(sig, "i32 %"+pname)
	}
	if result == "" {
		return g.voidFunc(fn, name, sig)
	}
	if result != "i32" {
		return g.errf(fn.ID, "cannot translate node #%d: only i32 results are lowered yet", fn.ID)
	}
	fmt.Fprintf(g.out, "define i32 @%s(%s) {\n", llvmSymbol(g.symbolName(name)), strings.Join(sig, ", "))
	g.out.WriteString("entry:\n")
	body, err := g.blockOf(fn.ID)
	if err != nil {
		return err
	}
	if err := g.block(body); err != nil {
		return err
	}
	g.out.WriteString("}\n")
	return nil
}

// voidFunc lowers a function that returns no value.
func (g *generator) voidFunc(fn willc.Record, name string, sig []string) error {
	fmt.Fprintf(g.out, "define void @%s(%s) {\n", llvmSymbol(g.symbolName(name)), strings.Join(sig, ", "))
	g.out.WriteString("entry:\n")
	body, err := g.blockOf(fn.ID)
	if err != nil {
		return err
	}
	if err := g.voidBlock(body); err != nil {
		return err
	}
	g.out.WriteString("}\n")
	return nil
}

// voidBlock lowers a body with no value: an empty body, or one bare return.
func (g *generator) voidBlock(b willc.Record) error {
	stmts := g.children(b.ID)
	sortByID(stmts)
	if len(stmts) == 0 {
		g.out.WriteString("  ret void\n")
		return nil
	}
	if len(stmts) == 1 && stmts[0].Kind == "return" && refField(stmts[0], "value") == 0 {
		g.out.WriteString("  ret void\n")
		return nil
	}
	return g.errf(b.ID, "cannot translate node #%d: only an empty body or a bare return is lowered yet", b.ID)
}

func (g *generator) blockOf(owner int) (willc.Record, error) {
	for _, r := range g.recs {
		if r.Kind == "block" && refField(r, "parent") == owner {
			return r, nil
		}
	}
	return willc.Record{}, g.errf(owner, "cannot translate node #%d: no body", owner)
}

func (g *generator) block(b willc.Record) error {
	stmts := g.children(b.ID)
	sortByID(stmts)
	if len(stmts) != 1 || stmts[0].Kind != "return" {
		return g.errf(b.ID, "cannot translate node #%d: only a single return is lowered yet", b.ID)
	}
	ret := stmts[0]
	value, err := g.value(refField(ret, "value"))
	if err != nil {
		return err
	}
	fmt.Fprintf(g.out, "  ret i32 %s\n", value)
	return nil
}

// value emits the instructions for one expression and returns its SSA name.
func (g *generator) value(id int) (string, error) {
	r, ok := g.recs[id]
	if !ok {
		return "", g.errf(id, "cannot translate node #%d: no such node", id)
	}
	switch r.Kind {
	case "lit":
		if fieldText(r, "kind") != "int" {
			return "", g.errf(id, "cannot translate node #%d: only integer literals are lowered yet", id)
		}
		return fieldText(r, "text"), nil
	case "name":
		if name, ok := g.scope[refField(r, "to")]; ok {
			return name, nil
		}
		return "", g.errf(id, "cannot translate node #%d: name is not a parameter", id)
	case "bin":
		l, err := g.value(refField(r, "l"))
		if err != nil {
			return "", err
		}
		right, err := g.value(refField(r, "r"))
		if err != nil {
			return "", err
		}
		op, err := llvmOp(fieldText(r, "op"))
		if err != nil {
			return "", g.errf(id, "cannot translate node #%d: %v", id, err)
		}
		g.tmp++
		name := "%t" + strconv.Itoa(g.tmp)
		fmt.Fprintf(g.out, "  %s = %s i32 %s, %s\n", name, op, l, right)
		return name, nil
	case "call":
		return g.call(r)
	case "mcall":
		return g.mcall(r)
	}
	return "", g.errf(id, "cannot translate node #%d: node kind %s is not lowered yet", id, r.Kind)
}

// mcall lowers a method call. A call to a function of another package is
// written as mcall with the qualified name in to; a C link reached through the
// c qualifier is lowered too. A real method call is not lowered yet.
func (g *generator) mcall(r willc.Record) (string, error) {
	if qualified := textField(r, "to"); qualified != "" {
		return g.emitCall(r, qualified, false)
	}
	name := fieldText(r, "text")
	recv := g.recs[refField(r, "recv")]
	cimport := refField(recv, "to")
	if recv.Kind == "name" && cimport != 0 {
		if link := g.linkNamed(cimport, name); link != "" {
			return g.emitCall(r, link, true)
		}
	}
	return "", g.errf(r.ID, "cannot translate node #%d: method call %s is not lowered yet", r.ID, name)
}

// linkNamed returns the C symbol of name declared under one cimport record.
func (g *generator) linkNamed(cimport int, name string) string {
	for _, r := range g.recs {
		if r.Kind == "link" && refField(r, "parent") == cimport && fieldText(r, "text") == name {
			return name
		}
	}
	return ""
}

// call lowers a call to a file function, to a function of another package
// (named by to), or to an import c link. Only i32 arguments and an i32 result
// are lowered yet.
func (g *generator) call(r willc.Record) (string, error) {
	if qualified := textField(r, "to"); qualified != "" {
		return g.emitCall(r, qualified, false)
	}
	target := refField(r, "to")
	name := g.cur.funcs[target]
	link := false
	if name == "" {
		name = g.cur.links[target]
		link = true
	}
	if name == "" {
		return "", g.errf(r.ID, "cannot translate node #%d: the callee is not a file function or a c link", r.ID)
	}
	return g.emitCall(r, name, link)
}

// emitCall writes one call with its integer arguments.
func (g *generator) emitCall(r willc.Record, name string, link bool) (string, error) {
	args := g.callArgs(r)
	values := make([]string, 0, len(args))
	for _, a := range args {
		v, err := g.value(a)
		if err != nil {
			return "", err
		}
		values = append(values, v)
	}
	if link {
		g.declare(name, len(values))
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = "i32 " + v
	}
	g.tmp++
	tmp := "%t" + strconv.Itoa(g.tmp)
	fmt.Fprintf(g.out, "  %s = call i32 @%s(%s)\n", tmp, llvmSymbol(name), strings.Join(parts, ", "))
	return tmp, nil
}

func (g *generator) declare(name string, args int) {
	if g.declared[name] {
		return
	}
	g.declared[name] = true
	fmt.Fprintf(g.out, "declare i32 @%s(%s)\n", llvmSymbol(name), i32Types(args))
}

func i32Types(n int) string {
	if n == 0 {
		return ""
	}
	return strings.Repeat("i32, ", n-1) + "i32"
}

// callArgs lists the argument records of a call in id order. A method call
// also has a receiver child, which is not an argument.
func (g *generator) callArgs(r willc.Record) []int {
	skip := 0
	if r.Kind == "mcall" {
		skip = refField(r, "recv")
	}
	var ids []int
	for _, c := range g.recs {
		if refField(c, "parent") != r.ID || c.Kind == "type" || c.ID == skip {
			continue
		}
		ids = append(ids, c.ID)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	return ids
}

func llvmOp(op string) (string, error) {
	switch op {
	case "+":
		return "add", nil
	case "-":
		return "sub", nil
	case "*":
		return "mul", nil
	}
	return "", fmt.Errorf("operator %s is not lowered yet", op)
}

func (g *generator) children(parent int) []willc.Record {
	var out []willc.Record
	for _, r := range g.recs {
		if refField(r, "parent") == parent && r.Kind != "type" {
			out = append(out, r)
		}
	}
	return out
}

func refField(r willc.Record, name string) int {
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == willc.RefValue {
			return f.Value.Ref
		}
	}
	return 0
}

// textField returns a field written as quoted text, or "" when the field holds
// a node reference.
func textField(r willc.Record, name string) string {
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == willc.TextValue {
			return f.Value.Text
		}
	}
	return ""
}

// hasChild reports whether one record has a child of the given kind.
func hasChild(recs map[int]willc.Record, parent int, kind string) bool {
	for _, r := range recs {
		if r.Kind == kind && refField(r, "parent") == parent {
			return true
		}
	}
	return false
}

// fieldText reads a text field; unquoted words such as lit kind=int are text
// too.
func fieldText(r willc.Record, name string) string {
	for _, f := range r.Fields {
		if f.Name != name {
			continue
		}
		switch f.Value.Kind {
		case willc.TextValue, willc.WordValue:
			return f.Value.Text
		}
	}
	return ""
}

func sortByID(rs []willc.Record) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j].ID < rs[j-1].ID; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}
