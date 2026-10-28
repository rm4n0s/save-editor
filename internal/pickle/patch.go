package pickle

import (
	"encoding/binary"
	"math"
	"math/big"
	"slices"
	"strconv"

	"github.com/rm4n0s/errors"
)

// Edit gives the slot at Site a new primitive value.
type Edit struct {
	Site  Site
	Value *Value
}

// Patch returns the pickle with each edit's slot holding the edit's value. Every other byte
// is kept, except the FRAME opcodes of protocol 4 and later, which are dropped when anything
// changes (they give lengths, and frames are optional for the reader). The new values are
// written with opcodes of protocol 2 or lower, as the protocol of the pickle allows, so
// Python 2 can read a protocol 2 pickle again.
//
// A literal that the memo keeps is left in place for the GETs that read it later, and is
// then popped and replaced: only this slot changes, as an assignment in Python would.
func (d *Document) Patch(edits []Edit) ([]byte, error) {
	if len(edits) == 0 {
		return slices.Clone(d.Data), nil
	}
	edits = slices.Clone(edits)
	slices.SortFunc(edits, func(a, b Edit) int { return a.Site.Start - b.Site.Start })
	for i, e := range edits {
		if e.Site.Kind != SiteLiteral && e.Site.Kind != SiteMemo {
			return nil, errors.New(tagPickleSiteNotEditable, "the slot was not written by one opcode", "offset", e.Site.Start)
		}
		if e.Site.Start < 0 || e.Site.MemoEnd > d.End || e.Site.Start >= e.Site.End || e.Site.End > e.Site.MemoEnd {
			return nil, errors.New(tagPickleSiteNotEditable, "the slot is not in the pickle", "offset", e.Site.Start)
		}
		if i > 0 && edits[i-1].Site.MemoEnd > e.Site.Start {
			return nil, errors.New(tagPickleSiteNotEditable, "two edits of the same slot", "offset", e.Site.Start)
		}
	}

	out := make([]byte, 0, len(d.Data)+64*len(edits))
	frames := d.frames
	pos := 0
	// copyTo copies the bytes up to end, leaving out the frames.
	copyTo := func(end int) {
		for len(frames) > 0 && frames[0][0] < end {
			out = append(out, d.Data[pos:frames[0][0]]...)
			pos = frames[0][1]
			frames = frames[1:]
		}
		out = append(out, d.Data[pos:end]...)
		pos = end
	}
	for _, e := range edits {
		lit, err := d.encodeLiteral(e.Value)
		if err != nil {
			return nil, err
		}
		if e.Site.Kind == SiteLiteral && e.Site.MemoEnd > e.Site.End {
			copyTo(e.Site.MemoEnd)
			out = append(out, opPop)
			out = append(out, lit...)
			continue
		}
		copyTo(e.Site.Start)
		out = append(out, lit...)
		pos = e.Site.End
	}
	copyTo(d.End)
	return append(out, d.Data[d.End:]...), nil
}

// encodeLiteral writes the opcode that pushes v.
func (d *Document) encodeLiteral(v *Value) ([]byte, error) {
	switch v.Kind {
	case KindNone:
		return []byte{opNone}, nil
	case KindBool:
		switch {
		case d.Protocol >= 2 && v.Bool:
			return []byte{opTrue}, nil
		case d.Protocol >= 2:
			return []byte{opFalse}, nil
		case v.Bool:
			return []byte("I01\n"), nil
		default:
			return []byte("I00\n"), nil
		}
	case KindInt:
		return d.encodeInt(v.Int), nil
	case KindFloat:
		if d.Protocol < 1 {
			return []byte("F" + strconv.FormatFloat(v.Float, 'g', -1, 64) + "\n"), nil
		}
		return binary.BigEndian.AppendUint64([]byte{opBinFloat}, math.Float64bits(v.Float)), nil
	case KindString:
		if d.Protocol < 1 {
			return append(append([]byte{opUnicode}, encodeRawUnicodeEscape(v.Str)...), '\n'), nil
		}
		return append(binary.LittleEndian.AppendUint32([]byte{opBinUnicode}, uint32(len(v.Str))), v.Str...), nil
	case KindByteString:
		if d.Protocol < 1 {
			return []byte("S" + strconv.Quote(v.Str) + "\n"), nil
		}
		if len(v.Str) < 256 {
			return append([]byte{opShortBinString, byte(len(v.Str))}, v.Str...), nil
		}
		return append(binary.LittleEndian.AppendUint32([]byte{opBinString}, uint32(len(v.Str))), v.Str...), nil
	}
	return nil, errors.New(tagPickleValueNotPrimitive, "only None, bools, numbers and strings can be written in place",
		"kind", v.Kind.String())
}

func (d *Document) encodeInt(n *big.Int) []byte {
	if n.IsInt64() {
		i := n.Int64()
		switch {
		case d.Protocol >= 1 && i >= 0 && i < 1<<8:
			return []byte{opBinInt1, byte(i)}
		case d.Protocol >= 1 && i >= 0 && i < 1<<16:
			return binary.LittleEndian.AppendUint16([]byte{opBinInt2}, uint16(i))
		case d.Protocol >= 1 && i >= math.MinInt32 && i <= math.MaxInt32:
			return binary.LittleEndian.AppendUint32([]byte{opBinInt}, uint32(int32(i)))
		}
	}
	if d.Protocol < 2 {
		return []byte("L" + n.String() + "L\n")
	}
	b := encodeLong(n)
	if len(b) < 256 {
		return append([]byte{opLong1, byte(len(b))}, b...)
	}
	return append(binary.LittleEndian.AppendUint32([]byte{opLong4}, uint32(len(b))), b...)
}

// encodeLong writes n as the shortest little-endian two's complement, as Python's
// pickle.encode_long does.
func encodeLong(n *big.Int) []byte {
	if n.Sign() == 0 {
		return nil
	}
	size := n.BitLen()/8 + 1
	v := new(big.Int).Set(n)
	if n.Sign() < 0 {
		v.Add(v, new(big.Int).Lsh(big.NewInt(1), uint(8*size)))
	}
	be := v.FillBytes(make([]byte, size))
	le := make([]byte, size)
	for i, c := range be {
		le[size-1-i] = c
	}
	// A negative number that fits in one byte less, such as -128, drops its top 0xff.
	if n.Sign() < 0 && size > 1 && le[size-1] == 0xff && le[size-2]&0x80 != 0 {
		le = le[:size-1]
	}
	return le
}

// encodeRawUnicodeEscape writes s as Python's raw-unicode-escape, escaping the backslash and
// the newline as protocol 0 does.
func encodeRawUnicodeEscape(s string) []byte {
	var out []byte
	for _, r := range s {
		switch {
		case r == '\\' || r == '\n' || r == '\r' || r == 0x1a || (r > 0xff && r <= 0xffff):
			out = append(out, []byte("\\u"+leftPad(strconv.FormatInt(int64(r), 16), 4))...)
		case r > 0xffff:
			out = append(out, []byte("\\U"+leftPad(strconv.FormatInt(int64(r), 16), 8))...)
		default:
			out = append(out, byte(r))
		}
	}
	return out
}

func leftPad(s string, n int) string {
	for len(s) < n {
		s = "0" + s
	}
	return s
}

// The tags of the errors that this file makes.
const (
	tagPickleSiteNotEditable   = "PickleSiteNotEditable"
	tagPickleValueNotPrimitive = "PickleValueNotPrimitive"
)
