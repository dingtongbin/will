package gen

import (
	"fmt"
	"math"
	"strconv"

	"github.com/will-lang/will/internal/willc"
)

// isPlaceExpr reports whether an expression names storage whose address can be
// taken. A literal or call result is a value, not a place.
func (g *generator) isPlaceExpr(id int) bool {
	r, ok := g.recs[id]
	if !ok {
		return false
	}
	switch r.Kind {
	case "name", "field", "index", "slice":
		return true
	}
	return false
}

// maxSigned holds the largest value of each signed width. The smallest value
// is in signedMins.
var maxSigned = map[int]string{
	8:  "127",
	16: "32767",
	32: "2147483647",
	64: "9223372036854775807",
}

// maxUnsigned holds the largest value of each unsigned width.
var maxUnsigned = map[int]string{
	8:  "255",
	16: "65535",
	32: "4294967295",
	64: "18446744073709551615",
}

// castValue lowers T(x). The conversion is checked: an integer that does not
// fit the target type, a NaN, and a float outside the target range panic. A
// conversion between floating-point types only changes precision.
func (g *generator) castValue(r willc.Record) (string, error) {
	exprID := refField(r, "expr")
	toRec, ok := g.recs[refField(r, "to")]
	if !ok {
		return "", g.errf(r.ID, "cannot translate node #%d: the target type is missing", r.ID)
	}
	toText := fieldText(toRec, "text")
	fromText, err := g.willTypeText(exprID)
	if err != nil {
		return "", err
	}
	if toText == "str" || toText == "[]u8" {
		return "", g.errf(r.ID, "cannot translate node #%d: a conversion to %s is a place", r.ID, toText)
	}
	if toText == "raw" && g.isPlaceExpr(exprID) {
		// A place always means "take its address". The integer form is only
		// for a value that is not a place, such as raw(0usize).
		ptr, _, err := g.place(exprID)
		if err != nil {
			return "", g.errf(r.ID, "cannot translate node #%d: raw() takes a place or a usize", r.ID)
		}
		return ptr, nil
	}
	if toText == "raw" && fromText != "usize" {
		if lit, ok := g.recs[exprID]; ok && lit.Kind == "lit" &&
			fieldText(lit, "kind") == "int" && fieldText(lit, "text") == "0" {
			return "null", nil
		}
		return "", g.errf(r.ID, "cannot translate node #%d: raw() takes a place or a usize", r.ID)
	}
	v, err := g.value(exprID)
	if err != nil {
		return "", err
	}
	if fromText == "raw" {
		if toText != "usize" && toText != "u64" {
			return "", g.errf(r.ID, "cannot translate node #%d: a raw value converts to usize only", r.ID)
		}
		out := g.newTmp()
		fmt.Fprintf(g.out, "  %s = ptrtoint ptr %s to i64\n", out, v)
		return out, nil
	}
	if toText == "raw" {
		if fromText != "usize" {
			return "", g.errf(r.ID, "cannot translate node #%d: only a usize converts to raw", r.ID)
		}
		out := g.newTmp()
		fmt.Fprintf(g.out, "  %s = inttoptr i64 %s to ptr\n", out, v)
		return out, nil
	}
	fromLLVM, fromSigned, ok := llvmScalar(fromText)
	if !ok {
		return "", g.errf(r.ID, "cannot translate node #%d: conversion from %s is not lowered yet", r.ID, fromText)
	}
	toLLVM, toSigned, ok := llvmScalar(toText)
	if !ok {
		return "", g.errf(r.ID, "cannot translate node #%d: conversion to %s is not lowered yet", r.ID, toText)
	}
	if fromLLVM == "i1" || toLLVM == "i1" {
		return "", g.errf(r.ID, "cannot translate node #%d: bool does not convert to or from a number", r.ID)
	}
	switch {
	case isFloatType(fromLLVM) && isFloatType(toLLVM):
		if fromLLVM == toLLVM {
			return v, nil
		}
		out := g.newTmp()
		instr := "fpext"
		if toLLVM == "float" {
			instr = "fptrunc"
		}
		fmt.Fprintf(g.out, "  %s = %s %s %s to %s\n", out, instr, fromLLVM, v, toLLVM)
		return out, nil
	case isFloatType(fromLLVM):
		return g.floatToInt(r, v, fromLLVM, toLLVM, toSigned)
	case isFloatType(toLLVM):
		out := g.newTmp()
		instr := "sitofp"
		if !fromSigned {
			instr = "uitofp"
		}
		fmt.Fprintf(g.out, "  %s = %s %s %s to %s\n", out, instr, fromLLVM, v, toLLVM)
		return out, nil
	}
	return g.intToInt(r, v, fromLLVM, fromSigned, toLLVM, toSigned)
}

