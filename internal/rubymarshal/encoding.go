package rubymarshal

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// encKind is how the bytes of a String are turned into text.
type encKind uint8

const (
	// encUTF8 is UTF-8 text.
	encUTF8 encKind = iota
	// encASCII is US-ASCII text.
	encASCII
	// encBinary is ASCII-8BIT: bytes that are shown as text when they are valid UTF-8, which is
	// what a legacy game that wrote text to a binary String holds.
	encBinary
	// encCharset is text of another character set, converted by golang.org/x/text.
	encCharset
	// encUnknown is an encoding this package has no table for; its strings are shown as bytes.
	encUnknown
)

// textEncoding is the encoding that a Ruby String carries.
type textEncoding struct {
	name    string
	kind    encKind
	charset encoding.Encoding
}

var (
	encodingUTF8   = &textEncoding{name: "UTF-8", kind: encUTF8}
	encodingASCII  = &textEncoding{name: "US-ASCII", kind: encASCII}
	encodingBinary = &textEncoding{name: "ASCII-8BIT", kind: encBinary}
)

// charsets are the other encodings that a RPG Maker game is likely to use, by the
// lower-cased name Ruby gives them (and a few aliases).
var charsets = map[string]encoding.Encoding{
	"shift_jis":    japanese.ShiftJIS,
	"windows-31j":  japanese.ShiftJIS,
	"cp932":        japanese.ShiftJIS,
	"euc-jp":       japanese.EUCJP,
	"iso-8859-1":   charmap.ISO8859_1,
	"iso-8859-2":   charmap.ISO8859_2,
	"iso-8859-15":  charmap.ISO8859_15,
	"windows-1250": charmap.Windows1250,
	"windows-1251": charmap.Windows1251,
	"windows-1252": charmap.Windows1252,
	"windows-1253": charmap.Windows1253,
	"windows-1254": charmap.Windows1254,
	"windows-1257": charmap.Windows1257,
	"gbk":          simplifiedchinese.GBK,
	"gb2312":       simplifiedchinese.GBK,
	"gb18030":      simplifiedchinese.GB18030,
	"big5":         traditionalchinese.Big5,
	"euc-kr":       korean.EUCKR,
	"windows-949":  korean.EUCKR,
}

// lookupEncoding is the encoding that Ruby calls name.
func lookupEncoding(name string) *textEncoding {
	lower := strings.ToLower(name)
	switch lower {
	case "utf-8":
		return encodingUTF8
	case "us-ascii", "ascii", "ansi_x3.4-1968":
		return encodingASCII
	case "ascii-8bit", "binary":
		return encodingBinary
	}
	if cs, ok := charsets[lower]; ok {
		return &textEncoding{name: name, kind: encCharset, charset: cs}
	}
	return &textEncoding{name: name, kind: encUnknown}
}

// Name is the name of the encoding as Ruby spells it.
func (e *textEncoding) Name() string { return e.name }

// decode is the text of the bytes, or false when the bytes are not text of this encoding
// that encode gives back unchanged.
func (e *textEncoding) decode(b []byte) (string, bool) {
	switch e.kind {
	case encUTF8, encBinary:
		return string(b), utf8.Valid(b)
	case encASCII:
		for _, c := range b {
			if c >= utf8.RuneSelf {
				return "", false
			}
		}
		return string(b), true
	case encCharset:
		out, err := e.charset.NewDecoder().Bytes(b)
		if err != nil || !utf8.Valid(out) {
			return "", false
		}
		back, err := e.charset.NewEncoder().Bytes(out)
		if err != nil || !bytes.Equal(back, b) {
			return "", false
		}
		return string(out), true
	default:
		return "", false
	}
}

// encode is the bytes that store the text.
func (e *textEncoding) encode(s string) ([]byte, error) {
	switch e.kind {
	case encUTF8, encBinary:
		if !utf8.ValidString(s) {
			return nil, errors.New(TagUnstorableText, "The text is not valid UTF-8", "encoding", e.name)
		}
		return []byte(s), nil
	case encASCII:
		for i := 0; i < len(s); i++ {
			if s[i] >= utf8.RuneSelf {
				return nil, errors.New(TagUnstorableText, "This string is US-ASCII: use only ASCII characters", "encoding", e.name)
			}
		}
		return []byte(s), nil
	case encCharset:
		out, err := e.charset.NewEncoder().Bytes([]byte(s))
		if err != nil {
			return nil, errors.New(TagUnstorableText,
				"This string is "+e.name+": some of these characters cannot be stored in it", "encoding", e.name)
		}
		return out, nil
	default:
		return nil, errors.New(TagUnstorableText, "The encoding of this string, "+e.name+", is not supported", "encoding", e.name)
	}
}

// check is the CheckText of a String site: nil when the text can be stored.
func (e *textEncoding) check(s string) error {
	_, err := e.encode(s)
	return err
}

// TagUnstorableText tags the refusal of a text that the encoding of a String cannot store.
const TagUnstorableText = "UnstorableText"
