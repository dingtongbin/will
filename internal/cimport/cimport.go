// Package cimport reads the small declaration subset of a C header that the
// compiler needs for a foreign call: typedef names, function prototypes, and
// the scalar or pointer type of each parameter and result. It deliberately
// ignores preprocessor directives, macros, attributes, and struct bodies; a
// function whose prototype needs one of those is left out.
package cimport

import (
	"fmt"
	"strings"

	"github.com/will-lang/will/internal/ast"
)

// Func is one C function prototype mapped to will types.
type Func struct {
	Name     string
	Params   []*ast.Type
	Result   *ast.Type
	Variadic bool
}

type token struct {
	text string
}

// Parse returns the function prototypes of one header source.
func Parse(name string, src []byte) ([]Func, error) {
	toks, err := scan(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	p := &parser{toks: toks, typedefs: map[string]*ast.Type{
		"size_t":    scalar("usize"),
		"ssize_t":   scalar("isize"),
		"ptrdiff_t": scalar("isize"),
		"int8_t":    scalar("i8"),
		"int16_t":   scalar("i16"),
		"int32_t":   scalar("i32"),
		"int64_t":   scalar("i64"),
		"uint8_t":   scalar("u8"),
		"uint16_t":  scalar("u16"),
		"uint32_t":  scalar("u32"),
		"uint64_t":  scalar("u64"),
		"intptr_t":  scalar("isize"),
		"uintptr_t": scalar("usize"),
	}}
	return p.header()
}

func scalar(name string) *ast.Type {
	return &ast.Type{Kind: ast.ScalarType, Name: name}
}

func unit() *ast.Type {
	return &ast.Type{Kind: ast.UnitType}
}

func scan(src []byte) ([]token, error) {
	var out []token
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			if i+1 >= len(src) {
				return nil, fmt.Errorf("unterminated block comment")
			}
			i += 2
		case c == '#':
			// A directive may continue over a backslash-newline pair.
			for i < len(src) {
				if src[i] == '\\' && i+1 < len(src) && src[i+1] == '\n' {
					i += 2
					continue
				}
				if src[i] == '\n' {
					break
				}
				i++
			}
		case c == '"' || c == '\'':
			quote := c
			i++
			for i < len(src) {
				if src[i] == '\\' {
					i += 2
					continue
				}
				if i < len(src) && src[i] == quote {
					i++
					break
				}
				i++
			}
		case c == '.' && i+2 < len(src) && src[i+1] == '.' && src[i+2] == '.':
			out = append(out, token{"..."})
			i += 3
		case isIdent(c):
			j := i + 1
			for j < len(src) && isIdent(src[j]) {
				j++
			}
			out = append(out, token{string(src[i:j])})
			i = j
		case c >= '0' && c <= '9':
			j := i + 1
			for j < len(src) && (isIdent(src[j]) || src[j] == '.') {
				j++
			}
			out = append(out, token{string(src[i:j])})
			i = j
		default:
			out = append(out, token{string(c)})
			i++
		}
	}
	return out, nil
}

