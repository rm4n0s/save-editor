package gvas

import (
	"encoding/binary"
	"math"
	"unicode/utf16"

	"github.com/rm4n0s/errors"
)

// reader reads little-endian values from data[pos:lim]. A read past lim fails; lim is narrowed
// to the end of a property while its body is read, so one property cannot read into the next.
type reader struct {
	d      []byte
	pos    int
	lim    int
	v      header
	doc    *Document
	nextID int
}

func newReader(d []byte, doc *Document) *reader {
	return &reader{d: d, lim: len(d), doc: doc}
}

// need checks that n more bytes can be read.
func (r *reader) need(n int) error {
	if n < 0 || n > r.lim-r.pos {
		return errors.New(tagTruncated, "the file ends in the middle of a value",
			"offset", r.pos, "wanted", n, "limit", r.lim)
	}
	return nil
}

func (r *reader) u8() (byte, error) {
	if err := r.need(1); err != nil {
		return 0, err
	}
	b := r.d[r.pos]
	r.pos++
	return b, nil
}

func (r *reader) u16() (uint16, error) {
	if err := r.need(2); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint16(r.d[r.pos:])
	r.pos += 2
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint32(r.d[r.pos:])
	r.pos += 4
	return v, nil
}

func (r *reader) i32() (int32, error) {
	v, err := r.u32()
	return int32(v), err
}

func (r *reader) skip(n int) error {
	if err := r.need(n); err != nil {
		return err
	}
	r.pos += n
	return nil
}

// fstring reads an FString: an int32 length, then that many bytes of ASCII/Latin-1 (positive
// length) or that many UTF-16 code units (negative length), the last being a null terminator.
// A missing terminator is accepted, as some games write one.
func (r *reader) fstring() (string, error) {
	n, err := r.i32()
	if err != nil {
		return "", err
	}
	switch {
	case n == 0:
		return "", nil
	case n == math.MinInt32:
		return "", errors.New(tagBadString, "a string has an impossible length", "offset", r.pos-4)
	case n > 0:
		if err := r.need(int(n)); err != nil {
			return "", err
		}
		raw := r.d[r.pos : r.pos+int(n)]
		r.pos += int(n)
		if raw[len(raw)-1] == 0 {
			raw = raw[:len(raw)-1]
		}
		runes := make([]rune, len(raw))
		for i, b := range raw {
			runes[i] = rune(b)
		}
		return string(runes), nil
	default:
		units := -int(n)
		if err := r.need(2 * units); err != nil {
			return "", err
		}
		u := make([]uint16, units)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(r.d[r.pos+2*i:])
		}
		r.pos += 2 * units
		if u[len(u)-1] == 0 {
			u = u[:len(u)-1]
		}
		return string(utf16.Decode(u)), nil
	}
}

// encodeFString writes s as UE does: ASCII with a null terminator when every character is
// ASCII, UTF-16 with a null terminator and a negative length otherwise. The empty string is
// the length 0.
func encodeFString(s string) []byte {
	if s == "" {
		return []byte{0, 0, 0, 0}
	}
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		out := make([]byte, 4, 4+len(s)+1)
		binary.LittleEndian.PutUint32(out, uint32(len(s)+1))
		out = append(out, s...)
		return append(out, 0)
	}
	u := utf16.Encode([]rune(s))
	u = append(u, 0)
	out := make([]byte, 4, 4+2*len(u))
	binary.LittleEndian.PutUint32(out, uint32(int32(-len(u))))
	for _, c := range u {
		out = binary.LittleEndian.AppendUint16(out, c)
	}
	return out
}
