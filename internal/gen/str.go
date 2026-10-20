package gen

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/will-lang/will/internal/willc"
)

// bytesGlobal returns the symbol of the constant holding the bytes of one
// string or byte-string literal. Equal data shares one global.
func (g *generator) bytesGlobal(data []byte) string {
	key := string(data)
	if name, ok := g.byteGlobals[key]; ok {
		return name
	}
	g.byteCount++
	name := fmt.Sprintf("@.bytes.%d", g.byteCount)
	g.byteGlobals[key] = name
	if len(data) == 0 {
		g.strings = append(g.strings, fmt.Sprintf("%s = private unnamed_addr constant [0 x i8] zeroinitializer\n", name))
		return name
	}
	g.strings = append(g.strings, fmt.Sprintf("%s = private unnamed_addr constant [%d x i8] c\"%s\", align 1\n",
		name, len(data), llvmCString(string(data))))
	return name
}

// unescape turns the text of a string literal into its bytes. The lexer has
// already validated the escapes; this decodes the same forms the language
// allows so the literal is stored as data, not source.
func unescape(text string) []byte {
	var out []byte
	for i := 0; i < len(text); i++ {
		if text[i] != '\\' || i+1 >= len(text) {
			out = append(out, text[i])
			continue
		}
		i++
		switch text[i] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case '0':
			out = append(out, 0)
		case '\\':
			out = append(out, '\\')
		case '\'':
			out = append(out, '\'')
		case '"':
			out = append(out, '"')
		case 'x':
			if i+2 < len(text) {
				if b, err := strconv.ParseUint(text[i+1:i+3], 16, 8); err == nil {
					out = append(out, byte(b))
					i += 2
					continue
				}
			}
			out = append(out, 'x')
		case 'u':
			if i+1 < len(text) && text[i+1] == '{' {
				if end := strings.IndexByte(text[i+2:], '}'); end >= 0 {
					if v, err := strconv.ParseUint(text[i+2:i+2+end], 16, 32); err == nil {
						var buf [utf8.UTFMax]byte
						n := utf8.EncodeRune(buf[:], rune(v))
						out = append(out, buf[:n]...)
						i += 2 + end
						continue
					}
				}
			}
			out = append(out, 'u')
		default:
			out = append(out, text[i])
		}
	}
	return out
}

// stringPlace builds the two-word value of a str or bytes literal in the slot
// reserved for it.
func (g *generator) stringPlace(r willc.Record) (string, string, error) {
	slot := g.allocSlot(g.strSlots, "str", r.ID, "{ ptr, i64 }")
	data := unescape(fieldText(r, "text"))
	sym := g.bytesGlobal(data)
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", sym, dataField)
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, slot)
	fmt.Fprintf(g.out, "  store i64 %d, ptr %s\n", len(data), lenField)
	return slot, "{ ptr, i64 }", nil
}

// strEqual compares two str values by length and bytes. memcmp runs on the
// shorter length, and the lengths must also be equal, so a prefix of a longer
// string never compares equal.
func (g *generator) strEqual(lID, rID int, op string, r willc.Record) (string, error) {
	aData, aLen, err := g.strParts(lID)
	if err != nil {
		return "", err
	}
	bData, bLen, err := g.strParts(rID)
	if err != nil {
		return "", err
	}
	shorter := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", shorter, aLen, bLen)
	minLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = select i1 %s, i64 %s, i64 %s\n", minLen, shorter, aLen, bLen)
	g.needMemcmpDecl = true
	cmp := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @memcmp(ptr %s, ptr %s, i64 %s)\n", cmp, aData, bData, minLen)
	same := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", same, cmp)
	sameLen := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, %s\n", sameLen, aLen, bLen)
	equal := g.newTmp()
	fmt.Fprintf(g.out, "  %s = and i1 %s, %s\n", equal, same, sameLen)
	if op == "!=" {
		out := g.newTmp()
		fmt.Fprintf(g.out, "  %s = xor i1 %s, true\n", out, equal)
		return out, nil
	}
	return equal, nil
}

// strParts loads the data pointer and the length of a str value.
func (g *generator) strParts(id int) (string, string, error) {
	base, typ, err := g.place(id)
	if err != nil {
		return "", "", err
	}
	if typ != "{ ptr, i64 }" {
		return "", "", g.errf(id, "cannot translate node #%d: the value is not a str", id)
	}
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, base)
	data := g.load("ptr", dataField)
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, base)
	length := g.load("i64", lenField)
	return data, length, nil
}

