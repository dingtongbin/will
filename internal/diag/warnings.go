package diag

// WarningFamilies lists the warning families the build volume fixes. Every
// warning belongs to exactly one family; a warning outside them cannot be
// switched off.
var WarningFamilies = []string{"shadow", "leak"}

// Family returns the warning family of a code, or "" when the code has none.
func Family(code string) string {
	switch code {
	case CodeShadow:
		return "shadow"
	case CodeExplicitLeak:
		return "leak"
	}
	return ""
}

// KnownFamily reports whether name is one of the warning families.
func KnownFamily(name string) bool {
	for _, f := range WarningFamilies {
		if f == name {
			return true
		}
	}
	return false
}

// FilterDisabled drops every warning whose family is switched off. Warnings
// with no family always stay.
func FilterDisabled(ds []Diagnostic, disabled map[string]bool) []Diagnostic {
	if len(disabled) == 0 {
		return ds
	}
	out := make([]Diagnostic, 0, len(ds))
	for _, d := range ds {
		if d.Severity == Warning {
			if family := Family(d.Code); family != "" && disabled[family] {
				continue
			}
		}
		out = append(out, d)
	}
	return out
}
