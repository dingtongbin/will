package gen

import "github.com/will-lang/will/internal/willc"

// waitArg returns the single expression of wait(expr). wait is a predeclared
// synchronous bridge: it is transparent for lowering, and the inner call
// decides whether it blocks a thread or parks a state-machine step.
func (g *generator) waitArg(r willc.Record) (int, bool) {
	if r.Kind != "call" || fieldText(r, "text") != "" {
		return 0, false
	}
	to, ok := g.recs[refField(r, "to")]
	if !ok || to.Kind != "name" || fieldText(to, "text") != "wait" {
		return 0, false
	}
	args := refsOf(r, "arg")
	if len(args) != 1 {
		return 0, false
	}
	return args[0], true
}
