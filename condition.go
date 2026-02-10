package deep

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/brunoga/deep/internal/unsafe"
)

// Condition represents a logical check against a value of type T.
type Condition[T any] interface {
	Evaluate(v *T) (bool, error)
}

// Path represents a path to a field or element within a structure.
// Syntax: "Field", "Field.SubField", "Slice[0]", "Map.Key", "Ptr.Field".
type Path string

// resolve traverses v using the path and returns the reflect.Value found.
func (p Path) resolve(v reflect.Value) (reflect.Value, error) {
	current, err := dereference(v)
	if err != nil {
		return reflect.Value{}, err
	}

	parts := parsePath(string(p))
	for _, part := range parts {
		if !current.IsValid() {
			return reflect.Value{}, fmt.Errorf("path traversal failed: nil value at intermediate step")
		}

		if part.isIndex {
			if current.Kind() != reflect.Slice && current.Kind() != reflect.Array {
				return reflect.Value{}, fmt.Errorf("cannot index into %v", current.Type())
			}
			if part.index < 0 || part.index >= current.Len() {
				return reflect.Value{}, fmt.Errorf("index out of bounds: %d", part.index)
			}
			current = current.Index(part.index)
		} else if current.Kind() == reflect.Map {
			keyType := current.Type().Key()
			var keyVal reflect.Value
			if keyType.Kind() == reflect.String {
				keyVal = reflect.ValueOf(part.key)
			} else if keyType.Kind() == reflect.Int {
				i, err := strconv.Atoi(part.key)
				if err != nil {
					return reflect.Value{}, fmt.Errorf("invalid int key: %s", part.key)
				}
				keyVal = reflect.ValueOf(i)
			} else {
				return reflect.Value{}, fmt.Errorf("unsupported map key type for path: %v", keyType)
			}

			val := current.MapIndex(keyVal)
			if !val.IsValid() {
				return reflect.Value{}, nil
			}
			current = val
		} else {
			if current.Kind() != reflect.Struct {
				return reflect.Value{}, fmt.Errorf("cannot access field %s on %v", part.key, current.Type())
			}

			// We use FieldByName and disableRO to support unexported fields.
			f := current.FieldByName(part.key)
			if !f.IsValid() {
				return reflect.Value{}, fmt.Errorf("field %s not found", part.key)
			}
			unsafe.DisableRO(&f)
			current = f
		}

		current, err = dereference(current)
		if err != nil {
			return reflect.Value{}, err
		}
	}
	return current, nil
}

func dereference(v reflect.Value) (reflect.Value, error) {
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}, fmt.Errorf("path traversal failed: nil pointer/interface")
		}
		v = v.Elem()
	}
	return v, nil
}

func compareValues(v1, v2 reflect.Value, op string) (bool, error) {
	if !v1.IsValid() || !v2.IsValid() {
		switch op {
		case "==":
			return !v1.IsValid() && !v2.IsValid(), nil
		case "!=":
			return v1.IsValid() != v2.IsValid(), nil
		default:
			return false, nil
		}
	}

	v2 = convertValue(v2, v1.Type())

	if op == "==" {
		return reflect.DeepEqual(v1.Interface(), v2.Interface()), nil
	}
	if op == "!=" {
		return !reflect.DeepEqual(v1.Interface(), v2.Interface()), nil
	}

	if v1.Kind() != v2.Kind() {
		return false, fmt.Errorf("type mismatch: %v and %v", v1.Type(), v2.Type())
	}

	switch v1.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return compareOrdered(v1.Int(), v2.Int(), op)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return compareOrdered(v1.Uint(), v2.Uint(), op)
	case reflect.Float32, reflect.Float64:
		return compareOrdered(v1.Float(), v2.Float(), op)
	case reflect.String:
		return compareOrdered(v1.String(), v2.String(), op)
	}
	return false, fmt.Errorf("unsupported comparison %s for kind %v", op, v1.Kind())
}

func compareOrdered[T int64 | uint64 | float64 | string](a, b T, op string) (bool, error) {
	switch op {
	case ">":
		return a > b, nil
	case "<":
		return a < b, nil
	case ">=":
		return a >= b, nil
	case "<=":
		return a <= b, nil
	default:
		return false, fmt.Errorf("unsupported operator: %s", op)
	}
}

type pathPart struct {
	key     string
	index   int
	isIndex bool
}

