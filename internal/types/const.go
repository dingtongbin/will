package types

import (
	"strings"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

// intConst is an unsigned decimal magnitude with an overflow bit. All integer
// types are at most 64 bits wide, so a value past uint64 fits nothing.
type intConst struct {
	mag      uint64
	overflow bool
}

const maxUint64 = ^uint64(0)

func parseIntLiteral(text string) (intConst, string, bool) {
	body := strings.ReplaceAll(text, "_", "")
	base := uint64(10)
	i := 0
	if len(body) > 1 && body[0] == '0' {
		switch body[1] {
		case 'x':
			base = 16
			i = 2
		case 'b':
			base = 2
			i = 2
		case 'o':
			base = 8
			i = 2
		}
	}
	v := intConst{}
	digits := 0
	for i < len(body) {
		d := digitValue(body[i])
		if d < 0 || uint64(d) >= base {
			break
		}
		digits++
		if !v.overflow {
			if v.mag > (maxUint64-uint64(d))/base {
				v.overflow = true
			} else {
				v.mag = v.mag*base + uint64(d)
			}
		}
		i++
	}
	if digits == 0 {
		return intConst{}, "", false
	}
	suffix := body[i:]
	for j := 0; j < len(suffix); j++ {
		c := suffix[j]
		if j == 0 && !isLetter(c) {
			return intConst{}, "", false
		}
		if !isLetter(c) && (c < '0' || c > '9') {
			return intConst{}, "", false
		}
	}
	return v, suffix, true
}

func digitValue(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}

func isLetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// intWidth reports the width and signedness of an integer type name.
func intWidth(name string) (uint, bool, bool) {
	switch name {
	case "i8":
		return 8, true, true
	case "i16":
		return 16, true, true
	case "i32":
		return 32, true, true
	case "i64", "isize":
		return 64, true, true
	case "u8":
		return 8, false, true
	case "u16":
		return 16, false, true
	case "u32":
		return 32, false, true
	case "u64", "usize":
		return 64, false, true
	}
	return 0, false, false
}

func fitsInt(v intConst, name string) bool {
	bits, signed, ok := intWidth(name)
	if !ok {
		return true
	}
	if v.overflow {
		return false
	}
	if !signed {
		if bits == 64 {
			return true
		}
		return v.mag <= (uint64(1)<<bits)-1
	}
	if bits == 64 {
		return v.mag <= uint64(1)<<63-1
	}
	return v.mag <= (uint64(1)<<(bits-1))-1
}

// checkIntConstant reports T0005 when value does not fit want. An empty want
// means the untyped-constant default, i32.
func (c *checker) checkIntConstant(pos source.Pos, value intConst, text, want string) {
	name := want
	if name == "" {
		name = "i32"
	}
	if intTypeName(name) && fitsInt(value, name) {
		return
	}
	c.bag.Error(pos, diag.CodeConstantRange, "constant "+text+" is out of range for "+name,
		"write a suffix, or a smaller value")
}

func intTypeName(name string) bool {
	_, _, ok := intWidth(name)
	return ok
}
