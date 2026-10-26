package gen

import "fmt"

// chanDrop is one generated callback that releases a buffered channel element
// before the runtime frees the channel ring.
type chanDrop struct {
	text string
	sym  string
}

// channelDrop returns the drop callback for one channel element type, queuing
// its definition once per module.
func (g *generator) channelDrop(text string) string {
	text = g.qualifyTypeText(g.pkg, text)
	if sym, ok := g.chanDropByText(text); ok {
		return sym
	}
	sym := qualify(g.pkg, "chan.drop."+text)
	g.chanDrops = append(g.chanDrops, chanDrop{text: text, sym: sym})
	return sym
}

func (g *generator) chanDropByText(text string) (string, bool) {
	for _, d := range g.chanDrops {
		if d.text == text {
			return d.sym, true
		}
	}
	return "", false
}

func (g *generator) emitChanDrops() error {
	if len(g.chanDrops) == 0 {
		return nil
	}
	drops := g.chanDrops
	g.chanDrops = nil
	for _, d := range drops {
		if g.chanDropDone[d.sym] {
			continue
		}
		g.chanDropDone[d.sym] = true
		fmt.Fprintf(g.out, "define internal void @%s(ptr %%elem) {\nentry:\n", llvmSymbol(d.sym))
		if err := g.releaseOwnedValue("%elem", d.text); err != nil {
			return err
		}
		g.out.WriteString("  ret void\n}\n")
	}
	return nil
}
