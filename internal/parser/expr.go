package parser

import (
	"github.com/will-lang/will/internal/ast"
	"github.com/will-lang/will/internal/diag"
	"github.com/will-lang/will/internal/source"
	"github.com/will-lang/will/internal/token"
)

const (
	precOr    = 1
	precAnd   = 2
	precCmp   = 3
	precBOr   = 4
	precBXor  = 5
	precBAnd  = 6
	precShift = 7
	precAdd   = 8
	precMul   = 9
	precPow   = 10
)

func binaryPrec(k token.Kind) int {
	switch k {
	case token.OrOr:
		return precOr
	case token.AndAnd:
		return precAnd
	case token.Eq, token.Ne, token.Lt, token.Le, token.Gt, token.Ge:
		return precCmp
	case token.Pipe:
		return precBOr
	case token.Caret:
		return precBXor
	case token.Amp:
		return precBAnd
	case token.Shl, token.Shr, token.ShlWrap:
		return precShift
	case token.Plus, token.Minus, token.PlusWrap, token.MinusWrap, token.PlusSat, token.MinusSat:
		return precAdd
	case token.Star, token.Slash, token.FloorDiv, token.Percent, token.StarWrap, token.StarSat:
		return precMul
	case token.Pow:
		return precPow
	}
	return 0
}

func (p *Parser) parseExpr(minPrec int, allowBrace bool) ast.Expr {
	left := p.parseUnary(allowBrace)
	for !p.failed {
		prec := binaryPrec(p.cur().Kind)
		if prec == 0 || prec < minPrec {
			break
		}
		op := p.next()
		p.skipNL()
		nextPrec := prec + 1
		if op.Kind == token.Pow {
			nextPrec = prec
		}
		right := p.parseExpr(nextPrec, allowBrace)
		span := ast.Span{Start: left.Pos(), End: right.EndPos()}
		left = &ast.BinaryExpr{Span: span, Op: op.Kind, L: left, R: right}
	}
	return left
}

func (p *Parser) parseUnary(allowBrace bool) ast.Expr {
	if p.atText("ref") || p.atText("mut") {
		tok := p.next()
		p.skipNL()
		x := p.parseUnary(allowBrace)
		return &ast.BorrowExpr{
			Span: ast.Span{Start: tok.Pos, End: x.EndPos()},
			Mut:  tok.Text == "mut",
			X:    x,
		}
	}
	switch p.cur().Kind {
	case token.Minus, token.KwNot, token.Tilde:
		op := p.next()
		p.skipNL()
		x := p.parseUnary(allowBrace)
		return &ast.UnaryExpr{
			Span: ast.Span{Start: op.Pos, End: x.EndPos()},
			Op:   op.Kind,
			X:    x,
		}
	}
	return p.parsePostfix(allowBrace)
}

func (p *Parser) parsePostfix(allowBrace bool) ast.Expr {
	return p.parsePostfixUntil(allowBrace, false)
}

// parseAwaitOperand parses the operand of await. It accepts calls, fields,
// indexes, and type arguments, but stops before ? and ! so the suffix applies
// to the awaited value, not to the call inside await.
func (p *Parser) parseAwaitOperand(allowBrace bool) ast.Expr {
	return p.parsePostfixUntil(allowBrace, true)
}

