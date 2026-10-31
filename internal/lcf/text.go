package lcf

import (
	"slices"
	"strconv"
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

// The strings of a save are bytes in the code page of the game (Windows code pages: 932 for
// Japanese games, 1252 for most Western ones), and the file does not say which, except for saves
// that EasyRPG Player wrote, which have the code page in their easyrpg_data. RPG Maker 2000 and
// 2003 use the code page of the system they run on. EasyRPG finds it with a character set
// detector over the text of the game's database (liblcf, ReaderUtil::DetectEncoding); a save
// has no database, so guessCodepage looks at the few strings the save does have.

const tagTextNotStorable = "TextNotStorable"

// codec turns the bytes of the strings of one save into text and back.
type codec struct {
	cp     int
	name   string
	enc    encoding.Encoding
	reason string // how the code page was chosen
}

type codepage struct {
	cp   int
	name string
	enc  encoding.Encoding
}

// codepages are the code pages that can be used, by number.
var codepages = []codepage{
	{874, "Windows-874 (Thai)", charmap.Windows874},
	{932, "Shift-JIS (code page 932)", japanese.ShiftJIS},
	{936, "GBK (code page 936)", simplifiedchinese.GBK},
	{949, "EUC-KR (code page 949)", korean.EUCKR},
	{950, "Big5 (code page 950)", traditionalchinese.Big5},
	{1250, "Windows-1250 (Central European)", charmap.Windows1250},
	{1251, "Windows-1251 (Cyrillic)", charmap.Windows1251},
	{1252, "Windows-1252 (Western European)", charmap.Windows1252},
	{1253, "Windows-1253 (Greek)", charmap.Windows1253},
	{1254, "Windows-1254 (Turkish)", charmap.Windows1254},
	{1255, "Windows-1255 (Hebrew)", charmap.Windows1255},
	{1256, "Windows-1256 (Arabic)", charmap.Windows1256},
	{1257, "Windows-1257 (Baltic)", charmap.Windows1257},
	{1258, "Windows-1258 (Vietnamese)", charmap.Windows1258},
}

// newCodec is the codec of a code page; ok is false when the code page is not one of codepages.
func newCodec(cp int, reason string) (c *codec, ok bool) {
	for _, p := range codepages {
		if p.cp == cp {
			return &codec{cp: cp, name: p.name, enc: p.enc, reason: reason}, true
		}
	}
	return nil, false
}

// decode is the text of the bytes of a string. Bytes the code page does not define become
// U+FFFD.
func (c *codec) decode(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	out, err := c.enc.NewDecoder().Bytes(b)
	if err != nil {
		return strings.ToValidUTF8(string(b), "�")
	}
	return string(out)
}

// check is the CheckText of the strings of the save.
func (c *codec) check(s string) error {
	_, err := c.encode(s)
	return err
}

// encode is the bytes of a text in the code page. It fails, with a message that is for the
// user, when the code page cannot store some character of the text.
func (c *codec) encode(s string) ([]byte, error) {
	if s == "" {
		return []byte{}, nil
	}
	b, err := c.enc.NewEncoder().Bytes([]byte(s))
	if err == nil && c.decode(b) == s {
		return b, nil
	}
	var bad []string
	seen := map[rune]bool{}
	for _, r := range s {
		if seen[r] {
			continue
		}
		seen[r] = true
		rb, rerr := c.enc.NewEncoder().Bytes([]byte(string(r)))
		if r == utf8.RuneError || rerr != nil || c.decode(rb) != string(r) {
			bad = append(bad, strconv.QuoteRune(r))
		}
	}
	if len(bad) == 0 {
		bad = append(bad, "some of the text")
	}
	if len(bad) > 5 {
		bad = append(bad[:5], "...")
	}
	return nil, errors.New(tagTextNotStorable,
		"The text encoding of this save ("+c.name+") cannot store "+strings.Join(bad, " "),
		"encoding", c.name, "text", s)
}

// guessCodepage chooses the code page of the strings of a save that does not say it. Only the
// strings with a byte above 0x7F count (the names of files and the like are ASCII in every code
// page). The guess is made in this order:
//
//   - no such string: nothing tells, so Windows-1252, the code page of most games;
//   - Cyrillic (1251) if every byte above 0x7F is a Cyrillic letter or common punctuation of
//     1251, there are at least two Cyrillic letters, at least half of the letters are such bytes
//     and at least 60% of those are lowercase (which separates Russian words from the lead bytes
//     of Chinese text);
//   - Japanese (932) if the bytes read as Shift-JIS with kana in them; Korean (949) if they are
//     EUC-KR and at least three quarters of the characters are Hangul; then, for text of at
//     least two characters with no letter of ASCII touching a character of the East Asian
//     script (which separates them from accented Latin letters), Shift-JIS without half-width
//     katakana, else GBK (936), else Big5 (950);
//   - Central European (1250) if there is a byte that is a letter there and a rare symbol in
//     1252 (such as 0xB3, "ł" in 1250 and "³" in 1252);
//   - otherwise Windows-1252.
//
// It is a guess and the Details of the document say so; EasyRPG's saves carry the real value.
// A short Chinese name whose bytes happen to look like Russian, or a Big5 text that is also valid
// GBK, is not told apart.
func guessCodepage(all [][]byte) (cp int, reason string) {
	var strs [][]byte
	for _, s := range all {
		if slices.ContainsFunc(s, func(b byte) bool { return b >= 0x80 }) {
			strs = append(strs, s)
		}
	}
	if len(strs) == 0 {
		return 1252, "all the text is ASCII, so the code page cannot be told; Windows-1252 is assumed"
	}
	cyrillicBytes := true
	var ascii, cyr, lower, strong int
	for _, s := range strs {
		for _, b := range s {
			switch {
			case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z':
				ascii++
			case b >= 0xC0, b == 0xA8, b == 0xB8:
				cyr++
				if b >= 0xE0 || b == 0xB8 {
					lower++
				}
			}
			if b >= 0x80 && b < 0xC0 && !isCyrillicPunctuation(b) {
				cyrillicBytes = false
			}
			switch b {
			case 0x8C, 0x8D, 0x8F, 0x9C, 0x9D, 0x9F, 0xA5, 0xAF, 0xB3, 0xB9:
				strong++
			}
		}
	}
	if cyrillicBytes && cyr >= 2 && cyr*2 >= ascii+cyr && lower*10 >= cyr*6 {
		return 1251, "most letters are Cyrillic letters of Windows-1251; guessed code page 1251"
	}
	if cp, why := guessEastAsian(strs); cp != 0 {
		return cp, why
	}
	if strong > 0 {
		return 1250, "the text has bytes that are letters of Central European text and rare symbols in Western text; guessed code page 1250"
	}
	return 1252, "the text has accented letters but no sign of another script; guessed code page 1252"
}

// isCyrillicPunctuation reports whether b, below 0xC0, is a byte that Russian text in
// Windows-1251 uses besides its letters: no-break space, quotes, dashes, the ellipsis,
// "«", "»" and "№".
func isCyrillicPunctuation(b byte) bool {
	switch {
	case b == 0x85, b >= 0x91 && b <= 0x97, b == 0xA0, b == 0xAB, b == 0xBB, b == 0xB9:
		return true
	}
	return false
}

// runeClass counts what the characters of decoded text are.
type runeClass struct {
	// invalid are the bytes that are not a character of the code page, other the characters that
	// no Japanese, Korean or Chinese text would have.
	invalid, other              int
	kana, halfKana, hangul, han int
	// wide are the characters of the East Asian scripts, mixed the ASCII letters next to one.
	wide, mixed int
}

// classify counts the non-ASCII characters of the strings decoded with enc.
func classify(strs [][]byte, enc encoding.Encoding) runeClass {
	var rc runeClass
	for _, s := range strs {
		out, err := enc.NewDecoder().Bytes(s)
		if err != nil {
			rc.invalid++
			continue
		}
		prevLetter, prevWide := false, false
		for _, r := range string(out) {
			isWide := false
			switch {
			case r < 0x80:
			case r == utf8.RuneError:
				rc.invalid++
			case r >= 0xFF61 && r <= 0xFF9F:
				rc.halfKana++
			case r >= 0x3040 && r <= 0x30FF:
				rc.kana++
				isWide = true
			case r >= 0xAC00 && r <= 0xD7A3:
				rc.hangul++
				isWide = true
			case r >= 0x4E00 && r <= 0x9FFF, r >= 0x3400 && r <= 0x4DBF, r >= 0xF900 && r <= 0xFAFF:
				rc.han++
				isWide = true
			case r >= 0x3000 && r <= 0x303F, r >= 0xFF00 && r <= 0xFFEF, r >= 0x2000 && r <= 0x2BFF,
				r >= 0x0391 && r <= 0x03C9, r >= 0x0410 && r <= 0x044F:
				isWide = true
			default:
				rc.other++
			}
			letter := r < 0x80 && (r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
			if isWide {
				rc.wide++
			}
			if (letter && prevWide) || (isWide && prevLetter) {
				rc.mixed++
			}
			prevLetter, prevWide = letter, isWide
		}
	}
	return rc
}

// isEUCKR reports whether every byte above 0x7F of the strings is in 0xA1-0xFE, as in the
// EUC-KR text of Korean games (Windows' own extension of it, which uses lower lead bytes, is
// also what Shift-JIS kanji looks like).
func isEUCKR(strs [][]byte) bool {
	for _, s := range strs {
		for _, b := range s {
			if b >= 0x80 && (b < 0xA1 || b == 0xFF) {
				return false
			}
		}
	}
	return true
}

// guessEastAsian is the part of guessCodepage for the double-byte code pages; it returns 0 when
// the strings do not look like any of them.
func guessEastAsian(strs [][]byte) (int, string) {
	sjis := classify(strs, japanese.ShiftJIS)
	sjisValid := sjis.invalid == 0 && sjis.other == 0
	if sjisValid && sjis.kana > 0 && sjis.kana >= sjis.halfKana {
		return 932, "the text reads as Japanese (kana) in Shift-JIS; guessed code page 932"
	}
	if isEUCKR(strs) {
		kr := classify(strs, korean.EUCKR)
		if kr.invalid == 0 && kr.other == 0 && kr.mixed == 0 && kr.hangul >= 2 && kr.hangul*4 >= kr.wide*3 {
			return 949, "the text reads as Korean (Hangul) in EUC-KR; guessed code page 949"
		}
	}
	if sjisValid && sjis.mixed == 0 && sjis.han >= 2 && sjis.halfKana == 0 {
		return 932, "the text reads as Japanese (kanji) in Shift-JIS; guessed code page 932"
	}
	gbk := classify(strs, simplifiedchinese.GBK)
	if gbk.invalid == 0 && gbk.other == 0 && gbk.mixed == 0 && gbk.han >= 2 {
		return 936, "the text reads as Chinese in GBK; guessed code page 936"
	}
	big5 := classify(strs, traditionalchinese.Big5)
	if big5.invalid == 0 && big5.other == 0 && big5.mixed == 0 && big5.han >= 2 {
		return 950, "the text reads as Chinese in Big5; guessed code page 950"
	}
	return 0, ""
}
