package willc

import (
	"sort"
	"strconv"
	"strings"
)

// Batch is a group of Array buffers that are allocated from one plate and
// released together at the same block end. Only arrays with a literal capacity,
// a plainly droppable element type, and no growing or escaping use qualify.
type Batch struct {
	ID         int
	Parent     int
	Pos        string
	Elem       string
	Cap        int
	Owners     []int
	News       []int
	Releases   []int
	Members    string
	ElementLen int
}

// Batches returns the release groups that can be backed by one plate. The .willc
// file still contains one release per owner; the decision record and codegen
// consume this grouping separately.
func Batches(d *Doc) []Batch {
	if d == nil {
		return nil
	}
	byID := make(map[int]Record, len(d.Records))
	for _, r := range d.Records {
		byID[r.ID] = r
	}
	type candidate struct {
		owner   int
		release int
		parent  int
		pos     string
		elem    string
		cap     int
		newID   int
	}
	var candidates []candidate
	for _, r := range d.Records {
		if r.Kind != "var" {
			continue
		}
		typ, _ := textField(r, "type")
		elem, ok := plainArrayElem(typ)
		if !ok {
			continue
		}
		var newRec *Record
		for i := range d.Records {
			if d.Records[i].Kind == "new" && refField(d.Records[i], "parent") == r.ID {
				newRec = &d.Records[i]
				break
			}
		}
		if newRec == nil {
			continue
		}
		capValue, ok := literalCap(*newRec, byID)
		if !ok || capValue == 0 {
			continue
		}
		releaseID, parent, releasePos, ok := releaseFor(r.ID, d.Records, byID)
		if !ok {
			continue
		}
		if insideLoop(r.ID, byID) || insideLoop(releaseID, byID) {
			continue
		}
		if !safeBatchUses(r.ID, d.Records, byID) {
			continue
		}
		candidates = append(candidates, candidate{owner: r.ID, release: releaseID, parent: parent, pos: releasePos, elem: elem, cap: capValue, newID: newRec.ID})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].parent != candidates[j].parent {
			return candidates[i].parent < candidates[j].parent
		}
		if candidates[i].pos != candidates[j].pos {
			return candidates[i].pos < candidates[j].pos
		}
		if candidates[i].elem != candidates[j].elem {
			return candidates[i].elem < candidates[j].elem
		}
		if candidates[i].cap != candidates[j].cap {
			return candidates[i].cap < candidates[j].cap
		}
		return candidates[i].owner < candidates[j].owner
	})
	var out []Batch
	for i := 0; i < len(candidates); {
		j := i + 1
		for j < len(candidates) &&
			candidates[j].parent == candidates[i].parent &&
			candidates[j].pos == candidates[i].pos &&
			candidates[j].elem == candidates[i].elem &&
			candidates[j].cap == candidates[i].cap {
			j++
		}
		if j-i >= 2 {
			b := Batch{
				ID:         len(out) + 1,
				Parent:     candidates[i].parent,
				Pos:        candidates[i].pos,
				Elem:       candidates[i].elem,
				Cap:        candidates[i].cap,
				ElementLen: j - i,
			}
			for k := i; k < j; k++ {
				b.Owners = append(b.Owners, candidates[k].owner)
				b.News = append(b.News, candidates[k].newID)
				b.Releases = append(b.Releases, candidates[k].release)
			}
			sort.Ints(b.Releases)
			parts := make([]string, len(b.Releases))
			for k, id := range b.Releases {
				parts[k] = "#" + strconv.Itoa(id)
			}
			b.Members = strings.Join(parts, ",")
			out = append(out, b)
		}
		i = j
	}
	return out
}

func textField(r Record, name string) (string, bool) {
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == TextValue {
			return f.Value.Text, true
		}
	}
	return "", false
}

func wordField(r Record, name string) string {
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == WordValue {
			return f.Value.Text
		}
	}
	return ""
}

func refField(r Record, name string) int {
	for _, f := range r.Fields {
		if f.Name == name && f.Value.Kind == RefValue {
			return f.Value.Ref
		}
	}
	return 0
}

func plainArrayElem(typ string) (string, bool) {
	if !strings.HasPrefix(typ, "Array<") || !strings.HasSuffix(typ, ">") {
		return "", false
	}
	elem := strings.TrimSpace(typ[len("Array<") : len(typ)-1])
	if elem == "" {
		return "", false
	}
	switch elem {
	case "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64",
		"usize", "isize", "f32", "f64", "bool":
		return elem, true
	}
	return "", false
}

func literalCap(r Record, byID map[int]Record) (int, bool) {
	arg := refField(r, "arg")
	lit, ok := byID[arg]
	if !ok || lit.Kind != "lit" {
		return 0, false
	}
	kind := wordField(lit, "kind")
	text, _ := textField(lit, "text")
	if kind != "int" || text == "" {
		return 0, false
	}
	n, err := strconv.Atoi(text)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func releaseFor(owner int, records []Record, byID map[int]Record) (int, int, string, bool) {
	for _, r := range records {
		if r.Kind != "release" || refField(r, "owner") != owner {
			continue
		}
		parent := refField(r, "parent")
		block, ok := byID[parent]
		if !ok || block.Kind != "block" {
			continue
		}
		pos, _ := textField(r, "pos")
		if pos == "" {
			continue
		}
		return r.ID, parent, pos, true
	}
	return 0, 0, "", false
}

// insideLoop reports whether a node is nested in a for statement. A plate is
// allocated once per group member site, not once per iteration, so loop bodies
// must keep the ordinary per-array allocation.
func insideLoop(id int, byID map[int]Record) bool {
	seen := map[int]bool{}
	for id != 0 && !seen[id] {
		seen[id] = true
		r, ok := byID[id]
		if !ok {
			return false
		}
		if r.Kind == "for" {
			return true
		}
		id = refField(r, "parent")
	}
	return false
}

func safeBatchUses(owner int, records []Record, byID map[int]Record) bool {
	for _, r := range records {
		if r.Kind != "name" || refField(r, "to") != owner {
			continue
		}
		parent, ok := byID[refField(r, "parent")]
		if !ok {
			return false
		}
		switch parent.Kind {
		case "discard":
		case "mcall":
			if refField(parent, "recv") != r.ID {
				return false
			}
			name, _ := textField(parent, "text")
			switch name {
			case "Len", "Cap", "Get", "Set", "Clear", "IsEmpty", "Clone":
			default:
				return false
			}
		default:
			return false
		}
	}
	return true
}
