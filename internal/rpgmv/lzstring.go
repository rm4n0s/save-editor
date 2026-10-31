package rpgmv

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
)

// This file is LZString.compressToBase64 and LZString.decompressFromBase64 of lz-string
// (https://github.com/pieroxy/lz-string), the compression of RPG Maker MV's save files,
// written again in Go. A text is a sequence of UTF-16 code units, as in JavaScript. The
// tests check the output against the real JavaScript under node.
//
// There are two layouts of the Base64 text:
//   - the one of the lz-string that RPG Maker MV ships (js/libs/lz-string.js, an old
//     version): the compressed text is a string of 16-bit characters, and the Base64 is of
//     the bytes of these characters, high byte first;
//   - the one of lz-string 1.4 and later: the compressed text is made of 6-bit characters,
//     each one a Base64 digit.
//
// Every MV save is of the first layout unless the game replaced the library. The same
// compression is behind both.

const (
	keyStrBase64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="

	tagLZCorrupt = "LZStringCorrupt"
	tagLZSurr    = "LZStringSurrogate"
)

// lzLayout says how the compressed characters are written as Base64.
type lzLayout uint8

const (
	// lzMV is the layout of the lz-string that RPG Maker MV ships.
	lzMV lzLayout = iota
	// lzDigits is the layout of lz-string 1.4 and later.
	lzDigits
)

func (l lzLayout) bits() int {
	if l == lzDigits {
		return 6
	}
	return 16
}