func parsePath(path string) []pathPart {
	var parts []pathPart
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			parts = append(parts, pathPart{key: buf.String()})
			buf.Reset()
		}
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch c {
		case '.':
			flush()
		case '[':
			flush()
			start := i + 1
			for i < len(path) && path[i] != ']' {
				i++
			}
			if i < len(path) {
				content := path[start:i]
				idx, err := strconv.Atoi(content)
				if err == nil {
					parts = append(parts, pathPart{index: idx, isIndex: true})
				}
			}
		default:
			buf.WriteByte(c)
		}
	}
	flush()
	return parts
}

// rawCondition is the internal non-generic interface for conditions.
type rawCondition interface {
	Evaluate(v any) (bool, error)
	paths() []Path
	withRelativePaths(prefix string) rawCondition
}

func toReflectValue(v any) reflect.Value {
	if rv, ok := v.(reflect.Value); ok {
		return rv
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	return rv
}

type rawCompareCondition struct {
	Path Path
	Val  any
	Op   string
}

func (c *rawCompareCondition) Evaluate(v any) (bool, error) {
	rv := toReflectValue(v)
	target, err := c.Path.resolve(rv)
	if err != nil {
		return false, err
	}
	return compareValues(target, reflect.ValueOf(c.Val), c.Op)
}

func (c *rawCompareCondition) paths() []Path {
	return []Path{c.Path}
}

func (c *rawCompareCondition) withRelativePaths(prefix string) rawCondition {
	return &rawCompareCondition{
		Path: Path(strings.TrimPrefix(strings.TrimPrefix(string(c.Path), prefix), ".")),
		Val:  c.Val,
		Op:   c.Op,
	}
}

type rawCompareFieldCondition struct {
	Path1 Path
	Path2 Path
	Op    string
}

func (c *rawCompareFieldCondition) Evaluate(v any) (bool, error) {
	rv := toReflectValue(v)
	target1, err := c.Path1.resolve(rv)
	if err != nil {
		return false, err
	}
	target2, err := c.Path2.resolve(rv)
	if err != nil {
		return false, err
	}
	return compareValues(target1, target2, c.Op)
}

func (c *rawCompareFieldCondition) paths() []Path {
	return []Path{c.Path1, c.Path2}
}

func (c *rawCompareFieldCondition) withRelativePaths(prefix string) rawCondition {
	return &rawCompareFieldCondition{
		Path1: Path(strings.TrimPrefix(strings.TrimPrefix(string(c.Path1), prefix), ".")),
		Path2: Path(strings.TrimPrefix(strings.TrimPrefix(string(c.Path2), prefix), ".")),
		Op:    c.Op,
	}
}

type rawAndCondition struct {
	Conditions []rawCondition
}

func (c *rawAndCondition) Evaluate(v any) (bool, error) {
	for _, sub := range c.Conditions {
		ok, err := sub.Evaluate(v)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (c *rawAndCondition) paths() []Path {
	var res []Path
	for _, sub := range c.Conditions {
		res = append(res, sub.paths()...)
	}
	return res
}

func (c *rawAndCondition) withRelativePaths(prefix string) rawCondition {
	res := &rawAndCondition{Conditions: make([]rawCondition, len(c.Conditions))}
	for i, sub := range c.Conditions {
		res.Conditions[i] = sub.withRelativePaths(prefix)
	}
	return res
}

type rawOrCondition struct {
	Conditions []rawCondition
}

func (c *rawOrCondition) Evaluate(v any) (bool, error) {
	for _, sub := range c.Conditions {
		ok, err := sub.Evaluate(v)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (c *rawOrCondition) paths() []Path {
	var res []Path
	for _, sub := range c.Conditions {
		res = append(res, sub.paths()...)
	}
	return res
}

func (c *rawOrCondition) withRelativePaths(prefix string) rawCondition {
	res := &rawOrCondition{Conditions: make([]rawCondition, len(c.Conditions))}
	for i, sub := range c.Conditions {
		res.Conditions[i] = sub.withRelativePaths(prefix)
	}
	return res
}

type rawNotCondition struct {
	C rawCondition
}

func (c *rawNotCondition) Evaluate(v any) (bool, error) {
	ok, err := c.C.Evaluate(v)
	if err != nil {
		return false, err
	}
	return !ok, nil
}

func (c *rawNotCondition) paths() []Path {
	return c.C.paths()
}

func (c *rawNotCondition) withRelativePaths(prefix string) rawCondition {
	return &rawNotCondition{C: c.C.withRelativePaths(prefix)}
}

type CompareCondition[T any] struct {
	Path Path
	Val  any
	Op   string
}

func (c CompareCondition[T]) Evaluate(v *T) (bool, error) {
	raw := &rawCompareCondition{Path: c.Path, Val: c.Val, Op: c.Op}
	return raw.Evaluate(v)
}

func Equal[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: "=="}
}

func NotEqual[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: "!="}
}

func Greater[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: ">"}
}

func Less[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: "<"}
}

func GreaterEqual[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: ">="}
}

