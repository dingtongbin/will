package mod

import "sort"

// Dep is one selected dependency.
type Dep struct {
	Path       string
	Version    Version
	Local      string
	TargetPath string
}

// Select chooses one version per module path: the highest required version,
// with replace winning over everything. The result is sorted by path.
func (m *Module) Select() []Dep {
	order := []string{}
	byPath := map[string][]Require{}
	for _, r := range m.Requires {
		if _, seen := byPath[r.Path]; !seen {
			order = append(order, r.Path)
		}
		byPath[r.Path] = append(byPath[r.Path], r)
	}
	replaceIdx := map[string]Replace{}
	for _, rep := range m.Replaces {
		replaceIdx[rep.Path+"@"+rep.Version.String()] = rep
	}

	deps := make([]Dep, 0, len(order))
	for _, path := range order {
		dep := selectOne(path, byPath[path])
		dep = applyReplace(dep, replaceIdx)
		deps = append(deps, dep)
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Path < deps[j].Path })
	return deps
}

func selectOne(path string, reqs []Require) Dep {
	dep := Dep{Path: path}
	best := false
	for _, r := range reqs {
		if !best || r.Version.Compare(dep.Version) > 0 {
			dep.Version = r.Version
			best = true
		}
	}
	return dep
}

func applyReplace(dep Dep, idx map[string]Replace) Dep {
	for i := 0; i <= 16; i++ {
		rep, ok := idx[dep.Path+"@"+dep.Version.String()]
		if !ok {
			return dep
		}
		if !rep.IsVersioned {
			dep.Local = rep.Target
			return dep
		}
		dep.TargetPath = rep.Target
		dep.Version = rep.TargetVer
	}
	return dep
}
