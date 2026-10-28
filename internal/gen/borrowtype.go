package gen

import (
	"strings"
)

// referenceValue returns the pointer value of an expression whose type is ref T
// or mut T. It differs from value, which dereferences a scalar for arithmetic
// and copies.
func (g *generator) referenceValue(id int) (string, error) {
	r, ok := g.recs[id]
	if !ok {
		return "", g.errf(id, "cannot translate node #%d: no such node", id)
	}
	switch r.Kind {
	case "borrow":
		return g.value(id)
	case "name":
		decl := refField(r, "to")
		if d, ok := g.recs[decl]; ok && isSingleBorrow(fieldText(d, "type")) {
			if d.Kind == "param" || d.Kind == "receiver" {
				if name, ok := g.scope[decl]; ok {
					return name, nil
				}
			}
			if slot, ok := g.slots[decl]; ok {
				return g.load("ptr", slot), nil
			}
		}
	case "field":
		if text, err := g.willTypeText(id); err == nil {
			if _, kind := borrowText(text); kind == "ref" || kind == "mut" {
				ptr, _, err := g.place(id)
				if err != nil {
					return "", err
				}
				return g.load("ptr", ptr), nil
			}
		}
	case "index":
		if text, err := g.willTypeText(id); err == nil {
			if _, kind := borrowText(text); kind == "ref" || kind == "mut" {
				ptr, _, err := g.place(id)
				if err != nil {
					return "", err
				}
				return g.load("ptr", ptr), nil
			}
		}
	}
	return g.value(id)
}

// borrowText splits an explicit borrow prefix from a canonical type text.
// kind is "ref" or "mut" for ref T and mut T; mut []T is a mutable view and
// keeps the view layout, so it reports kind "mutview".
func borrowText(text string) (base, kind string) {
	if strings.HasPrefix(text, "ref ") {
		return strings.TrimPrefix(text, "ref "), "ref"
	}
	if strings.HasPrefix(text, "mut []") {
		return strings.TrimPrefix(text, "mut "), "mutview"
	}
	if strings.HasPrefix(text, "mut ") {
		return strings.TrimPrefix(text, "mut "), "mut"
	}
	return text, ""
}

func isMutText(text string) bool {
	_, kind := borrowText(text)
	return kind == "mut" || kind == "mutview"
}

func isSingleBorrow(text string) bool {
	_, kind := borrowText(text)
	return kind == "ref" || kind == "mut"
}

func baseBorrowText(text string) string {
	base, _ := borrowText(text)
	return base
}

func (g *generator) isReferenceExpr(id int) bool {
	r, ok := g.recs[id]
	if !ok {
		return false
	}
	switch r.Kind {
	case "borrow":
		return true
	case "name":
		if d, ok := g.recs[refField(r, "to")]; ok {
			_, kind := borrowText(fieldText(d, "type"))
			return kind == "ref" || kind == "mut"
		}
	case "field", "index":
		if text, err := g.willTypeText(id); err == nil {
			_, kind := borrowText(text)
			return kind == "ref" || kind == "mut"
		}
	}
	return false
}
