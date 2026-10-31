package rpgmv

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/rm4n0s/errors"
)

// These vectors were made by the lz-string that RPG Maker MV ships (js/libs/lz-string.js) and
// by lz-string 1.5.0, whose compressToBase64 is the other layout.
var lzVectors = []struct {
	text         string
	mv, digitsV4 string
}{
	{"", "QAA=", "Q==="},
	{"a", "IZA=", "IZA="},
	{"abababababab", "IYI17SgA", "IYI17Sg="},
	{`{"a":1}`, "N4IghiBcCMC+QA==", "N4IghiBcCMC+Q==="},
	{"€😀 é", "jUEl4NwAewBAl0AA", "jUEl4NwAewBAl0A="},
}

var lzLayouts = []struct {
	name   string
	layout lzLayout
}{{"MV", lzMV}, {"digits", lzDigits}}

func lzUnits(s string) []uint16 { return utf16.Encode([]rune(s)) }

func TestLZStringVectors(t *testing.T) {
	for _, tt := range lzVectors {
		for _, l := range lzLayouts {
			want := tt.mv
			if l.layout == lzDigits {
				want = tt.digitsV4
			}
			if got := lzCompressToBase64(lzUnits(tt.text), l.layout); got != want {
				t.Errorf("%s: compress(%q) = %q, want %q", l.name, tt.text, got, want)
			}
			if tt.text == "" {
				continue
			}
			back, err := lzDecompressFromBase64(want, l.layout, maxUnits)
			if err != nil || string(utf16.Decode(back)) != tt.text {
				t.Errorf("%s: decompress(%q) = %q, %v; want %q", l.name, want, string(utf16.Decode(back)), err, tt.text)
			}
		}
	}
}

func TestLZStringRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for range 300 {
		in := randomUnits(r)
		if len(in) == 0 {
			continue
		}
		for _, l := range lzLayouts {
			packed := lzCompressToBase64(in, l.layout)
			back, err := lzDecompressFromBase64(packed, l.layout, maxUnits)
			if err != nil || !slices.Equal(back, in) {
				t.Fatalf("%s: round trip of %d units failed: %v", l.name, len(in), err)
			}
		}
	}
}

// randomUnits makes a random text as UTF-16 code units: ASCII, Latin-1, other BMP characters,
// astral characters as pairs, lone surrogates, and repeats of earlier parts.
func randomUnits(r *rand.Rand) []uint16 {
	out := []uint16{}
	for range r.Intn(300) {
		switch r.Intn(8) {
		case 0, 1, 2:
			out = append(out, uint16('a'+r.Intn(4)))
		case 3:
			out = append(out, uint16(r.Intn(256)))
		case 4:
			out = append(out, uint16(0x100+r.Intn(0xD000)))
		case 5:
			a, b := utf16.EncodeRune(rune(0x10000 + r.Intn(0xFFFFF)))
			out = append(out, uint16(a), uint16(b))
		case 6:
			out = append(out, uint16(0xD800+r.Intn(0x800)))
		case 7:
			if len(out) > 4 {
				from := r.Intn(len(out) - 2)
				out = append(out, out[from:from+1+r.Intn(len(out)-from-1)]...)
			}
		}
	}
	if r.Intn(10) == 0 {
		out = slices.Repeat(out, 1+r.Intn(5))
	}
	return out
}

func TestLZStringDecompressErrors(t *testing.T) {
	const base64 = "lzDecompressFromBase64.LZStringCorrupt"
	const core = "lzDecompressFromBase64->lzDecompress.LZStringCorrupt"
	tests := []struct {
		name, in string
		layout   lzLayout
		route    string
	}{
		{"empty", "", lzMV, base64},
		{"only padding", "====", lzMV, base64},
		{"not base64", "IY!A=", lzMV, base64},
		{"no text", "QAA=", lzMV, core},
		{"truncated", "N4IghiBc", lzMV, core},
		{"truncated digits", "N4Ighi", lzDigits, core},
		{"end marker first", "g", lzDigits, core},
		{"bad code", "IYBw", lzDigits, core},
		{"bad code MV", "sIAA", lzMV, core},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := lzDecompressFromBase64(tt.in, tt.layout, maxUnits)
			if err == nil {
				t.Fatalf("decompressed to %v", out)
			}
			assertRoute(t, err, tt.route)
		})
	}
	_, err := lzDecompress(nil, 16, maxUnits)
	assertRoute(t, err, "lzDecompress.LZStringCorrupt")
	// A text longer than the limit.
	packed := lzCompressToBase64(lzUnits(strings.Repeat("abc", 1000)), lzMV)
	_, err = lzDecompressFromBase64(packed, lzMV, 100)
	assertRoute(t, err, "lzDecompressFromBase64->lzDecompress.LZStringCorrupt")
	if _, err := lzDecompressFromBase64(packed, lzMV, 3000); err != nil {
		t.Errorf("a text at the limit: %v", err)
	}
}