func isIdent(c byte) bool {
	return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

type parser struct {
	toks     []token
	pos      int
	typedefs map[string]*ast.Type
}

func (p *parser) header() ([]Func, error) {
	var out []Func
	for p.pos < len(p.toks) {
		if p.at("{") || p.at("}") {
			p.pos++
			continue
		}
		if p.at("typedef") {
			p.typedef()
			continue
		}
		if p.at("struct") || p.at("union") || p.at("enum") {
			p.skipTag()
			continue
		}
		if p.at("extern") {
			p.pos++
			if p.pos < len(p.toks) && strings.HasPrefix(p.toks[p.pos].text, "\"") {
				p.pos++
			}
			continue
		}
		start := p.pos
		end, kind := p.declarationEnd()
		if kind != "func" {
			p.pos = end
			continue
		}
		fn, err := p.prototype(start, end)
		if err != nil {
			// A prototype the subset cannot express is skipped; a later call to
			// that name reports an undefined C member.
			p.pos = end
			continue
		}
		out = append(out, fn)
		p.pos = end
	}
	return out, nil
}

func (p *parser) at(s string) bool {
	return p.pos < len(p.toks) && p.toks[p.pos].text == s
}

// declarationEnd returns the token after one top-level declaration and says
// whether it looks like a function prototype.
func (p *parser) declarationEnd() (int, string) {
	depth := 0
	hasParen := false
	for i := p.pos; i < len(p.toks); i++ {
		switch p.toks[i].text {
		case "(", "[":
			if p.toks[i].text == "(" && depth == 0 {
				hasParen = true
			}
			depth++
		case ")", "]":
			if depth > 0 {
				depth--
			}
		case "{":
			if depth == 0 && !hasParen {
				// A tag body; skip it and its terminating semicolon.
				return p.skipBraces(i), "tag"
			}
			depth++
		case "}":
			if depth > 0 {
				depth--
			}
		case ";":
			if depth == 0 {
				if hasParen {
					return i, "func"
				}
				return i + 1, "var"
			}
		}
	}
	return len(p.toks), "eof"
}

func (p *parser) skipBraces(open int) int {
	depth := 0
	for i := open; i < len(p.toks); i++ {
		switch p.toks[i].text {
		case "{":
			depth++
		case "}":
			depth--
			if depth == 0 {
				if i+1 < len(p.toks) && p.toks[i+1].text == ";" {
					return i + 2
				}
				return i + 1
			}
		}
	}
	return len(p.toks)
}

func (p *parser) skipTag() {
	// struct name { ... } or enum name; a tag body is skipped as one unit.
	for p.pos < len(p.toks) {
		if p.at(";") {
			p.pos++
			return
		}
		if p.at("{") {
			p.pos = p.skipBraces(p.pos)
			return
		}
		p.pos++
	}
}

func (p *parser) typedef() {
	start := p.pos
	p.pos++
	semi := p.pos
	depth := 0
	for semi < len(p.toks) {
		switch p.toks[semi].text {
		case "{":
			depth++
		case "}":
			if depth > 0 {
				depth--
			}
		case ";":
			if depth == 0 {
				goto found
			}
		}
		semi++
	}
found:
	if semi >= len(p.toks) {
		p.pos = len(p.toks)
		return
	}
	body := p.toks[p.pos:semi]
	if len(body) == 0 {
		p.pos = semi + 1
		return
	}
	// A typedef of a function pointer is outside the subset.
	for _, t := range body {
		if t.text == "(" {
			p.pos = semi + 1
			return
		}
	}
	name := body[len(body)-1].text
	typ, ok := p.typeOf(body[:len(body)-1])
	if ok {
		// A standard typedef such as size_t already has the will spelling the
		// runtime uses; do not replace it with the host's C width.
		if _, exists := p.typedefs[name]; !exists {
			p.typedefs[name] = typ
		}
	}
	p.pos = semi + 1
	_ = start
}

func (p *parser) prototype(start, end int) (Func, error) {
	open := -1
	for i := start; i < end; i++ {
		if p.toks[i].text == "(" {
			open = i
			break
		}
	}
	if open <= start {
		return Func{}, fmt.Errorf("no function name")
	}
	name := p.toks[open-1].text
	if !isIdent(name[0]) {
		return Func{}, fmt.Errorf("bad function name")
	}
	result, ok := p.typeOf(p.toks[start : open-1])
	if !ok {
		return Func{}, fmt.Errorf("result type")
	}
	close := p.matchParen(open)
	if close < 0 || close > end {
		return Func{}, fmt.Errorf("unclosed parameters")
	}
	params, variadic, ok := p.params(open+1, close)
	if !ok {
		return Func{}, fmt.Errorf("parameters")
	}
	return Func{Name: name, Params: params, Result: result, Variadic: variadic}, nil
}

func (p *parser) matchParen(open int) int {
	depth := 0
	for i := open; i < len(p.toks); i++ {
		switch p.toks[i].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func (p *parser) params(start, end int) ([]*ast.Type, bool, bool) {
	var out []*ast.Type
	variadic := false
	part := start
	depth := 0
	flush := func(to int) bool {
		toks := p.toks[part:to]
		if len(toks) == 0 {
			return true
		}
		if len(toks) == 1 && toks[0].text == "void" {
			return true
		}
		if len(toks) == 1 && toks[0].text == "..." {
			variadic = true
			return true
		}
		for i, t := range toks {
			if t.text == "..." {
				variadic = true
				toks = toks[:i]
				break
			}
		}
		if len(toks) == 0 {
			return true
		}
		typ, ok := p.paramType(toks)
		if !ok {
			return false
		}
		out = append(out, typ)
		return true
	}
	for i := start; i <= end; i++ {
		if i == end || (p.toks[i].text == "," && depth == 0) {
			if !flush(i) {
				return nil, false, false
			}
			part = i + 1
			continue
		}
		switch p.toks[i].text {
		case "(", "[":
			depth++
		case ")", "]":
			depth--
		}
	}
	return out, variadic, true
}

func (p *parser) paramType(toks []token) (*ast.Type, bool) {
	// Drop a parameter name: the last identifier after a complete type.
	if len(toks) > 1 {
		last := toks[len(toks)-1].text
		if isIdent(last[0]) && !p.isTypeWord(last) {
			toks = toks[:len(toks)-1]
		}
	}
	return p.typeOf(toks)
}

func (p *parser) isTypeWord(word string) bool {
	switch word {
	case "void", "char", "short", "int", "long", "float", "double", "signed",
		"unsigned", "_Bool", "bool", "struct", "union", "enum", "const",
		"volatile", "restrict":
		return true
	}
	_, ok := p.typedefs[word]
	return ok
}

// typeOf maps one C type spelling to a will type. A pointer is raw; a scalar is
// its will width; void is the unit type.
func (p *parser) typeOf(toks []token) (*ast.Type, bool) {
	var words []string
	pointers := 0
	for i := 0; i < len(toks); i++ {
		t := toks[i].text
		switch t {
		case "const", "volatile", "restrict", "static", "extern", "inline",
			"register", "_Noreturn", "__cdecl", "__stdcall":
			continue
		case "__attribute__", "__declspec":
			// Skip the balanced argument list.
			if i+1 < len(toks) && toks[i+1].text == "(" {
				depth := 0
				for i++; i < len(toks); i++ {
					if toks[i].text == "(" {
						depth++
					} else if toks[i].text == ")" {
						depth--
						if depth == 0 {
							break
						}
					}
				}
			}
			continue
		case "*":
			pointers++
			continue
		case "[":
			// An array parameter is a pointer.
			pointers++
			continue
		}
		if t == "struct" || t == "union" || t == "enum" {
			if pointers > 0 {
				return scalar("raw"), true
			}
			return nil, false
		}
		words = append(words, t)
	}
	if pointers > 0 {
		return scalar("raw"), true
	}
	if len(words) == 0 {
		return nil, false
	}
	if typ, ok := p.typedefs[words[0]]; ok && len(words) == 1 {
		return typ, true
	}
	unsigned := false
	longs := 0
	short := false
	base := ""
	for _, w := range words {
		switch w {
		case "unsigned":
			unsigned = true
		case "signed":
		case "long":
			longs++
		case "short":
			short = true
		default:
			if base == "" {
				base = w
			}
		}
	}
	cname := base
	if base == "int" || base == "" {
		switch {
		case short:
			if unsigned {
				return scalar("u16"), true
			}
			return scalar("i16"), true
		case longs > 0:
			if unsigned {
				return scalar("u64"), true
			}
			return scalar("i64"), true
		default:
			if unsigned {
				return scalar("u32"), true
			}
			return scalar("i32"), true
		}
	}
	if typ, ok := p.typedefs[cname]; ok {
		return typ, true
	}
	switch cname {
	case "void":
		return unit(), true
	case "char":
		if unsigned {
			return scalar("u8"), true
		}
		return scalar("i8"), true
	case "_Bool", "bool":
		return scalar("bool"), true
	case "float":
		return scalar("f32"), true
	case "double":
		return scalar("f64"), true
	case "size_t":
		return scalar("usize"), true
	}
	return nil, false
}
