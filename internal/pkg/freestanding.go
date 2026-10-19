package pkg

import (
	"github.com/will-lang/will/internal/check"
	"github.com/will-lang/will/internal/diag"
)

// Freestanding runs the --freestanding rule over every file of the module,
// sorted like the other whole-module diagnostics.
func (b *Build) Freestanding() []diag.Diagnostic {
	var bag diag.Bag
	for _, p := range b.Packages {
		for _, f := range p.Files {
			if f.AST == nil {
				continue
			}
			bag.AddAll(check.Freestanding(f.AST))
		}
	}
	return bag.Diagnostics()
}
