package rubymarshal

import (
	"bytes"
	"math"
	"strconv"
	"strings"

	"github.com/rm4n0s/errors"
)

// parseFloat reads the text of a Float. Ruby stops at a NUL, where old dumps kept more bytes
// of the mantissa.
func parseFloat(b []byte) (float64, bool) {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	s := string(b)
	switch s {
	case "nan":
		return math.NaN(), true
	case "inf":
		return math.Inf(1), true
	case "-inf":
		return math.Inf(-1), true
	}
	if s == "" || strings.ContainsAny(s, "_ nNiI") {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	return f, true
}

// formatFloat is the text that Ruby's w_float writes: the shortest digits that read back as
// f, with an exponent when the number is large or small ("1e2", "1.5e-05" as "1.5e-5").
func formatFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	case f == 0:
		if math.Signbit(f) {
			return "-0"
		}
		return "0"
	}
	var sb strings.Builder
	if f < 0 {
		sb.WriteByte('-')
		f = -f
	}
	e := strconv.FormatFloat(f, 'e', -1, 64)
	i := strings.IndexByte(e, 'e')
	exp, _ := strconv.Atoi(e[i+1:])
	digits := strings.Replace(e[:i], ".", "", 1)
	decpt := exp + 1
	digs := len(digits)
	switch {
	case decpt < -3 || decpt > digs:
		sb.WriteByte(digits[0])
		if digs > 1 {
			sb.WriteByte('.')
			sb.WriteString(digits[1:])
		}
		sb.WriteString("e" + strconv.Itoa(decpt-1))
	case decpt > 0:
		sb.WriteString(digits[:decpt])
		if digs > decpt {
			sb.WriteByte('.')
			sb.WriteString(digits[decpt:])
		}
	default:
		sb.WriteString("0.")
		sb.WriteString(strings.Repeat("0", -decpt))
		sb.WriteString(digits)
	}
	return sb.String()
}