// strIndex decodes the scalar at one byte offset of a str. The offset must be
// inside the string and the bytes there must be one valid UTF-8 scalar.
func (g *generator) strIndex(r willc.Record) (string, error) {
	data, length, err := g.strParts(refField(r, "recv"))
	if err != nil {
		return "", err
	}
	idx, err := g.indexValue(refField(r, "i"), r)
	if err != nil {
		return "", err
	}
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp uge i64 %s, %s\n", bad, idx, length)
	g.panicCheck(bad, panicIndex, r)
	addr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds i8, ptr %s, i64 %s\n", addr, data, idx)
	rem := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, %s\n", rem, length, idx)
	out := g.utf8Slots[r.ID]
	if out == "" {
		return "", g.errf(r.ID, "cannot translate node #%d: the decode has no slot", r.ID)
	}
	n := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_utf8_decode(ptr %s, i64 %s, ptr %s)\n", n, addr, rem, out)
	g.needUTF8Decl = true
	broken := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp sle i32 %s, 0\n", broken, n)
	g.panicCheck(broken, panicUTF8, r)
	return g.load("i32", out), nil
}

// forStr lowers a loop over a str. The index is a byte offset and the element
// binding receives one decoded scalar; the step advances by the scalar's byte
// length, so the loop never splits a scalar.
func (g *generator) forStr(r willc.Record, void bool, label int) (bool, error) {
	body, ok := g.recs[refField(r, "body")]
	if !ok {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop body is missing", r.ID)
	}
	names := bindNames(r)
	if len(names) < 1 || len(names) > 2 {
		return false, g.errf(r.ID, "cannot translate node #%d: a loop over a str binds one element or an offset and an element", r.ID)
	}
	binds := g.loopBinds[r.ID]
	idx := ""
	if binds != nil {
		idx = binds[hiddenIndex]
	}
	elemName := names[len(names)-1]
	elemSlot := ""
	if binds != nil {
		elemSlot = binds[elemName]
	}
	if idx == "" || elemSlot == "" {
		return false, g.errf(r.ID, "cannot translate node #%d: the loop bindings have no slots", r.ID)
	}
	out := g.utf8Slots[r.ID]
	advance := g.utf8nSlots[r.ID]
	if out == "" || advance == "" {
		return false, g.errf(r.ID, "cannot translate node #%d: the decode has no slot", r.ID)
	}
	data, length, err := g.strParts(refField(r, "iter"))
	if err != nil {
		return false, err
	}
	condLabel := fmt.Sprintf("for.cond.%d", label)
	bodyLabel := fmt.Sprintf("for.body.%d", label)
	stepLabel := fmt.Sprintf("for.step.%d", label)
	endLabel := fmt.Sprintf("for.end.%d", label)

	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", idx)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(condLabel + ":\n")
	current := g.load("i64", idx)
	cond := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", cond, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, bodyLabel, endLabel)
	g.out.WriteString(bodyLabel + ":\n")
	addr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds i8, ptr %s, i64 %s\n", addr, data, current)
	rem := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sub i64 %s, %s\n", rem, length, current)
	n := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_utf8_decode(ptr %s, i64 %s, ptr %s)\n", n, addr, rem, out)
	g.needUTF8Decl = true
	broken := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp sle i32 %s, 0\n", broken, n)
	g.panicCheck(broken, panicUTF8, r)
	n64 := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sext i32 %s to i64\n", n64, n)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", n64, advance)
	scalar := g.load("i32", out)
	fmt.Fprintf(g.out, "  store i32 %s, ptr %s\n", scalar, elemSlot)
	g.loops = append(g.loops, loopLabels{step: stepLabel, end: endLabel})
	done, err := g.body(body, void)
	g.loops = g.loops[:len(g.loops)-1]
	if err != nil {
		return false, err
	}
	if !done {
		fmt.Fprintf(g.out, "  br label %%%s\n", stepLabel)
	}
	g.out.WriteString(stepLabel + ":\n")
	at := g.load("i64", idx)
	step := g.load("i64", advance)
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, %s\n", next, at, step)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, idx)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(endLabel + ":\n")
	return false, nil
}

