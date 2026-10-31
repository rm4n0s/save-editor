package jsondoc

import (
	"math"
	"math/big"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

const (
	tagSyntax  = "JsonSyntax"
	tagTooDeep = "JsonTooDeep"
)

// chunk is how many values, sites, spans and numbers the parser allocates at once.
const chunk = 512

type parser struct {
	data     []byte
	i        int
	depth    int
	maxDepth int
	minInt   *big.Int
	maxInt   *big.Int
	doc      *Doc

	// stack holds the children of the containers being read; a container takes its part
	// from the top when it ends.
	stack []model.Child

	values []model.Value
	sites  []model.Site
	spans  []Span
	nums   []big.Int
	scrap  []byte
}

func (p *parser) newValue() *model.Value {
	if len(p.values) == 0 {
		p.values = make([]model.Value, chunk)
	}
	v := &p.values[0]
	p.values = p.values[1:]
	return v
}

// newSlot makes a value and its site, with the span of the token from start to the
// parser's position.
func (p *parser) newSlot(kind model.Kind, start int) (*model.Value, *model.Site) {
	if len(p.sites) == 0 {
		p.sites = make([]model.Site, chunk)
		p.spans = make([]Span, chunk)
	}
	s := &p.sites[0]
	p.sites = p.sites[1:]
	sp := &p.spans[0]
	p.spans = p.spans[1:]
	*sp = Span{Start: start, End: p.i, Kind: kind}
	s.ID = len(p.doc.sites)
	s.Ref = sp
	if kind == model.KindInt {
		s.IntMin, s.IntMax = p.minInt, p.maxInt
	}
	p.doc.sites = append(p.doc.sites, s)
	v := p.newValue()
	v.Kind = kind
	return v, s
}

func (p *parser) newInt() *big.Int {
	if len(p.nums) == 0 {
		p.nums = make([]big.Int, chunk)
	}
	n := &p.nums[0]
	p.nums = p.nums[1:]
	return n
}

// where is the position of the parser as a line and a column, for an error message.
func (p *parser) where() string {
	line, col := 1, 1
	for _, c := range p.data[:min(p.i, len(p.data))] {
		if c == '\n' {
			line++
			col = 1
		} else if c&0xC0 != 0x80 {
			col++
		}
	}
	return "line " + strconv.Itoa(line) + ", column " + strconv.Itoa(col)
}

func (p *parser) skipSpace() {
	for p.i < len(p.data) {
		switch p.data[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) parseDocument() error {
	if len(p.data) >= 3 && p.data[0] == 0xEF && p.data[1] == 0xBB && p.data[2] == 0xBF {
		p.i = 3
	}
	p.skipSpace()
	if p.i >= len(p.data) {
		return errors.New(tagSyntax, "The file has no JSON value", "at", p.where())
	}
	root, err := p.parseValue()
	if err != nil {
		return err
	}
	p.skipSpace()
	if p.i < len(p.data) {
		return errors.New(tagSyntax, "Unexpected "+describe(p.data[p.i])+" after the JSON value",
			"at", p.where(), "offset", p.i)
	}
	p.doc.Root = root
	return nil
}

// describe names a byte for an error message.
func describe(c byte) string {
	if c >= 0x20 && c < 0x7f {
		return "'" + string(rune(c)) + "'"
	}
	return "byte 0x" + strconv.FormatUint(uint64(c), 16)
}

func (p *parser) parseValue() (model.Slot, error) {
	if p.i >= len(p.data) {
		return model.Slot{}, errors.New(tagSyntax, "The JSON ends where a value is expected", "at", p.where())
	}
	start := p.i
	switch c := p.data[p.i]; {
	case c == '{':
		return p.parseObject()
	case c == '[':
		return p.parseArray()
	case c == '"':
		s, lossy, err := p.parseString()
		if err != nil {
			return model.Slot{}, err
		}
		v, site := p.newSlot(model.KindString, start)
		v.Str = s
		if lossy {
			// The text has a lone surrogate that a Go string cannot hold: it is shown with
			// U+FFFD and cannot be replaced, so that it is never written back.
			return model.Slot{Value: v}, nil
		}
		return model.Slot{Value: v, Site: site}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		return p.parseNumber()
	case c == 't':
		if err := p.literal("true"); err != nil {
			return model.Slot{}, err
		}
		v, site := p.newSlot(model.KindBool, start)
		v.Bool = true
		return model.Slot{Value: v, Site: site}, nil
	case c == 'f':
		if err := p.literal("false"); err != nil {
			return model.Slot{}, err
		}
		v, site := p.newSlot(model.KindBool, start)
		return model.Slot{Value: v, Site: site}, nil
	case c == 'n':
		if err := p.literal("null"); err != nil {
			return model.Slot{}, err
		}
		v, site := p.newSlot(model.KindNull, start)
		return model.Slot{Value: v, Site: site}, nil
	default:
		return model.Slot{}, errors.New(tagSyntax, "Unexpected "+describe(c)+" where a JSON value is expected",
			"at", p.where(), "offset", p.i)
	}
}

func (p *parser) literal(word string) error {
	if p.i+len(word) > len(p.data) || string(p.data[p.i:p.i+len(word)]) != word {
		return errors.New(tagSyntax, "Expected "+word, "at", p.where(), "offset", p.i)
	}
	p.i += len(word)
	if p.i < len(p.data) && isNameByte(p.data[p.i]) {
		return errors.New(tagSyntax, "Expected "+word, "at", p.where(), "offset", p.i)
	}
	return nil
}

func isNameByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func (p *parser) enter() error {
	p.depth++
	if p.depth > p.maxDepth {
		return errors.New(tagTooDeep, "The JSON is nested too deeply", "limit", p.maxDepth, "at", p.where())
	}
	return nil
}

func (p *parser) parseArray() (model.Slot, error) {
	if err := p.enter(); err != nil {
		return model.Slot{}, err
	}
	p.i++
	base := len(p.stack)
	p.skipSpace()
	if p.i < len(p.data) && p.data[p.i] == ']' {
		p.i++
	} else {
		for {
			p.skipSpace()
			slot, err := p.parseValue()
			if err != nil {
				return model.Slot{}, err
			}
			p.stack = append(p.stack, model.Child{Label: "[" + strconv.Itoa(len(p.stack)-base) + "]", Slot: slot})
			p.skipSpace()
			if p.i >= len(p.data) {
				return model.Slot{}, errors.New(tagSyntax, "The JSON ends inside an array", "at", p.where())
			}
			c := p.data[p.i]
			p.i++
			if c == ']' {
				break
			}
			if c != ',' {
				p.i--
				return model.Slot{}, errors.New(tagSyntax, "Expected ',' or ']' in an array, found "+describe(c),
					"at", p.where(), "offset", p.i)
			}
		}
	}
	return model.Slot{Value: p.finish(model.KindList, base)}, nil
}

func (p *parser) parseObject() (model.Slot, error) {
	if err := p.enter(); err != nil {
		return model.Slot{}, err
	}
	p.i++
	base := len(p.stack)
	p.skipSpace()
	if p.i < len(p.data) && p.data[p.i] == '}' {
		p.i++
	} else {
		for {
			p.skipSpace()
			if p.i >= len(p.data) || p.data[p.i] != '"' {
				if p.i >= len(p.data) {
					return model.Slot{}, errors.New(tagSyntax, "The JSON ends inside an object", "at", p.where())
				}
				return model.Slot{}, errors.New(tagSyntax, "Expected a string key in an object, found "+describe(p.data[p.i]),
					"at", p.where(), "offset", p.i)
			}
			key, _, err := p.parseString()
			if err != nil {
				return model.Slot{}, err
			}
			p.skipSpace()
			if p.i >= len(p.data) || p.data[p.i] != ':' {
				return model.Slot{}, errors.New(tagSyntax, "Expected ':' after the key "+strconv.Quote(key),
					"at", p.where(), "offset", p.i)
			}
			p.i++
			p.skipSpace()
			slot, err := p.parseValue()
			if err != nil {
				return model.Slot{}, err
			}
			p.stack = append(p.stack, model.Child{Label: key, Slot: slot})
			p.skipSpace()
			if p.i >= len(p.data) {
				return model.Slot{}, errors.New(tagSyntax, "The JSON ends inside an object", "at", p.where())
			}
			c := p.data[p.i]
			p.i++
			if c == '}' {
				break
			}
			if c != ',' {
				p.i--
				return model.Slot{}, errors.New(tagSyntax, "Expected ',' or '}' in an object, found "+describe(c),
					"at", p.where(), "offset", p.i)
			}
		}
	}
	return model.Slot{Value: p.finish(model.KindMap, base)}, nil
}

// finish makes the container of the children on the stack from base up, and pops them.
func (p *parser) finish(kind model.Kind, base int) *model.Value {
	v := p.newValue()
	v.Kind = kind
	if n := len(p.stack) - base; n > 0 {
		v.Children = make([]model.Child, n)
		copy(v.Children, p.stack[base:])
		clear(p.stack[base:])
		p.stack = p.stack[:base]
	}
	p.depth--
	return v
}

// parseNumber reads a number by the grammar of RFC 8259.
func (p *parser) parseNumber() (model.Slot, error) {
	start := p.i
	d := p.data
	i := p.i
	if d[i] == '-' {
		i++
	}
	digits := func() int {
		n := 0
		for i < len(d) && d[i] >= '0' && d[i] <= '9' {
			i++
			n++
		}
		return n
	}
	switch {
	case i < len(d) && d[i] == '0':
		i++
	case i < len(d) && d[i] >= '1' && d[i] <= '9':
		digits()
	default:
		p.i = i
		return model.Slot{}, errors.New(tagSyntax, "Expected a digit in a number", "at", p.where(), "offset", i)
	}
	isInt := true
	if i < len(d) && d[i] == '.' {
		isInt = false
		i++
		if digits() == 0 {
			p.i = i
			return model.Slot{}, errors.New(tagSyntax, "Expected a digit after the '.' of a number", "at", p.where(), "offset", i)
		}
	}
	if i < len(d) && (d[i] == 'e' || d[i] == 'E') {
		isInt = false
		i++
		if i < len(d) && (d[i] == '+' || d[i] == '-') {
			i++
		}
		if digits() == 0 {
			p.i = i
			return model.Slot{}, errors.New(tagSyntax, "Expected a digit in the exponent of a number", "at", p.where(), "offset", i)
		}
	}
	p.i = i
	if i < len(d) && isNameByte(d[i]) {
		return model.Slot{}, errors.New(tagSyntax, "Unexpected "+describe(d[i])+" in a number", "at", p.where(), "offset", i)
	}
	text := d[start:i]
	if isInt {
		v, site := p.newSlot(model.KindInt, start)
		n := p.newInt()
		if len(text) <= 18 {
			x, err := strconv.ParseInt(string(text), 10, 64)
			if err != nil {
				return model.Slot{}, errors.NewErr(tagSyntax, err).SetMetadata("at", p.where(), "offset", start)
			}
			n.SetInt64(x)
		} else if _, ok := n.SetString(string(text), 10); !ok {
			return model.Slot{}, errors.New(tagSyntax, "Not a whole number", "at", p.where(), "offset", start)
		}
		v.Int = n
		return model.Slot{Value: v, Site: site}, nil
	}
	f, err := strconv.ParseFloat(string(text), 64)
	if err != nil && !isRange(err) {
		return model.Slot{}, errors.NewErr(tagSyntax, err).SetMetadata("at", p.where(), "offset", start)
	}
	if math.IsNaN(f) {
		return model.Slot{}, errors.New(tagSyntax, "Not a number", "at", p.where(), "offset", start)
	}
	v, site := p.newSlot(model.KindFloat, start)
	v.Float = f
	return model.Slot{Value: v, Site: site}, nil
}

func isRange(err error) bool {
	var ne *strconv.NumError
	return errors.As(err, &ne) && ne.Err == strconv.ErrRange
}

// parseString reads the string at the parser's position. lossy is set when the text has an
// escaped lone surrogate, which is read as U+FFFD.
func (p *parser) parseString() (s string, lossy bool, err error) {
	d := p.data
	start := p.i
	i := p.i + 1
	high := false
	escaped := false
scan:
	for ; i < len(d); i++ {
		c := d[i]
		switch {
		case c == '"':
			break scan
		case c == '\\':
			escaped = true
			i++
		case c < 0x20:
			p.i = i
			return "", false, errors.New(tagSyntax, "A string has the control character "+describe(c)+" that JSON requires to be escaped",
				"at", p.where(), "offset", i)
		case c >= 0x80:
			high = true
		}
	}
	if i >= len(d) {
		p.i = start
		return "", false, errors.New(tagSyntax, "A string is not closed", "at", p.where(), "offset", start)
	}
	body := d[start+1 : i]
	if high && !utf8.Valid(body) {
		p.i = start
		return "", false, errors.New(tagSyntax, "A string is not valid UTF-8", "at", p.where(), "offset", start)
	}
	p.i = i + 1
	if !escaped {
		return string(body), false, nil
	}
	buf := p.scrap[:0]
	for j := 0; j < len(body); j++ {
		c := body[j]
		if c != '\\' {
			buf = append(buf, c)
			continue
		}
		j++
		if j >= len(body) {
			p.i = start + 1 + j
			return "", false, errors.New(tagSyntax, "A string ends inside an escape", "at", p.where(), "offset", start+1+j)
		}
		switch body[j] {
		case '"', '\\', '/':
			buf = append(buf, body[j])
		case 'b':
			buf = append(buf, '\b')
		case 'f':
			buf = append(buf, '\f')
		case 'n':
			buf = append(buf, '\n')
		case 'r':
			buf = append(buf, '\r')
		case 't':
			buf = append(buf, '\t')
		case 'u':
			r, ok := hex4(body, j+1)
			if !ok {
				p.i = start + 1 + j
				return "", false, errors.New(tagSyntax, "A \\u escape needs four hexadecimal digits", "at", p.where(), "offset", start+1+j)
			}
			j += 4
			if utf16.IsSurrogate(r) {
				if r < 0xDC00 && j+6 < len(body) && body[j+1] == '\\' && body[j+2] == 'u' {
					if r2, ok := hex4(body, j+3); ok && r2 >= 0xDC00 && r2 < 0xE000 {
						r = utf16.DecodeRune(r, r2)
						j += 6
					}
				}
				if utf16.IsSurrogate(r) {
					r = utf8.RuneError
					lossy = true
				}
			}
			buf = utf8.AppendRune(buf, r)
		default:
			p.i = start + 1 + j
			return "", false, errors.New(tagSyntax, "A string has the unknown escape \\"+string(rune(body[j])),
				"at", p.where(), "offset", start+1+j)
		}
	}
	p.scrap = buf
	return string(buf), lossy, nil
}

// hex4 reads four hexadecimal digits of b from at.
func hex4(b []byte, at int) (rune, bool) {
	if at+4 > len(b) {
		return 0, false
	}
	var r rune
	for _, c := range b[at : at+4] {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}
