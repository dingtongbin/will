package parser

import (
	"strings"

	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/token"
)

func (p *Parser) parseFile() *ast.File {
	start := p.cur()
	f := &ast.File{}
	p.skipNL()
	for !p.at(token.EOF) && p.at(token.KwImport) {
		f.Imports = append(f.Imports, p.parseImport())
		p.endStatement()
		p.skipNL()
	}
	for !p.at(token.EOF) && !p.failed {
		doc := p.takeDoc()
		if p.at(token.At) {
			f.Decls = p.appendDecl(f.Decls, p.parseDecorated(doc)...)
		} else {
			f.Decls = p.appendDecl(f.Decls, p.parseDecl(doc))
		}
		p.endStatement()
		p.skipNL()
	}
	f.Span = ast.Span{Start: start.Pos, End: p.prev().End}
	return f
}

// appendDecl adds one declaration and the methods written inside its type
// body. A method is a top-level declaration after parsing, so every later
// pass sees the same method table a flat receiver declaration would produce.
func (p *Parser) appendDecl(out []ast.Decl, decls ...ast.Decl) []ast.Decl {
	for _, d := range decls {
		out = append(out, d)
		if s, ok := d.(*ast.StructDecl); ok && len(s.Methods) != 0 {
			out = append(out, toDecls(s.Methods)...)
			s.Methods = nil
		}
		if lane, ok := d.(*ast.LaneDecl); ok {
			if s, ok := lane.Decl.(*ast.StructDecl); ok && len(s.Methods) != 0 {
				out = append(out, toDecls(s.Methods)...)
				s.Methods = nil
			}
		}
	}
	return out
}

func toDecls(methods []*ast.FuncDecl) []ast.Decl {
	out := make([]ast.Decl, len(methods))
	for i, m := range methods {
		out[i] = m
	}
	return out
}

func (p *Parser) takeDoc() string {
	var b strings.Builder
	for p.toks[p.pos].Kind == token.DocComment {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.TrimSpace(p.toks[p.pos].Value))
		p.pos++
		for p.toks[p.pos].Kind == token.Newline {
			p.pos++
		}
	}
	return b.String()
}

func (p *Parser) parseImport() *ast.Import {
	start := p.expect(token.KwImport, "import")
	imp := &ast.Import{}
	switch {
	case p.at(token.String):
		imp.Path = p.next().Value
	case p.atText("_"):
		imp.SideEffect = true
		p.next()
		imp.Path = p.expect(token.String, "an import path").Value
	case p.atText("c"):
		imp.C = true
		p.next()
		imp.Path = p.expect(token.String, "a header path").Value
		if p.at(token.LBrace) {
			imp.Links = p.parseLinks()
		}
	case p.at(token.Ident):
		imp.Alias = p.next().Text
		imp.Path = p.expect(token.String, "an import path").Value
	default:
		p.unexpected("an import path")
	}
	imp.Span = p.spanFrom(start)
	return imp
}

func (p *Parser) parseLinks() []*ast.Link {
	p.expect(token.LBrace, "{")
	var links []*ast.Link
	p.skipNL()
	for !p.at(token.RBrace) && !p.at(token.EOF) && !p.failed {
		start := p.cur()
		link := &ast.Link{}
		if p.atText("fast") {
			link.Fast = true
			p.next()
		}
		if p.atText("blocking") {
			link.Blocking = true
			p.next()
		}
		link.Name, _ = p.expectName("a link name")
		link.Span = p.spanFrom(start)
		links = append(links, link)
		p.endStatement()
	}
	p.expect(token.RBrace, "}")
	return links
}

type decorators struct {
	derive []string
	test   bool
}

