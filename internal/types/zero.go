package types

// hasZero reports whether a local variable of t may omit its initializer. An
// enum and Result have no zero value; a struct has one only when every field
// does. Option always has None, and the predeclared handles and containers
// have their documented empty forms.
func (c *checker) hasZero(t *Type) bool {
	return c.hasZeroSeen(t, map[*Type]bool{})
}

func (c *checker) hasZeroSeen(t *Type, seen map[*Type]bool) bool {
	if t == nil {
		return true
	}
	if seen[t] {
		return true
	}
	seen[t] = true
	switch t.Kind() {
	case ScalarKind, ArrayKind, ViewKind, FuncKind, UnitKind, RangeKind:
		return true
	case NamedKind:
		switch t.Name() {
		case "Result", "Value":
			return false
		case "Option", "Array", "Map", "Set", "rc", "weak", "chan", "Task", "Duration":
			return true
		}
		if c.env.enums[t.Name()] {
			return false
		}
		if fields, ok := c.env.fields[t.Name()]; ok {
			for _, f := range fields {
				if !c.hasZeroSeen(f.typ, seen) {
					return false
				}
			}
		}
		return true
	}
	return true
}
