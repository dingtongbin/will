package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// isPrintCall reports whether a call names the predeclared print function.
func (g *generator) isPrintCall(r willc.Record) bool {
	if r.Kind != "call" {
		return false
	}
	callee, ok := g.recs[refField(r, "to")]
	return ok && callee.Kind == "name" && fieldText(callee, "text") == "print"
}

// printCall writes each argument to standard output. A str is written as its
// bytes; a scalar is formatted by the runtime, so no temporary str is built.
func (g *generator) printCall(r willc.Record) (string, string, error) {
	for _, arg := range refsOf(r, "arg") {
		text, err := g.willTypeText(arg)
		if err != nil {
			return "", "", err
		}
		if text == "str" {
			data, length, err := g.strParts(arg)
			if err != nil {
				return "", "", err
			}
			g.needPrintDecl = true
			fmt.Fprintf(g.out, "  call void @willrt_write_stdout(ptr %s, i64 %s)\n", data, length)
			continue
		}
		typ, signed, ok := llvmScalar(text)
		if !ok {
			return "", "", g.errf(arg, "cannot translate node #%d: print of %s is not lowered yet", arg, text)
		}
		value, err := g.value(arg)
		if err != nil {
			return "", "", err
		}
		g.needPrintDecl = true
		switch {
		case typ == "i1":
			wide := g.newTmp()
			fmt.Fprintf(g.out, "  %s = zext i1 %s to i32\n", wide, value)
			fmt.Fprintf(g.out, "  call void @willrt_print_bool(i32 %s)\n", wide)
		case typ == "double":
			fmt.Fprintf(g.out, "  call void @willrt_print_f64(double %s)\n", value)
		case typ == "float":
			wide := g.newTmp()
			fmt.Fprintf(g.out, "  %s = fpext float %s to double\n", wide, value)
			fmt.Fprintf(g.out, "  call void @willrt_print_f64(double %s)\n", wide)
		default:
			wide := g.toI64(typ, signed, value)
			fn := "willrt_print_i64"
			if !signed {
				fn = "willrt_print_u64"
			}
			fmt.Fprintf(g.out, "  call void @%s(i64 %s)\n", fn, wide)
		}
	}
	return "", "", nil
}

// toI64 widens or narrows an integer value to the i64 the print helper takes.
func (g *generator) toI64(typ string, signed bool, value string) string {
	width := intWidth(typ)
	if width == 64 {
		return value
	}
	out := g.newTmp()
	if width < 64 {
		op := "zext"
		if signed {
			op = "sext"
		}
		fmt.Fprintf(g.out, "  %s = %s %s %s to i64\n", out, op, typ, value)
		return out
	}
	fmt.Fprintf(g.out, "  %s = trunc %s %s to i64\n", out, typ, value)
	return out
}
