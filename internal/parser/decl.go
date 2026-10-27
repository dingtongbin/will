package parser

import (
	"strings"

	"github.com/will-lang/will/internal/ast"
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
		f.Decls = append(f.Decls, p.parseDecl(doc))
		p.endStatement()
		p.skipNL()
	}
	f.Span = ast.Span{Start: start.Pos, End: p.prev().End}
	return f
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
	case token.KwStruct:
		return p.parseStructDecl(doc)
	case token.KwEnum:
		return p.parseEnumDecl(doc)
	case token.KwInterface:
		return p.parseInterfaceDecl(doc)
	case token.KwDerive:
		return p.parseDeriveDecl(doc)
	case token.KwLane:
		return p.parseLaneDecl()
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
	if p.at(token.LParen) {
		fn.Recv = p.parseReceiver()
	}
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

func (p *Parser) parseReceiver() *ast.Receiver {
	start := p.expect(token.LParen, "(")
	p.enterGroup()
	p.skipNL()
	recv := &ast.Receiver{}
	if p.atText("mut") {
		recv.Mut = true
		p.next()
	}
	name, nameTok := p.expectName("a receiver name")
	recv.Name = name
	recv.NamePos = nameTok.Pos
	p.expect(token.Colon, ":")
	recv.Type = p.parseType()
	p.expect(token.RParen, ")")
	p.leaveGroup()
	recv.Span = p.spanFrom(start)
	return recv
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
	if p.atText("mut") {
		param.Mut = true
		p.next()
	}
	name, nameTok := p.expectName("a parameter name")
	param.Name = name
	param.NamePos = nameTok.Pos
	p.expect(token.Colon, ":")
	if p.at(token.DotDotDot) {
		param.Variadic = true
		p.next()
	}
	param.Type = p.parseType()
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

func (p *Parser) parseStructDecl(doc string) *ast.StructDecl {
	start := p.expect(token.KwStruct, "struct")
	s := &ast.StructDecl{Doc: doc}
	name, nameTok := p.expectName("a struct name")
	s.Name = name
	s.NamePos = nameTok.Pos
	s.TypeParams = p.parseTypeParams()
	p.expect(token.LBrace, "{")
	old := p.ignoreNL
	p.ignoreNL = 0
	p.skipNL()
	for !p.at(token.RBrace) && !p.at(token.EOF) && !p.failed {
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
	}
	p.expect(token.RBrace, "}")
	p.ignoreNL = old
	s.Span = p.spanFrom(start)
	return s
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
	sig.Params = p.parseParams()
	if p.at(token.Arrow) {
		p.next()
		p.skipNL()
		sig.Result = p.parseType()
	}
	sig.Span = p.spanFrom(start)
	return sig
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
	case token.KwStruct:
		l.Decl = p.parseStructDecl("")
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
