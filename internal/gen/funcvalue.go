package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// funcTypeParts splits func(A,B)->R into its parameter texts and its result.
// An empty result means the function value returns nothing.
func funcTypeParts(text string) ([]string, string, bool) {
	if !strings.HasPrefix(text, "func(") {
		return nil, "", false
	}
	depth := 0
	close := -1
	for i := len("func"); i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				close = i
			}
		}
		if close >= 0 {
			break
		}
	}
	if close < 0 {
		return nil, "", false
	}
	var params []string
	if inner := strings.TrimSpace(text[len("func("):close]); inner != "" {
		params = splitTypeList(inner)
	}
	result := ""
	if rest := strings.TrimPrefix(text[close+1:], "->"); rest != "" && rest != "()" {
		result = rest
	}
	return params, result, true
}

// lambdaTypeText reconstructs the function type of a lambda record after it was
// turned into a function.
func (g *generator) lambdaTypeText(fn willc.Record) string {
	var params []willc.Record
	result := ""
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
	parts := make([]string, 0, len(params))
	for _, p := range params {
		parts = append(parts, fieldText(p, "type"))
	}
	text := "func(" + strings.Join(parts, ",") + ")"
	if result != "" {
		text += "->" + result
	}
	return text
}

// isFuncValue reports whether a call target is a value holding a function
// pointer rather than a named function of the module.
func (g *generator) isFuncValue(decl willc.Record) bool {
	switch decl.Kind {
	case "var", "param":
		return strings.HasPrefix(fieldText(decl, "type"), "func(")
	case "func":
		return g.lambdaNames[decl.ID] != ""
	}
	return false
}

// funcPointer returns the code pointer one function value holds.
func (g *generator) funcPointer(decl willc.Record) (string, error) {
	if decl.Kind == "func" {
		sym := g.cur.funcs[decl.ID]
		if sym == "" {
			return "", g.errf(decl.ID, "cannot translate node #%d: the closure has no symbol", decl.ID)
		}
		return "@" + llvmSymbol(sym), nil
	}
	if ptr, ok := g.scope[decl.ID]; ok {
		return ptr, nil
	}
	if slot, ok := g.slots[decl.ID]; ok {
		return g.load("ptr", slot), nil
	}
	if info := g.cur.globals[decl.ID]; info != nil {
		return g.load("ptr", "@"+llvmSymbol(info.sym)), nil
	}
	return "", g.errf(decl.ID, "cannot translate node #%d: the function value is not in a slot", decl.ID)
}

// functionValueResult returns the will result text of a call through a function
// value. known tells an empty result (void) apart from a call that is not one.
func (g *generator) functionValueResult(r willc.Record) (string, bool) {
	target := refField(r, "to")
	decl, ok := g.recs[target]
	if !ok || !g.isFuncValue(decl) {
		return "", false
	}
	text := fieldText(decl, "type")
	if decl.Kind == "func" {
		text = g.lambdaTypeText(decl)
	}
	_, result, ok := funcTypeParts(text)
	if !ok {
		return "", false
	}
	return result, true
}

// indirectCall lowers a call through a function value. Scalar arguments are
// passed by value and aggregate arguments by borrow, the same rule the named
// functions follow.
func (g *generator) indirectCall(r willc.Record, decl willc.Record) (string, string, error) {
	text := fieldText(decl, "type")
	if decl.Kind == "func" {
		text = g.lambdaTypeText(decl)
	}
	params, result, ok := funcTypeParts(text)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not a function type", r.ID, text)
	}
	ptr, err := g.funcPointer(decl)
	if err != nil {
		return "", "", err
	}
	args := g.callArgs(r)
	if len(args) != len(params) {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the function value takes %d arguments", r.ID, len(params))
	}
	var sig []string
	for _, id := range args {
		typ, err := g.typeOf(id)
		if err != nil {
			return "", "", err
		}
		if isAggregateType(typ) {
			place, placeType, err := g.place(id)
			if err != nil {
				return "", "", err
			}
			if placeType != typ {
				return "", "", g.errf(id, "cannot translate node #%d: the argument needs %s", id, typ)
			}
			sig = append(sig, "ptr "+place)
			continue
		}
		value, err := g.value(id)
		if err != nil {
			return "", "", err
		}
		sig = append(sig, typ+" "+value)
	}
	retLLVM := ""
	if result != "" {
		if typ, ok := llvmTypeText(result); ok {
			retLLVM = typ
		} else if adt, ok := g.adtType(result); ok {
			retLLVM = adt.llvm
		} else {
			return "", "", g.errf(r.ID, "cannot translate node #%d: result type %s is not lowered yet", r.ID, result)
		}
	}
	call := "call void " + ptr
	ret := ""
	if retLLVM != "" {
		ret = g.newTmp()
		call = "  " + ret + " = call " + retLLVM + " " + ptr
	} else {
		call = "  call void " + ptr
	}
	fmt.Fprintf(g.out, "%s(%s)\n", call, strings.Join(sig, ", "))
	return ret, result, nil
}

// planLambdas turns every lambda record into a function of the document and
// refuses one that captures an outer local, which needs an environment.
func (g *generator) planLambdas() error {
	var lambdas []willc.Record
	for _, r := range g.recs {
		if r.Kind == "lambda" {
			lambdas = append(lambdas, r)
		}
	}
	sortByID(lambdas)
	for _, lam := range lambdas {
		if err := g.checkNoCaptures(lam); err != nil {
			return err
		}
		g.lambdaSeq++
		name := "lambda." + strconv.Itoa(g.lambdaSeq)
		lam.Kind = "func"
		found := false
		for i := range lam.Fields {
			if lam.Fields[i].Name == "text" {
				lam.Fields[i].Value = willc.Text(name)
				found = true
			}
		}
		if !found {
			lam.Fields = append(lam.Fields, willc.Field{Name: "text", Value: willc.Text(name)})
		}
		g.recs[lam.ID] = lam
		g.lambdaNames[lam.ID] = name
	}
	return nil
}

// checkNoCaptures reports the first name inside a lambda that refers to a
// declaration outside it and is not a module-level global or function.
func (g *generator) checkNoCaptures(lam willc.Record) error {
	inside := map[int]bool{lam.ID: true}
	changed := true
	for changed {
		changed = false
		for _, r := range g.recs {
			if inside[r.ID] {
				continue
			}
			if inside[refField(r, "parent")] {
				inside[r.ID] = true
				changed = true
			}
		}
	}
	for _, r := range g.recs {
		if !inside[r.ID] || r.Kind != "name" {
			continue
		}
		target := refField(r, "to")
		if target == 0 || inside[target] {
			continue
		}
		decl, ok := g.recs[target]
		if !ok {
			continue
		}
		if decl.Kind == "func" && refField(decl, "parent") == g.cur.moduleID {
			continue
		}
		if (decl.Kind == "var" || decl.Kind == "const") && refField(decl, "parent") == g.cur.moduleID {
			continue
		}
		if decl.Kind == "import" {
			continue
		}
		return g.errf(lam.ID, "cannot translate node #%d: a closure captures %s; closures with captures are not lowered yet", lam.ID, fieldText(r, "text"))
	}
	return nil
}
