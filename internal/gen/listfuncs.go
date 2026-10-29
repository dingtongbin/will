package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/gcmeta"
	"github.com/will-lang/will/internal/willc"
)

func (g *generator) listFuncValue(id int) (string, string, error) {
	base, typ, err := g.place(id)
	if err != nil {
		return "", "", err
	}
	if typ != "{ ptr, ptr }" {
		return "", "", g.errf(id, "cannot translate node #%d: the callback is not a function value", id)
	}
	codePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 0\n",
		codePtr, base)
	code := g.load("ptr", codePtr)
	envPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 1\n",
		envPtr, base)
	env := g.load("ptr", envPtr)
	return code, env, nil
}

// listCallBool calls one predicate callback and returns its i1 result.
func (g *generator) listCallBool(code, env, argLLVM, value string) string {
	out := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call i1 %s(ptr %s, %s %s)\n", out, code, env, argLLVM, value)
	return out
}

// listAny reports whether one element satisfies the callback.
func (g *generator) listAny(r willc.Record, elem, recvText string) (string, string, error) {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return "", "", err
	}
	defer done()
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list any takes one callback", r.ID)
	}
	code, env, err := g.listFuncValue(args[0])
	if err != nil {
		return "", "", err
	}
	length := g.load("i64", g.listField(object, payload, 1))
	dataField := g.listField(object, payload, 0)
	index := g.newTmp()
	g.tempAlloca(index, "i64")
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	g.labels++
	n := g.labels
	condLabel := fmt.Sprintf("list.any.%d", n)
	bodyLabel := fmt.Sprintf("list.anybody.%d", n)
	stepLabel := fmt.Sprintf("list.anystep.%d", n)
	trueLabel := fmt.Sprintf("list.anytrue.%d", n)
	falseLabel := fmt.Sprintf("list.anyfalse.%d", n)
	doneLabel := fmt.Sprintf("list.anydone.%d", n)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(condLabel + ":\n")
	current := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, bodyLabel, falseLabel)
	g.out.WriteString(bodyLabel + ":\n")
	data := g.loadContainerData(dataField)
	cell := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		cell, elemLLVM, data, current)
	value := g.load(elemLLVM, cell)
	hit := g.listCallBool(code, env, elemLLVM, value)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", hit, trueLabel, stepLabel)
	g.out.WriteString(stepLabel + ":\n")
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, current)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(trueLabel + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	g.out.WriteString(falseLabel + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	g.out.WriteString(doneLabel + ":\n")
	result := g.newTmp()
	fmt.Fprintf(g.out, "  %s = phi i1 [ 1, %%%s ], [ 0, %%%s ]\n", result, trueLabel, falseLabel)
	return result, "bool", nil
}

// listAll reports whether every element satisfies the callback.
func (g *generator) listAll(r willc.Record, elem, recvText string) (string, string, error) {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return "", "", err
	}
	defer done()
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list all takes one callback", r.ID)
	}
	code, env, err := g.listFuncValue(args[0])
	if err != nil {
		return "", "", err
	}
	length := g.load("i64", g.listField(object, payload, 1))
	dataField := g.listField(object, payload, 0)
	index := g.newTmp()
	g.tempAlloca(index, "i64")
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	g.labels++
	n := g.labels
	condLabel := fmt.Sprintf("list.all.%d", n)
	bodyLabel := fmt.Sprintf("list.allbody.%d", n)
	stepLabel := fmt.Sprintf("list.allstep.%d", n)
	falseLabel := fmt.Sprintf("list.allfalse.%d", n)
	trueLabel := fmt.Sprintf("list.alltrue.%d", n)
	doneLabel := fmt.Sprintf("list.alldone.%d", n)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(condLabel + ":\n")
	current := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, bodyLabel, trueLabel)
	g.out.WriteString(bodyLabel + ":\n")
	data := g.loadContainerData(dataField)
	cell := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		cell, elemLLVM, data, current)
	value := g.load(elemLLVM, cell)
	hit := g.listCallBool(code, env, elemLLVM, value)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", hit, stepLabel, falseLabel)
	g.out.WriteString(stepLabel + ":\n")
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, current)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(trueLabel + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	g.out.WriteString(falseLabel + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	g.out.WriteString(doneLabel + ":\n")
	result := g.newTmp()
	fmt.Fprintf(g.out, "  %s = phi i1 [ 1, %%%s ], [ 0, %%%s ]\n", result, trueLabel, falseLabel)
	return result, "bool", nil
}

// listFilter returns a new list with the elements that satisfy the callback.
func (g *generator) listFilter(r willc.Record, elem, recvText string) (string, string, error) {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return "", "", err
	}
	defer done()
	size, elemLLVM, descriptor, err := g.listElemLayout(r, elem)
	if err != nil {
		return "", "", err
	}
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list filter takes one callback", r.ID)
	}
	code, env, err := g.listFuncValue(args[0])
	if err != nil {
		return "", "", err
	}
	t, ok := g.classTypeOfText(recvText)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: no GC layout for %s", r.ID, recvText)
	}
	slot := g.allocSlot(g.newSlots, "list", r.ID, "ptr")
	result := g.newTmp()
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_alloc(i64 %d, ptr %s)\n",
		result, t.Size, gcmeta.DescriptorSymbol(t.Name))
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", result, slot)
	if root := g.gcRootSlots[r.ID]; root != "" {
		fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier(ptr %s, ptr %s)\n",
			root, result)
	}
	result = g.pinObjectValue(result)
	defer g.unpinContainer(result)
	length := g.load("i64", g.listField(object, payload, 1))
	srcDataField := g.listField(object, payload, 0)
	outDataField := g.listField(result, payload, 0)
	outLenField := g.listField(result, payload, 1)
	outCapField := g.listField(result, payload, 2)
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, length, size)
	outData := g.newTmp()
	g.gcAllocSequence(outData, bytes, descriptor)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", outData, outDataField)
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", outLenField)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, outCapField)
	index := g.newTmp()
	g.tempAlloca(index, "i64")
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	g.labels++
	n := g.labels
	condLabel := fmt.Sprintf("list.filter.%d", n)
	bodyLabel := fmt.Sprintf("list.filterbody.%d", n)
	stepLabel := fmt.Sprintf("list.filterstep.%d", n)
	pushLabel := fmt.Sprintf("list.filterpush.%d", n)
	doneLabel := fmt.Sprintf("list.filterdone.%d", n)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(condLabel + ":\n")
	current := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, bodyLabel, doneLabel)
	g.out.WriteString(bodyLabel + ":\n")
	data := g.loadContainerData(srcDataField)
	cell := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		cell, elemLLVM, data, current)
	value := g.load(elemLLVM, cell)
	hit := g.listCallBool(code, env, elemLLVM, value)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", hit, pushLabel, stepLabel)
	g.out.WriteString(pushLabel + ":\n")
	if err := g.listPushValue(result, payload, elem, value); err != nil {
		return "", "", err
	}
	fmt.Fprintf(g.out, "  br label %%%s\n", stepLabel)
	g.out.WriteString(stepLabel + ":\n")
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, current)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(doneLabel + ":\n")
	return g.load("ptr", slot), recvText, nil
}