func TestLZStringBomb(t *testing.T) {
	// A long run of one character is a short compressed text; the limit stops a huge one.
	big := make([]uint16, 3<<20)
	for i := range big {
		big[i] = 'a'
	}
	packed := lzCompressToBase64(big, lzMV)
	if len(packed) > 60000 {
		t.Fatalf("packed size %d", len(packed))
	}
	back, err := lzDecompressFromBase64(packed, lzMV, maxUnits)
	if err != nil || len(back) != len(big) {
		t.Fatalf("%d units, %v", len(back), err)
	}
}

func TestUnitsConversion(t *testing.T) {
	text := "aé€😀\u0000z"
	u := toUnits([]byte(text))
	if !slices.Equal(u, lzUnits(text)) {
		t.Error("toUnits differs from utf16.Encode")
	}
	b, err := toUTF8(u)
	if err != nil || string(b) != text {
		t.Errorf("toUTF8 = %q, %v", b, err)
	}
	for _, bad := range [][]uint16{{0xD800}, {0xDC00, 'a'}, {'a', 0xD800, 'b'}, {0xD800, 0xD800, 0xDC00}} {
		_, err := toUTF8(bad)
		assertRoute(t, err, "toUTF8.LZStringSurrogate")
	}
}

func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	e, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("want an *errors.Error, got %T %v", err, err)
	}
	if !e.HasRoute(route) {
		t.Errorf("route %s\nwant %s", e.Route(), route)
	}
}

// node runs a script of testdata under node with the input on stdin, or skips the test when
// node is missing.
func node(t *testing.T, script string, input []byte, args ...string) []byte {
	t.Helper()
	path, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	cmd := exec.Command(path, append([]string{filepath.Join("testdata", script)}, args...)...)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node %s: %v\n%s", script, err, stderr.String())
	}
	return out
}

type lzLibrary struct {
	path   string
	layout lzLayout
}

// lzLibraries are the lz-string files to check against: the one in an RPG Maker MV
// corescript checkout (RPGMV_CORESCRIPT), and the lz-string.js files named by RPGMV_LZSTRING
// (a path list), which are of lz-string 1.4 or later.
func lzLibraries(t *testing.T) []lzLibrary {
	var libs []lzLibrary
	if dir := os.Getenv("RPGMV_CORESCRIPT"); dir != "" {
		libs = append(libs, lzLibrary{filepath.Join(dir, "js", "libs", "lz-string.js"), lzMV})
	}
	for _, p := range filepath.SplitList(os.Getenv("RPGMV_LZSTRING")) {
		libs = append(libs, lzLibrary{p, lzDigits})
	}
	if len(libs) == 0 {
		t.Skip("RPGMV_CORESCRIPT (a checkout of github.com/rpgtkoolmv/corescript) or RPGMV_LZSTRING (an lz-string.js) is not set")
	}
	return libs
}

func TestLZStringAgainstJavaScript(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	texts := [][]uint16{
		lzUnits("a"), lzUnits("hello hello hello"), lzUnits(strings.Repeat("0123456789", 500)),
		lzUnits(`{"actors":[null,{"_name":"Ünï 😀","@":"Game_Actor"}],"gold":1e21}`),
		lzUnits("€"), lzUnits("😀😀😀"), {0xD800}, {'a', 0xDC00},
	}
	for range 250 {
		texts = append(texts, randomUnits(r))
	}
	type testCase struct {
		Units []uint16 `json:"units"`
		Go    string   `json:"go"`
	}
	for _, lib := range lzLibraries(t) {
		var cases []testCase
		for _, u := range texts {
			cases = append(cases, testCase{u, lzCompressToBase64(u, lib.layout)})
		}
		input, err := json.Marshal(cases)
		if err != nil {
			t.Fatal(err)
		}
		var results []struct {
			Compressed string   `json:"compressed"`
			GoBack     []uint16 `json:"goBack"`
		}
		if err := json.Unmarshal(node(t, "lzcheck.js", input, lib.path), &results); err != nil {
			t.Fatal(err)
		}
		for i, c := range cases {
			if results[i].Compressed != c.Go {
				t.Fatalf("%s: text %d (%d units): lz-string made %q, Go made %q", lib.path, i, len(c.Units), results[i].Compressed, c.Go)
			}
			if len(c.Units) > 0 && !slices.Equal(results[i].GoBack, c.Units) {
				t.Fatalf("%s: text %d: lz-string read the Go output back as %d units, want %d", lib.path, i, len(results[i].GoBack), len(c.Units))
			}
			// And the other way: Go reads what lz-string made.
			back, err := lzDecompressFromBase64(results[i].Compressed, lib.layout, maxUnits)
			if len(c.Units) > 0 && (err != nil || !slices.Equal(back, c.Units)) {
				t.Fatalf("%s: text %d: Go could not read the output of lz-string: %v", lib.path, i, err)
			}
		}
	}
}
