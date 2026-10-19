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
	m := map[int]willc.Record{}
	for _, r := range doc.Records {
		m[r.ID] = r
	}
	g := &generator{recs: m, scope: map[int]string{}, out: &strings.Builder{}}
	for _, r := range doc.Records {
		if r.Kind == "func" {
			if err := g.funcDecl(r); err != nil {
				return nil, err
			}
		}
	}
	return []byte(g.out.String()), nil
}

type generator struct {
	recs  map[int]willc.Record
	scope map[int]string
	out   *strings.Builder
	tmp   int
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
	if result != "i32" {
		return g.errf(fn.ID, "cannot translate node #%d: only i32 results are lowered yet", fn.ID)
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
	}
	return "", g.errf(id, "cannot translate node #%d: node kind %s is not lowered yet", id, r.Kind)
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

func fieldText(r willc.Record, name string) string {
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == willc.TextValue {
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
