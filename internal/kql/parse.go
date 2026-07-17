// Package kql parses the Kibana Query Language subset documented for Kibana 7.10
// and compiles it to Elasticsearch query DSL.
package kql

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const wildcardMarker = "\x00"

var numberPattern = regexp.MustCompile(`^(-?[1-9]+\d*(?:\.\d+)?)$|^(-?0\.\d*[1-9]+)$|^0$|^0\.0$|^\.\d+$`)

type nodeKind uint8

const (
	nodeTerm nodeKind = iota
	nodeRange
	nodeAnd
	nodeOr
	nodeNot
	nodeNested
)

type node struct {
	kind  nodeKind
	pos   int
	field *literal
	value literal
	op    string
	left  *node
	right *node
	child *node
}

type literal struct {
	encoded  string
	text     string
	value    any
	quoted   bool
	wildcard bool
	pos      int
}

type syntaxError struct {
	position int
	message  string
}

func (e *syntaxError) Error() string {
	return fmt.Sprintf("KQL syntax error at position %d: %s", e.position+1, e.message)
}

// Parse converts a KQL expression into Elasticsearch query DSL. Invalid or
// unsupported syntax returns an error; input is never reinterpreted as Lucene.
func Parse(query string) (map[string]any, error) {
	if trimSpace(query) == "" {
		return map[string]any{"match_all": map[string]any{}}, nil
	}

	p := parser{input: query}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if !p.eof() {
		return nil, p.errorf(p.pos, "unexpected %q", p.input[p.pos:])
	}

	return compileNode(root, "")
}

type parser struct {
	input string
	pos   int
}

func (p *parser) parseOr() (*node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	if !p.consumeBinary("or") {
		return left, nil
	}
	right, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	return &node{kind: nodeOr, pos: left.pos, left: left, right: right}, nil
}

func (p *parser) parseAnd() (*node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	if !p.consumeBinary("and") {
		return left, nil
	}
	right, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	return &node{kind: nodeAnd, pos: left.pos, left: left, right: right}, nil
}

func (p *parser) parseNot() (*node, error) {
	p.skipSpace()
	start := p.pos
	if !p.consumeNot() {
		return p.parsePrimary()
	}
	child, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	return &node{kind: nodeNot, pos: start, child: child}, nil
}

func (p *parser) parsePrimary() (*node, error) {
	p.skipSpace()
	if p.eof() {
		return nil, p.errorf(p.pos, "expected an expression")
	}

	start := p.pos
	switch p.input[p.pos] {
	case '(':
		p.pos++
		p.skipSpace()
		if p.eof() || p.input[p.pos] == ')' {
			return nil, p.errorf(p.pos, "empty parenthesized expression")
		}
		child, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.eof() || p.input[p.pos] != ')' {
			return nil, p.errorf(p.pos, "expected ')'")
		}
		p.pos++
		return child, nil
	case ')', '}':
		return nil, p.errorf(start, "unexpected %q", p.input[start:start+1])
	case '{':
		return nil, p.errorf(start, "a nested query requires a path before '{'")
	default:
		return p.parseExpression()
	}
}

func (p *parser) parseExpression() (*node, error) {
	first, err := p.parseLiteral()
	if err != nil {
		return nil, err
	}
	if err := validateExpressionLiteral(first); err != nil {
		return nil, err
	}

	literalEnd := p.pos
	next := p.skipSpaceFrom(literalEnd)
	if next >= len(p.input) {
		return &node{kind: nodeTerm, pos: first.pos, value: first}, nil
	}

	switch p.input[next] {
	case ':':
		p.pos = next + 1
		if err := validateField(first); err != nil {
			return nil, err
		}
		return p.parseFieldExpression(first)
	case '<', '>':
		p.pos = next
		if err := validateField(first); err != nil {
			return nil, err
		}
		return p.parseRangeExpression(first)
	default:
		p.pos = literalEnd
		return &node{kind: nodeTerm, pos: first.pos, value: first}, nil
	}
}

