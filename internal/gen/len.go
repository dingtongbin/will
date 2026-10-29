package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// lowerLen lowers the predeclared len(value). A fixed array has a compile-time
// length; every owning container and every view stores its live length in the
// second word of its header.
func (g *generator) lowerLen(r willc.Record) (string, error) {
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", g.errf(r.ID, "cannot translate node #%d: len takes one value", r.ID)
	}
	id := args[0]
	text, err := g.willTypeText(id)
	if err != nil {
		return "", err
	}
	text = stripPredeclaredPackage(text)
	if n, _, ok := arrayTypeText(text); ok {
		return strconv.Itoa(n), nil
	}
	if _, ok := listElemText(text); ok {
		object, err := g.classPointerValue(id)
		if err != nil {
			return "", err
		}
		payload, ok := g.listPayloadText(text)
		if !ok {
			return "", g.errf(r.ID, "cannot translate node #%d: no payload for %s", r.ID, text)
		}
		object = g.pinObjectValue(object)
		defer g.unpinContainer(object)
		return g.load("i64", g.listField(object, payload, 1)), nil
	}
	base, typ, err := g.place(id)
	if err != nil {
		return "", err
	}
	if text == "str" || text == "[]u8" || strings.HasPrefix(text, "[]") ||
		strings.HasPrefix(text, "mut []") || strings.HasPrefix(text, "Array<") ||
		strings.HasPrefix(text, "Map<") || strings.HasPrefix(text, "Set<") {
		g.tmp++
		field := "%t" + strconv.Itoa(g.tmp)
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 1\n", field, typ, base)
		return g.load("i64", field), nil
	}
	return "", g.errf(r.ID, "cannot translate node #%d: len of %s is not lowered", r.ID, text)
}