// parseDecorated reads one or more @decorator lines and applies them to the
// declaration that follows. Only @derive(...) and @test exist.
func (p *Parser) parseDecorated(doc string) []ast.Decl {
	var d decorators
	for p.at(token.At) {
		start := p.next()
		name, nameTok := p.expectName("a decorator name")
		switch name {
		case "derive":
			if !p.at(token.LParen) {
				p.unexpected("(")
				return nil
			}
			p.next()
			for {
				p.skipNL()
				item, _ := p.expectName("a derived interface name")
				d.derive = append(d.derive, item)
				if !p.at(token.Comma) {
					break
				}
				p.next()
			}
			p.expect(token.RParen, ")")
		case "test":
			d.test = true
			if p.at(token.LParen) {
				p.bag.Error(nameTok.Pos, diag.CodeUnexpectedToken, "@test takes no arguments", "remove the parentheses")
			}
		default:
			p.bag.Error(nameTok.Pos, diag.CodeUnexpectedToken, "unknown decorator "+name,
				"use @derive or @test")
		}
		_ = start
		p.skipNL()
	}
	decl := p.parseDecl(doc)
	var out []ast.Decl
	out = append(out, decl)
	switch target := decl.(type) {
	case *ast.FuncDecl:
		target.Test = d.test
		if len(d.derive) != 0 {
			p.bag.Error(decl.Pos(), diag.CodeUnexpectedToken, "@derive needs a struct or enum",
				"move the decorator to the type declaration")
		}
	case *ast.StructDecl:
		for _, name := range d.derive {
			out = append(out, &ast.DeriveDecl{
				Span: ast.Span{Start: target.Start, End: target.End},
				Doc:  doc, Name: name, NamePos: target.NamePos,
				Type: &ast.Type{Span: ast.Span{Start: target.NamePos, End: target.NamePos},
					Kind: ast.NamedType, Name: target.Name},
			})
		}
		if d.test {
			p.bag.Error(decl.Pos(), diag.CodeUnexpectedToken, "@test needs a function",
				"move the decorator to the test function")
		}
	case *ast.EnumDecl:
		for _, name := range d.derive {
			out = append(out, &ast.DeriveDecl{
				Span: ast.Span{Start: target.Start, End: target.End},
				Doc:  doc, Name: name, NamePos: target.NamePos,
				Type: &ast.Type{Span: ast.Span{Start: target.NamePos, End: target.NamePos},
					Kind: ast.NamedType, Name: target.Name},
			})
		}
		if d.test {
			p.bag.Error(decl.Pos(), diag.CodeUnexpectedToken, "@test needs a function",
				"move the decorator to the test function")
		}
	default:
		p.bag.Error(decl.Pos(), diag.CodeUnexpectedToken, "decorator needs a declaration",
			"use @derive on a struct or enum, or @test on a function")
	}
	return out
}

func (p *Parser) parseDecl(doc string) ast.Decl {
	switch p.cur().Kind {
	case token.KwConst, token.KwVar:
		return p.parseValueDecl(doc, false)
	case token.KwDef:
		return p.parseFuncDecl(doc)
	case token.KwAsync:
		return p.parseAsyncFuncDecl(doc)
	case token.KwType:
		return p.parseTypeAliasDecl(doc)
	case token.KwClass:
		return p.parseTypeDecl(doc, true)
	case token.KwStruct:
		return p.parseTypeDecl(doc, false)
	case token.KwEnum:
		return p.parseEnumDecl(doc)
	case token.KwInterface:
		return p.parseInterfaceDecl(doc)
	}
	p.unexpected("a declaration")
	return &ast.VarDecl{Span: p.emptySpan(p.cur())}
}

func (p *Parser) parseValueDecl(doc string, allowZero bool) ast.Decl {
	start := p.next()
	name, nameTok := p.expectName("a name")
	var typ *ast.Type
	if p.at(token.Colon) {
		p.next()
		p.skipNL()
		typ = p.parseType()
	}
	var value ast.Expr
	if start.Kind == token.KwConst {
		p.expect(token.Assign, "=")
		p.skipNL()
		value = p.parseExpr(0, true)
	} else if p.at(token.Assign) {
		p.next()
		p.skipNL()
		value = p.parseExpr(0, true)
	} else if typ == nil {
		p.unexpected("= or :")
	}
	span := p.spanFrom(start)
	if start.Kind == token.KwConst {
		return &ast.ConstDecl{Span: span, Doc: doc, Name: name, NamePos: nameTok.Pos, Type: typ, Value: value}
	}
	return &ast.VarDecl{Span: span, Doc: doc, Name: name, NamePos: nameTok.Pos, Type: typ, Value: value}
}

func (p *Parser) parseFuncDecl(doc string) *ast.FuncDecl {
	return p.parseFuncDeclFrom(doc, p.cur(), false)
}

