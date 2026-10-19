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
	return EmitWithEntry(doc, "")
}

// EmitWithEntry translates one willc document and, when entry names a function
// that returns no value, wraps it in the i32-returning main the C runtime
// calls, so a program whose entry returns nothing exits with 0.
func EmitWithEntry(doc *willc.Doc, entry string) ([]byte, error) {
	m := map[int]willc.Record{}
	for _, r := range doc.Records {
		m[r.ID] = r
	}
	g := &generator{
		recs:     m,
		scope:    map[int]string{},
		funcs:    map[int]string{},
		links:    map[int]string{},
		declared: map[string]bool{},
		out:      &strings.Builder{},
	}
	// A void entry cannot be the C main itself, so it is mangled and called
	// by a generated wrapper.
	if entry != "" {
		for _, r := range doc.Records {
			if r.Kind == "func" && fieldText(r, "text") == entry && fieldText(r, "type") == "" {
				g.wrappedEntry = "will$" + entry
				break
			}
		}
	}
	g.collect()
	for _, r := range doc.Records {
		if r.Kind == "func" {
			if err := g.funcDecl(r); err != nil {
				return nil, err
			}
		}
	}
	if g.wrappedEntry != "" {
		// The wrapper is the C entry point; the user's entry keeps its name
		// under a mangled symbol.
		fmt.Fprintf(g.out, "define i32 @main() {\nentry:\n  call void @%s()\n  ret i32 0\n}\n", g.wrappedEntry)
	}
	return []byte(g.out.String()), nil
}

type generator struct {
	recs     map[int]willc.Record
	scope    map[int]string
	funcs    map[int]string
	links    map[int]string
	declared map[string]bool
	out      *strings.Builder
	tmp      int
	// wrappedEntry holds the mangled name of a void entry once it is known.
	wrappedEntry string
}

// collect records the callable names: file functions and import c links.
func (g *generator) collect() {
	for _, r := range g.recs {
		switch r.Kind {
		case "func":
			g.funcs[r.ID] = g.symbolName(fieldText(r, "text"))
		case "link":
			g.links[r.ID] = fieldText(r, "text")
		}
	}
}

// symbolName returns the IR symbol of one file function. A void entry is
// mangled, because the generated main calls it under that name.
func (g *generator) symbolName(name string) string {
	if g.wrappedEntry != "" && g.wrappedEntry == "will$"+name {
		return g.wrappedEntry
	}
	return name
}

func (g *generator) errf(id int, format string, args ...any) error {
	return &willc.Error{ID: id, Message: fmt.Sprintf(format, args...)}
}

func (g *generator) funcDecl(fn willc.Record) error {
	name := fieldText(fn, "text")
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
	fmt.Fprintf(g.out, "define i32 @%s(%s) {\n", name, strings.Join(sig, ", "))
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
	fmt.Fprintf(g.out, "define void @%s(%s) {\n", g.symbolName(name), strings.Join(sig, ", "))
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

// mcall lowers a method call. Only a C link reached through the c qualifier
// is lowered yet, as in c.putchar(x).
func (g *generator) mcall(r willc.Record) (string, error) {
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

// call lowers a call to a file function or to an import c link. Only i32
// arguments and an i32 result are lowered yet.
func (g *generator) call(r willc.Record) (string, error) {
	target := refField(r, "to")
	name := g.funcs[target]
	link := false
	if name == "" {
		name = g.links[target]
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
	fmt.Fprintf(g.out, "  %s = call i32 @%s(%s)\n", tmp, name, strings.Join(parts, ", "))
	return tmp, nil
}

func (g *generator) declare(name string, args int) {
	if g.declared[name] {
		return
	}
	g.declared[name] = true
	fmt.Fprintf(g.out, "declare i32 @%s(%s)\n", name, i32Types(args))
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