func LessEqual[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: "<="}
}

type CompareFieldCondition[T any] struct {
	Path1 Path
	Path2 Path
	Op    string
}

func (c CompareFieldCondition[T]) Evaluate(v *T) (bool, error) {
	raw := &rawCompareFieldCondition{Path1: c.Path1, Path2: c.Path2, Op: c.Op}
	return raw.Evaluate(v)
}

func EqualField[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: "=="}
}

func NotEqualField[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: "!="}
}

func GreaterField[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: ">"}
}

func LessField[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: "<"}
}

func GreaterEqualField[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: ">="}
}

func LessEqualField[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: "<="}
}

type AndCondition[T any] struct {
	Conditions []Condition[T]
}

func (c AndCondition[T]) Evaluate(v *T) (bool, error) {
	for _, sub := range c.Conditions {
		ok, err := sub.Evaluate(v)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func And[T any](conds ...Condition[T]) Condition[T] {
	return AndCondition[T]{Conditions: conds}
}

type OrCondition[T any] struct {
	Conditions []Condition[T]
}

func (c OrCondition[T]) Evaluate(v *T) (bool, error) {
	for _, sub := range c.Conditions {
		ok, err := sub.Evaluate(v)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func Or[T any](conds ...Condition[T]) Condition[T] {
	return OrCondition[T]{Conditions: conds}
}

type NotCondition[T any] struct {
	C Condition[T]
}

func (c NotCondition[T]) Evaluate(v *T) (bool, error) {
	ok, err := c.C.Evaluate(v)
	if err != nil {
		return false, err
	}
	return !ok, nil
}

func Not[T any](c Condition[T]) Condition[T] {
	return NotCondition[T]{C: c}
}

// typedRawCondition wraps a rawCondition to satisfy Condition[T].
type typedRawCondition[T any] struct {
	raw rawCondition
}

func (c *typedRawCondition[T]) Evaluate(v *T) (bool, error) {
	return c.raw.Evaluate(v)
}

// ParseCondition parses a string expression into a Condition[T] tree.
func ParseCondition[T any](expr string) (Condition[T], error) {
	raw, err := parseRawCondition(expr)
	if err != nil {
		return nil, err
	}
	return &typedRawCondition[T]{raw: raw}, nil
}

func parseRawCondition(expr string) (rawCondition, error) {
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
	case isAlpha(c):
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
	for l.pos < len(l.input) && (isAlpha(l.input[l.pos]) || isDigit(l.input[l.pos]) || l.input[l.pos] == '.' || l.input[l.pos] == '[' || l.input[l.pos] == ']') {
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

func (p *parser) parseExpr() (rawCondition, error) {
	return p.parseOr()
}

func (p *parser) parseOr() (rawCondition, error) {
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
		left = &rawOrCondition{Conditions: []rawCondition{left, right}}
	}
	return left, nil
}

func (p *parser) parseAnd() (rawCondition, error) {
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
		left = &rawAndCondition{Conditions: []rawCondition{left, right}}
	}
	return left, nil
}

func (p *parser) parseFactor() (rawCondition, error) {
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

func (p *parser) parseComparison() (rawCondition, error) {
	path := p.curr.val
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
			f, _ := strconv.ParseFloat(valTok.val, 64)
			val = f
		} else {
			i, _ := strconv.ParseInt(valTok.val, 10, 64)
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
		return &rawCompareFieldCondition{Path1: Path(path), Path2: Path(fieldPath), Op: opStr}, nil
	}
	return &rawCompareCondition{Path: Path(path), Val: val, Op: opStr}, nil
}

func init() {
	gob.Register(&condSurrogate{})
}

type condSurrogate struct {
	Kind string `json:"k" gob:"k"`
	Data any    `json:"d,omitempty" gob:"d,omitempty"`
}

func marshalCondition[T any](c Condition[T]) (any, error) {
	return marshalConditionAny(c)
}

func marshalConditionAny(c any) (any, error) {
	if c == nil {
		return nil, nil
	}

	// Use reflection to extract the underlying fields regardless of T.
	v := reflect.ValueOf(c)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	// We can't use type switches easily because of the generic T.
	// We use the Type name and Field access instead.
	typeName := v.Type().Name()
	if strings.HasPrefix(typeName, "CompareCondition") {
		op := v.FieldByName("Op").String()
		kind := "compare"
		if op == "==" {
			kind = "equal"
		} else if op == "!=" {
			kind = "not_equal"
		}
		return &condSurrogate{
			Kind: kind,
			Data: map[string]any{
				"p": v.FieldByName("Path").String(),
				"v": v.FieldByName("Val").Interface(),
				"o": op,
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "CompareFieldCondition") {
		op := v.FieldByName("Op").String()
		kind := "compare_field"
		if op == "==" {
			kind = "equal_field"
		} else if op == "!=" {
			kind = "not_equal_field"
		}
		return &condSurrogate{
			Kind: kind,
			Data: map[string]any{
				"p1": v.FieldByName("Path1").String(),
				"p2": v.FieldByName("Path2").String(),
				"o":  op,
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "AndCondition") {
		condsVal := v.FieldByName("Conditions")
		conds := make([]any, 0, condsVal.Len())
		for i := 0; i < condsVal.Len(); i++ {
			s, err := marshalConditionAny(condsVal.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			conds = append(conds, s)
		}
		return &condSurrogate{
			Kind: "and",
			Data: conds,
		}, nil
	}
	if strings.HasPrefix(typeName, "OrCondition") {
		condsVal := v.FieldByName("Conditions")
		conds := make([]any, 0, condsVal.Len())
		for i := 0; i < condsVal.Len(); i++ {
			s, err := marshalConditionAny(condsVal.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			conds = append(conds, s)
		}
		return &condSurrogate{
			Kind: "or",
			Data: conds,
		}, nil
	}
	if strings.HasPrefix(typeName, "NotCondition") {
		sub, err := marshalConditionAny(v.FieldByName("C").Interface())
		if err != nil {
			return nil, err
		}
		return &condSurrogate{
			Kind: "not",
			Data: sub,
		}, nil
	}

	return nil, fmt.Errorf("unknown condition type: %T", c)
}

func unmarshalCondition[T any](data []byte) (Condition[T], error) {
	var s condSurrogate
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return convertFromCondSurrogate[T](&s)
}

func convertFromCondSurrogate[T any](s any) (Condition[T], error) {
	if s == nil {
		return nil, nil
	}

	var kind string
	var data any

	switch v := s.(type) {
	case *condSurrogate:
		kind = v.Kind
		data = v.Data
	case map[string]any:
		kind = v["k"].(string)
		data = v["d"]
	default:
		return nil, fmt.Errorf("invalid condition surrogate type: %T", s)
	}

	switch kind {
	case "equal":
		d := data.(map[string]any)
		return CompareCondition[T]{Path: Path(d["p"].(string)), Val: d["v"], Op: "=="}, nil
	case "not_equal":
		d := data.(map[string]any)
		return CompareCondition[T]{Path: Path(d["p"].(string)), Val: d["v"], Op: "!="}, nil
	case "compare":
		d := data.(map[string]any)
		return CompareCondition[T]{Path: Path(d["p"].(string)), Val: d["v"], Op: d["o"].(string)}, nil
	case "equal_field":
		d := data.(map[string]any)
		return CompareFieldCondition[T]{Path1: Path(d["p1"].(string)), Path2: Path(d["p2"].(string)), Op: "=="}, nil
	case "not_equal_field":
		d := data.(map[string]any)
		return CompareFieldCondition[T]{Path1: Path(d["p1"].(string)), Path2: Path(d["p2"].(string)), Op: "!="}, nil
	case "compare_field":
		d := data.(map[string]any)
		return CompareFieldCondition[T]{Path1: Path(d["p1"].(string)), Path2: Path(d["p2"].(string)), Op: d["o"].(string)}, nil
	case "and":
		d := data.([]any)
		conds := make([]Condition[T], 0, len(d))
		for _, subData := range d {
			sub, err := convertFromCondSurrogate[T](subData)
			if err != nil {
				return nil, err
			}
			conds = append(conds, sub)
		}
		return AndCondition[T]{Conditions: conds}, nil
	case "or":
		d := data.([]any)
		conds := make([]Condition[T], 0, len(d))
		for _, subData := range d {
			sub, err := convertFromCondSurrogate[T](subData)
			if err != nil {
				return nil, err
			}
			conds = append(conds, sub)
		}
		return OrCondition[T]{Conditions: conds}, nil
	case "not":
		sub, err := convertFromCondSurrogate[T](data)
		if err != nil {
			return nil, err
		}
		return NotCondition[T]{C: sub}, nil
	}

	return nil, fmt.Errorf("unknown condition kind: %s", kind)
}