func (p *Parser) parseAsyncFuncDecl(doc string) *ast.FuncDecl {
	start := p.next()
	return p.parseFuncDeclFrom(doc, start, true)
}

func (p *Parser) parseFuncDeclFrom(doc string, start token.Token, async bool) *ast.FuncDecl {
	p.expect(token.KwDef, "def")
	fn := &ast.FuncDecl{Doc: doc, Async: async}
	name, nameTok := p.expectName("a function name")
	fn.Name = name
	fn.NamePos = nameTok.Pos
	fn.TypeParams = p.parseTypeParams()
	fn.Params = p.parseParams()
	if p.at(token.Arrow) {
		p.next()
		p.skipNL()
		fn.Result = p.parseType()
	}
	fn.Body = p.parseBlock("a function body")
	fn.Span = p.spanFrom(start)
	return fn
}

func (p *Parser) parseParams() []*ast.Param {
	p.expect(token.LParen, "(")
	p.enterGroup()
	p.skipNL()
	var params []*ast.Param
	if !p.at(token.RParen) {
		for {
			params = append(params, p.parseParam())
			if !p.at(token.Comma) {
				break
			}
			p.next()
			p.skipNL()
			if p.at(token.RParen) {
				break
			}
		}
	}
	p.expect(token.RParen, ")")
	p.leaveGroup()
	return params
}

func (p *Parser) parseParam() *ast.Param {
	start := p.cur()
	param := &ast.Param{}
	name, nameTok := p.expectName("a parameter name")
	param.Name = name
	param.NamePos = nameTok.Pos
	p.expect(token.Colon, ":")
	if p.at(token.DotDotDot) {
		param.Variadic = true
		p.next()
	}
	param.Type = p.parseType()
	if p.at(token.Assign) {
		p.next()
		p.skipNL()
		param.Default = p.parseExpr(0, true)
	}
	param.Span = p.spanFrom(start)
	return param
}

func (p *Parser) parseTypeAliasDecl(doc string) *ast.TypeAliasDecl {
	start := p.expect(token.KwType, "type")
	name, nameTok := p.expectName("a type alias name")
	p.expect(token.Assign, "=")
	p.skipNL()
	typ := p.parseType()
	return &ast.TypeAliasDecl{
		Span:    p.spanFrom(start),
		Doc:     doc,
		Name:    name,
		NamePos: nameTok.Pos,
		Type:    typ,
	}
}

func (p *Parser) parseTypeParams() []*ast.TypeParam {
	if !p.at(token.Lt) {
		return nil
	}
	p.next()
	p.skipNL()
	if p.atTypeClose() {
		p.consumeTypeClose()
		return nil
	}
	var params []*ast.TypeParam
	for {
		start := p.cur()
		name, nameTok := p.expectName("a type parameter name")
		tp := &ast.TypeParam{Name: name, NamePos: nameTok.Pos}
		if p.at(token.Colon) {
			p.next()
			p.skipNL()
			for {
				tp.Constraints = append(tp.Constraints, p.parseType())
				if !p.at(token.Plus) {
					break
				}
				p.next()
				p.skipNL()
			}
		}
		tp.Span = p.spanFrom(start)
		params = append(params, tp)
		if !p.at(token.Comma) {
			break
		}
		p.next()
		p.skipNL()
		if p.atTypeClose() {
			break
		}
	}
	p.consumeTypeClose()
	return params
}

func (p *Parser) parseTypeDecl(doc string, class bool) *ast.StructDecl {
	start := p.cur()
	keyword := "struct"
	if class {
		keyword = "class"
	}
	p.expect(start.Kind, keyword)
	s := &ast.StructDecl{Doc: doc, Class: class}
	name, nameTok := p.expectName("a " + keyword + " name")
	s.Name = name
	s.NamePos = nameTok.Pos
	s.TypeParams = p.parseTypeParams()
	p.expect(token.LBrace, "{")
	old := p.ignoreNL
	p.ignoreNL = 0
	p.skipNL()
	for !p.at(token.RBrace) && !p.at(token.EOF) && !p.failed {
		if p.at(token.KwDef) || p.at(token.KwAsync) {
			s.Methods = append(s.Methods, p.parseTypeMethod(s.Name, class, s.TypeParams))
			p.endStatement()
			p.skipNL()
			continue
		}
		fstart := p.cur()
		field := &ast.Field{}
		name, nameTok := p.expectName("a field name")
		field.Name = name
		field.NamePos = nameTok.Pos
		p.expect(token.Colon, ":")
		field.Type = p.parseType()
		if p.at(token.Assign) {
			p.next()
			p.skipNL()
			field.Default = p.parseExpr(0, true)
		}
		field.Span = p.spanFrom(fstart)
		s.Fields = append(s.Fields, field)
		p.endStatement()
		p.skipNL()
	}
	p.expect(token.RBrace, "}")
	p.ignoreNL = old
	s.Span = p.spanFrom(start)
	return s
}

