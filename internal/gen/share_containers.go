package gen

import "fmt"

// shareFixedArray promotes every rc handle inside an inline [N]T. The array
// itself has no buffer, so each element is visited in declaration order.
func (g *generator) shareFixedArray(ptr, elem string, n, depth int) error {
	if n == 0 || !g.needsShareText(elem) {
		return nil
	}
	elemLLVM, ok := g.typeLLVMOf(elem)
	if !ok || elemLLVM == "" {
		return fmt.Errorf("fixed array element %s is not lowered yet", elem)
	}
	typ := fmt.Sprintf("[%d x %s]", n, elemLLVM)
	for i := 0; i < n; i++ {
		at := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
			at, typ, ptr, i)
		if err := g.shareValueDepth(at, elem, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// shareEntries promotes the owning fields of every live Map or Set entry.
func (g *generator) shareEntries(ptr, k, v string, isMap bool, depth int) error {
	var entry string
	var fields []string
	if isMap {
		llvm, ok := g.mapEntryLLVM(k, v)
		if !ok {
			return fmt.Errorf("map entry is not lowered yet")
		}
		entry = llvm
		fields = []string{k, v}
	} else {
		llvm, ok := g.containerValueType(k)
		if !ok || llvm == "" {
			return fmt.Errorf("set element %s is not lowered yet", k)
		}
		entry = llvm
		fields = []string{k}
	}
	dataPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 0\n", dataPtr, ptr)
	lenPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, i64, i64 }, ptr %s, i32 0, i32 1\n", lenPtr, ptr)
	data := g.load("ptr", dataPtr)
	length := g.load("i64", lenPtr)
	return g.emitElementLoop("share.entries", data, entry, length, func(element string) error {
		for i, fieldText := range fields {
			if !g.needsShareText(fieldText) {
				continue
			}
			field := element
			if len(fields) > 1 {
				field = g.newTmp()
				fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
					field, entry, element, i)
			}
			if err := g.shareValueDepth(field, fieldText, depth+1); err != nil {
				return err
			}
		}
		return nil
	})
}

// shareADTValue promotes the payload of the active Option or Result variant.
func (g *generator) shareADTValue(ptr, kind string, args []string, depth int) error {
	info, ok := g.adtType(kindTypeText(kind, args))
	if !ok {
		return fmt.Errorf("adt %s is not lowered yet", kindTypeText(kind, args))
	}
	tag := g.load("i32", ptr)
	for _, branch := range adtBranches(kind, args) {
		if !g.needsShareText(branch.text) {
			continue
		}
		g.labels++
		n := g.labels
		keep := fmt.Sprintf("share.adt.%d.%d", n, branch.tag)
		done := fmt.Sprintf("share.adt.done.%d.%d", n, branch.tag)
		cond := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", cond, tag, branch.tag)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, keep, done)
		g.out.WriteString(keep + ":\n")
		payload := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
			payload, info.llvm, ptr, info.payloadIndex)
		if err := g.shareValueDepth(payload, branch.text, depth+1); err != nil {
			return err
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
		g.out.WriteString(done + ":\n")
	}
	return nil
}

// shareEnumValue promotes the owning payload of the active user enum variant.
func (g *generator) shareEnumValue(ptr string, info *enumInfo, depth int) error {
	if info == nil || info.payloadIndex == 0 || len(info.variants) == 0 {
		return nil
	}
	tag := g.load("i32", ptr)
	for i := range info.variants {
		v := &info.variants[i]
		if len(v.args) == 0 || len(v.argLLVM) != len(v.args) {
			continue
		}
		owning := false
		for _, arg := range v.args {
			if g.needsShareText(arg) {
				owning = true
				break
			}
		}
		if !owning {
			continue
		}
		g.labels++
		n := g.labels
		keep := fmt.Sprintf("share.enum.%d.%d", n, v.tag)
		done := fmt.Sprintf("share.enum.done.%d.%d", n, v.tag)
		cond := g.newTmp()
		fmt.Fprintf(g.out, "  %s = icmp eq i32 %s, %d\n", cond, tag, v.tag)
		fmt.Fprintf(g.out, "  br i1 %s, label %%%s, label %%%s\n", cond, keep, done)
		g.out.WriteString(keep + ":\n")
		payload := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
			payload, info.llvm, ptr, info.payloadIndex)
		for j, arg := range v.args {
			if !g.needsShareText(arg) {
				continue
			}
			at := payload
			if len(v.args) > 1 {
				at = g.newTmp()
				fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n",
					at, v.payload, payload, j)
			}
			if err := g.shareValueDepth(at, arg, depth+1); err != nil {
				return err
			}
		}
		fmt.Fprintf(g.out, "  br label %%%s\n", done)
		g.out.WriteString(done + ":\n")
	}
	return nil
}

// adtBranchTexts lists the payload type of every live Option or Result branch.
func adtBranchTexts(kind string, args []string) []string {
	if kind == "option" {
		return []string{args[0]}
	}
	if len(args) > 1 {
		return []string{args[0], args[1]}
	}
	return []string{args[0]}
}
