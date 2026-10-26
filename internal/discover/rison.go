// Package discover builds Kibana Discover links: the RISON text Kibana keeps its
// app state in, and the URL of a Discover page that shows the documents of one
// Elasticsearch query over one time range. Nothing here sends a request.
package discover

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RISON (https://github.com/Nanonid/rison) is JSON with fewer characters to escape
// in a URL: objects are (k:v,...), arrays !(a,...), true, false and null are !t,
// !f and !n, and a string is bare when it is an "id", else quoted with ' and with
// ' and ! escaped as !' and !!. Kibana reads _g and _a of a Discover URL as RISON.

// Object is a RISON object whose keys keep the order given. A Go map is encoded
// with its keys sorted, so the same value always gives the same text.
type Object []Field

// Field is one key and value of an Object.
type Field struct {
	Key   string
	Value any
}

// notIDChar are the characters an id never holds, and notIDStart those it never
// starts with (rison.js not_idchar and not_idstart).
const (
	notIDChar  = " '!:(),*@$"
	notIDStart = "-0123456789"
)

// isID reports a string RISON may write bare: not empty, none of notIDChar, not
// starting with a digit or '-', as rison.js's id_ok. Control characters are quoted
// too (stricter than rison.js, and still valid RISON), so a bare id never holds one.
func isID(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if strings.ContainsRune(notIDChar, r) || r < 0x20 || r == 0x7f {
			return false
		}
		if i == 0 && strings.ContainsRune(notIDStart, r) {
			return false
		}
	}
	return true
}

// Encode returns the RISON text of v. It takes nil, bool, string, the number
// types, json.Number, map[string]any (keys sorted), Object (keys in order), []any,
// []map[string]any and []string; any other value is first converted through JSON
// (encoding/json's rules), so a struct encodes as its JSON object would.
func Encode(v any) (string, error) {
	var b strings.Builder
	if err := encode(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

func encode(b *strings.Builder, v any) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("!n")
	case bool:
		if x {
			b.WriteString("!t")
		} else {
			b.WriteString("!f")
		}
	case string:
		encodeString(b, x)
	case json.Number:
		n, err := risonNumber(string(x))
		if err != nil {
			return err
		}
		b.WriteString(n)
	case float64:
		return encodeFloat(b, x)
	case float32:
		return encodeFloat(b, float64(x))
	case int:
		b.WriteString(strconv.FormatInt(int64(x), 10))
	case int8:
		b.WriteString(strconv.FormatInt(int64(x), 10))
	case int16:
		b.WriteString(strconv.FormatInt(int64(x), 10))
	case int32:
		b.WriteString(strconv.FormatInt(int64(x), 10))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case uint:
		b.WriteString(strconv.FormatUint(uint64(x), 10))
	case uint8:
		b.WriteString(strconv.FormatUint(uint64(x), 10))
	case uint16:
		b.WriteString(strconv.FormatUint(uint64(x), 10))
	case uint32:
		b.WriteString(strconv.FormatUint(uint64(x), 10))
	case uint64:
		b.WriteString(strconv.FormatUint(x, 10))
	case Object:
		b.WriteByte('(')
		for i, f := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeString(b, f.Key)
			b.WriteByte(':')
			if err := encode(b, f.Value); err != nil {
				return err
			}
		}
		b.WriteByte(')')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		obj := make(Object, 0, len(keys))
		for _, k := range keys {
			obj = append(obj, Field{k, x[k]})
		}
		return encode(b, obj)
	case []any:
		b.WriteString("!(")
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := encode(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(')')
	case []map[string]any:
		list := make([]any, len(x))
		for i, e := range x {
			list[i] = e
		}
		return encode(b, list)
	case []string:
		list := make([]any, len(x))
		for i, e := range x {
			list[i] = e
		}
		return encode(b, list)
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			return fmt.Errorf("rison: %w", err)
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		var generic any
		if err := dec.Decode(&generic); err != nil {
			return fmt.Errorf("rison: %w", err)
		}
		return encode(b, generic)
	}
	return nil
}