// parseTypeMethod parses a method written inside a type body. The receiver is
// the explicit self parameter; it carries the declared type identity and is
// not repeated in the callable parameter list.
func (p *Parser) parseTypeMethod(typeName string, class bool, typeParams []*ast.TypeParam) *ast.FuncDecl {
	start := p.cur()
	async := false
	if p.at(token.KwAsync) {
		p.next()
		async = true
	}
	p.expect(token.KwDef, "def")
	fn := &ast.FuncDecl{Async: async}
	name, nameTok := p.expectName("a method name")
	fn.Name = name
	fn.NamePos = nameTok.Pos
	fn.TypeParams = p.parseTypeParams()
	p.expect(token.LParen, "(")
	p.enterGroup()
	p.skipNL()
	recvStart := p.cur()
	recvName, recvTok := p.expectName("self")
	recv := &ast.Receiver{Name: recvName, NamePos: recvTok.Pos}
	if p.at(token.Colon) {
		p.next()
		recv.Type = p.parseType()
	} else {
		recv.Type = &ast.Type{Span: ast.Span{Start: recvStart.Pos, End: recvTok.End},
			Kind: ast.NamedType, Name: typeName}
		// A method of a generic type carries the type parameters in its
		// receiver type, so the checker and the emitter specialize the
		// receiver exactly like a written Box<T>.
		for _, tp := range typeParams {
			recv.Type.Args = append(recv.Type.Args, &ast.Type{
				Span: ast.Span{Start: tp.NamePos, End: tp.NamePos},
				Kind: ast.NamedType, Name: tp.Name})
		}
	}
	if recvName != "self" {
		p.bag.Error(recvTok.Pos, diag.CodeUnexpectedToken,
			"a method starts with self", "write self as the first parameter")
	}
	if !p.at(token.RParen) {
		if !p.at(token.Comma) {
			p.unexpected(",")
		} else {
			p.next()
			p.skipNL()
		}
	}
	for !p.at(token.RParen) && !p.at(token.EOF) && !p.failed {
		fn.Params = append(fn.Params, p.parseParam())
		if !p.at(token.Comma) {
			break
		}
		p.next()
		p.skipNL()
	}
	p.expect(token.RParen, ")")
	p.leaveGroup()
	recv.Span = ast.Span{Start: recvStart.Pos, End: recvTok.End}
	fn.Recv = recv
	if p.at(token.Arrow) {
		p.next()
		p.skipNL()
		fn.Result = p.parseType()
	}
	fn.Body = p.parseBlock("a method body")
	fn.Span = p.spanFrom(start)
	return fn
}

func (p *Parser) parseEnumDecl(doc string) *ast.EnumDecl {
	start := p.expect(token.KwEnum, "enum")
	e := &ast.EnumDecl{Doc: doc}
	name, nameTok := p.expectName("an enum name")
	e.Name = name
	e.NamePos = nameTok.Pos
	e.TypeParams = p.parseTypeParams()
	p.expect(token.LBrace, "{")
	old := p.ignoreNL
	p.ignoreNL = 0
	p.skipNL()
	for !p.at(token.RBrace) && !p.at(token.EOF) && !p.failed {
		vstart := p.cur()
		variant := &ast.Variant{}
		name, nameTok := p.expectName("a variant name")
		variant.Name = name
		variant.NamePos = nameTok.Pos
		if p.at(token.LParen) {
			variant.Args = p.parseTypeList()
		}
		variant.Span = p.spanFrom(vstart)
		e.Variants = append(e.Variants, variant)
		p.endStatement()
	}
	p.expect(token.RBrace, "}")
	p.ignoreNL = old
	e.Span = p.spanFrom(start)
	return e
}