func (p *parser) parseFieldExpression(field literal) (*node, error) {
	p.skipSpace()
	if p.eof() {
		return nil, p.errorf(p.pos, "expected a value after ':'")
	}

	switch p.input[p.pos] {
	case '{':
		if field.wildcard {
			return nil, p.errorf(field.pos, "nested paths cannot contain wildcards")
		}
		start := field.pos
		p.pos++
		p.skipSpace()
		if p.eof() || p.input[p.pos] == '}' {
			return nil, p.errorf(p.pos, "empty nested expression")
		}
		child, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.eof() || p.input[p.pos] != '}' {
			return nil, p.errorf(p.pos, "expected '}'")
		}
		p.pos++
		return &node{kind: nodeNested, pos: start, field: &field, child: child}, nil
	case '(':
		p.pos++
		p.skipSpace()
		if p.eof() || p.input[p.pos] == ')' {
			return nil, p.errorf(p.pos, "empty field value list")
		}
		child, err := p.parseValueOr(field)
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.eof() || p.input[p.pos] != ')' {
			return nil, p.errorf(p.pos, "expected ')' after field value list")
		}
		p.pos++
		return child, nil
	default:
		value, err := p.parseLiteral()
		if err != nil {
			return nil, err
		}
		return &node{kind: nodeTerm, pos: field.pos, field: &field, value: value}, nil
	}
}

func (p *parser) parseRangeExpression(field literal) (*node, error) {
	start := p.pos
	op := ""
	switch {
	case strings.HasPrefix(p.input[p.pos:], ">="):
		op = "gte"
		p.pos += 2
	case strings.HasPrefix(p.input[p.pos:], "<="):
		op = "lte"
		p.pos += 2
	case p.input[p.pos] == '>':
		op = "gt"
		p.pos++
	case p.input[p.pos] == '<':
		op = "lt"
		p.pos++
	}

	if field.wildcard {
		return nil, p.errorf(field.pos, "ranges on wildcard fields require data-view field metadata")
	}
	value, err := p.parseLiteral()
	if err != nil {
		return nil, err
	}
	if value.wildcard {
		return nil, p.errorf(value.pos, "range values cannot contain wildcards")
	}
	return &node{kind: nodeRange, pos: start, field: &field, value: value, op: op}, nil
}

func (p *parser) parseValueOr(field literal) (*node, error) {
	left, err := p.parseValueAnd(field)
	if err != nil {
		return nil, err
	}
	if !p.consumeBinary("or") {
		return left, nil
	}
	right, err := p.parseValueOr(field)
	if err != nil {
		return nil, err
	}
	return &node{kind: nodeOr, pos: left.pos, left: left, right: right}, nil
}

func (p *parser) parseValueAnd(field literal) (*node, error) {
	left, err := p.parseValueNot(field)
	if err != nil {
		return nil, err
	}
	if !p.consumeBinary("and") {
		return left, nil
	}
	right, err := p.parseValueAnd(field)
	if err != nil {
		return nil, err
	}
	return &node{kind: nodeAnd, pos: left.pos, left: left, right: right}, nil
}

func (p *parser) parseValueNot(field literal) (*node, error) {
	p.skipSpace()
	start := p.pos
	if !p.consumeNot() {
		return p.parseValuePrimary(field)
	}
	child, err := p.parseValuePrimary(field)
	if err != nil {
		return nil, err
	}
	return &node{kind: nodeNot, pos: start, child: child}, nil
}

func (p *parser) parseValuePrimary(field literal) (*node, error) {
	p.skipSpace()
	if p.eof() {
		return nil, p.errorf(p.pos, "expected a field value")
	}
	if p.input[p.pos] == '(' {
		p.pos++
		p.skipSpace()
		if p.eof() || p.input[p.pos] == ')' {
			return nil, p.errorf(p.pos, "empty field value group")
		}
		child, err := p.parseValueOr(field)
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.eof() || p.input[p.pos] != ')' {
			return nil, p.errorf(p.pos, "expected ')' in field value list")
		}
		p.pos++
		return child, nil
	}
	if p.input[p.pos] == ')' {
		return nil, p.errorf(p.pos, "expected a field value")
	}

	value, err := p.parseLiteral()
	if err != nil {
		return nil, err
	}
	return &node{kind: nodeTerm, pos: value.pos, field: &field, value: value}, nil
}