// intToInt lowers an integer conversion and checks the value against the
// target type when the target range is not a superset of the source range.
func (g *generator) intToInt(r willc.Record, v, fromLLVM string, fromSigned bool, toLLVM string, toSigned bool) (string, error) {
	srcWidth := intWidth(fromLLVM)
	dstWidth := intWidth(toLLVM)
	if srcWidth < 8 || dstWidth < 8 {
		return "", g.errf(r.ID, "cannot translate node #%d: only integer widths of at least 8 are converted", r.ID)
	}
	if srcWidth == dstWidth && fromSigned == toSigned {
		return v, nil
	}
	check := func(predicate, bound string) {
		bad := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp %s %s %s, %s\n", bad, predicate, fromLLVM, v, bound)
		g.panicCheck(bad, panicConversion, r)
	}
	if fromSigned {
		if toSigned {
			if dstWidth < srcWidth {
				check("slt", signedMins[dstWidth])
				check("sgt", maxSigned[dstWidth])
			}
		} else {
			if dstWidth <= srcWidth {
				check("slt", "0")
				if dstWidth < srcWidth {
					check("sgt", maxUnsigned[dstWidth])
				}
			}
		}
	} else {
		if toSigned {
			if dstWidth <= srcWidth {
				check("ugt", maxSigned[dstWidth])
			}
		} else if dstWidth < srcWidth {
			check("ugt", maxUnsigned[dstWidth])
		}
	}
	if dstWidth == srcWidth {
		return v, nil
	}
	out := g.newTmp()
	instr := "trunc"
	if dstWidth > srcWidth {
		instr = "sext"
		if !fromSigned {
			instr = "zext"
		}
	}
	fmt.Fprintf(g.out, "  %s = %s %s %s to %s\n", out, instr, fromLLVM, v, toLLVM)
	return out, nil
}

// floatToInt lowers a checked float-to-integer conversion. The upper bound is
// the power of two just outside the target range, which is exactly
// representable in every floating-point width.
func (g *generator) floatToInt(r willc.Record, v, fromLLVM, toLLVM string, toSigned bool) (string, error) {
	width := intWidth(toLLVM)
	if width < 8 {
		return "", g.errf(r.ID, "cannot translate node #%d: %s is not an integer type", r.ID, toLLVM)
	}
	nan := g.newTmp()
	fmt.Fprintf(g.out, "  %s = fcmp uno %s %s, %s\n", nan, fromLLVM, v, v)
	g.panicCheck(nan, panicNaN, r)
	low, high := 0.0, math.Ldexp(1, width)
	if toSigned {
		low = -math.Ldexp(1, width-1)
		high = math.Ldexp(1, width-1)
	}
	if low != 0 {
		badLow := g.newTmp()
		fmt.Fprintf(g.out, "  %s = fcmp olt %s %s, %s\n", badLow, fromLLVM, v, floatText(low))
		g.panicCheck(badLow, panicConversion, r)
	} else {
		badLow := g.newTmp()
		fmt.Fprintf(g.out, "  %s = fcmp olt %s %s, 0.0\n", badLow, fromLLVM, v)
		g.panicCheck(badLow, panicConversion, r)
	}
	badHigh := g.newTmp()
	fmt.Fprintf(g.out, "  %s = fcmp oge %s %s, %s\n", badHigh, fromLLVM, v, floatText(high))
	g.panicCheck(badHigh, panicConversion, r)
	out := g.newTmp()
	instr := "fptosi"
	if !toSigned {
		instr = "fptoui"
	}
	fmt.Fprintf(g.out, "  %s = %s %s %s to %s\n", out, instr, fromLLVM, v, toLLVM)
	return out, nil
}

// floatText spells a power of two as an LLVM floating-point constant. One
// fractional digit keeps the value from being read as an integer.
func floatText(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}