func (p *Parser) parseTypeList() []*ast.Type {
	p.expect(token.LParen, "(")
	p.enterGroup()
	p.skipNL()
	var types []*ast.Type
	if !p.at(token.RParen) {
		for {
			types = append(types, p.parseType())
			if !p.at(token.Comma) {
				break
			}
			p.next()
			p.skipNL()
			if p.at(token.RParen) {
				break
			}
		}
	}
	p.expect(token.RParen, ")")
	p.leaveGroup()
	return types
}

func (p *Parser) parseInterfaceDecl(doc string) *ast.InterfaceDecl {
	start := p.expect(token.KwInterface, "interface")
	iface := &ast.InterfaceDecl{Doc: doc}
	name, nameTok := p.expectName("an interface name")
	iface.Name = name
	iface.NamePos = nameTok.Pos
	iface.TypeParams = p.parseTypeParams()
	p.expect(token.LBrace, "{")
	old := p.ignoreNL
	p.ignoreNL = 0
	p.skipNL()
	for !p.at(token.RBrace) && !p.at(token.EOF) && !p.failed {
		iface.Methods = append(iface.Methods, p.parseMethodSig())
		p.endStatement()
	}
	p.expect(token.RBrace, "}")
	p.ignoreNL = old
	iface.Span = p.spanFrom(start)
	return iface
}

func (p *Parser) parseMethodSig() *ast.MethodSig {
	start := p.cur()
	async := false
	if p.at(token.KwAsync) {
		p.next()
		async = true
	}
	p.expect(token.KwDef, "def")
	sig := &ast.MethodSig{Async: async}
	name, nameTok := p.expectName("a method name")
	sig.Name = name
	sig.NamePos = nameTok.Pos
	sig.Params = p.parseSignatureParams()
	if p.at(token.Arrow) {
		p.next()
		p.skipNL()
		sig.Result = p.parseType()
	}
	sig.Span = p.spanFrom(start)
	return sig
}

// parseFromSources reads the optional from clause after a return type. Each
// source is a place expression, so a field path stays attached to its root.
// parseSignatureParams reads an interface method's parameter list. The
// receiver is written as self for an interface that documents it, but it is
// not part of the callable parameter list.
func (p *Parser) parseSignatureParams() []*ast.Param {
	p.expect(token.LParen, "(")
	p.enterGroup()
	p.skipNL()
	if p.atText("self") && (p.peekRaw(1).Kind == token.Comma || p.peekRaw(1).Kind == token.RParen) {
		p.next()
		if !p.at(token.RParen) {
			p.next()
			p.skipNL()
		}
	}
	var params []*ast.Param
	for !p.at(token.RParen) && !p.at(token.EOF) && !p.failed {
		params = append(params, p.parseParam())
		if !p.at(token.Comma) {
			break
		}
		p.next()
		p.skipNL()
	}
	p.expect(token.RParen, ")")
	p.leaveGroup()
	return params
}

func (p *Parser) parseDeriveDecl(doc string) *ast.DeriveDecl {
	start := p.expect(token.KwDerive, "derive")
	d := &ast.DeriveDecl{Doc: doc}
	name, nameTok := p.expectName("a derived interface name")
	d.Name = name
	d.NamePos = nameTok.Pos
	p.expect(token.KwFor, "for")
	d.Type = p.parseType()
	d.Span = p.spanFrom(start)
	return d
}

func (p *Parser) parseLaneDecl() *ast.LaneDecl {
	start := p.expect(token.KwLane, "lane")
	l := &ast.LaneDecl{}
	name, nameTok := p.expectName("a lane name")
	l.Name = name
	l.NamePos = nameTok.Pos
	switch p.cur().Kind {
	case token.KwClass:
		l.Decl = p.parseTypeDecl("", true)
	case token.KwStruct:
		l.Decl = p.parseTypeDecl("", false)
	case token.KwEnum:
		l.Decl = p.parseEnumDecl("")
	case token.KwInterface:
		l.Decl = p.parseInterfaceDecl("")
	default:
		p.unexpected("a type declaration")
	}
	l.Span = p.spanFrom(start)
	return l
}
