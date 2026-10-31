package rubymarshal

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// maxDepth is how deep values may nest; a real save is far shallower.
const maxDepth = 5000

// reader reads the dumps of one file.
type reader struct {
	doc    *Doc
	data   []byte
	pos    int
	defEnc *textEncoding
	// syms and objs are the tables of the dump being read: the symbols by index, and the
	// objects that '@' links refer to, numbered in the order Ruby numbers them.
	syms  []string
	objs  []*model.Slot
	level int
}

// pending is what an 'I' wrapper has to finish once it has read the instance variables of
// the value it wraps.
type pending struct {
	// consumed says the value read the instance variables itself ('u' does, so that the
	// numbering of the objects in them comes before the object).
	consumed bool
	// str is the String whose encoding the instance variables give.
	str *strRec
}

// strRec is a String that waits for its encoding.
type strRec struct {
	slot       *model.Slot
	start, end int
	raw        []byte
}

func (r *reader) byte() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, errors.New(TagTruncated, "the file ends in the middle of a value", "offset", r.pos)
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || n > len(r.data)-r.pos {
		return nil, errors.New(TagTruncated, "the file ends in the middle of a value", "offset", r.pos, "need", n)
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

// long reads w_long's variable-length integer.
func (r *reader) long() (int, error) {
	c, err := r.byte()
	if err != nil {
		return 0, err
	}
	s := int8(c)
	switch {
	case s == 0:
		return 0, nil
	case s > 4:
		return int(s) - 5, nil
	case s < -4:
		return int(s) + 5, nil
	case s > 0:
		b, err := r.bytes(int(s))
		if err != nil {
			return 0, err
		}
		x := 0
		for i, v := range b {
			x |= int(v) << (8 * i)
		}
		return x, nil
	default:
		b, err := r.bytes(int(-s))
		if err != nil {
			return 0, err
		}
		x := -1
		for i, v := range b {
			x &^= 0xff << (8 * i)
			x |= int(v) << (8 * i)
		}
		return x, nil
	}
}

// length reads a count of the things that follow, each at least one byte long.
func (r *reader) length() (int, error) {
	n, err := r.long()
	if err != nil {
		return 0, err
	}
	if n < 0 || n > len(r.data)-r.pos {
		return 0, errors.New(TagBadLength, "a length is larger than the rest of the file", "offset", r.pos, "length", n)
	}
	return n, nil
}

// symbol reads a symbol (':' or ';', possibly in an 'I' wrapper) where one must be.
func (r *reader) symbol() (string, error) {
	t, err := r.byte()
	if err != nil {
		return "", err
	}
	ivar := false
	if t == 'I' {
		ivar = true
		if t, err = r.byte(); err != nil {
			return "", err
		}
	}
	var name string
	switch t {
	case ':':
		n, err := r.length()
		if err != nil {
			return "", err
		}
		b, err := r.bytes(n)
		if err != nil {
			return "", err
		}
		name = string(b)
		r.syms = append(r.syms, name)
	case ';':
		i, err := r.long()
		if err != nil {
			return "", err
		}
		if i < 0 || i >= len(r.syms) {
			return "", errors.New(TagBadLink, "a symbol link refers to a symbol not read yet", "offset", r.pos, "index", i)
		}
		name = r.syms[i]
	default:
		return "", errors.New(TagBadType, "a symbol was expected", "offset", r.pos-1, "type", string(rune(t)))
	}
	if ivar {
		if err := r.skipIvars(); err != nil {
			return "", err
		}
	}
	return name, nil
}

// skipIvars reads the instance variables that follow a value and drops them.
func (r *reader) skipIvars() error {
	n, err := r.length()
	if err != nil {
		return err
	}
	for range n {
		if _, err := r.symbol(); err != nil {
			return err
		}
		if _, err := r.value(); err != nil {
			return err
		}
	}
	return nil
}

func (r *reader) register(s *model.Slot) {
	r.objs = append(r.objs, s)
}

func (r *reader) newSite(rec *siteRec) *model.Site {
	r.doc.nextID++
	rec.doc = r.doc
	s := &model.Site{ID: r.doc.nextID, Ref: rec}
	switch rec.kind {
	case skFixnum:
		s.IntMin, s.IntMax = fixnumMin, fixnumMax
	case skI32:
		s.IntMin, s.IntMax = int32Min, int32Max
	case skString:
		s.CheckText = rec.enc.check
	}
	return s
}

// value reads a value that is not the target of an 'I' wrapper.
func (r *reader) value() (*model.Slot, error) {
	slot, pend, err := r.valueP(false)
	if err != nil {
		return nil, err
	}
	if pend != nil && pend.str != nil {
		r.finishString(pend.str, r.defEnc)
	}
	return slot, nil
}

// label is how a key of a Hash is shown.
func label(v *model.Value) string {
	switch {
	case v.Type == "Symbol":
		return ":" + v.Str
	case v.Kind == model.KindString:
		return strconv.Quote(v.Str)
	default:
		return v.Repr()
	}
}

// valueP reads a value. ivar says the value is wrapped in an 'I', which returns a pending
// when the wrapper has something to finish.
func (r *reader) valueP(ivar bool) (*model.Slot, *pending, error) {
	if r.level >= maxDepth {
		return nil, nil, errors.New(TagTooDeep, "values nest too deeply", "offset", r.pos)
	}
	r.level++
	slot, pend, err := r.valueCase(ivar)
	r.level--
	return slot, pend, err
}

// valueCase reads a value by its type code.
//
//nolint:gocyclo // one case per type code of marshal.c
func (r *reader) valueCase(ivar bool) (*model.Slot, *pending, error) {
	start := r.pos
	t, err := r.byte()
	if err != nil {
		return nil, nil, err
	}
	switch t {
	case '0':
		return &model.Slot{Value: &model.Value{Kind: model.KindNull, Type: "NilClass", Str: "nil"}}, nil, nil
	case 'T', 'F':
		v := &model.Value{Kind: model.KindBool, Type: "TrueClass", Bool: t == 'T'}
		if t == 'F' {
			v.Type = "FalseClass"
		}
		return &model.Slot{Value: v, Site: r.newSite(&siteRec{kind: skBool, start: start, end: r.pos})}, nil, nil
	case 'i':
		n, err := r.long()
		if err != nil {
			return nil, nil, err
		}
		v := &model.Value{Kind: model.KindInt, Type: "Fixnum", Int: big.NewInt(int64(n))}
		return &model.Slot{Value: v, Site: r.newSite(&siteRec{kind: skFixnum, start: start, end: r.pos})}, nil, nil
	case 'l':
		return r.bignum(start)
	case 'f':
		return r.float(start)
	case ':':
		r.pos = start
		name, err := r.symbol()
		if err != nil {
			return nil, nil, err
		}
		return symbolSlot(name), nil, nil
	case ';':
		r.pos = start
		name, err := r.symbol()
		if err != nil {
			return nil, nil, err
		}
		return symbolSlot(name), nil, nil
	case '@':
		i, err := r.long()
		if err != nil {
			return nil, nil, err
		}
		if i < 0 || i >= len(r.objs) {
			return nil, nil, errors.New(TagBadLink, "an object link refers to an object not read yet", "offset", start, "index", i)
		}
		s := *r.objs[i]
		return &s, nil, nil
	case '"':
		n, err := r.length()
		if err != nil {
			return nil, nil, err
		}
		raw, err := r.bytes(n)
		if err != nil {
			return nil, nil, err
		}
		slot := &model.Slot{Value: &model.Value{Kind: model.KindString, Type: "String"}}
		r.register(slot)
		rec := &strRec{slot: slot, start: start, end: r.pos, raw: raw}
		if ivar {
			return slot, &pending{str: rec}, nil
		}
		r.finishString(rec, r.defEnc)
		return slot, nil, nil
	case '[':
		n, err := r.length()
		if err != nil {
			return nil, nil, err
		}
		v := &model.Value{Kind: model.KindList, Type: "Array", Children: make([]model.Child, 0, n)}
		slot := &model.Slot{Value: v}
		r.register(slot)
		for i := range n {
			c, err := r.value()
			if err != nil {
				return nil, nil, err
			}
			v.Children = append(v.Children, model.Child{Label: "[" + strconv.Itoa(i) + "]", Slot: *c})
		}
		return slot, nil, nil
	case '{', '}':
		n, err := r.length()
		if err != nil {
			return nil, nil, err
		}
		v := &model.Value{Kind: model.KindMap, Type: "Hash", Children: make([]model.Child, 0, n)}
		slot := &model.Slot{Value: v}
		r.register(slot)
		for range n {
			k, err := r.value()
			if err != nil {
				return nil, nil, err
			}
			c, err := r.value()
			if err != nil {
				return nil, nil, err
			}
			v.Children = append(v.Children, model.Child{Label: label(k.Value), Slot: *c})
		}
		if t == '}' {
			c, err := r.value()
			if err != nil {
				return nil, nil, err
			}
			v.Children = append(v.Children, model.Child{Label: "(default)", Slot: *c})
		}
		return slot, nil, nil
	case 'o', 'S':
		class, err := r.symbol()
		if err != nil {
			return nil, nil, err
		}
		v := &model.Value{Kind: model.KindObject, Type: class}
		slot := &model.Slot{Value: v}
		r.register(slot)
		n, err := r.length()
		if err != nil {
			return nil, nil, err
		}
		for range n {
			name, err := r.symbol()
			if err != nil {
				return nil, nil, err
			}
			c, err := r.value()
			if err != nil {
				return nil, nil, err
			}
			v.Children = append(v.Children, model.Child{Label: name, Slot: *c})
		}
		return slot, nil, nil
	case 'c', 'm', 'M':
		n, err := r.length()
		if err != nil {
			return nil, nil, err
		}
		name, err := r.bytes(n)
		if err != nil {
			return nil, nil, err
		}
		kind := "Class"
		if t != 'c' {
			kind = "Module"
		}
		slot := &model.Slot{Value: &model.Value{Kind: model.KindOpaque, Type: kind, Str: string(name)}}
		r.register(slot)
		return slot, nil, nil
	case 'd', 'U':
		class, err := r.symbol()
		if err != nil {
			return nil, nil, err
		}
		v := &model.Value{Kind: model.KindObject, Type: class}
		slot := &model.Slot{Value: v}
		r.register(slot)
		c, err := r.value()
		if err != nil {
			return nil, nil, err
		}
		name := "(data)"
		if t == 'U' {
			name = "(marshal_dump)"
		}
		v.Children = append(v.Children, model.Child{Label: name, Slot: *c})
		return slot, nil, nil
	case 'u':
		return r.userDef(ivar)
	case '/':
		n, err := r.length()
		if err != nil {
			return nil, nil, err
		}
		src, err := r.bytes(n)
		if err != nil {
			return nil, nil, err
		}
		opt, err := r.byte()
		if err != nil {
			return nil, nil, err
		}
		var flags strings.Builder
		for _, f := range []struct {
			bit byte
			c   string
		}{{1, "i"}, {2, "x"}, {4, "m"}} {
			if opt&f.bit != 0 {
				flags.WriteString(f.c)
			}
		}
		slot := &model.Slot{Value: &model.Value{Kind: model.KindOpaque, Type: "Regexp", Str: "/" + string(src) + "/" + flags.String()}}
		r.register(slot)
		return slot, nil, nil
	case 'C', 'e':
		class, err := r.symbol()
		if err != nil {
			return nil, nil, err
		}
		slot, pend, err := r.valueP(ivar)
		if err != nil {
			return nil, nil, err
		}
		if t == 'C' {
			slot.Value.Type = class
		}
		return slot, pend, nil
	case 'I':
		slot, pend, err := r.valueP(true)
		if err != nil {
			return nil, nil, err
		}
		if err := r.ivars(slot, pend); err != nil {
			return nil, nil, err
		}
		return slot, nil, nil
	default:
		return nil, nil, errors.New(TagBadType, "unknown type code", "offset", start, "type", string(rune(t)))
	}
}

func symbolSlot(name string) *model.Slot {
	return &model.Slot{Value: &model.Value{Kind: model.KindString, Type: "Symbol", Str: name}}
}

// ivars reads the instance variables of an 'I' wrapper. They give a String its encoding; on
// a container they are shown as more children, and on anything else they are dropped.
func (r *reader) ivars(slot *model.Slot, pend *pending) error {
	if pend != nil && pend.consumed {
		return nil
	}
	n, err := r.length()
	if err != nil {
		return err
	}
	enc := r.defEnc
	v := slot.Value
	for range n {
		name, err := r.symbol()
		if err != nil {
			return err
		}
		if name == "E" && r.pos < len(r.data) && (r.data[r.pos] == 'T' || r.data[r.pos] == 'F') {
			enc = encodingASCII
			if r.data[r.pos] == 'T' {
				enc = encodingUTF8
			}
			r.pos++
			continue
		}
		c, err := r.value()
		if err != nil {
			return err
		}
		switch {
		case name == "encoding" && pend != nil && pend.str != nil:
			encName := c.Value.Str
			if c.Value.Kind == model.KindBytes {
				encName = string(c.Value.Bytes)
			}
			enc = lookupEncoding(encName)
		case v.Kind == model.KindList || v.Kind == model.KindMap || v.Kind == model.KindObject:
			v.Children = append(v.Children, model.Child{Label: name, Slot: *c})
		}
	}
	if pend != nil && pend.str != nil {
		r.finishString(pend.str, enc)
	}
	return nil
}

// finishString gives a String its text and its site once its encoding is known.
func (r *reader) finishString(rec *strRec, enc *textEncoding) {
	v := rec.slot.Value
	text, ok := enc.decode(rec.raw)
	if !ok {
		v.Kind = model.KindBytes
		v.Bytes = rec.raw
		return
	}
	v.Kind = model.KindString
	v.Str = text
	rec.slot.Site = r.newSite(&siteRec{kind: skString, start: rec.start, end: rec.end, enc: enc})
}

func (r *reader) bignum(start int) (*model.Slot, *pending, error) {
	sign, err := r.byte()
	if err != nil {
		return nil, nil, err
	}
	if sign != '+' && sign != '-' {
		return nil, nil, errors.New(TagBadBignum, "a bignum has no sign", "offset", start)
	}
	n, err := r.long()
	if err != nil {
		return nil, nil, err
	}
	if n < 0 || n > (len(r.data)-r.pos)/2 {
		return nil, nil, errors.New(TagBadLength, "a bignum is longer than the rest of the file", "offset", start, "length", n)
	}
	b, err := r.bytes(2 * n)
	if err != nil {
		return nil, nil, err
	}
	be := make([]byte, len(b))
	for i, c := range b {
		be[len(b)-1-i] = c
	}
	x := new(big.Int).SetBytes(be)
	if sign == '-' {
		x.Neg(x)
	}
	slot := &model.Slot{
		Value: &model.Value{Kind: model.KindInt, Type: "Bignum", Int: x},
		Site:  r.newSite(&siteRec{kind: skBignum, start: start, end: r.pos}),
	}
	r.register(slot)
	return slot, nil, nil
}

func (r *reader) float(start int) (*model.Slot, *pending, error) {
	n, err := r.length()
	if err != nil {
		return nil, nil, err
	}
	b, err := r.bytes(n)
	if err != nil {
		return nil, nil, err
	}
	f, ok := parseFloat(b)
	if !ok {
		return nil, nil, errors.New(TagBadFloat, "a float is not a number", "offset", start, "text", string(b))
	}
	slot := &model.Slot{
		Value: &model.Value{Kind: model.KindFloat, Type: "Float", Float: f},
		Site:  r.newSite(&siteRec{kind: skFloat, start: start, end: r.pos}),
	}
	r.register(slot)
	return slot, nil, nil
}

// Tags of the errors of the reader.
const (
	// TagTruncated tags a file that ends in the middle of a value.
	TagTruncated = "Truncated"
	// TagBadLength tags a length or count larger than the rest of the file.
	TagBadLength = "BadLength"
	// TagBadType tags a type code Marshal does not have, or the wrong one.
	TagBadType = "BadType"
	// TagBadLink tags a link to an object or symbol that has not been read.
	TagBadLink = "BadLink"
	// TagTooDeep tags values that nest too deeply.
	TagTooDeep = "TooDeep"
	// TagBadBignum tags a Bignum whose sign is neither + nor -.
	TagBadBignum = "BadBignum"
	// TagBadFloat tags a Float whose text is not a number.
	TagBadFloat = "BadFloat"
)
