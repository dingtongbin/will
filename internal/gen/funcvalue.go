package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/willc"
)

// captureInfo is one outer declaration a lambda reads. The environment stores
// the value itself, so a closure keeps working after its creating frame ends.
type captureInfo struct {
	decl  int
	name  string
	typ   string
	text  string
	local bool
	param bool
}

// lambdaInfo is the lowered shape of one lambda: its function symbol, the
// declarations it captures, and the literal struct type of its environment.
type lambdaInfo struct {
	name     string
	captures []captureInfo
	envType  string
}

// thunkRequest is a named function that has to be wrapped in the closure
// calling convention because a program used it as a value.
type thunkRequest struct {
	decl int
	sym  string
}

// funcTypeParts splits func(A,B)->R into its parameter texts and its result.
// An empty result means the function value returns nothing.
func funcTypeParts(text string) ([]string, string, bool) {
	if !strings.HasPrefix(text, "func(") {
		return nil, "", false
	}
	depth := 0
	close := -1
	for i := len("func"); i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				close = i
			}
		}
		if close >= 0 {
			break
		}
	}
	if close < 0 {
		return nil, "", false
	}
	var params []string
	if inner := strings.TrimSpace(text[len("func("):close]); inner != "" {
		params = splitTypeList(inner)
	}
	result := ""
	if rest := strings.TrimPrefix(text[close+1:], "->"); rest != "" && rest != "()" {
		result = rest
	}
	return params, result, true
}

// lambdaTypeText reconstructs the function type of a lambda record after it was
// turned into a function.
func (g *generator) lambdaTypeText(fn willc.Record) string {
	var params []willc.Record
	result := ""
	for _, r := range g.recs {
		if refField(r, "parent") != fn.ID {
			continue
		}
		switch r.Kind {
		case "param":
			params = append(params, r)
		case "type":
			result = fieldText(r, "text")
		}
	}
	sortByID(params)
	parts := make([]string, 0, len(params))
	for _, p := range params {
		parts = append(parts, fieldText(p, "type"))
	}
	text := "func(" + strings.Join(parts, ",") + ")"
	if result != "" {
		text += "->" + result
	}
	return text
}

// isFuncValue reports whether a call target is a value holding a closure
// rather than a named function of the module.
func (g *generator) isFuncValue(decl willc.Record) bool {
	switch decl.Kind {
	case "var", "param":
		return strings.HasPrefix(fieldText(decl, "type"), "func(")
	case "func":
		return g.lambdaNames[decl.ID] != ""
	}
	return false
}

// functionValueType returns the will function type text of a value target.
func (g *generator) functionValueType(decl willc.Record) string {
	if decl.Kind == "func" {
		return g.lambdaTypeText(decl)
	}
	return fieldText(decl, "type")
}

// closureCode returns the code and environment pointers a closure value holds.
func (g *generator) closureCode(decl willc.Record) (string, string, error) {
	var slot, typ string
	if snap, ok := g.deferSnapshots[decl.ID]; ok {
		slot = g.deferFieldPtr(snap)
		typ = snap.llvm
	} else {
		switch {
		case decl.Kind == "func":
			var err error
			slot, typ, err = g.closurePlace(decl)
			if err != nil {
				return "", "", err
			}
		case g.slots[decl.ID] != "":
			slot, typ = g.slots[decl.ID], g.slotTypes[decl.ID]
		case g.captureFields[decl.ID] != "":
			slot, typ = g.captureFields[decl.ID], g.captureTypeOf(decl.ID)
		case g.scope[decl.ID] != "" && g.borrows[decl.ID]:
			slot = g.scope[decl.ID]
			var err error
			typ, err = g.declLLVMType(decl.ID)
			if err != nil {
				return "", "", err
			}
		case g.cur.globals[decl.ID] != nil:
			info := g.cur.globals[decl.ID]
			slot, typ = "@"+llvmSymbol(info.sym), info.typ
		default:
			return "", "", g.errf(decl.ID, "cannot translate node #%d: the closure value is not in a slot", decl.ID)
		}
	}
	if typ != "{ ptr, ptr }" {
		return "", "", g.errf(decl.ID, "cannot translate node #%d: the closure value has type %s", decl.ID, typ)
	}
	codePtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 0\n", codePtr, slot)
	envPtr := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 1\n", envPtr, slot)
	code := g.newTmp()
	fmt.Fprintf(g.out, "  %s = load ptr, ptr %s\n", code, codePtr)
	env := g.newTmp()
	fmt.Fprintf(g.out, "  %s = load ptr, ptr %s\n", env, envPtr)
	return code, env, nil
}

