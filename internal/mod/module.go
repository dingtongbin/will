package mod

import (
	"strconv"
	"strings"

	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
)

// Require is one dependency requirement.
type Require struct {
	Path    string
	Version Version
}

// Replace redirects one required version to a local directory or to another
// version.
type Replace struct {
	Path        string
	Version     Version
	Target      string
	TargetVer   Version
	IsVersioned bool
}

// Module is a parsed will.mod.
type Module struct {
	Path     string
	Will     int
	Requires []Require
	Replaces []Replace
	Features []string
	// Links are external libraries to pass to the linker, in source order.
	Links []string
}

const (
	helpModule  = "module <import path>"
	helpWill    = "will <integer>"
	helpRequire = "require <path>@v<major>.<minor>.<patch>"
	helpReplace = "replace <path>@<version> => <local dir or path@version>"
	helpFeature = "feature <name>"
)

// Parse reads a will.mod file.
func Parse(file string, data []byte) (*Module, []diag.Diagnostic) {
	p := &parser{file: file}
	for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		p.line = i + 1
		p.parseLine(line)
	}
	if p.bag.HasErrors() {
		return nil, p.bag.Diagnostics()
	}
	if p.module.Path == "" {
		p.bad(1, helpModule, "module is missing")
	}
	if !p.hasWill {
		p.bad(1, helpWill, "will is missing")
	}
	if p.bag.HasErrors() {
		return nil, p.bag.Diagnostics()
	}
	return &p.module, nil
}

type parser struct {
	file    string
	line    int
	module  Module
	hasWill bool
	bag     diag.Bag
}

func (p *parser) parseLine(line string) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "module":
		if len(fields) != 2 {
			p.bad(p.line, helpModule, "module takes one path")
			return
		}
		if p.module.Path != "" {
			p.bad(p.line, helpModule, "module appears once")
			return
		}
		p.module.Path = fields[1]
	case "will":
		if len(fields) != 2 {
			p.bad(p.line, helpWill, "will takes one integer")
			return
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil || !allDigits(fields[1]) {
			p.bad(p.line, helpWill, "will takes one integer")
			return
		}
		if p.hasWill {
			p.bad(p.line, helpWill, "will appears once")
			return
		}
		p.module.Will = n
		p.hasWill = true
	case "require":
		if len(fields) != 2 {
			p.bad(p.line, helpRequire, "require takes one path@version")
			return
		}
		path, version, ok := splitAtLast(fields[1])
		if !ok || path == "" {
			p.bad(p.line, helpRequire, "require takes one path@version")
			return
		}
		v, ok := ParseVersion(version)
		if !ok {
			p.bad(p.line, helpRequire, "the version is v<major>.<minor>.<patch>")
			return
		}
		p.module.Requires = append(p.module.Requires, Require{Path: path, Version: v})
	case "replace":
		if len(fields) != 4 || fields[2] != "=>" {
			p.bad(p.line, helpReplace, "replace is path@version => target")
			return
		}
		path, version, ok := splitAtLast(fields[1])
		v, ok2 := ParseVersion(version)
		if !ok || path == "" || !ok2 {
			p.bad(p.line, helpReplace, "the replaced key is path@v<major>.<minor>.<patch>")
			return
		}
		rep := Replace{Path: path, Version: v, Target: fields[3]}
		if tpath, tver, ok := splitAtLast(fields[3]); ok {
			if tv, ok := ParseVersion(tver); ok {
				rep.IsVersioned = true
				rep.Target = tpath
				rep.TargetVer = tv
			}
		}
		p.module.Replaces = append(p.module.Replaces, rep)
	case "link":
		if len(fields) != 2 || fields[1] == "" {
			p.bad(p.line, "link <library name or path>", "link takes one library name or path")
			return
		}
		p.module.Links = append(p.module.Links, fields[1])
	case "feature":
		if len(fields) != 2 || fields[1] == "" {
			p.bad(p.line, helpFeature, "feature takes one name")
			return
		}
		p.module.Features = append(p.module.Features, fields[1])
	default:
		p.bad(p.line, "module, will, require, replace, feature, or link", "unknown field "+fields[0])
	}
}

func (p *parser) bad(line int, help, detail string) {
	pos := source.Pos{File: p.file, Line: line, Col: 1}
	p.bag.Error(pos, diag.CodeInvalidWillMod, "invalid will.mod", help+" ("+detail+")")
}

// splitAtLast splits s at its last @.
func splitAtLast(s string) (string, string, bool) {
	i := strings.LastIndexByte(s, '@')
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}
