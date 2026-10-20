package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// interpPart is one piece of an interpolation after its bytes and length have
// been evaluated. A scalar part writes through its formatter into the buffer.
type interpPart struct {
	text   bool
	data   string
	length string
	llvm   string
	value  string
	fn     string
}

// interpValue builds f"..." as one str: the lengths are measured first, one
// buffer is allocated, and every part is copied or formatted into it.
func (g *generator) interpValue(r willc.Record) (string, string, error) {
	var parts []interpPart
	total := "0"
	for _, f := range r.Fields {
		if f.Name != "part" {
			continue
		}
		part := interpPart{}
		if f.Value.Kind == willc.TextValue {
			part.text = true
			part.data = g.bytesGlobal([]byte(f.Value.Text))
			part.length = fmt.Sprint(len(f.Value.Text))
		} else if f.Value.Kind == willc.RefValue {
			id := f.Value.Ref
			text, err := g.willTypeText(id)
			if err != nil {
				return "", "", err
			}
			if text == "str" {
				data, length, err := g.strParts(id)
				if err != nil {
					return "", "", err
				}
				part.text = true
				part.data = data
				part.length = length
			} else {
				typ, signed, ok := llvmScalar(text)
				if !ok {
					return "", "", g.errf(id, "cannot translate node #%d: interpolation of %s is not lowered yet", id, text)
				}
				value, err := g.value(id)
				if err != nil {
					return "", "", err
				}
				kind, llvm, wide, err := g.formatKind(id, typ, signed, value)
				if err != nil {
					return "", "", err
				}
				g.needInterpDecl = true
				part.llvm = llvm
				part.value = wide
				part.fn = kind
				part.length = g.newTmp()
				fmt.Fprintf(g.out, "  %s = call i64 @willrt_fmt_%s_len(%s %s)\n", part.length, kind, llvm, wide)
			}
		}
		sum := g.newTmp()
		fmt.Fprintf(g.out, "  %s = add i64 %s, %s\n", sum, total, part.length)
		total = sum
		parts = append(parts, part)
	}

	g.labels++
	n := g.labels
	zero := fmt.Sprintf("interp.zero.%d", n)
	alloc := fmt.Sprintf("interp.alloc.%d", n)
	ready := fmt.Sprintf("interp.ready.%d", n)
	empty := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i64 %s, 0\n", empty, total)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", empty, zero, alloc)
	g.out.WriteString(alloc + ":\n")
	buffer := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", buffer, total)
	g.needAllocDecl = true
	fmt.Fprintf(g.out, "  br label %%%s\n", ready)
	g.out.WriteString(zero + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", ready)
	g.out.WriteString(ready + ":\n")
	data := g.newTmp()
	fmt.Fprintf(g.out, "  %s = phi ptr [ null, %%%s ], [ %s, %%%s ]\n", data, zero, buffer, alloc)

	offset := "0"
	g.needMemcpyDecl = true
	for _, part := range parts {
		dst := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds i8, ptr %s, i64 %s\n", dst, data, offset)
		if part.text {
			if part.length != "0" {
				fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %s, i64 %s)\n", dst, part.data, part.length)
			}
		} else {
			remaining := g.newTmp()
			fmt.Fprintf(g.out, "  %s = sub i64 %s, %s\n", remaining, total, offset)
			fmt.Fprintf(g.out, "  call i64 @willrt_fmt_%s_into(ptr %s, i64 %s, %s %s)\n", part.fn, dst, remaining, part.llvm, part.value)
		}
		next := g.newTmp()
		fmt.Fprintf(g.out, "  %s = add i64 %s, %s\n", next, offset, part.length)
		offset = next
	}

	slot := g.allocSlot(g.interpSlots, "interp", r.ID, "{ ptr, i64 }")
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", data, dataField)
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, slot)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", total, lenField)
	return slot, "{ ptr, i64 }", nil
}

// formatKind picks the runtime formatter of one scalar and widens it to the
// type the formatter takes.
func (g *generator) formatKind(id int, typ string, signed bool, value string) (string, string, string, error) {
	switch typ {
	case "i1":
		return "bool", "i32", g.zextTo(value, "i1", "i32"), nil
	case "double":
		return "f64", "double", value, nil
	case "float":
		wide := g.newTmp()
		fmt.Fprintf(g.out, "  %s = fpext float %s to double\n", wide, value)
		return "f64", "double", wide, nil
	case "ptr":
		return "", "", "", g.errf(id, "cannot translate node #%d: a pointer cannot be interpolated", id)
	default:
		if intWidth(typ) <= 0 {
			return "", "", "", g.errf(id, "cannot translate node #%d: %s cannot be interpolated", id, typ)
		}
		wide := g.toI64(typ, signed, value)
		if signed {
			return "i64", "i64", wide, nil
		}
		return "u64", "i64", wide, nil
	}
}

// zextTo widens one integer value.
func (g *generator) zextTo(value, from, to string) string {
	out := g.newTmp()
	fmt.Fprintf(g.out, "  %s = zext %s %s to %s\n", out, from, value, to)
	return out
}