// listFind returns Some(first matching element) or None.
func (g *generator) listFind(r willc.Record, elem, recvText string) (string, string, error) {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return "", "", err
	}
	defer done()
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list find takes one callback", r.ID)
	}
	code, env, err := g.listFuncValue(args[0])
	if err != nil {
		return "", "", err
	}
	optText := "Option<" + elem + ">"
	info, ok := g.adtType(optText)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: %s is not lowered yet", r.ID, optText)
	}
	slot := g.allocSlot(g.callSlots, "find", r.ID, info.llvm)
	tagPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0\n",
		tagPtr, info.llvm, slot)
	fmt.Fprintf(g.out, "  store i32 0, ptr %s\n", tagPtr)
	length := g.load("i64", g.listField(object, payload, 1))
	dataField := g.listField(object, payload, 0)
	index := g.newTmp()
	g.tempAlloca(index, "i64")
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	g.labels++
	n := g.labels
	condLabel := fmt.Sprintf("list.find.%d", n)
	bodyLabel := fmt.Sprintf("list.findbody.%d", n)
	stepLabel := fmt.Sprintf("list.findstep.%d", n)
	someLabel := fmt.Sprintf("list.findsome.%d", n)
	noneLabel := fmt.Sprintf("list.findnone.%d", n)
	doneLabel := fmt.Sprintf("list.finddone.%d", n)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(condLabel + ":\n")
	current := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, bodyLabel, noneLabel)
	g.out.WriteString(bodyLabel + ":\n")
	data := g.loadContainerData(dataField)
	cell := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		cell, elemLLVM, data, current)
	value := g.load(elemLLVM, cell)
	hit := g.listCallBool(code, env, elemLLVM, value)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", hit, someLabel, stepLabel)
	g.out.WriteString(stepLabel + ":\n")
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, current)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(someLabel + ":\n")
	if info.payloadIndex != 0 {
		payloadPtr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
			payloadPtr, info.llvm, slot, info.payloadIndex)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", elemLLVM, value, payloadPtr)
	}
	fmt.Fprintf(g.out, "  store i32 1, ptr %s\n", tagPtr)
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	g.out.WriteString(noneLabel + ":\n")
	fmt.Fprintf(g.out, "  br label %%%s\n", doneLabel)
	g.out.WriteString(doneLabel + ":\n")
	return g.load(info.llvm, slot), optText, nil
}

