package gen

import (
	"fmt"

	"github.com/will-lang/will/internal/willc"
)

// arrayBatch is one plate-backed group of equal-sized Array buffers. The first
// construction allocates the plate; later members take successive slots. The
// common release record releases the plate once.
type arrayBatch struct {
	plan      willc.Batch
	plateSlot string
	next      int
	released  bool
	slotBytes int
}

func (g *generator) buildArrayBatches(d *docState) {
	g.arrayBatchByNew = map[int]*arrayBatch{}
	g.arrayBatchByOwner = map[int]*arrayBatch{}
	g.arrayBatches = nil
	for _, b := range d.batches {
		ab := &arrayBatch{plan: b}
		g.arrayBatches = append(g.arrayBatches, ab)
		for _, id := range b.News {
			g.arrayBatchByNew[id] = ab
		}
		for _, id := range b.Owners {
			g.arrayBatchByOwner[id] = ab
		}
	}
}

func (g *generator) arrayBatchData(ab *arrayBatch, elemSize int) string {
	if ab.slotBytes == 0 {
		ab.slotBytes = ab.plan.Cap * elemSize
	}
	slot := g.batchSlot(ab)
	g.needPlateDecl = true
	if ab.next == 0 {
		plate := g.newTmp()
		fmt.Fprintf(g.out, "  %s = call ptr @willrt_plate_new(i64 %d, i64 %d)\n",
			plate, ab.plan.ElementLen, ab.slotBytes)
		fmt.Fprintf(g.out, "  store ptr %s, ptr %s\n", plate, slot)
	}
	base := g.load("ptr", slot)
	offset := ab.next * ab.slotBytes
	ab.next++
	data := g.newTmp()
	fmt.Fprintf(g.out, "  %s = getelementptr i8, ptr %s, i64 %d\n", data, base, offset)
	return data
}

func (g *generator) batchSlot(ab *arrayBatch) string {
	if ab.plateSlot != "" {
		return ab.plateSlot
	}
	ab.plateSlot = g.newSlot("batch.plate")
	line := fmt.Sprintf("  %s = alloca ptr", ab.plateSlot)
	if g.lateAllowed {
		g.lateAllocas = append(g.lateAllocas, line)
	} else {
		g.out.WriteString(line + "\n")
	}
	return ab.plateSlot
}