// captureTypeOf returns the LLVM type of a captured declaration while its
// lambda is being lowered.
func (g *generator) captureTypeOf(decl int) string {
	info := g.lambdas[g.currentLambda]
	if info == nil {
		return ""
	}
	for _, cap := range info.captures {
		if cap.decl == decl {
			return cap.typ
		}
	}
	return ""
}

// functionValueResult returns the will result text of a call through a function
// value. known tells an empty result (void) apart from a call that is not one.
func (g *generator) functionValueResult(r willc.Record) (string, bool) {
	target := refField(r, "to")
	decl, ok := g.recs[target]
	if !ok || !g.isFuncValue(decl) {
		return "", false
	}
	_, result, ok := funcTypeParts(g.functionValueType(decl))
	if !ok {
		return "", false
	}
	return result, true
}

// indirectCall lowers a call through a closure. The call always passes the
// environment first; a closure without captures passes a null environment.
func (g *generator) indirectCall(r willc.Record, decl willc.Record) (string, string, error) {
	params, result, ok := funcTypeParts(g.functionValueType(decl))
	if !ok {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the callee is not a function value", r.ID)
	}
	code, env, err := g.closureCode(decl)
	if err != nil {
		return "", "", err
	}
	args := g.callArgs(r)
	if len(args) != len(params) {
		return "", "", g.errf(r.ID, "cannot translate node #%d: the function value takes %d arguments", r.ID, len(params))
	}
	sig := []string{"ptr " + env}
	for i, id := range args {
		sig, err = g.callArgParts(sig, r, i, id, params[i], false, false)
		if err != nil {
			return "", "", err
		}
	}
	retLLVM := ""
	if result != "" {
		retLLVM = g.resultLLVM(result)
		if retLLVM == "" {
			return "", "", g.errf(r.ID, "cannot translate node #%d: result type %s is not lowered yet", r.ID, result)
		}
	}
	if retLLVM == "" {
		fmt.Fprintf(g.out, "  call void %s(%s)\n", code, strings.Join(sig, ", "))
		return "", "", nil
	}
	ret := g.newTmp()
	fmt.Fprintf(g.out, "  %s = call %s %s(%s)\n", ret, retLLVM, code, strings.Join(sig, ", "))
	return ret, result, nil
}

// planLambdas turns every lambda record into a function of the document and
// records what it captures. A capture is stored by value in a heap
// environment, so a closure that escapes its creating frame still reads it.
func (g *generator) planLambdas() error {
	var lambdas []willc.Record
	for _, r := range g.recs {
		if r.Kind == "lambda" {
			lambdas = append(lambdas, r)
		}
	}
	sortByID(lambdas)
	infos := make(map[int]*lambdaInfo, len(lambdas))
	for _, lam := range lambdas {
		info, err := g.lambdaCaptureInfo(lam)
		if err != nil {
			return err
		}
		infos[lam.ID] = info
	}
	for _, lam := range lambdas {
		info := infos[lam.ID]
		g.lambdaSeq++
		info.name = "lambda." + strconv.Itoa(g.lambdaSeq)
		lam.Kind = "func"
		found := false
		for i := range lam.Fields {
			if lam.Fields[i].Name == "text" {
				lam.Fields[i].Value = willc.Text(info.name)
				found = true
			}
		}
		if !found {
			lam.Fields = append(lam.Fields, willc.Field{Name: "text", Value: willc.Text(info.name)})
		}
		g.recs[lam.ID] = lam
		g.lambdaNames[lam.ID] = info.name
		g.lambdas[lam.ID] = info
	}
	return nil
}