func (p *Parser) parsePostfixUntil(allowBrace, stopWaitSuffix bool) ast.Expr {
	x := p.parsePrimary(allowBrace)
	for !p.failed {
		if allowBrace && p.at(token.LBrace) {
			name, pos, ok := aggregateName(x)
			if !ok {
				break
			}
			x = p.parseAggregate(name, pos, nil)
			continue
		}
		switch p.cur().Kind {
		case token.LParen:
			x = p.parseCall(x, nil)
		case token.LBracket:
			if name, ok := x.(*ast.NameExpr); ok && isContainerCtor(name.Name) {
				if end, ok := p.bracketArgsEnd(p.pos); ok &&
					end+1 < len(p.toks) && p.toks[end+1].Kind == token.LParen {
					typeArgs := p.parseBracketTypeArgs()
					x = p.parseCall(x, typeArgs)
					continue
				}
			}
			x = p.parseIndex(x)
		case token.Dot:
			x = p.parseFieldOrMethod(x)
		case token.Question:
			if stopWaitSuffix {
				return x
			}
			tok := p.next()
			x = &ast.TryExpr{Span: ast.Span{Start: x.Pos(), End: tok.End}, X: x}
		case token.Bang:
			if stopWaitSuffix {
				return x
			}
			tok := p.next()
			x = &ast.UnwrapExpr{Span: ast.Span{Start: x.Pos(), End: tok.End}, X: x}
		case token.Lt:
			next, handled := p.parseTypeArgsPostfix(x, allowBrace)
			if !handled {
				return x
			}
			x = next
		default:
			return x
		}
	}
	return x
}

func (p *Parser) parseTypeArgsPostfix(x ast.Expr, allowBrace bool) (ast.Expr, bool) {
	end, ok := p.typeArgsEnd(p.pos)
	if !ok || end+1 >= len(p.toks) {
		return x, false
	}
	next := p.toks[end+1].Kind
	if next != token.LParen && !(allowBrace && next == token.LBrace) {
		return x, false
	}
	typeArgs := p.parseTypeArgsInExpr()
	if p.at(token.LParen) {
		return p.parseCall(x, typeArgs), true
	}
	name, pos, ok := aggregateName(x)
	if !ok {
		p.unexpected("a type name")
		return x, true
	}
	return p.parseAggregate(name, pos, typeArgs), true
}

// aggregateName accepts a bare name or a package-qualified name in front of an
// aggregate literal.
func aggregateName(x ast.Expr) (string, source.Pos, bool) {
	switch v := x.(type) {
	case *ast.NameExpr:
		return v.Name, v.Pos(), true
	case *ast.FieldExpr:
		recv, ok := v.Recv.(*ast.NameExpr)
		if !ok {
			return "", source.Pos{}, false
		}
		return recv.Name + "." + v.Name, recv.Pos(), true
	}
	return "", source.Pos{}, false
}

func (p *Parser) parseFieldOrMethod(x ast.Expr) ast.Expr {
	p.next()
	p.skipNL()
	name, nameTok := p.expectName("a field name")
	var typeArgs []*ast.Type
	if p.at(token.Lt) {
		if end, ok := p.typeArgsEnd(p.pos); ok && end+1 < len(p.toks) && p.toks[end+1].Kind == token.LParen {
			typeArgs = p.parseTypeArgsInExpr()
		}
	}
	if p.at(token.LParen) {
		args := p.parseCallArgs()
		return &ast.MethodCallExpr{
			Span:     ast.Span{Start: x.Pos(), End: p.prev().End},
			Recv:     x,
			Name:     name,
			NamePos:  nameTok.Pos,
			TypeArgs: typeArgs,
			Args:     args,
		}
	}
	if len(typeArgs) > 0 {
		p.unexpected("(")
	}
	return &ast.FieldExpr{
		Span:    ast.Span{Start: x.Pos(), End: p.prev().End},
		Recv:    x,
		Name:    name,
		NamePos: nameTok.Pos,
	}
}

func (p *Parser) parseAggregate(name string, pos source.Pos, typeArgs []*ast.Type) ast.Expr {
	p.expect(token.LBrace, "{")
	p.enterGroup()
	p.skipNL()
	var fields []*ast.AggregateField
	for !p.at(token.RBrace) && !p.at(token.EOF) && !p.failed {
		start := p.cur()
		field := &ast.AggregateField{}
		field.Name, _ = p.expectName("a field name")
		p.expect(token.Colon, ":")
		p.skipNL()
		field.Value = p.parseExpr(0, true)
		field.Span = p.spanFrom(start)
		fields = append(fields, field)
		if !p.at(token.Comma) {
			break
		}
		p.next()
		p.skipNL()
	}
	p.expect(token.RBrace, "}")
	p.leaveGroup()
	return &ast.AggregateExpr{
		Span:     ast.Span{Start: pos, End: p.prev().End},
		TypeName: name,
		TypeArgs: typeArgs,
		Fields:   fields,
	}
}

