package deep

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseCondition parses a string expression into a Condition[T] tree.
func ParseCondition[T any](expr string) (Condition[T], error) {
	raw, err := parseRawCondition(expr)
	if err != nil {
		return nil, err
	}
	return &typedRawCondition[T]{raw: raw}, nil
}

func parseRawCondition(expr string) (internalConditionImpl, error) {
	p := &parser{lexer: newLexer(expr)}
	p.next()
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.curr.kind != tokEOF {
		return nil, fmt.Errorf("unexpected token: %v", p.curr.val)
	}
	return cond, nil
}

type tokenKind int

const (
	tokError tokenKind = iota
	tokEOF
	tokIdent
	tokString
	tokNumber
	tokBool
	tokEq
	tokNeq
	tokGt
	tokLt
	tokGte
	tokLte
	tokAnd
	tokOr
	tokNot
	tokLParen
	tokRParen
)

type token struct {
	kind tokenKind
	val  string
}

type lexer struct {
	input string
	pos   int
}

func newLexer(input string) *lexer {
	return &lexer{input: input}
}

func (l *lexer) next() token {
	l.skipWhitespace()
	if l.pos >= len(l.input) {
		return token{kind: tokEOF}
	}
	c := l.input[l.pos]
	switch {
	case c == '(':
		l.pos++
		return token{kind: tokLParen, val: "("}
	case c == ')':
		l.pos++
		return token{kind: tokRParen, val: ")"}
	case c == '=' && l.peek() == '=':
		l.pos += 2
		return token{kind: tokEq, val: "=="}
	case c == '!' && l.peek() == '=':
		l.pos += 2
		return token{kind: tokNeq, val: "!="}
	case c == '>' && l.peek() == '=':
		l.pos += 2
		return token{kind: tokGte, val: ">="}
	case c == '>':
		l.pos++
		return token{kind: tokGt, val: ">"}
	case c == '<' && l.peek() == '=':
		l.pos += 2
		return token{kind: tokLte, val: "<="}
	case c == '<':
		l.pos++
		return token{kind: tokLt, val: "<"}
	case c == '\'' || c == '"':
		return l.lexString(c)
	case isDigit(c):
		return l.lexNumber()
	case isAlpha(c) || c == '/':
		return l.lexIdent()
	}
	return token{kind: tokError, val: string(c)}
}

func (l *lexer) peek() byte {
	if l.pos+1 < len(l.input) {
		return l.input[l.pos+1]
	}
	return 0
}

func (l *lexer) skipWhitespace() {
	for l.pos < len(l.input) && isWhitespace(l.input[l.pos]) {
		l.pos++
	}
}

func (l *lexer) lexString(quote byte) token {
	l.pos++
	start := l.pos
	for l.pos < len(l.input) && l.input[l.pos] != quote {
		l.pos++
	}
	val := l.input[start:l.pos]
	if l.pos < len(l.input) {
		l.pos++
	}
	return token{kind: tokString, val: val}
}

func (l *lexer) lexNumber() token {
	start := l.pos
	for l.pos < len(l.input) && (isDigit(l.input[l.pos]) || l.input[l.pos] == '.') {
		l.pos++
	}
	return token{kind: tokNumber, val: l.input[start:l.pos]}
}

func (l *lexer) lexIdent() token {
	start := l.pos
	for l.pos < len(l.input) && (isAlpha(l.input[l.pos]) || isDigit(l.input[l.pos]) || l.input[l.pos] == '.' || l.input[l.pos] == '[' || l.input[l.pos] == ']' || l.input[l.pos] == '/' || l.input[l.pos] == '~') {
		l.pos++
	}
	val := l.input[start:l.pos]
	upper := strings.ToUpper(val)
	switch upper {
	case "AND":
		return token{kind: tokAnd, val: val}
	case "OR":
		return token{kind: tokOr, val: val}
	case "NOT":
		return token{kind: tokNot, val: val}
	case "TRUE":
		return token{kind: tokBool, val: "true"}
	case "FALSE":
		return token{kind: tokBool, val: "false"}
	}
	return token{kind: tokIdent, val: val}
}

func isWhitespace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool      { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' }

type parser struct {
	lexer *lexer
	curr  token
}

func (p *parser) next() {
	p.curr = p.lexer.next()
}

func (p *parser) parseExpr() (internalConditionImpl, error) {
	return p.parseOr()
}

func (p *parser) parseOr() (internalConditionImpl, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.curr.kind == tokOr {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &rawOrCondition{Conditions: []internalConditionImpl{left, right}}
	}
	return left, nil
}

func (p *parser) parseAnd() (internalConditionImpl, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for p.curr.kind == tokAnd {
		p.next()
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		left = &rawAndCondition{Conditions: []internalConditionImpl{left, right}}
	}
	return left, nil
}

func (p *parser) parseFactor() (internalConditionImpl, error) {
	switch p.curr.kind {
	case tokNot:
		p.next()
		cond, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return &rawNotCondition{C: cond}, nil
	case tokLParen:
		p.next()
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.curr.kind != tokRParen {
			return nil, fmt.Errorf("expected ')', got %v", p.curr.val)
		}
		p.next()
		return cond, nil
	case tokIdent:
		return p.parseComparison()
	}
	return nil, fmt.Errorf("unexpected token: %v", p.curr.val)
}

func (p *parser) parseComparison() (internalConditionImpl, error) {
	condPath := p.curr.val
	p.next()
	opTok := p.curr
	if opTok.kind < tokEq || opTok.kind > tokLte {
		return nil, fmt.Errorf("expected comparison operator, got %v", opTok.val)
	}
	p.next()
	valTok := p.curr
	var val any
	var isField bool
	var fieldPath string

	switch valTok.kind {
	case tokString:
		val = valTok.val
	case tokNumber:
		if strings.Contains(valTok.val, ".") {
			f, err := strconv.ParseFloat(valTok.val, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid float literal: %s", valTok.val)
			}
			val = f
		} else {
			i, err := strconv.ParseInt(valTok.val, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid integer literal: %s", valTok.val)
			}
			val = int(i)
		}
	case tokBool:
		val = (valTok.val == "true")
	case tokIdent:
		isField = true
		fieldPath = valTok.val
	default:
		return nil, fmt.Errorf("expected value or field, got %v", valTok.val)
	}
	p.next()

	ops := map[tokenKind]string{
		tokEq: "==", tokNeq: "!=", tokGt: ">", tokLt: "<", tokGte: ">=", tokLte: "<=",
	}
	opStr := ops[opTok.kind]

	if isField {
		return &rawCompareFieldCondition{Path1: deepPath(condPath), Path2: deepPath(fieldPath), Op: opStr}, nil
	}
	return &rawCompareCondition{Path: deepPath(condPath), Val: val, Op: opStr}, nil
}
