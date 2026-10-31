package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// castPlace lowers a conversion whose target is a str or a byte view. A byte
// view becomes a str only when every byte is valid UTF-8, and the bytes are
// copied so a later write through the view cannot change the str. A scalar
// becomes the shortest UTF-8 form of itself.
func (g *generator) castPlace(r willc.Record) (string, string, error) {
	toRec, ok := g.recs[refField(r, "to")]
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the target type is missing", r.ID)
	}
	toText := fieldText(toRec, "text")
	exprID := refField(r, "expr")
	fromText, err := g.willTypeText(exprID)
	if err != nil {
		return "", "", err
	}
	switch toText {
	case "[]u8":
		if fromText == "str" || fromText == "[]u8" {
			return g.place(exprID)
		}
	case "str":
		switch fromText {
		case "str":
			return g.place(exprID)
		case "[]u8":
			return g.bytesToStr(r, exprID)
		case "list[u8]":
			return g.arrayToStr(r, exprID)
		case "u32", "u8":
			return g.scalarToStr(r, exprID)
		case "bool", "f32", "f64", "i8", "i16", "i32", "i64", "isize", "u16", "u64", "usize":
			return g.displayToStr(r, exprID, fromText)
		}
	}
	return "", "", g.errf(r.ID, "cannot translate node %d: conversion from %s to %s is not lowered yet", r.ID, fromText, toText)
}

// displayToStr turns a scalar into its decimal, boolean, or floating text.
func (g *generator) displayToStr(r willc.Record, exprID int, fromText string) (string, string, error) {
	typ, signed, ok := llvmScalar(fromText)
	if !ok || typ == "ptr" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s has no text form", r.ID, fromText)
	}
	value, err := g.value(exprID)
	if err != nil {
		return "", "", err
	}
	kind, llvm, wide, err := g.formatKind(exprID, typ, signed, value)
	if err != nil {
		return "", "", err
	}
	g.needInterpDecl = true
	length := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i64 @willrt_fmt_%s_len(%s %s)\n", length, kind, llvm, wide)
	buffer := g.newTmp()
	if err := g.gcAllocText(buffer, length); err != nil {
		return "", "", err
	}
	fmt.Fprintf(g.out, "  call i64 @willrt_fmt_%s_into(ptr %s, i64 %s, %s %s)\n", kind, buffer, length, llvm, wide)
	return g.storeStr(r, buffer, length)
}

// bytesToStr validates the byte view and copies it into a str.
func (g *generator) bytesToStr(r willc.Record, exprID int) (string, string, error) {
	src, typ, err := g.place(exprID)
	if err != nil {
		return "", "", err
	}
	if typ != "{ ptr, i64 }" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the source needs { ptr, i64 }", r.ID)
	}
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, src)
	data := g.load("ptr", dataPtr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, src)
	length := g.load("i64", lenPtr)
	return g.copyBytesToStr(r, data, length)
}

// arrayToStr validates a byte list and copies it into a str.
func (g *generator) arrayToStr(r willc.Record, exprID int) (string, string, error) {
	src, typ, err := g.place(exprID)
	if err != nil {
		return "", "", err
	}
	if typ != "ptr" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the source needs list[u8]", r.ID)
	}
	t, ok := g.classTypeOfText("list[u8]")
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: no GC layout for list[u8]", r.ID)
	}
	payload, err := g.ensureClassPayload(t)
	if err != nil {
		return "", "", err
	}
	object := g.load("ptr", src)
	data := g.load("ptr", g.listField(object, payload, 0))
	length := g.load("i64", g.listField(object, payload, 1))
	return g.copyBytesToStr(r, data, length)
}

// copyBytesToStr checks that the bytes are valid UTF-8 and makes an owned copy.
func (g *generator) copyBytesToStr(r willc.Record, data, length string) (string, string, error) {
	g.needUtf8Decl = true
	valid := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_utf8_valid(ptr %s, i64 %s)\n", valid, data, length)
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", bad, valid)
	g.labels++
	n := g.labels
	badLabel := fmt.Sprintf("utf8.bad.%d", n)
	okLabel := fmt.Sprintf("utf8.ok.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", bad, badLabel, okLabel)
	g.out.WriteString(badLabel + ":\n")
	g.needPanicDecl = true
	message := g.globalString("invalid UTF-8")
	fmt.Fprintf(g.out, "  call void @willrt_panic_n(ptr %s, i64 13, ptr %s, ptr %s)\n",
		message, g.globalString(fieldText(r, "pos")), g.globalString(g.fnSymbol))
	g.out.WriteString("  unreachable\n")
	g.out.WriteString(okLabel + ":\n")
	dst := g.newTmp()
	if err := g.gcAllocText(dst, length); err != nil {
		return "", "", err
	}
	g.needMemcpyDecl = true
	fmt.Fprintf(g.out, "  call void @memcpy(ptr %s, ptr %s, i64 %s)\n", dst, data, length)
	return g.storeStr(r, dst, length)
}

// scalarToStr encodes one Unicode scalar into a fresh str.
func (g *generator) scalarToStr(r willc.Record, exprID int) (string, string, error) {
	value, err := g.value(exprID)
	if err != nil {
		return "", "", err
	}
	typ, err := g.typeOf(exprID)
	if err != nil {
		return "", "", err
	}
	wide := value
	if typ != "i32" {
		wide = g.newTmp()
		fmt.Fprintf(g.out, "  %s = zext %s %s to i32\n", wide, typ, value)
	}
	buffer := g.newTmp()
	if err := g.gcAllocText(buffer, "4"); err != nil {
		return "", "", err
	}
	g.needUtf8Decl = true
	size := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i32 @willrt_utf8_encode(i32 %s, ptr %s)\n", size, wide, buffer)
	bad := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, 0\n", bad, size)
	g.labels++
	n := g.labels
	badLabel := fmt.Sprintf("utf8.bad.%d", n)
	okLabel := fmt.Sprintf("utf8.ok.%d", n)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", bad, badLabel, okLabel)
	g.out.WriteString(badLabel + ":\n")
	g.needPanicDecl = true
	message := g.globalString("invalid UTF-8")
	fmt.Fprintf(g.out, "  call void @willrt_panic_n(ptr %s, i64 13, ptr %s, ptr %s)\n",
		message, g.globalString(fieldText(r, "pos")), g.globalString(g.fnSymbol))
	g.out.WriteString("  unreachable\n")
	g.out.WriteString(okLabel + ":\n")
	length := g.newTmp()
	fmt.Fprintf(g.out, "  %s = sext i32 %s to i64\n", length, size)
	return g.storeStr(r, buffer, length)
}

// storeStr writes a data pointer and a length into the cast's result slot.
func (g *generator) storeStr(r willc.Record, data, length string) (string, string, error) {
	slot := g.allocSlot(g.castSlots, "cast", r.ID, "{ ptr, i64 }")
	dataField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 0\n", dataField, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", data, dataField)
	lenField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64 }, ptr %s, i32 0, i32 1\n", lenField, slot)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, lenField)
	return slot, "{ ptr, i64 }", nil
}