func (p *Parser) parsePrimary(allowBrace bool) ast.Expr {
	tok := p.cur()
	switch tok.Kind {
	case token.Int, token.Float, token.Duration, token.Char, token.String, token.Bytes:
		p.next()
		return &ast.LiteralExpr{Span: p.emptySpan(tok), Kind: tok.Kind, Text: tok.Text, Value: tok.Value}
	case token.FString:
		p.next()
		return p.parseInterp(tok)
	case token.KwTrue, token.KwFalse:
		p.next()
		return &ast.LiteralExpr{Span: p.emptySpan(tok), Kind: tok.Kind, Text: tok.Text}
	case token.KwNone:
		p.next()
		return &ast.NameExpr{Span: p.emptySpan(tok), Name: "None"}
	case token.KwAwait:
		p.next()
		inner := p.parseAwaitOperand(allowBrace)
		return &ast.AwaitExpr{
			Span: ast.Span{Start: tok.Pos, End: inner.Pos()},
			X:    inner,
		}
	case token.Ident:
		p.next()
		if tok.Text == "new" && startsType(p.cur().Kind) {
			return p.parseNew(tok)
		}
		return &ast.NameExpr{Span: p.emptySpan(tok), Name: tok.Text}
	case token.KwIf:
		return p.parseIf()
	case token.KwMatch:
		return p.parseMatch()
	case token.KwSpawn:
		return p.parseSpawn()
	case token.KwDeadline:
		return p.parseDeadline()
	case token.KwFunc:
		return p.parseLambda()
	case token.LBracket:
		return p.parseArrayLiteral()
	case token.LParen:
		if p.peekRaw(1).Kind == token.RParen {
			open := p.next()
			close := p.next()
			return &ast.UnitExpr{Span: ast.Span{Start: open.Pos, End: close.End}}
		}
		p.next()
		p.enterGroup()
		p.skipNL()
		x := p.parseExpr(0, true)
		p.expect(token.RParen, ")")
		p.leaveGroup()
		return x
	}
	p.unexpected("an expression")
	return &ast.NameExpr{Span: p.emptySpan(tok)}
}

// parseArrayLiteral parses one fixed-array literal. The explicit element type
// before the brace makes the element type and the literal length unambiguous.
func (p *Parser) parseArrayLiteral() ast.Expr {
	start := p.cur()
	typ := p.parseArrayType()
	if typ.Kind != ast.ArrayType {
		p.unexpected("a fixed array literal")
		return &ast.ArrayLitExpr{Span: p.spanFrom(start), Type: typ}
	}
	p.expect(token.LBrace, "{")
	p.enterGroup()
	p.skipNL()
	lit := &ast.ArrayLitExpr{Type: typ}
	for !p.at(token.RBrace) && !p.at(token.EOF) && !p.failed {
		lit.Elems = append(lit.Elems, p.parseExpr(0, true))
		if !p.at(token.Comma) {
			break
		}
		p.next()
		p.skipNL()
	}
	p.expect(token.RBrace, "}")
	p.leaveGroup()
	lit.Span = p.spanFrom(start)
	return lit
}

func startsType(k token.Kind) bool {
	return k == token.Ident || k == token.LBracket || k == token.KwFunc
}

func (p *Parser) parseNew(start token.Token) ast.Expr {
	typ := p.parseType()
	args := p.parseCallArgs()
	return &ast.NewExpr{
		Span: ast.Span{Start: start.Pos, End: p.prev().End},
		Type: typ,
		Args: args,
	}
}