// listMap returns a new list of the callback results.
func (g *generator) listMap(r willc.Record, elem, recvText string) (string, string, error) {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return "", "", err
	}
	defer done()
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	args := g.callArgs(r)
	if len(args) != 1 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list map takes one callback", r.ID)
	}
	code, env, err := g.listFuncValue(args[0])
	if err != nil {
		return "", "", err
	}
	outElem := g.callbackResultText(args[0])
	if outElem == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the callback result type is not known", r.ID)
	}
	outText := "list[" + outElem + "]"
	outLLVM, ok := g.typeLLVMOf(outElem)
	if !ok || outLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, outElem)
	}
	t, ok := g.classTypeOfText(outText)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: no GC layout for %s", r.ID, outText)
	}
	outPayload, err := g.ensureClassPayload(t)
	if err != nil {
		return "", "", err
	}
	size, _, ok := g.sizeAlign(outLLVM)
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s has no layout", r.ID, outElem)
	}
	descriptor, err := g.sequenceDescriptor(outElem)
	if err != nil {
		return "", "", err
	}
	slot := g.allocSlot(g.newSlots, "list", r.ID, "ptr")
	result := g.newTmp()
	g.needGCDecl = true
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_gc_alloc(i64 %d, ptr %s)\n",
		result, t.Size, gcmeta.DescriptorSymbol(t.Name))
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", result, slot)
	if root := g.gcRootSlots[r.ID]; root != "" {
		fmt.Fprintf(g.out, "  call void @willrt_gc_write_barrier(ptr %s, ptr %s)\n",
			root, result)
	}
	result = g.pinObjectValue(result)
	defer g.unpinContainer(result)
	length := g.load("i64", g.listField(object, payload, 1))
	srcDataField := g.listField(object, payload, 0)
	bytes := g.newTmp()
	fmt.Fprintf(g.out, "  %s = mul i64 %s, %d\n", bytes, length, size)
	outData := g.newTmp()
	g.gcAllocSequence(outData, bytes, descriptor)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", outData, g.listField(result, outPayload, 0))
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", g.listField(result, outPayload, 1))
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", length, g.listField(result, outPayload, 2))
	index := g.newTmp()
	g.tempAlloca(index, "i64")
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	g.labels++
	n := g.labels
	condLabel := fmt.Sprintf("list.map.%d", n)
	bodyLabel := fmt.Sprintf("list.mapbody.%d", n)
	stepLabel := fmt.Sprintf("list.mapstep.%d", n)
	doneLabel := fmt.Sprintf("list.mapdone.%d", n)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(condLabel + ":\n")
	current := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, bodyLabel, doneLabel)
	g.out.WriteString(bodyLabel + ":\n")
	data := g.loadContainerData(srcDataField)
	cell := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		cell, elemLLVM, data, current)
	value := g.load(elemLLVM, cell)
	mapped := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call %s %s(ptr %s, %s %s)\n",
		mapped, outLLVM, code, env, elemLLVM, value)
	if err := g.listPushValue(result, outPayload, outElem, mapped); err != nil {
		return "", "", err
	}
	fmt.Fprintf(g.out, "  br label %%%s\n", stepLabel)
	g.out.WriteString(stepLabel + ":\n")
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, current)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(doneLabel + ":\n")
	return g.load("ptr", slot), outText, nil
}