// lambdaCaptureInfo collects the declarations one lambda reads from outside its
// body. A module-level global or a function is named directly, not captured; a
// constant is substituted; everything else becomes an environment field.
func (g *generator) lambdaCaptureInfo(lam willc.Record) (*lambdaInfo, error) {
	inside := map[int]bool{lam.ID: true}
	changed := true
	for changed {
		changed = false
		for _, r := range g.recs {
			if inside[r.ID] {
				continue
			}
			if inside[refField(r, "parent")] {
				inside[r.ID] = true
				changed = true
			}
		}
	}
	var names []willc.Record
	for _, r := range g.recs {
		if inside[r.ID] && r.Kind == "name" {
			names = append(names, r)
		}
	}
	sortByID(names)
	info := &lambdaInfo{}
	seen := map[string]bool{}
	for _, r := range names {
		target := refField(r, "to")
		if target == 0 || inside[target] {
			continue
		}
		decl, ok := g.recs[target]
		if !ok {
			continue
		}
		if decl.Kind == "const" || decl.Kind == "func" || decl.Kind == "import" {
			continue
		}
		if (decl.Kind == "var" || decl.Kind == "const") && refField(decl, "parent") == g.cur.moduleID {
			continue
		}
		if decl.Kind != "var" && decl.Kind != "param" && decl.Kind != "receiver" {
			return nil, g.errf(lam.ID, "cannot translate node #%d: capturing %s is not lowered yet", lam.ID, fieldText(r, "text"))
		}
		key := strconv.Itoa(decl.ID)
		if seen[key] {
			continue
		}
		typ, err := g.declLLVMType(decl.ID)
		if err != nil {
			return nil, err
		}
		seen[key] = true
		text := fieldText(decl, "type")
		if decl.Kind == "receiver" {
			text = fieldText(decl, "type")
		}
		info.captures = append(info.captures, captureInfo{
			decl:  decl.ID,
			name:  fieldText(r, "text"),
			typ:   typ,
			text:  text,
			local: decl.Kind == "var" && g.slots[decl.ID] != "",
			param: decl.Kind == "param" || decl.Kind == "receiver",
		})
	}
	if len(info.captures) != 0 {
		types := make([]string, 0, len(info.captures))
		for _, cap := range info.captures {
			types = append(types, cap.typ)
		}
		info.envType = "{ " + strings.Join(types, ", ") + " }"
	}
	return info, nil
}

// closurePlace builds one closure value: the code pointer of the lambda and a
// heap environment holding every captured value.
func (g *generator) closurePlace(lam willc.Record) (string, string, error) {
	info := g.lambdas[lam.ID]
	if info == nil {
		return "", "", g.errf(lam.ID, "cannot translate node #%d: the lambda has no plan", lam.ID)
	}
	slot := g.allocSlot(g.closureSlots, "closure", lam.ID, "{ ptr, ptr }")
	env := "null"
	if len(info.captures) != 0 {
		var err error
		env, err = g.buildEnvironment(lam, info)
		if err != nil {
			return "", "", err
		}
	}
	codeField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 0\n", codeField, slot)
	fmt.Fprintf(g.out, "  store ptr @%s, ptr %s\n", llvmSymbol(g.cur.funcs[lam.ID]), codeField)
	envField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 1\n", envField, slot)
	fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", env, envField)
	return slot, "{ ptr, ptr }", nil
}