func (p *parser) parseLiteral() (literal, error) {
	p.skipSpace()
	if p.eof() {
		return literal{}, p.errorf(p.pos, "expected a literal")
	}
	if p.input[p.pos] == '"' {
		return p.parseQuoted()
	}
	return p.parseUnquoted()
}

func (p *parser) parseQuoted() (literal, error) {
	start := p.pos
	p.pos++
	var value strings.Builder
	for !p.eof() {
		c := p.input[p.pos]
		if c == '"' {
			p.pos++
			text := value.String()
			return literal{encoded: text, text: text, value: text, quoted: true, pos: start}, nil
		}
		if c != '\\' {
			value.WriteByte(c)
			p.pos++
			continue
		}

		if p.pos+1 >= len(p.input) {
			return literal{}, p.errorf(start, "unterminated quoted string")
		}
		next := p.input[p.pos+1]
		switch next {
		case '\\', '"':
			value.WriteByte(next)
			p.pos += 2
		case 't':
			value.WriteByte('\t')
			p.pos += 2
		case 'r':
			value.WriteByte('\r')
			p.pos += 2
		case 'n':
			value.WriteByte('\n')
			p.pos += 2
		default:
			// Kibana treats unknown backslash sequences inside quotes literally.
			value.WriteByte('\\')
			p.pos++
		}
	}
	return literal{}, p.errorf(start, "unterminated quoted string")
}

func (p *parser) parseUnquoted() (literal, error) {
	start := p.pos
	if p.notAt(start) {
		return literal{}, p.errorf(start, "NOT here requires a parenthesized field value list")
	}

	var encoded strings.Builder
	for !p.eof() {
		c := p.input[p.pos]
		if isSpace(c) {
			if p.binaryAt(p.pos, "and") || p.binaryAt(p.pos, "or") {
				break
			}
			encoded.WriteByte(c)
			p.pos++
			continue
		}
		if p.pos > start && p.notAt(p.pos) {
			break
		}

		switch c {
		case '(', ')', ':', '<', '>', '"', '{', '}':
			goto done
		case '\\':
			if err := p.parseEscape(&encoded); err != nil {
				return literal{}, err
			}
		case '*':
			encoded.WriteString(wildcardMarker)
			p.pos++
		case '?', '~', '^', '[', ']':
			return literal{}, p.errorf(p.pos, "unsupported Lucene syntax %q", string(c))
		case 0:
			return literal{}, p.errorf(p.pos, "NUL is not allowed")
		default:
			if (c == '&' || c == '|') && p.pos+1 < len(p.input) && p.input[p.pos+1] == c {
				return literal{}, p.errorf(p.pos, "unsupported Lucene operator %q", p.input[p.pos:p.pos+2])
			}
			encoded.WriteByte(c)
			p.pos++
		}
	}

done:
	raw := trimSpace(encoded.String())
	if raw == "" {
		return literal{}, p.errorf(start, "expected an unquoted literal")
	}
	text := strings.ReplaceAll(raw, wildcardMarker, "*")
	if strings.HasPrefix(text, "/") && strings.HasSuffix(text, "/") && len(text) > 1 {
		return literal{}, p.errorf(start, "regular expressions are Lucene syntax and are not supported")
	}

	value := any(text)
	wildcard := strings.Contains(raw, wildcardMarker)
	if !wildcard {
		value = primitiveValue(text)
	}
	return literal{encoded: raw, text: text, value: value, wildcard: wildcard, pos: start}, nil
}

