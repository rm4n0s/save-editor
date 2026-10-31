package jsondoc

import (
	"math"
	"strconv"
	"unicode/utf8"
)

const maxFloat = math.MaxFloat64

// AppendFloat appends f as a JSON number that reads back as f: the shortest digits (those of
// a float32 when float32 is set), in plain notation for the magnitudes 1e-6 up to 1e21 and
// with an exponent beyond, always with a fraction or an exponent so that a reader does not
// take it for a whole number. f must be finite.
func AppendFloat(dst []byte, f float64, float32 bool) []byte {
	bits := 64
	if float32 {
		bits = 32
	}
	abs := math.Abs(f)
	start := len(dst)
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		dst = strconv.AppendFloat(dst, f, 'e', -1, bits)
	} else {
		dst = strconv.AppendFloat(dst, f, 'f', -1, bits)
	}
	for _, c := range dst[start:] {
		if c == '.' || c == 'e' {
			return dst
		}
	}
	return append(dst, '.', '0')
}

const hexDigits = "0123456789abcdef"

// AppendString appends s as a JSON string with the least escaping: the quote, the backslash
// and the control characters. The rest, the text as UTF-8, is written as it is. s must be
// valid UTF-8; a byte that is not becomes U+FFFD.
func AppendString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			dst = append(dst, '\\', c)
		case c == '\n':
			dst = append(dst, '\\', 'n')
		case c == '\r':
			dst = append(dst, '\\', 'r')
		case c == '\t':
			dst = append(dst, '\\', 't')
		case c == '\b':
			dst = append(dst, '\\', 'b')
		case c == '\f':
			dst = append(dst, '\\', 'f')
		case c < 0x20:
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&15])
		case c < utf8.RuneSelf:
			dst = append(dst, c)
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n == 1 {
				dst = append(dst, "�"...)
			} else {
				dst = append(dst, s[i:i+n]...)
			}
			i += n
			continue
		}
		i++
	}
	return append(dst, '"')
}