// buildEnvironment allocates one environment and stores every captured value.
// The size comes from a pointer difference, so a 32-bit target gets the right
// layout without the generator knowing the data layout.
func (g *generator) buildEnvironment(lam willc.Record, info *lambdaInfo) (string, error) {
	end := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr %s, ptr null, i32 1\n", end, info.envType)
	size := g.newTmp()
	fmt.Fprintf(g.out, "  %s = ptrtoint ptr %s to i64\n", size, end)
	env := g.newTmp()
	g.needAllocDecl = true
	fmt.Fprintf(g.out, "  %s = call ptr @willrt_alloc(i64 %s)\n", env, size)
	for i, cap := range info.captures {
		field := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d\n", field, info.envType, env, i)
		src, typ, err := g.captureSource(cap)
		if err != nil {
			return "", err
		}
		if typ != cap.typ {
			return "", g.errf(lam.ID, "cannot translate node #%d: capture %s has type %s, want %s", lam.ID, cap.name, typ, cap.typ)
		}
		if ownsReleaseText(g, cap.text) {
			// The environment can outlive the creating frame, so it owns an
			// independent value. Moving the source here would break a second
			// closure that captures the same name.
			if err := g.cloneOwnedValue(src, field, cap.text); err != nil {
				return "", err
			}
			continue
		}
		value := g.load(typ, src)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", typ, value, field)
	}
	return env, nil
}

// captureSource returns the storage a captured value is copied from in the
// frame that creates the closure.
func (g *generator) captureSource(cap captureInfo) (string, string, error) {
	if ptr := g.captureFields[cap.decl]; ptr != "" {
		return ptr, cap.typ, nil
	}
	if slot := g.slots[cap.decl]; slot != "" {
		return slot, g.slotTypes[cap.decl], nil
	}
	if binds, ok := g.loopBinds[cap.decl]; ok {
		slot, ok := binds[cap.name]
		if !ok {
			return "", "", g.errf(cap.decl, "cannot translate node #%d: the loop binding has no slot", cap.decl)
		}
		return slot, cap.typ, nil
	}
	if value, ok := g.scope[cap.decl]; ok {
		if g.borrows[cap.decl] {
			return value, cap.typ, nil
		}
		slot := g.captureSpill(cap.decl, cap.typ)
		fmt.Fprintf(g.out, "  store %s %s, ptr %s\n", cap.typ, value, slot)
		return slot, cap.typ, nil
	}
	if info := g.cur.globals[cap.decl]; info != nil {
		return "@" + llvmSymbol(info.sym), info.typ, nil
	}
	return "", "", g.errf(cap.decl, "cannot translate node #%d: the captured value is not in a slot", cap.decl)
}

// captureSpill allocates one stack slot for a scalar parameter that a closure
// captures. The slot is reused by every closure made in the frame.
func (g *generator) captureSpill(decl int, typ string) string {
	if slot := g.captureSpills[decl]; slot != "" {
		return slot
	}
	slot := "%v.capture." + strconv.Itoa(decl)
	g.captureSpills[decl] = slot
	fmt.Fprintf(g.out, "  %s = alloca %s\n", slot, typ)
	return slot
}

// setupCaptures points every captured declaration at its environment field, so
// reads and writes of the captured name use the heap environment.
func (g *generator) setupCaptures(fn int) error {
	info := g.lambdas[fn]
	if info == nil || len(info.captures) == 0 {
		return nil
	}
	for i, cap := range info.captures {
		ptr := g.newTmp()
		fmt.Fprintf(g.out, "  %s = getelementptr inbounds %s, ptr %%closure.env, i32 0, i32 %d\n", ptr, info.envType, i)
		g.captureFields[cap.decl] = ptr
	}
	return nil
}

