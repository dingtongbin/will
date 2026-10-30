package types

import "strings"

// Send reports whether a value of t can move to another task. The answer is
// structural: a handle is sendable only when its payload is, a task when its
// result is, and an interface only when the compiler knows every implementation
// is sendable. An open interface is not.
func (r *Result) Send(t *Type) bool {
	return r.send(t, map[string]bool{})
}

// Sync reports whether a value of t can be read from another task while the
// owner keeps it. Mutable state must sit behind a guard or an atomic handle;
// a task handle is affine and is never Sync.
func (r *Result) Sync(t *Type) bool {
	return r.sync(t, map[string]bool{})
}

func (r *Result) send(t *Type, active map[string]bool) bool {
	if t == nil || r == nil || r.env == nil {
		return true
	}
	if t.Kind() == ViewKind {
		return false
	}
	key := t.String()
	if active[key] {
		return true
	}
	active[key] = true
	defer delete(active, key)
	switch t.Kind() {
	case ScalarKind:
		return t.Name() != "raw"
	case ArrayKind, ViewKind:
		return r.send(t.Elem(), active)
	case FuncKind:
		return true
	case UnitKind, RangeKind:
		return true
	case NamedKind:
		return r.namedSend(t, active)
	}
	return false
}

func (r *Result) sync(t *Type, active map[string]bool) bool {
	if t == nil || r == nil || r.env == nil {
		return true
	}
	if t.Kind() == ViewKind {
		return false
	}
	key := t.String()
	if active[key] {
		return true
	}
	active[key] = true
	defer delete(active, key)
	switch t.Kind() {
	case ScalarKind:
		return t.Name() != "raw"
	case ArrayKind, ViewKind:
		return r.sync(t.Elem(), active)
	case FuncKind:
		return true
	case UnitKind, RangeKind:
		return true
	case NamedKind:
		return r.namedSync(t, active)
	}
	return false
}

func (r *Result) namedSend(t *Type, active map[string]bool) bool {
	if r.isTypeParam(t.Name()) {
		return true
	}
	if isLockType(t.Name()) {
		return len(t.Args()) == 1 && r.send(t.Args()[0], active)
	}
	switch t.Name() {
	case "raw":
		return false
	case "Task":
		return len(t.Args()) == 1 && r.send(t.Args()[0], active)
	case "chan":
		return len(t.Args()) == 1 && r.send(t.Args()[0], active)
	case "Option", "Result", "list", "dict", "set":
		for _, arg := range t.Args() {
			if !r.send(arg, active) {
				return false
			}
		}
		return true
	case "Duration", "Value", "Show", "Error", "TaskError":
		return true
	}
	if isGuardType(t) {
		return false
	}
	if isSyncHandleType(t.Name()) {
		return true
	}
	short := shortNamed(t.Name())
	if _, ok := r.env.ifaces[short]; ok {
		return false
	}
	if r.env.classes[short] {
		return true
	}
	if fields, ok := r.env.fields[short]; ok {
		bind := r.bindings(short, t.Args())
		for _, f := range fields {
			if !r.send(substitute(f.typ, bind), active) {
				return false
			}
		}
		return true
	}
	if _, ok := r.env.enums[short]; ok {
		bind := r.bindings(short, t.Args())
		for _, v := range r.env.variants {
			if v.enum == nil || shortNamed(v.enum.Name()) != short {
				continue
			}
			for _, p := range v.payload {
				if !r.send(substitute(p, bind), active) {
					return false
				}
			}
		}
		return true
	}
	return false
}

func (r *Result) namedSync(t *Type, active map[string]bool) bool {
	if r.isTypeParam(t.Name()) {
		return true
	}
	if isLockType(t.Name()) {
		if len(t.Args()) != 1 {
			return false
		}
		if shortNamed(t.Name()) == "Mutex" {
			return r.send(t.Args()[0], active)
		}
		return r.send(t.Args()[0], active) && r.sync(t.Args()[0], active)
	}
	switch t.Name() {
	case "Task", "raw":
		return false
	case "chan":
		return len(t.Args()) == 1 && r.send(t.Args()[0], active)
	case "Option", "Result", "list", "dict", "set":
		for _, arg := range t.Args() {
			if !r.sync(arg, active) {
				return false
			}
		}
		return true
	case "Duration", "Value", "Show", "Error", "TaskError":
		return true
	}
	if isGuardType(t) {
		return false
	}
	if isSyncHandleType(t.Name()) {
		return true
	}
	short := shortNamed(t.Name())
	if _, ok := r.env.ifaces[short]; ok {
		return false
	}
	if r.env.classes[short] {
		return true
	}
	if fields, ok := r.env.fields[short]; ok {
		bind := r.bindings(short, t.Args())
		for _, f := range fields {
			if !r.sync(substitute(f.typ, bind), active) {
				return false
			}
		}
		return true
	}
	if _, ok := r.env.enums[short]; ok {
		bind := r.bindings(short, t.Args())
		for _, v := range r.env.variants {
			if v.enum == nil || shortNamed(v.enum.Name()) != short {
				continue
			}
			for _, p := range v.payload {
				if !r.sync(substitute(p, bind), active) {
					return false
				}
			}
		}
		return true
	}
	return false
}

func (r *Result) isTypeParam(name string) bool {
	if r.env.typeParams[name] {
		return true
	}
	for _, params := range r.env.params {
		for _, p := range params {
			if p == name {
				return true
			}
		}
	}
	return false
}

func (r *Result) bindings(name string, args []*Type) map[string]*Type {
	params := r.env.params[name]
	if len(params) == 0 || len(params) != len(args) {
		return nil
	}
	out := make(map[string]*Type, len(params))
	for i, p := range params {
		out[p] = args[i]
	}
	return out
}

func substitute(t *Type, bind map[string]*Type) *Type {
	if t == nil || len(bind) == 0 {
		return t
	}
	if t.Kind() == NamedKind && len(t.Args()) == 0 {
		if b := bind[t.Name()]; b != nil {
			return b
		}
	}
	if len(t.Args()) == 0 && t.Kind() != ArrayKind && t.Kind() != ViewKind &&
		t.Kind() != FuncKind {
		return t
	}
	out := *t
	if len(t.Args()) > 0 {
		out.args = make([]*Type, len(t.Args()))
		for i, a := range t.Args() {
			out.args[i] = substitute(a, bind)
		}
	}
	if t.Elem() != nil {
		out.elem = substitute(t.Elem(), bind)
	}
	if len(t.Params()) > 0 {
		out.params = make([]*Type, len(t.Params()))
		for i, p := range t.Params() {
			out.params[i] = substitute(p, bind)
		}
	}
	out.result = substitute(t.Result(), bind)
	out.text = ""
	return &out
}

// isLockType reports the two std/sync wrappers whose private state holds a raw
// lock word. The guard makes the payload safe to share, so the wrapper follows
// its payload rather than the raw field.
func isLockType(name string) bool {
	short := shortNamed(name)
	return short == "Mutex" || short == "RwLock"
}

// isSyncHandleType reports the std/sync primitive handles whose raw state is
// protected by the runtime lock protocol. Their state is shared, not copied.
func isSyncHandleType(name string) bool {
	switch shortNamed(name) {
	case "Semaphore", "Count", "Barrier", "Once":
		return true
	}
	return false
}

func shortNamed(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}