func encodeString(b *strings.Builder, s string) {
	if isID(s) {
		b.WriteString(s)
		return
	}
	b.WriteByte('\'')
	for _, r := range s {
		if r == '\'' || r == '!' {
			b.WriteByte('!')
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
}

func encodeFloat(b *strings.Builder, f float64) error {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return errors.New("rison: a number must be finite")
	}
	format := byte('g')
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		format = 'f'
	}
	n, err := risonNumber(strconv.FormatFloat(f, format, -1, 64))
	if err != nil {
		return err
	}
	b.WriteString(n)
	return nil
}

// numberPattern is a JSON number; risonNumber drops the exponent's '+', which
// RISON does not accept.
var numberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func risonNumber(s string) (string, error) {
	if !numberPattern.MatchString(s) {
		return "", fmt.Errorf("rison: %q is not a number", s)
	}
	return strings.ToLower(strings.Replace(s, "+", "", 1)), nil
}

// Decode reads RISON text back: objects as map[string]any, arrays as []any,
// numbers as float64. kibana-cli only writes RISON; Decode lets the tests check
// that a link holds exactly what was meant (Parse).
func Decode(s string) (any, error) {
	p := &parser{s: s}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	if p.i != len(p.s) {
		return nil, fmt.Errorf("rison: extra text at %d", p.i)
	}
	return v, nil
}

type parser struct {
	s string
	i int
}

func (p *parser) errorf(format string, args ...any) error {
	return fmt.Errorf("rison at %d: %s", p.i, fmt.Sprintf(format, args...))
}

func (p *parser) value() (any, error) {
	if p.i >= len(p.s) {
		return nil, p.errorf("unexpected end")
	}
	switch c := p.s[p.i]; {
	case c == '!':
		if p.i+1 >= len(p.s) {
			return nil, p.errorf("lone !")
		}
		p.i += 2
		switch p.s[p.i-1] {
		case 't':
			return true, nil
		case 'f':
			return false, nil
		case 'n':
			return nil, nil
		case '(':
			return p.array()
		}
		return nil, p.errorf("unknown !%c", p.s[p.i-1])
	case c == '(':
		p.i++
		return p.object()
	case c == '\'':
		return p.quoted()
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	default:
		return p.id()
	}
}

func (p *parser) array() (any, error) {
	out := []any{}
	if p.i < len(p.s) && p.s[p.i] == ')' {
		p.i++
		return out, nil
	}
	for {
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		if p.i >= len(p.s) {
			return nil, p.errorf("unclosed array")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ')':
			p.i++
			return out, nil
		default:
			return nil, p.errorf("expected , or ) in an array")
		}
	}
}

func (p *parser) object() (any, error) {
	out := map[string]any{}
	if p.i < len(p.s) && p.s[p.i] == ')' {
		p.i++
		return out, nil
	}
	for {
		var key any
		var err error
		if p.i < len(p.s) && p.s[p.i] == '\'' {
			key, err = p.quoted()
		} else {
			key, err = p.id()
		}
		if err != nil {
			return nil, err
		}
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, p.errorf("expected : after a key")
		}
		p.i++
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out[key.(string)] = v
		if p.i >= len(p.s) {
			return nil, p.errorf("unclosed object")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ')':
			p.i++
			return out, nil
		default:
			return nil, p.errorf("expected , or ) in an object")
		}
	}
}

func (p *parser) quoted() (any, error) {
	p.i++ // the opening '
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch c {
		case '\'':
			p.i++
			return b.String(), nil
		case '!':
			if p.i+1 >= len(p.s) || (p.s[p.i+1] != '\'' && p.s[p.i+1] != '!') {
				return nil, p.errorf("bad escape in a string")
			}
			b.WriteByte(p.s[p.i+1])
			p.i += 2
		default:
			b.WriteByte(c)
			p.i++
		}
	}
	return nil, p.errorf("unclosed string")
}

func (p *parser) id() (any, error) {
	start := p.i
	for p.i < len(p.s) && !strings.ContainsRune(notIDChar, rune(p.s[p.i])) {
		p.i++
	}
	s := p.s[start:p.i]
	if s == "" || strings.ContainsRune(notIDStart, rune(s[0])) {
		return nil, p.errorf("expected a value")
	}
	return s, nil
}

var risonNumberPattern = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([eE]-?[0-9]+)?`)

func (p *parser) number() (any, error) {
	m := risonNumberPattern.FindString(p.s[p.i:])
	if m == "" {
		return nil, p.errorf("bad number")
	}
	p.i += len(m)
	return strconv.ParseFloat(m, 64)
}