func (p *parser) parseEscape(encoded *strings.Builder) error {
	start := p.pos
	p.pos++
	if p.eof() {
		return p.errorf(start, "trailing backslash")
	}

	switch p.input[p.pos] {
	case 't':
		encoded.WriteByte('\t')
		p.pos++
		return nil
	case 'r':
		encoded.WriteByte('\r')
		p.pos++
		return nil
	case 'n':
		encoded.WriteByte('\n')
		p.pos++
		return nil
	case '\\', '(', ')', ':', '<', '>', '"', '*', '{', '}':
		encoded.WriteByte(p.input[p.pos])
		p.pos++
		return nil
	}

	for _, keyword := range []string{"and", "not", "or"} {
		if p.equalFoldAt(p.pos, keyword) {
			encoded.WriteString(p.input[p.pos : p.pos+len(keyword)])
			p.pos += len(keyword)
			return nil
		}
	}
	return p.errorf(start, "unsupported escape sequence")
}

func (p *parser) consumeBinary(keyword string) bool {
	start := p.pos
	if p.eof() || !isSpace(p.input[p.pos]) {
		return false
	}
	i := p.skipSpaceFrom(p.pos)
	if !p.equalFoldAt(i, keyword) {
		return false
	}
	i += len(keyword)
	if i >= len(p.input) || !isSpace(p.input[i]) {
		return false
	}
	p.pos = p.skipSpaceFrom(i)
	return p.pos != start
}

func (p *parser) consumeNot() bool {
	if !p.notAt(p.pos) {
		return false
	}
	p.pos += len("not")
	p.pos = p.skipSpaceFrom(p.pos)
	return true
}

func (p *parser) binaryAt(pos int, keyword string) bool {
	if pos >= len(p.input) || !isSpace(p.input[pos]) {
		return false
	}
	i := p.skipSpaceFrom(pos)
	if !p.equalFoldAt(i, keyword) {
		return false
	}
	i += len(keyword)
	return i < len(p.input) && isSpace(p.input[i])
}

func (p *parser) notAt(pos int) bool {
	if !p.equalFoldAt(pos, "not") {
		return false
	}
	end := pos + len("not")
	return end < len(p.input) && isSpace(p.input[end])
}

func (p *parser) equalFoldAt(pos int, value string) bool {
	return pos >= 0 && pos+len(value) <= len(p.input) && strings.EqualFold(p.input[pos:pos+len(value)], value)
}

func (p *parser) skipSpace() {
	p.pos = p.skipSpaceFrom(p.pos)
}

func (p *parser) skipSpaceFrom(pos int) int {
	for pos < len(p.input) && isSpace(p.input[pos]) {
		pos++
	}
	return pos
}

func (p *parser) eof() bool {
	return p.pos >= len(p.input)
}

func (p *parser) errorf(pos int, format string, args ...any) error {
	return &syntaxError{position: pos, message: fmt.Sprintf(format, args...)}
}

func validateExpressionLiteral(value literal) error {
	if value.quoted || len(value.text) < 2 {
		return nil
	}
	if value.text[0] == '+' || value.text[0] == '!' || (value.text[0] == '-' && !numberPattern.MatchString(value.text)) {
		return &syntaxError{position: value.pos, message: "Lucene unary operators are not supported"}
	}
	return nil
}

func validateField(field literal) error {
	if field.text == "" {
		return &syntaxError{position: field.pos, message: "field names cannot be empty"}
	}
	if strings.EqualFold(field.text, "_exists_") {
		return &syntaxError{position: field.pos, message: "use field:* instead of Lucene _exists_ syntax"}
	}
	return validateExpressionLiteral(field)
}

func primitiveValue(value string) any {
	switch value {
	case "null":
		return nil
	case "true":
		return true
	case "false":
		return false
	}
	if numberPattern.MatchString(value) {
		if number, err := strconv.ParseFloat(value, 64); err == nil {
			return number
		}
	}
	return value
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func trimSpace(value string) string {
	return strings.Trim(value, " \t\r\n")
}
