package rubymarshal

import (
	"encoding/binary"
	"math"
	"math/big"
	"slices"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// siteKind is how the value at a site is written.
type siteKind uint8

const (
	// skBool is 'T' or 'F'.
	skBool siteKind = iota
	// skFixnum is 'i' and a w_long.
	skFixnum
	// skBignum is 'l': a sign, a count of 16-bit words and the words.
	skBignum
	// skFloat is 'f' and the text of the number.
	skFloat
	// skString is '"', a length and the bytes.
	skString
	// skF64 is a little-endian double inside the data of a 'u' value (Color, Tone).
	skF64
	// skI32 is a little-endian int32 inside the data of a 'u' value (Rect).
	skI32
)

// siteRec is the Ref of a site: the bytes of the value in the file.
type siteRec struct {
	doc        *Doc
	kind       siteKind
	start, end int
	enc        *textEncoding
}

// modelKind is the model kind of the values at the site.
func (s *siteRec) modelKind() model.Kind {
	switch s.kind {
	case skBool:
		return model.KindBool
	case skFixnum, skBignum, skI32:
		return model.KindInt
	case skFloat, skF64:
		return model.KindFloat
	default:
		return model.KindString
	}
}

// Encode is the file with the edits made. Each edit replaces the bytes of one value; every
// other byte is kept. Every Site must come from d, and every value must have the kind of
// the site's value and fit the site.
func (d *Doc) Encode(edits []model.Edit) ([]byte, error) {
	type splice struct {
		start, end int
		with       []byte
	}
	splices := make([]splice, 0, len(edits))
	seen := make(map[*siteRec]bool, len(edits))
	for _, e := range edits {
		if e.Site == nil {
			return nil, errors.New(TagForeignSite, "an edit has no site")
		}
		rec, ok := e.Site.Ref.(*siteRec)
		if !ok || rec.doc != d {
			return nil, errors.New(TagForeignSite, "the site is not from this file", "site", e.Site.ID)
		}
		if seen[rec] {
			return nil, errors.New(TagDuplicateEdit, "two edits of the same value", "site", e.Site.ID)
		}
		seen[rec] = true
		v := e.Value
		if v == nil || v.Kind != rec.modelKind() {
			return nil, errors.New(TagWrongKind, "the new value has not the kind of the old one", "site", e.Site.ID)
		}
		if v.Kind == model.KindInt {
			if v.Int == nil {
				return nil, errors.New(TagWrongKind, "the new number has no value", "site", e.Site.ID)
			}
			if (e.Site.IntMin != nil && v.Int.Cmp(e.Site.IntMin) < 0) || (e.Site.IntMax != nil && v.Int.Cmp(e.Site.IntMax) > 0) {
				return nil, errors.New(TagOutOfRange, "the number does not fit the value in the file", "site", e.Site.ID, "value", v.Int.String())
			}
		}
		var with []byte
		switch rec.kind {
		case skBool:
			with = []byte{'F'}
			if v.Bool {
				with[0] = 'T'
			}
		case skFixnum:
			with = append([]byte{'i'}, appendLong(nil, int(v.Int.Int64()))...)
		case skBignum:
			with = appendBignum(v.Int)
		case skFloat:
			text := formatFloat(v.Float)
			with = append(append([]byte{'f'}, appendLong(nil, len(text))...), text...)
		case skString:
			raw, err := rec.enc.encode(v.Str)
			if err != nil {
				return nil, err
			}
			with = append(append([]byte{'"'}, appendLong(nil, len(raw))...), raw...)
		case skF64:
			with = binary.LittleEndian.AppendUint64(nil, math.Float64bits(v.Float))
		case skI32:
			with = binary.LittleEndian.AppendUint32(nil, uint32(int32(v.Int.Int64())))
		}
		splices = append(splices, splice{rec.start, rec.end, with})
	}
	slices.SortFunc(splices, func(a, b splice) int { return a.start - b.start })
	out := make([]byte, 0, len(d.Data)+16*len(splices))
	pos := 0
	for _, s := range splices {
		out = append(out, d.Data[pos:s.start]...)
		out = append(out, s.with...)
		pos = s.end
	}
	return append(out, d.Data[pos:]...), nil
}

// appendLong writes n the way w_long does.
func appendLong(b []byte, n int) []byte {
	switch {
	case n == 0:
		return append(b, 0)
	case n > 0 && n < 123:
		return append(b, byte(n+5))
	case n < 0 && n > -124:
		return append(b, byte(n-5))
	}
	var buf [4]byte
	for i := 1; i <= 4; i++ {
		buf[i-1] = byte(n)
		n >>= 8
		if n == 0 {
			return append(append(b, byte(i)), buf[:i]...)
		}
		if n == -1 {
			return append(append(b, byte(-i)), buf[:i]...)
		}
	}
	return b
}

// appendBignum writes x as an 'l'. It has at least one 16-bit word, as Ruby reads zero
// words in no version.
func appendBignum(x *big.Int) []byte {
	mag := new(big.Int).Abs(x).Bytes() // big-endian
	slices.Reverse(mag)
	if len(mag)%2 == 1 {
		mag = append(mag, 0)
	}
	if len(mag) == 0 {
		mag = []byte{0, 0}
	}
	out := []byte{'l', '+'}
	if x.Sign() < 0 {
		out[1] = '-'
	}
	out = appendLong(out, len(mag)/2)
	return append(out, mag...)
}

// Tags of the errors of Encode.
const (
	// TagForeignSite tags an edit whose site is not one of this file.
	TagForeignSite = "ForeignSite"
	// TagDuplicateEdit tags two edits of the same value.
	TagDuplicateEdit = "DuplicateEdit"
	// TagWrongKind tags a new value whose kind is not the old one's.
	TagWrongKind = "WrongKind"
	// TagOutOfRange tags a number the file's value cannot hold.
	TagOutOfRange = "OutOfRange"
)
