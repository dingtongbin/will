package types

import (
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
)

// checkAliases expands every declared alias once, so a cycle is reported even
// when no other declaration uses it.
func (c *checker) checkAliases(e *env) {
	for _, decl := range e.aliasOrder {
		c.resolveAlias(decl)
	}
}

// resolveAlias returns the checked target of decl. Expanding the target, rather
// than interning the alias name, keeps the alias transparent to identity,
// layout, and diagnostics.
func (c *checker) resolveAlias(decl *ast.TypeAliasDecl) *Type {
	if c.aliasResolved == nil {
		c.aliasResolved = map[string]*Type{}
		c.aliasActive = map[string]bool{}
		c.aliasReported = map[string]bool{}
	}
	if t, ok := c.aliasResolved[decl.Name]; ok {
		return t
	}
	if c.aliasActive[decl.Name] {
		if !c.aliasReported[decl.Name] {
			chain := append(append([]string(nil), c.aliasStack...), decl.Name)
			c.bag.Error(decl.NamePos, diag.CodeTypeAliasCycle,
				"type alias cycle: "+strings.Join(chain, " -> "),
				"break the cycle; aliases must resolve to a concrete type")
			c.aliasReported[decl.Name] = true
		}
		return c.in.named(decl.Name, nil)
	}
	c.aliasActive[decl.Name] = true
	c.aliasStack = append(c.aliasStack, decl.Name)
	target := c.buildType(decl.Type, nil)
	c.aliasStack = c.aliasStack[:len(c.aliasStack)-1]
	delete(c.aliasActive, decl.Name)
	c.aliasResolved[decl.Name] = target
	return target
}