func (p *Parser) parseLaneHere() ast.Expr {
	start := p.next()
	p.expect(token.Dot, ".")
	name := p.cur()
	if name.Kind != token.Ident || name.Text != "here" {
		p.unexpected("lane.here()")
		return &ast.LaneHereExpr{Span: p.spanFrom(start)}
	}
	p.next()
	p.expect(token.LParen, "(")
	p.expect(token.RParen, ")")
	return &ast.LaneHereExpr{Span: p.spanFrom(start)}
}

func (p *Parser) parseIf() *ast.IfExpr {
	start := p.next()
	p.skipNL()
	cond := p.parseExpr(0, false)
	then := p.parseBlock("a block")
	save := p.pos
	p.skipNL()
	var els ast.Stmt
	if p.at(token.KwElse) {
		p.next()
		p.skipNL()
		if p.at(token.KwIf) {
			els = p.parseIf()
		} else {
			els = p.parseBlock("a block")
		}
	} else {
		p.pos = save
	}
	return &ast.IfExpr{
		Span: ast.Span{Start: start.Pos, End: p.prev().End},
		Cond: cond,
		Then: then,
		Else: els,
	}
}

func (p *Parser) parseMatch() *ast.MatchExpr {
	start := p.next()
	p.skipNL()
	subject := p.parseExpr(0, false)
	p.expect(token.LBrace, "{")
	old := p.ignoreNL
	p.ignoreNL = 0
	m := &ast.MatchExpr{Subject: subject}
	p.skipNL()
	for !p.at(token.RBrace) && !p.at(token.EOF) && !p.failed {
		m.Arms = append(m.Arms, p.parseMatchArm())
		p.endStatement()
	}
	p.expect(token.RBrace, "}")
	p.ignoreNL = old
	m.Span = p.spanFrom(start)
	return m
}

func (p *Parser) parseMatchArm() *ast.MatchArm {
	start := p.cur()
	arm := &ast.MatchArm{Pattern: p.parsePattern()}
	if p.at(token.KwIf) {
		p.next()
		p.skipNL()
		arm.Guard = p.parseExpr(0, true)
	}
	p.expect(token.FatArrow, "=>")
	arm.Body = p.parseArmBody()
	arm.Span = p.spanFrom(start)
	return arm
}

func (p *Parser) parseSpawn() ast.Expr {
	start := p.next()
	call := p.parsePostfix(true)
	if !isCall(call) {
		/* The concurrency volume fixes this shape as K0005: the operand of a
		   spawn is one call, and anything else is reported here, at the
		   spawn, not as a generic syntax error. */
		p.bag.Error(call.Pos(), diag.CodeSpawnNotCall,
			"spawn takes a single call",
			"write spawn F(x), or spawn (func() { ... })()")
		p.failed = true
	}
	return &ast.SpawnExpr{
		Span: ast.Span{Start: start.Pos, End: call.EndPos()},
		Call: call,
	}
}

func (p *Parser) parseDeadline() *ast.DeadlineExpr {
	start := p.next()
	p.skipNL()
	duration := p.parseExpr(0, false)
	body := p.parseBlock("a deadline body")
	return &ast.DeadlineExpr{
		Span:     ast.Span{Start: start.Pos, End: p.prev().End},
		Duration: duration,
		Body:     body,
	}
}

func (p *Parser) parseLambda() ast.Expr {
	start := p.expect(token.KwFunc, "func")
	params := p.parseParams()
	var result *ast.Type
	if p.at(token.Arrow) {
		p.next()
		p.skipNL()
		result = p.parseType()
	}
	body := p.parseBlock("a function body")
	return &ast.LambdaExpr{
		Span:   ast.Span{Start: start.Pos, End: p.prev().End},
		Params: params,
		Result: result,
		Body:   body,
	}
}