// placeNamedFunction builds a closure for a named function. The function has no
// environment, so the code pointer is a wrapper that follows the closure
// calling convention and forwards every argument.
func (g *generator) placeNamedFunction(r willc.Record, decl willc.Record) (string, string, error) {
	if g.receiverOf(decl.ID) != nil {
		return "", "", g.errf(r.ID, "cannot translate node #%d: a method used as a value is not lowered yet", r.ID)
	}
	sym := g.cur.funcs[decl.ID]
	if sym == "" {
		return "", "", g.errf(r.ID, "cannot translate node #%d: a function of another document used as a value is not lowered yet", r.ID)
	}
	for _, mut := range g.paramMut[sym] {
		if mut {
			return "", "", g.errf(r.ID, "cannot translate node #%d: a function with mutable parameters has no function value yet", r.ID)
		}
	}
	thunk := qualify(g.pkg, "thunk."+fieldText(decl, "text"))
	if !g.thunkEmitted[thunk] {
		g.thunkEmitted[thunk] = true
		g.pendingThunks = append(g.pendingThunks, thunkRequest{decl: decl.ID, sym: thunk})
	}
	slot := g.allocSlot(g.closureSlots, "closure", r.ID, "{ ptr, ptr }")
	codeField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 0\n", codeField, slot)
	fmt.Fprintf(g.out, "  store ptr @%s, ptr %s\n", llvmSymbol(thunk), codeField)
	envField := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr inbounds { ptr, ptr }, ptr %s, i32 0, i32 1\n", envField, slot)
	fmt.Fprintf(g.out, "  store ptr null, ptr %s\n", envField)
	return slot, "{ ptr, ptr }", nil
}

// emitThunks writes the wrappers requested while lowering this document.
func (g *generator) emitThunks() error {
	for _, req := range g.pendingThunks {
		if err := g.emitThunk(req); err != nil {
			return err
		}
	}
	g.pendingThunks = nil
	return nil
}

// emitThunk writes one wrapper with the hidden environment parameter.
func (g *generator) emitThunk(req thunkRequest) error {
	decl, ok := g.recs[req.decl]
	if !ok {
		return g.errf(req.decl, "cannot translate node #%d: the wrapped function is missing", req.decl)
	}
	var params []willc.Record
	result := ""
	for _, r := range g.recs {
		if refField(r, "parent") != decl.ID {
			continue
		}
		switch r.Kind {
		case "param":
			params = append(params, r)
		case "type":
			result = fieldText(r, "text")
		}
	}
	sortByID(params)
	sig := []string{"ptr %closure.env"}
	var args []string
	for _, p := range params {
		typ, err := g.declLLVMType(p.ID)
		if err != nil {
			return err
		}
		name := "%" + fieldText(p, "text")
		if isAggregateType(typ) || typ == "{ ptr, ptr }" {
			typ = "ptr"
		}
		sig = append(sig, typ+" "+name)
		args = append(args, typ+" "+name)
	}
	ret := g.resultLLVM(result)
	if ret == "" {
		fmt.Fprintf(g.out, "define void @%s(%s) {\nentry:\n  call void @%s(%s)\n  ret void\n}\n",
			llvmSymbol(req.sym), strings.Join(sig, ", "), llvmSymbol(g.cur.funcs[decl.ID]), strings.Join(args, ", "))
		return nil
	}
	fmt.Fprintf(g.out, "define %s @%s(%s) {\nentry:\n  %%r = call %s @%s(%s)\n  ret %s %%r\n}\n",
		ret, llvmSymbol(req.sym), strings.Join(sig, ", "), ret,
		llvmSymbol(g.cur.funcs[decl.ID]), strings.Join(args, ", "), ret)
	return nil
}

// outerFunc returns the function that contains a record, starting at its
// parent so a lambda record itself does not answer.
func (g *generator) outerFunc(id int) int {
	cur := refField(g.recs[id], "parent")
	for cur != 0 {
		r, ok := g.recs[cur]
		if !ok {
			return 0
		}
		if r.Kind == "func" {
			return cur
		}
		cur = refField(r, "parent")
	}
	return 0
}

// isClosureRecord reports whether a record builds a closure value inside fn.
func (g *generator) isClosureRecord(r willc.Record, fn int) bool {
	if r.Kind == "func" && g.lambdaNames[r.ID] != "" {
		return g.outerFunc(r.ID) == fn
	}
	if r.Kind == "name" {
		if target, ok := g.recs[refField(r, "to")]; ok && target.Kind == "func" && g.lambdaNames[target.ID] == "" {
			return g.enclosingFunc(r.ID) == fn && g.cur.funcs[target.ID] != ""
		}
	}
	return false
}