// listFold folds the elements into one accumulator.
func (g *generator) listFold(r willc.Record, elem, recvText string) (string, string, error) {
	object, payload, done, err := g.listReceiver(r, recvText)
	if err != nil {
		return "", "", err
	}
	defer done()
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list element %s is not lowered yet", r.ID, elem)
	}
	args := g.callArgs(r)
	if len(args) != 2 {
		return "", "", g.errf(r.ID, "cannot translate node #%d: list fold takes an initial value and a callback", r.ID)
	}
	accText, err := g.willTypeText(args[0])
	if err != nil {
		return "", "", err
	}
	accLLVM, ok := g.typeLLVMOf(accText)
	if !ok || accLLVM == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: fold accumulator %s is not lowered yet", r.ID, accText)
	}
	init, err := g.value(args[0])
	if err != nil {
		return "", "", err
	}
	code, env, err := g.listFuncValue(args[1])
	if err != nil {
		return "", "", err
	}
	accSlot := g.newTmp()
	g.tempAlloca(accSlot, accLLVM)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", accLLVM, init, accSlot)
	length := g.load("i64", g.listField(object, payload, 1))
	dataField := g.listField(object, payload, 0)
	index := g.newTmp()
	g.tempAlloca(index, "i64")
	fmt.Fprintf(g.out, "  store i64 0, ptr %s\n", index)
	g.labels++
	n := g.labels
	condLabel := fmt.Sprintf("list.fold.%d", n)
	bodyLabel := fmt.Sprintf("list.foldbody.%d", n)
	doneLabel := fmt.Sprintf("list.folddone.%d", n)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(condLabel + ":\n")
	current := g.load("i64", index)
	more := g.newTmp()
	fmt.Fprintf(g.out, "  %s = icmp ult i64 %s, %s\n", more, current, length)
	fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", more, bodyLabel, doneLabel)
	g.out.WriteString(bodyLabel + ":\n")
	data := g.loadContainerData(dataField)
	cell := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i64 %s\n",
		cell, elemLLVM, data, current)
	value := g.load(elemLLVM, cell)
	acc := g.load(accLLVM, accSlot)
	nextAcc := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call %s %s(ptr %s, %s %s, %s %s)\n",
		nextAcc, accLLVM, code, env, accLLVM, acc, elemLLVM, value)
	fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", accLLVM, nextAcc, accSlot)
	next := g.newTmp()
	fmt.Fprintf(g.out, "  %s = add i64 %s, 1\n", next, current)
	fmt.Fprintf(g.out, "  store i64 %s, ptr %s\n", next, index)
	fmt.Fprintf(g.out, "  br label %%%s\n", condLabel)
	g.out.WriteString(doneLabel + ":\n")
	return g.load(accLLVM, accSlot), accText, nil
}

// callbackResultText returns the declared result type of one callback value.
func (g *generator) callbackResultText(id int) string {
	if r, ok := g.recs[id]; ok {
		if r.Kind == "lambda" || r.Kind == "func" {
			return fieldText(r, "type")
		}
	}
	if fnText, err := g.willTypeText(id); err == nil {
		if _, out, ok := funcTypeParts(fnText); ok {
			return out
		}
	}
	return ""
}
