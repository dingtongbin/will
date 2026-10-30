package gen

// referenceValue returns the value of an expression. The language has no
// explicit borrow expression, so a reference value is an ordinary value.
func (g *generator) referenceValue(id int) (string, error) {
	return g.value(id)
}

// borrowText returns the type text unchanged. Source borrow wrappers no longer
// exist; the second result stays for callers that still ask for a kind.
func borrowText(text string) (base, kind string) {
	return text, ""
}

func isMutText(text string) bool {
	return false
}

func isSingleBorrow(text string) bool {
	return false
}

func baseBorrowText(text string) string {
	return text
}

func (g *generator) isReferenceExpr(id int) bool {
	return false
}
