package gen

import (
	"fmt"
	"strconv"

	"github.com/will-lang/will/internal/willc"
)

// Messages for the checks the language inserts. The report's at line carries
// the position, so none of these interpolates a value.
const (
	panicOverflow    = "integer overflow"
	panicDivideByZero = "division by zero"
	panicShiftRange  = "shift amount out of range"
	panicIndex       = "index out of bounds"
	panicSlice       = "slice bounds out of range"
	panicConversion  = "conversion out of range"
	panicNaN         = "cannot convert NaN to integer"
	panicUTF8        = "invalid UTF-8"
)

// signedMins holds the smallest value of each signed width as LLVM text. The
// minimum of i64 cannot be written as a positive literal, so the text is
// spelled out instead of computed.
var signedMins = map[int]string{
	8:  "-128",
	16: "-32768",
	32: "-2147483648",
	64: "-9223372036854775808",
}

// intWidth returns the width of an LLVM integer type such as i32.
func intWidth(typ string) int {
	if len(typ) < 2 || typ[0] != 'i' {
		return 0
	}
	n, err := strconv.Atoi(typ[1:])
	if err != nil {
		return 0
	}
	return n
}

// llvmCString escapes text for an LLVM c"..." string. Only printable ASCII is
// written as itself; the position and symbol strings are ASCII source paths,
// but escaping every other byte keeps the global valid for any input.
func llvmCString(text string) string {
	out := make([]byte, 0, len(text))
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '"' || c == '\\':
			out = append(out, '\\', c)
		case c >= 0x20 && c <= 0x7e:
			out = append(out, c)
		default:
			out = append(out, '\\', "0123456789ABCDEF"[c>>4], "0123456789ABCDEF"[c&0xf])
		}
	}
	return string(out)
}

// globalString returns the symbol of a private constant holding text. Identical
// texts share one constant, so one message or one position is emitted once.
func (g *generator) globalString(text string) string {
	if name, ok := g.stringsByText[text]; ok {
		return name
	}
	name := fmt.Sprintf("@.str.%d", len(g.strings))
	g.stringsByText[text] = name
	g.strings = append(g.strings, fmt.Sprintf("%s = private unnamed_addr constant [%d x i8] c\"%s\\00\", align 1\n", name, len(text)+1, llvmCString(text)))
	return name
}

// panicCheck branches to a call to willrt_panic when cond is true. The call
// carries the fixed message, the source position of the checked node, and the
// symbol of the function being lowered, so the report needs no stack walk.
func (g *generator) panicCheck(cond, message string, r willc.Record) {
	g.labels++
	n := g.labels
	bad := fmt.Sprintf("panic.%d", n)
	ok := fmt.Sprintf("panic.ok.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, bad, ok)
	g.out.WriteString(bad + ":\n")
	fmt.Fprintf(g.out, "  call void @willrt_panic(ptr %s, ptr %s, ptr %s)\n",
		g.globalString(message),
		g.globalString(fieldText(r, "pos")),
		g.globalString(g.fnSymbol))
	g.out.WriteString("  unreachable\n")
	g.out.WriteString(ok + ":\n")
	g.needPanicDecl = true
}

// checkedArith lowers an integer arithmetic operator with the check the
// semantics volume requires. Floating-point and bitwise operators are lowered
// by arithInstr instead.
func (g *generator) checkedArith(op, typ string, signed bool, l, right string, rightID int, r willc.Record) (string, error) {
	width := intWidth(typ)
	if width < 8 {
		return "", g.errf(r.ID, "cannot translate node #%d: %s is not an integer type", r.ID, typ)
	}
	switch op {
	case "+", "-", "*":
		names := map[string]string{"+": "add", "-": "sub", "*": "mul"}
		prefix := "u"
		if signed {
			prefix = "s"
		}
		pair := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call {%s, i1} @llvm.%s%s.with.overflow.%s(%s %s, %s %s)\n",
			pair, typ, prefix, names[op], typ, typ, l, typ, right)
		value := g.newTmp()
		fmt.Fprintf(g.out, "  %s = extractvalue {%s, i1} %s, 0\n", value, typ, pair)
		overflow := g.newTmp()
		fmt.Fprintf(g.out, "  %s = extractvalue {%s, i1} %s, 1\n", overflow, typ, pair)
		g.panicCheck(overflow, panicOverflow, r)
		return value, nil
	case "/", "%":
		zero := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq %s %s, 0\n", zero, typ, right)
		g.panicCheck(zero, panicDivideByZero, r)
		if signed {
			min := g.newTmp()
			fmt.Fprintf(g.out, "  %s = icmp eq %s %s, %s\n", min, typ, l, signedMins[width])
			negOne := g.newTmp()
			fmt.Fprintf(g.out, "  %s = icmp eq %s %s, -1\n", negOne, typ, right)
			both := g.newTmp()
			fmt.Fprintf(g.out, "  %s = and i1 %s, %s\n", both, min, negOne)
			g.panicCheck(both, panicOverflow, r)
		}
		instr := "udiv"
		if op == "%" {
			instr = "urem"
		}
		if signed {
			instr = "sdiv"
			if op == "%" {
				instr = "srem"
			}
		}
		value := g.newTmp()
		fmt.Fprintf(g.out, "  %s = %s %s %s, %s\n", value, instr, typ, l, right)
		return value, nil
	case "<<", ">>":
		instr := "shl"
		if op == ">>" {
			instr = "lshr"
			if signed {
				instr = "ashr"
			}
		}
		if !g.shiftAmountFits(rightID, width) {
			bad := g.newTmp()
			fmt.Fprintf(g.out, "  %s = icmp uge %s %s, %d\n", bad, typ, right, width)
			g.panicCheck(bad, panicShiftRange, r)
		}
		value := g.newTmp()
		fmt.Fprintf(g.out, "  %s = %s %s %s, %s\n", value, instr, typ, l, right)
		return value, nil
	}
	return "", g.errf(r.ID, "cannot translate node #%d: operator %s is not checked arithmetic", r.ID, op)
}

// shiftAmountFits reports whether a shift count is a literal that is smaller
// than the width. A constant count out of range is a front-end error, so only
// a runtime count needs the check.
func (g *generator) shiftAmountFits(id, width int) bool {
	r, ok := g.recs[id]
	if !ok || r.Kind != "lit" || fieldText(r, "kind") != "int" {
		return false
	}
	n, err := strconv.ParseUint(literalIntText(fieldText(r, "text")), 0, 64)
	if err != nil {
		return false
	}
	return n < uint64(width)
}

// isCheckedArith reports whether an integer operator has a check the language
// inserts. The truncating shift `<<%` is excluded because it is the explicit
// way to drop bits.
func isCheckedArith(op string) bool {
	switch op {
	case "+", "-", "*", "/", "%", "<<", ">>":
		return true
	}
	return false
}

// newTmp reserves the next temporary name of the current function.
func (g *generator) newTmp() string {
	g.tmp++
	return "%t" + strconv.Itoa(g.tmp)
}
