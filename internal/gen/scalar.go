package gen

import (
	"strconv"
	"strings"
)

// llvmScalar maps a will scalar type to its LLVM type and its sign. LLVM has
// one integer type per width and no sign, so the sign is kept beside the type
// to pick the comparison predicate and the division, remainder, and shift
// instruction.
func llvmScalar(name string) (typ string, signed bool, ok bool) {
	switch name {
	case "i8":
		return "i8", true, true
	case "i16":
		return "i16", true, true
	case "i32":
		return "i32", true, true
	case "i64", "isize":
		return "i64", true, true
	case "u8":
		return "i8", false, true
	case "u16":
		return "i16", false, true
	case "u32":
		return "i32", false, true
	case "u64", "usize":
		return "i64", false, true
	case "f32":
		return "float", true, true
	case "f64":
		return "double", true, true
	case "bool":
		return "i1", false, true
	case "raw":
		return "ptr", false, true
	}
	return "", false, false
}

// isFloatType reports whether an LLVM type holds a floating-point value.
func isFloatType(typ string) bool {
	return typ == "float" || typ == "double"
}

// intSuffixes are the type suffixes an integer literal may carry.
var intSuffixes = []string{
	"i8", "i16", "i32", "i64", "isize",
	"u8", "u16", "u32", "u64", "usize",
}

// literalIntText returns an integer literal in a spelling LLVM accepts: the
// source text without its type suffix and digit separators.
func literalIntText(text string) string {
	for _, suffix := range intSuffixes {
		if !strings.HasSuffix(text, suffix) || len(text) == len(suffix) {
			continue
		}
		if value, ok := willIntDecimal(strings.TrimSuffix(text, suffix)); ok {
			return value
		}
	}
	if value, ok := willIntDecimal(text); ok {
		return value
	}
	return strings.ReplaceAll(text, "_", "")
}

// willIntDecimal renders an integer literal in the decimal spelling LLVM
// accepts. LLVM has no hexadecimal, binary, or octal integer constants.
func willIntDecimal(text string) (string, bool) {
	digits := strings.ReplaceAll(text, "_", "")
	base := 10
	body := digits
	switch {
	case strings.HasPrefix(digits, "0x") || strings.HasPrefix(digits, "0X"):
		base, body = 16, digits[2:]
	case strings.HasPrefix(digits, "0b") || strings.HasPrefix(digits, "0B"):
		base, body = 2, digits[2:]
	case strings.HasPrefix(digits, "0o") || strings.HasPrefix(digits, "0O"):
		base, body = 8, digits[2:]
	}
	if body == "" {
		return "", false
	}
	value, err := strconv.ParseUint(body, base, 64)
	if err != nil {
		return "", false
	}
	return strconv.FormatUint(value, 10), true
}

// literalFloatText returns a floating-point literal in a spelling LLVM
// accepts: the source text without its f32 or f64 suffix.
func literalFloatText(text string) string {
	text = strings.TrimSuffix(text, "f32")
	text = strings.TrimSuffix(text, "f64")
	return strings.ReplaceAll(text, "_", "")
}

// isUnsigned reports whether a will integer type is unsigned. The sign is not
// in the LLVM type, so every instruction that cares reads it from here.
func isUnsigned(name string) bool {
	switch name {
	case "u8", "u16", "u32", "u64", "usize":
		return true
	}
	return false
}

// compareInstr returns the instruction and predicate that compare two values of
// one type. Integers compare with icmp and the sign of the type; floats use
// fcmp and ordered predicates, so NaN is false for every comparison.
func compareInstr(typ string, signed bool, op string) (instr, predicate string) {
	if isFloatType(typ) {
		switch op {
		case "==":
			return "fcmp", "oeq"
		case "!=":
			return "fcmp", "une"
		case "<":
			return "fcmp", "olt"
		case "<=":
			return "fcmp", "ole"
		case ">":
			return "fcmp", "ogt"
		}
		return "fcmp", "oge"
	}
	switch op {
	case "==":
		return "icmp", "eq"
	case "!=":
		return "icmp", "ne"
	case "<":
		if signed {
			return "icmp", "slt"
		}
		return "icmp", "ult"
	case "<=":
		if signed {
			return "icmp", "sle"
		}
		return "icmp", "ule"
	case ">":
		if signed {
			return "icmp", "sgt"
		}
		return "icmp", "ugt"
	}
	if signed {
		return "icmp", "sge"
	}
	return "icmp", "uge"
}

// arithInstr maps one arithmetic operator to its instruction for a type.
func arithInstr(op, typ string, signed bool) string {
	float := isFloatType(typ)
	switch op {
	case "+":
		if float {
			return "fadd"
		}
		return "add"
	case "-":
		if float {
			return "fsub"
		}
		return "sub"
	case "*":
		if float {
			return "fmul"
		}
		return "mul"
	case "/":
		if float {
			return "fdiv"
		}
		if signed {
			return "sdiv"
		}
		return "udiv"
	case "%":
		if float {
			return "frem"
		}
		if signed {
			return "srem"
		}
		return "urem"
	case "&":
		return "and"
	case "|":
		return "or"
	case "^":
		return "xor"
	case "<<", "<<%":
		return "shl"
	case ">>", ">>%":
		if signed {
			return "ashr"
		}
		return "lshr"
	case "+%":
		if float {
			return "fadd"
		}
		return "add"
	case "-%":
		if float {
			return "fsub"
		}
		return "sub"
	case "*%":
		if float {
			return "fmul"
		}
		return "mul"
	}
	return ""
}
