package pkg

import (
	"os"
	"path/filepath"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/mod"
	"github.com/will-lang/will/internal/source"
)

// checkSums verifies the source face of every local replace target that has a
// will.sum line. A dependency without a line is left alone: will mod tidy
// writes that line.
func (g *graph) checkSums() []diag.Diagnostic {
	data, err := os.ReadFile(filepath.Join(g.root.Dir, "will.sum"))
	if err != nil {
		return nil
	}
	lines, ds := mod.ParseSum("will.sum", data)
	var bag diag.Bag
	bag.AddAll(ds)
	hashes := map[*moduleNode]string{}
	for _, line := range lines {
		n := g.localNode(line.Path)
		if n == nil {
			continue
		}
		got, ok := hashes[n]
		if !ok {
			hash, err := mod.SourceFaceHash(n.Dir)
			if err != nil {
				continue
			}
			got = "sha256:" + hash
			hashes[n] = got
		}
		if got != line.Sum {
			bag.Error(source.Pos{File: "will.sum", Line: line.Line, Col: 1},
				diag.CodeChecksumMismatch, "checksum mismatch for "+line.Path,
				"run will mod tidy")
		}
	}
	return bag.Diagnostics()
}

// localNode finds the local replace target a will.sum line names.
func (g *graph) localNode(path string) *moduleNode {
	for _, key := range g.order {
		n := g.nodes[key]
		if !n.Local {
			continue
		}
		for _, k := range n.Keys {
			if k == path {
				return n
			}
		}
	}
	return nil
}