// toUnits is the UTF-16 code units of a UTF-8 text.
func toUnits(b []byte) []uint16 {
	out := make([]uint16, 0, len(b))
	for i := 0; i < len(b); {
		if b[i] < utf8.RuneSelf {
			out = append(out, uint16(b[i]))
			i++
			continue
		}
		r, n := utf8.DecodeRune(b[i:])
		i += n
		if r >= 0x10000 {
			r1, r2 := utf16.EncodeRune(r)
			out = append(out, uint16(r1), uint16(r2))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

// toUTF8 is the UTF-8 text of UTF-16 code units. A lone surrogate, which has no UTF-8, is an
// error.
func toUTF8(u []uint16) ([]byte, error) {
	out := make([]byte, 0, len(u)+len(u)/8)
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		switch {
		case c < utf8.RuneSelf:
			out = append(out, byte(c))
			continue
		case c >= 0xD800 && c < 0xDC00 && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] < 0xE000:
			c = utf16.DecodeRune(c, rune(u[i+1]))
			i++
		case c >= 0xD800 && c < 0xE000:
			return nil, errors.New(tagLZSurr, "The save has half of a character pair (a lone surrogate) that cannot be shown or edited",
				"index", i)
		}
		out = utf8.AppendRune(out, c)
	}
	return out, nil
}

// lzCompressToBase64 is LZString.compressToBase64 of the library with the layout.
func lzCompressToBase64(in []uint16, layout lzLayout) string {
	chars := lzCompress(in, layout.bits())
	var sb strings.Builder
	if layout == lzDigits {
		sb.Grow(len(chars) + 3)
		for _, c := range chars {
			sb.WriteByte(keyStrBase64[c])
		}
		for sb.Len()%4 != 0 {
			sb.WriteByte('=')
		}
		return sb.String()
	}
	bytes := make([]byte, 0, len(chars)*2)
	for _, c := range chars {
		bytes = append(bytes, byte(c>>8), byte(c))
	}
	sb.Grow((len(bytes) + 2) / 3 * 4)
	for i := 0; i < len(bytes); i += 3 {
		var b [3]byte
		n := copy(b[:], bytes[i:])
		sb.WriteByte(keyStrBase64[b[0]>>2])
		sb.WriteByte(keyStrBase64[(b[0]&3)<<4|b[1]>>4])
		if n < 2 {
			sb.WriteString("==")
			continue
		}
		sb.WriteByte(keyStrBase64[(b[1]&15)<<2|b[2]>>6])
		if n < 3 {
			sb.WriteByte('=')
			continue
		}
		sb.WriteByte(keyStrBase64[b[2]&63])
	}
	return sb.String()
}

// lzCompress is LZString.compress, whose characters have bits bits.
func lzCompress(in []uint16, bits int) []uint16 {
	var (
		dict      = make(map[uint64]int, 1024)
		pending   = map[int]uint16{} // single characters whose literal has not been written
		dictSize  = 3
		numBits   = 2
		enlargeIn = 2
		out       = make([]uint16, 0, len(in)/2+4)
		val       int
		pos       int
	)
	writeBit := func(bit int) {
		val = val<<1 | bit
		if pos == bits-1 {
			pos = 0
			out = append(out, uint16(val))
			val = 0
		} else {
			pos++
		}
	}
	writeBits := func(value, n int) {
		for range n {
			writeBit(value & 1)
			value >>= 1
		}
	}
	grow := func() {
		enlargeIn--
		if enlargeIn == 0 {
			enlargeIn = 1 << numBits
			numBits++
		}
	}
	emit := func(w int) {
		if ch, ok := pending[w]; ok {
			if ch < 256 {
				writeBits(0, numBits)
				writeBits(int(ch), 8)
			} else {
				writeBits(1, numBits)
				writeBits(int(ch), 16)
			}
			grow()
			delete(pending, w)
		} else {
			writeBits(w, numBits)
		}
		grow()
	}
	w := 0
	for _, c := range in {
		code, ok := dict[uint64(c)]
		if !ok {
			code = dictSize
			dict[uint64(c)] = code
			pending[code] = c
			dictSize++
		}
		if w == 0 {
			w = code
			continue
		}
		key := uint64(w)<<16 | uint64(c)
		if wc, ok := dict[key]; ok {
			w = wc
			continue
		}
		emit(w)
		dict[key] = dictSize
		dictSize++
		w = code
	}
	if w != 0 {
		emit(w)
	}
	writeBits(2, numBits)
	for {
		val <<= 1
		if pos == bits-1 {
			out = append(out, uint16(val))
			break
		}
		pos++
	}
	return out
}

// lzDecompressFromBase64 is LZString.decompressFromBase64 of the library with the layout, with
// the limit of lzDecompress.
// White space and the padding at the end are ignored; any other character that is not
// Base64 is an error.
func lzDecompressFromBase64(in string, layout lzLayout, limit int) ([]uint16, error) {
	in = strings.TrimRight(in, "=\r\n\t ")
	if in == "" {
		return nil, errors.New(tagLZCorrupt, "The save is empty")
	}
	digits := make([]byte, len(in))
	for i := 0; i < len(in); i++ {
		k := strings.IndexByte(keyStrBase64[:64], in[i])
		if k < 0 {
			return nil, errors.New(tagLZCorrupt, "The save is not Base64 text", "offset", i)
		}
		digits[i] = byte(k)
	}
	if layout == lzDigits {
		chars := make([]uint16, len(digits))
		for i, d := range digits {
			chars[i] = uint16(d)
		}
		return lzDecompress(chars, 6, limit)
	}
	// The bytes of the 16-bit characters, high byte first. A last byte without its partner
	// is dropped, as the library drops it.
	chars := make([]uint16, 0, len(digits)*3/4/2+1)
	var acc uint32
	nbits := 0
	var high byte
	haveHigh := false
	for _, d := range digits {
		acc = acc<<6 | uint32(d)
		nbits += 6
		if nbits >= 8 {
			nbits -= 8
			b := byte(acc >> nbits)
			acc &= 1<<nbits - 1
			if haveHigh {
				chars = append(chars, uint16(high)<<8|uint16(b))
				haveHigh = false
			} else {
				high, haveHigh = b, true
			}
		}
	}
	return lzDecompress(chars, 16, limit)
}

// lzDecompress is LZString.decompress of characters that have bits bits. It fails when the
// text is longer than limit code units, so that a corrupt or hostile file cannot use all the
// memory.
func lzDecompress(in []uint16, bits, limit int) ([]uint16, error) {
	if len(in) == 0 {
		return nil, errors.New(tagLZCorrupt, "The save is empty")
	}
	var (
		reset   = 1 << (bits - 1)
		length  = len(in)
		val     = int(in[0])
		pos     = reset
		index   = 1
		numBits = 3
		enlarge = 4
	)
	readBits := func(n int) int {
		bitsRead := 0
		for power := 1; power < 1<<n; power <<= 1 {
			if val&pos != 0 {
				bitsRead |= power
			}
			pos >>= 1
			if pos == 0 {
				pos = reset
				val = 0
				if index < length {
					val = int(in[index])
				}
				index++
			}
		}
		return bitsRead
	}
	// An entry of the dictionary is a part of the output: each new entry is the previous
	// entry and the first character of the one after it, which are next to each other.
	type entry struct{ start, n int }
	var (
		out  = make([]uint16, 0, length*2)
		dict = make([]entry, 4, 1024)
	)
	switch readBits(2) {
	case 0:
		out = append(out, uint16(readBits(8)))
	case 1:
		out = append(out, uint16(readBits(16)))
	default:
		return nil, errors.New(tagLZCorrupt, "The save has no text")
	}
	dict[3] = entry{0, 1}
	w := entry{0, 1}
	for {
		if index > length {
			return nil, errors.New(tagLZCorrupt, "The save ends before the end of its text")
		}
		c := readBits(numBits)
		var e entry
		literal := false
		switch c {
		case 0, 1:
			width := 8
			if c == 1 {
				width = 16
			}
			out = append(out, uint16(readBits(width)))
			literal = true
			e = entry{len(out) - 1, 1}
			c = len(dict)
			dict = append(dict, e)
			enlarge--
		case 2:
			return out, nil
		}
		if enlarge == 0 {
			enlarge = 1 << numBits
			numBits++
		}
		switch {
		case literal:
		case c < len(dict):
			e = entry{len(out), dict[c].n}
			out = append(out, out[dict[c].start:dict[c].start+dict[c].n]...)
		case c == len(dict):
			e = entry{len(out), w.n + 1}
			out = append(out, out[w.start:w.start+w.n]...)
			out = append(out, out[w.start])
		default:
			return nil, errors.New(tagLZCorrupt, "The save has a code that is not in its dictionary", "offset", index)
		}
		if len(out) > limit {
			return nil, errors.New(tagLZCorrupt, "The save is too large when decompressed", "limit", limit)
		}
		dict = append(dict, entry{w.start, w.n + 1})
		enlarge--
		w = e
		if enlarge == 0 {
			enlarge = 1 << numBits
			numBits++
		}
	}
}
