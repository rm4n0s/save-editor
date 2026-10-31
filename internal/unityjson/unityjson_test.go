package unityjson

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"io"
	"math"
	"math/big"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/jsondoc"
	"github.com/rm4n0s/save-editor/internal/model"
)

const (
	kindJSON = "json"
	kindES3  = "es3"
	kindNone = "none"
)

// fixtures are the files of testdata and the format that detects each one.
var fixtures = []struct {
	name string
	want string
}{
	{"plain.json", kindJSON},
	{"root-array.json", kindJSON},
	{"es3-plain.es3", kindES3},
	{"es3-gzip.es3", kindES3},
	{"es3-aes.es3", kindES3},
	{"es3-aes-gzip.es3", kindES3},
	{"es3-other-password.es3", kindES3}, // detected by its shape and name, refused by Parse
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func formatOf(name string, data []byte) string {
	switch {
	case FormatES3.Detect(name, data):
		return kindES3
	case FormatJSON.Detect(name, data):
		return kindJSON
	}
	return kindNone
}

func parse(t *testing.T, name string) (*Document, []byte) {
	t.Helper()
	data := readFixture(t, name)
	f := FormatES3
	if strings.HasSuffix(name, ".json") {
		f = FormatJSON
	}
	doc, err := f.Parse(name, data)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return doc.(*Document), data
}

func at(t *testing.T, doc model.Document, path ...string) model.Slot {
	t.Helper()
	var slot model.Slot
	found := false
	for _, v := range doc.Variables() {
		if v.Name == path[0] {
			slot, found = v.Slot, true
			break
		}
	}
	if !found {
		t.Fatalf("no variable %q", path[0])
	}
	for _, label := range path[1:] {
		found = false
		for _, c := range slot.Value.Children {
			if c.Label == label {
				slot, found = c.Slot, true
				break
			}
		}
		if !found {
			t.Fatalf("no part %q in %v", label, path)
		}
	}
	return slot
}

func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	e, ok := errors.FromError(err)
	if !ok {
		t.Errorf("expected *errors.Error with route %s, got %T: %v", route, err, err)
		return
	}
	if !e.HasRoute(route) {
		t.Errorf("route %s, want %s", e.Route(), route)
	}
}

func i64(n int64) *model.Value  { return &model.Value{Kind: model.KindInt, Int: big.NewInt(n)} }
func fl(f float64) *model.Value { return &model.Value{Kind: model.KindFloat, Float: f} }
func str(s string) *model.Value { return &model.Value{Kind: model.KindString, Str: s} }
func boo(b bool) *model.Value   { return &model.Value{Kind: model.KindBool, Bool: b} }

func TestDetect(t *testing.T) {
	for _, f := range fixtures {
		if got := formatOf(f.name, readFixture(t, f.name)); got != f.want {
			t.Errorf("%s is taken by %s, want %s", f.name, got, f.want)
		}
	}
	// A JSON file has any name, and a .json name alone does not make a file JSON.
	plain := readFixture(t, "plain.json")
	for _, name := range []string{"save.dat", "x", "SAVE.JSON", "a.txt"} {
		if got := formatOf(name, plain); got != kindJSON {
			t.Errorf("plain JSON called %s is taken by %s", name, got)
		}
	}
	// With a BOM and white space in front.
	if got := formatOf("a.json", append([]byte("\xEF\xBB\xBF \r\n\t"), plain...)); got != kindJSON {
		t.Errorf("JSON with a byte order mark is taken by %s", got)
	}
	// An Easy Save file with a name that is not .es3 is still Easy Save's: the contents tell.
	for _, name := range []string{"es3-plain.es3", "es3-gzip.es3", "es3-aes.es3", "es3-aes-gzip.es3"} {
		if got := formatOf("slot1.dat", readFixture(t, name)); got != kindES3 {
			t.Errorf("%s under another name is taken by %s", name, got)
		}
	}
	notJSON := map[string][]byte{
		"empty":            nil,
		"text":             []byte("hello"),
		"a number":         []byte("42"),
		"a string":         []byte(`"text"`),
		"true":             []byte("true"),
		"xml":              []byte("<unity_prefs/>"),
		"base64":           []byte("N4IgzgnmAuCmC2IBcoD6YCGA3WBRAdhg"),
		"a broken text":    []byte(`{"a":`),
		"a broken array":   []byte(`[1,`),
		"binary":           {0, 1, 2, 3, '{'},
		"text then object": []byte(`note {"a":1}`),
	}
	for name, data := range notJSON {
		for _, file := range []string{"x.dat", "x.bin", "x.save", "x.es3"} {
			if got := formatOf(file, data); got != kindNone {
				t.Errorf("%s called %s is taken by %s", name, file, got)
			}
		}
	}
	// What begins as JSON and has a mistake in it is taken when the name says JSON, so that the
	// mistake is shown, and by no other name.
	for name, data := range map[string][]byte{"a broken object": []byte(`{"a":`), "a broken array": []byte(`[1,`)} {
		if got := formatOf("x.json", data); got != kindJSON {
			t.Errorf("%s called x.json is taken by %s", name, got)
		}
		if got := formatOf("x.dat", data); got != kindNone {
			t.Errorf("%s called x.dat is taken by %s", name, got)
		}
		_, err := FormatJSON.Parse("x.json", data)
		assertRoute(t, err, "parseJSON->Parse->ParseOptions->parser.parseDocument")
	}
	// An object that is not an Easy Save file is JSON, an Easy Save entry makes it Easy Save's.
	if got := formatOf("x.json", []byte(`{"a":{"__type":"int","value":1}}`)); got != kindES3 {
		t.Errorf("an Easy Save entry is taken by %s", got)
	}
	if got := formatOf("x.json", []byte(`{"a":{"__type":"int","value":1},"b":2}`)); got != kindJSON {
		t.Errorf("a mix of an entry and a number is taken by %s", got)
	}
	if got := formatOf("x.json", []byte(`{"a":{"__type":5}}`)); got != kindJSON {
		t.Errorf("an object with a __type that is a number is taken by %s", got)
	}
	if got := formatOf("x.json", []byte(`{}`)); got != kindJSON {
		t.Errorf("an empty object is taken by %s", got)
	}
}

// TestDetectOthers checks the files of every other package's testdata: none is taken but the
// JSON that the tests of other packages keep next to their fixtures.
func TestDetectOthers(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "*", "testdata", "*"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, path := range paths {
		if filepath.Dir(path) == filepath.Join("..", "unityjson", "testdata") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		count++
		isJSON := strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".expected")
		for _, name := range []string{filepath.Base(path), "x.dat", "x.bin", "x.save", "x.sav", "x.json", "x.txt"} {
			got := formatOf(name, data)
			if got != kindNone && (got != kindJSON || !isJSON) {
				t.Errorf("%s called %s is taken by %s", path, name, got)
			}
		}
	}
	if count < 40 {
		t.Errorf("only %d files of other formats were checked", count)
	}
}

func TestParse(t *testing.T) {
	want := map[string][]string{
		"playerName": {"string", `"Ünity Hero"`}, "level": {"int", "12"}, "gold": {"long", "5000000000"},
		"health": {"float", "87.5"}, "speed": {"double", "3.75"}, "alive": {"bool", "true"}, "rank": {"byte", "200"},
		"position": {"Vector3", "Vector3 (3 parts)"}, "inventory": {"List", "List (2 items)"}, "nothing": {"string", "null"},
	}
	for _, name := range []string{"es3-plain.es3", "es3-gzip.es3", "es3-aes.es3", "es3-aes-gzip.es3"} {
		t.Run(name, func(t *testing.T) {
			doc, _ := parse(t, name)
			vars := doc.Variables()
			if len(vars) != len(want) {
				t.Fatalf("%d variables", len(vars))
			}
			for _, v := range vars {
				w := want[v.Name]
				if v.Slot.Value.TypeName() != w[0] || v.Slot.Value.Repr() != w[1] {
					t.Errorf("%s = %s %s, want %s %s", v.Name, v.Slot.Value.TypeName(), v.Slot.Value.Repr(), w[0], w[1])
				}
				if v.Internal {
					t.Errorf("%s is internal", v.Name)
				}
			}
			// The wrapper and the "__type" keys are not shown, and cannot be edited.
			for _, v := range vars {
				for _, c := range v.Slot.Value.Children {
					if c.Label == "__type" || c.Label == "value" {
						t.Errorf("%s shows the key %q", v.Name, c.Label)
					}
				}
			}
			if !at(t, doc, "health").Value.Float32 || at(t, doc, "speed").Value.Float32 {
				t.Error("float is single precision and double is not")
			}
			details := map[string]string{}
			for _, d := range doc.Details() {
				details[d.Label] = d.Text
			}
			wantGz, wantAES := "none", "none"
			if strings.Contains(name, "gzip") {
				wantGz = "gzip"
			}
			if strings.Contains(name, "aes") {
				wantAES = "AES-128 (the default password)"
			}
			if details["Layout"] != "Easy Save 3 (JSON)" || details["Compression"] != wantGz || details["Encryption"] != wantAES || details["Entries"] != "10" {
				t.Errorf("details %v", details)
			}
		})
	}
	plain, _ := parse(t, "plain.json")
	for path, w := range map[string]string{"level": "12", "position/x": "1.5", "inventory/[2]": "3", "stats/tags": "list (0 items)", "nothing": "null"} {
		if got := at(t, plain, strings.Split(path, "/")...).Value.Repr(); got != w {
			t.Errorf("%s = %s, want %s", path, got, w)
		}
	}
	if n := len(plain.Details()); n != 2 || plain.Details()[0].Text != "JSON text" {
		t.Errorf("details %+v", plain.Details())
	}
	arr, _ := parse(t, "root-array.json")
	if v := arr.Variables(); len(v) != 6 || v[0].Name != "[0]" || v[2].Slot.Value.Kind != model.KindMap {
		t.Errorf("array variables %+v", v)
	}
}

func TestDuplicateKeys(t *testing.T) {
	doc, err := FormatJSON.Parse("x.json", []byte(`{"a":1,"a":2,"a":3}`))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, v := range doc.Variables() {
		names = append(names, v.Name)
	}
	if strings.Join(names, ",") != "a,a #2,a #3" {
		t.Errorf("names %v", names)
	}
}

func TestRoundTrip(t *testing.T) {
	for _, f := range fixtures {
		if f.name == "es3-other-password.es3" {
			continue
		}
		doc, data := parse(t, f.name)
		for _, edits := range [][]model.Edit{nil, {}} {
			out, err := doc.Encode(edits)
			if err != nil || !bytes.Equal(out, data) {
				t.Errorf("%s: Encode with no edits: %v, equal %v", f.name, err, bytes.Equal(out, data))
			}
			out[0] ^= 0xFF
		}
	}
}

// place is a primitive of the tree with its repr and site.
type place struct {
	repr string
	site *model.Site
}

func places(doc model.Document) map[string]place {
	out := map[string]place{}
	var walk func(path string, s model.Slot)
	walk = func(path string, s model.Slot) {
		out[path] = place{s.Value.Repr(), s.Site}
		for _, c := range s.Value.Children {
			walk(path+"/"+c.Label, c.Slot)
		}
	}
	for _, v := range doc.Variables() {
		walk(v.Name, v.Slot)
	}
	return out
}

var edits = []struct {
	name  string
	path  []string
	value *model.Value
	want  string // what the edited place shows
}{
	{"string", []string{"playerName"}, str("Zoë \"the\" 😀\n"), `"Zoë \"the\" 😀\n"`},
	{"string to empty", []string{"playerName"}, str(""), `""`},
	{"int", []string{"level"}, i64(-2147483648), "-2147483648"},
	{"int largest", []string{"level"}, i64(2147483647), "2147483647"},
	{"long", []string{"gold"}, i64(-9223372036854775808), "-9223372036854775808"},
	{"byte", []string{"rank"}, i64(0), "0"},
	{"float", []string{"health"}, fl(0.1), "0.1"},
	{"float whole", []string{"health"}, fl(3), "3.0"},
	{"double", []string{"speed"}, fl(1e300), "1e+300"},
	{"bool", []string{"alive"}, boo(false), "false"},
	{"nested float", []string{"position", "x"}, fl(-0.25), "-0.25"},
	{"nested int", []string{"position", "y"}, i64(7), "7"},
	{"list item", []string{"inventory", "[1]"}, str("bow"), `"bow"`},
}

func TestEdits(t *testing.T) {
	for _, file := range []string{"es3-plain.es3", "es3-gzip.es3", "es3-aes.es3", "es3-aes-gzip.es3"} {
		for _, tt := range edits {
			t.Run(file+" "+tt.name, func(t *testing.T) {
				doc, data := parse(t, file)
				slot := at(t, doc, tt.path...)
				if slot.Site == nil || slot.Value.Kind != tt.value.Kind {
					t.Fatalf("the old value is %v, site %v", slot.Value.Kind, slot.Site)
				}
				before := places(doc)
				out, err := doc.Encode([]model.Edit{{Site: slot.Site, Value: tt.value}})
				if err != nil {
					t.Fatal(err)
				}
				again, err := FormatES3.Parse(file, out)
				if err != nil {
					t.Fatalf("the output does not read back: %v", err)
				}
				after := places(again)
				if len(after) != len(before) {
					t.Errorf("%d places, want %d", len(after), len(before))
				}
				for path, old := range before {
					want := old.repr
					if old.site == slot.Site {
						want = tt.want
					}
					if got := after[path].repr; got != want {
						t.Errorf("%s = %s, want %s", path, got, want)
					}
				}
				// The layers are the same as before, around the new text.
				checkLayers(t, file, data, out)
			})
		}
	}
	for _, tt := range []struct {
		file string
		path []string
		v    *model.Value
		want string
	}{
		{"plain.json", []string{"level"}, i64(1 << 52), "4503599627370496"},
		{"plain.json", []string{"inventory", "[2]"}, i64(-9007199254740991), "-9007199254740991"},
		{"plain.json", []string{"stats", "luck"}, fl(2.5), "2.5"},
		{"plain.json", []string{"alive"}, boo(false), "false"},
		{"root-array.json", []string{"[1]"}, str("zwei"), `"zwei"`},
		{"root-array.json", []string{"[3]", "[0]"}, i64(40), "40"},
	} {
		t.Run(tt.file+" "+strings.Join(tt.path, "/"), func(t *testing.T) {
			doc, data := parse(t, tt.file)
			slot := at(t, doc, tt.path...)
			out, err := doc.Encode([]model.Edit{{Site: slot.Site, Value: tt.v}})
			if err != nil {
				t.Fatal(err)
			}
			again, err := FormatJSON.Parse(tt.file, out)
			if err != nil {
				t.Fatal(err)
			}
			if got := at(t, again, tt.path...).Value.Repr(); got != tt.want {
				t.Errorf("reads back as %s, want %s", got, tt.want)
			}
			// Only the bytes of the value changed.
			span := slot.Site.Ref.(*jsondoc.Span)
			tail := len(data) - span.End
			if !bytes.Equal(out[:span.Start], data[:span.Start]) || !bytes.Equal(out[len(out)-tail:], data[span.End:]) {
				t.Error("bytes outside the edited value changed")
			}
		})
	}
}

// checkLayers checks that the written file has the layers of the original.
func checkLayers(t *testing.T, name string, old, out []byte) {
	t.Helper()
	switch name {
	case "es3-plain.es3":
		if !looksLikeJSON(out) {
			t.Error("a plain file is not text")
		}
	case "es3-gzip.es3":
		if !isGzip(out) || !bytes.Equal(out[:10], old[:10]) {
			t.Errorf("the gzip header is %x, was %x", out[:min(10, len(out))], old[:10])
		}
	default:
		if len(out) < 2*blockSize || len(out)%blockSize != 0 || validJSON(out) {
			t.Fatalf("the encrypted file has %d bytes", len(out))
		}
		if bytes.Equal(out[:blockSize], old[:blockSize]) {
			t.Error("the IV was reused for a different text")
		}
		plain, ok, err := decrypt(out, "password")
		if err != nil || !ok {
			t.Fatalf("the output does not decrypt: %v %v", ok, err)
		}
		if strings.Contains(name, "gzip") != isGzip(plain) {
			t.Errorf("the gzip layer inside the encryption: %v", isGzip(plain))
		}
	}
}

// opensslDecrypt decrypts a file of the AES layer with the openssl program and a key derived
// by openssl itself, as an independent check of the cipher and the key derivation.
func opensslDecrypt(t *testing.T, file []byte, password string) []byte {
	t.Helper()
	path, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl is not installed")
	}
	iv := hex.EncodeToString(file[:blockSize])
	key, err := exec.Command(path, "kdf", "-keylen", "16", "-kdfopt", "digest:SHA1", "-kdfopt", "pass:"+password,
		"-kdfopt", "hexsalt:"+iv, "-kdfopt", "iter:100", "-binary", "PBKDF2").Output()
	if err != nil {
		t.Skipf("openssl kdf: %v", err)
	}
	cmd := exec.Command(path, "enc", "-d", "-aes-128-cbc", "-K", hex.EncodeToString(key), "-iv", iv)
	cmd.Stdin = bytes.NewReader(file[blockSize:])
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("openssl enc: %v\n%s", err, stderr.String())
	}
	return out
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAgainstOpenssl: the fixtures were made with openssl (gen.py) and our output is read by
// openssl, so the key derivation (PBKDF2-HMAC-SHA1, salt = IV, 100 iterations, 16 bytes), the
// cipher (AES-128-CBC) and the padding are not only what the Go code believes.
func TestAgainstOpenssl(t *testing.T) {
	plainDoc, _ := parse(t, "es3-plain.es3")
	for _, file := range []string{"es3-aes.es3", "es3-aes-gzip.es3"} {
		doc, data := parse(t, file)
		level := at(t, doc, "level")
		out, err := doc.Encode([]model.Edit{{Site: level.Site, Value: i64(99)}})
		if err != nil {
			t.Fatal(err)
		}
		edited, err := plainDoc.Encode([]model.Edit{{Site: at(t, plainDoc, "level").Site, Value: i64(99)}})
		if err != nil {
			t.Fatal(err)
		}
		original, err := plainDoc.Encode(nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct{ file, want []byte }{{data, original}, {out, edited}} {
			got := opensslDecrypt(t, c.file, "password")
			if strings.Contains(file, "gzip") {
				got = gunzip(t, got)
			}
			if !bytes.Equal(got, c.want) {
				t.Errorf("%s: openssl reads\n%s\nwant\n%s", file, got, c.want)
			}
		}
	}
	// The file with another password is what the password says: openssl opens it with that one.
	other := readFixture(t, "es3-other-password.es3")
	if got := opensslDecrypt(t, other, "hunter2"); !looksLikeJSON(got) {
		t.Error("the fixture of another password is not encrypted with it")
	}
}

func TestBoundsOfTypes(t *testing.T) {
	doc, _ := parse(t, "es3-plain.es3")
	tests := []struct {
		name   string
		lo, hi string
	}{
		{"level", "-2147483648", "2147483647"},
		{"gold", "-9223372036854775808", "9223372036854775807"},
		{"rank", "0", "255"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := at(t, doc, tt.name).Site
			lo, _ := new(big.Int).SetString(tt.lo, 10)
			hi, _ := new(big.Int).SetString(tt.hi, 10)
			for _, ok := range []*big.Int{lo, hi} {
				out, err := doc.Encode([]model.Edit{{Site: site, Value: &model.Value{Kind: model.KindInt, Int: ok}}})
				if err != nil {
					t.Fatalf("%v: %v", ok, err)
				}
				again, err := FormatES3.Parse("x.es3", out)
				if err != nil {
					t.Fatal(err)
				}
				if got := at(t, again, tt.name).Value.Int; got.Cmp(ok) != 0 {
					t.Errorf("%v reads back as %v", ok, got)
				}
			}
			for _, bad := range []*big.Int{new(big.Int).Sub(lo, big.NewInt(1)), new(big.Int).Add(hi, big.NewInt(1))} {
				_, err := doc.Encode([]model.Edit{{Site: site, Value: &model.Value{Kind: model.KindInt, Int: bad}}})
				assertRoute(t, err, "Encode->Doc.Patch.JsonEditRange")
			}
		})
	}
}

func TestRefusedEdits(t *testing.T) {
	doc, _ := parse(t, "es3-aes-gzip.es3")
	other, _ := parse(t, "es3-aes-gzip.es3")
	level, name := at(t, doc, "level").Site, at(t, doc, "playerName").Site
	tests := []struct {
		name  string
		edits []model.Edit
		route string
	}{
		{"a site of another document", []model.Edit{{Site: at(t, other, "level").Site, Value: i64(1)}}, "Encode->Doc.Patch.JsonEditSite"},
		{"no site", []model.Edit{{Value: i64(1)}}, "Encode->Doc.Patch.JsonEditSite"},
		{"a text for a number", []model.Edit{{Site: level, Value: str("1")}}, "Encode->Doc.Patch.JsonEditKind"},
		{"a number for a text", []model.Edit{{Site: name, Value: i64(1)}}, "Encode->Doc.Patch.JsonEditKind"},
		{"the same site twice", []model.Edit{{Site: level, Value: i64(1)}, {Site: level, Value: i64(2)}}, "Encode->Doc.Patch.JsonEditTwice"},
		{"a text that is not UTF-8", []model.Edit{{Site: name, Value: str("a\xffb")}}, "Encode->Doc.Patch.JsonEditText"},
		{"a number beyond the type", []model.Edit{{Site: level, Value: i64(1 << 31)}}, "Encode->Doc.Patch.JsonEditRange"},
		{"a float that is not finite", []model.Edit{{Site: at(t, doc, "speed").Site, Value: fl(math.Inf(1))}}, "Encode->Doc.Patch.JsonEditFloat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := doc.Encode(tt.edits)
			if err == nil {
				t.Fatalf("accepted: %d bytes", len(out))
			}
			assertRoute(t, err, tt.route)
		})
	}
}

func TestLooksEncrypted(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	random := func(n int) []byte {
		b := make([]byte, n)
		r.Read(b)
		return b
	}
	text := bytes.Repeat([]byte("abcdefgh"), 8)
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"random, 32 bytes", random(32), true},
		{"random, 4096 bytes", random(4096), true},
		{"random, not whole blocks", random(33), false},
		{"random, one block", random(16), false},
		{"empty", nil, false},
		{"zeros", make([]byte, 64), false},
		{"text", text, false},
		{"small numbers", bytes.Repeat([]byte{1, 2, 3, 4}, 16), false},
		{"mostly one byte", append(bytes.Repeat([]byte{0}, 60), random(4)...), false},
	}
	for _, tt := range tests {
		if got := looksEncrypted(tt.data); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
	// A random file is taken by name only: an .es3 file of whole blocks. Parse says why.
	data := random(1024)
	for name, want := range map[string]bool{"x.es3": true, "X.ES3": true, "x.dat": false, "x.bin": false, "x.json": false, "x": false} {
		if got := FormatES3.Detect(name, data); got != want {
			t.Errorf("random bytes called %s: detected %v, want %v", name, got, want)
		}
		if FormatJSON.Detect(name, data) && data[0] != '{' && data[0] != '[' {
			t.Errorf("random bytes called %s are JSON", name)
		}
	}
	if FormatES3.Detect("x.es3", random(1000)) {
		t.Error("random bytes that are not whole blocks are taken")
	}
	_, err := FormatES3.Parse("x.es3", data)
	assertRoute(t, err, "parseES3.UnityJSONEncrypted")
	if e, _ := errors.FromError(err); !strings.Contains(e.Message, "password other than the default") {
		t.Errorf("message %q", e.Message)
	}
}

func TestErrors(t *testing.T) {
	text := readFixture(t, "es3-plain.es3")
	bomb, err := wrap(bytes.Repeat([]byte{'['}, 64), []layer{{kind: layerGzip}})
	if err != nil {
		t.Fatal(err)
	}
	corrupt := bytes.Clone(readFixture(t, "es3-gzip.es3"))
	corrupt[len(corrupt)/2] ^= 0x55
	corrupt = corrupt[:len(corrupt)-4] // the trailer is cut too

	tests := []struct {
		name  string
		run   func() error
		route string
	}{
		{"JSON that is not", func() error { _, err := parseJSON("x.json", []byte("hello")); return err }, "parseJSON.UnityJSONNotSave"},
		{"Easy Save that is not", func() error { _, err := parseES3("x.es3", []byte("hello")); return err }, "parseES3.UnityJSONNotSave"},
		{"encrypted with another password", func() error { _, err := parseES3("x.es3", readFixture(t, "es3-other-password.es3")); return err }, "parseES3.UnityJSONEncrypted"},
		{"gzip that is cut", func() error { _, err := parseES3("x.es3", corrupt); return err }, "parseES3->unwrap->inflate.UnityJSONGzip"},
		{"gzip of text that is too long", func() error { _, _, err := inflate(bomb, 10); return err }, "inflate.UnityJSONGzipLarge"},
		{"gzip of text that is not JSON", func() error { _, err := parseES3("x.es3", mustWrap(t, []byte("hello"), layerGzip)); return err }, "parseES3.UnityJSONNotSave"},
		{"gzip of JSON that is broken", func() error { _, err := parseES3("x.es3", mustWrap(t, []byte(`{"a":`), layerGzip)); return err }, "parseES3->Parse"},
		{"a key that cannot be derived", func() error { _, err := newCipher("p", make([]byte, 16), 0); return err }, "newCipher.UnityJSONCrypto"},
		{"a key of a size AES does not have", func() error { _, err := newCipher("p", make([]byte, 16), 15); return err }, "newCipher.UnityJSONCrypto"},
		{"no random bytes", func() error { _, err := encrypt(text, "p", failingReader{}); return err }, "encrypt.UnityJSONCrypto"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil {
				t.Fatal("no error")
			}
			assertRoute(t, err, tt.route)
		})
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func mustWrap(t *testing.T, text []byte, kind layerKind) []byte {
	t.Helper()
	out, err := wrap(text, []layer{{kind: kind}})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestLayersAreFoundByTheirBytes: the gzip may be inside or outside the encryption, and a
// file may have gzip twice; every layer is written again in the order it was found.
func TestLayersAreFoundByTheirBytes(t *testing.T) {
	text := []byte(`{"a":{"__type":"int","value":1}}`)
	orders := [][]layer{
		{{kind: layerAES, password: "password"}, {kind: layerGzip}},
		{{kind: layerGzip}, {kind: layerAES, password: "password"}},
		{{kind: layerGzip}, {kind: layerGzip}},
		{{kind: layerAES, password: "password"}},
	}
	for i, layers := range orders {
		file, err := wrap(text, layers)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := FormatES3.Parse("x.es3", file)
		if err != nil {
			t.Fatalf("order %d: %v", i, err)
		}
		out, err := doc.Encode([]model.Edit{{Site: at(t, doc, "a").Site, Value: i64(2)}})
		if err != nil {
			t.Fatal(err)
		}
		again, err := FormatES3.Parse("x.es3", out)
		if err != nil || at(t, again, "a").Value.Repr() != "2" {
			t.Errorf("order %d: %v", i, err)
		}
		d := doc.(*Document)
		if len(d.layers) != len(layers) {
			t.Fatalf("order %d: %d layers, want %d", i, len(d.layers), len(layers))
		}
		for k := range layers {
			if d.layers[k].kind != layers[k].kind {
				t.Errorf("order %d: layer %d is %v", i, k, d.layers[k].kind)
			}
		}
	}
	// More layers than are peeled is not a save.
	deep := text
	for range maxLayers + 1 {
		deep = mustWrap(t, deep, layerGzip)
	}
	if FormatES3.Detect("x.es3", deep) {
		t.Error("a file with too many layers is taken")
	}
}

func TestShortType(t *testing.T) {
	tests := []struct{ in, want string }{
		{"int", "int"},
		{"int,System.Int32", "int"},
		{"System.Int32", "Int32"},
		{"UnityEngine.Vector3,UnityEngine.CoreModule", "Vector3"},
		{"System.Collections.Generic.List`1[[System.String,mscorlib]],mscorlib", "List"},
		{"System.Collections.Generic.Dictionary`2[[System.String, mscorlib],[System.Int32, mscorlib]], mscorlib", "Dictionary"},
		{"Game.Outer+Inner,Assembly-CSharp", "Inner"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := shortType(tt.in); got != tt.want {
			t.Errorf("shortType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestUnusualEntries(t *testing.T) {
	text := `{"a":{"__type":"int","value":1,"extra":2},"b":{"__type":"Thing","x":1,"__type2":3},"c":{"__type":"int"},` +
		`"d":{"__type":"ushort","value":65535},"e":{"__type":"Vec","value":{"__type":"Inner","q":1}}}`
	doc, err := FormatES3.Parse("x.es3", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	// An entry with more than a value is shown as the entry, with its type.
	if v := at(t, doc, "a").Value; v.Kind != model.KindObject || v.Type != "int" || len(v.Children) != 2 {
		t.Errorf("a = %v %s %d", v.Kind, v.Type, len(v.Children))
	}
	if v := at(t, doc, "b").Value; v.Type != "Thing" || len(v.Children) != 2 {
		t.Errorf("b = %s %d", v.Type, len(v.Children))
	}
	if v := at(t, doc, "c").Value; v.Type != "int" || len(v.Children) != 0 {
		t.Errorf("c = %s %d", v.Type, len(v.Children))
	}
	if v := at(t, doc, "d"); v.Value.Type != "ushort" || v.Site.IntMax.Int64() != 65535 || v.Site.IntMin.Int64() != 0 {
		t.Errorf("d = %s", v.Value.Type)
	}
	// An inner object's own type wins over the entry's.
	if v := at(t, doc, "e").Value; v.Type != "Inner" || v.Kind != model.KindObject {
		t.Errorf("e = %s %v", v.Type, v.Kind)
	}
}

// TestDamagedFilesNeverPanic reads every prefix of every fixture and each byte changed.
func TestDamagedFilesNeverPanic(t *testing.T) {
	for _, f := range fixtures {
		data := readFixture(t, f.name)
		read := func(b []byte) {
			for _, fm := range []model.Format{FormatJSON, FormatES3} {
				if !fm.Detect(f.name, b) {
					continue
				}
				doc, err := fm.Parse(f.name, b)
				if err != nil {
					continue
				}
				doc.Details()
				if out, err := doc.Encode(nil); err != nil || !bytes.Equal(out, b) {
					t.Errorf("%s: Encode(nil) of a damaged file: %v", f.name, err)
				}
			}
		}
		for n := range len(data) {
			read(data[:n])
		}
		for i := range data {
			damaged := bytes.Clone(data)
			damaged[i] ^= 0x5A
			read(damaged)
		}
	}
}

// TestIVThatLooksLikeText: the IV is random, so some encrypted files begin with a brace, a
// bracket, white space or the bytes of gzip. They are still encrypted files.
func TestIVThatLooksLikeText(t *testing.T) {
	text := bytes.Clone(readFixture(t, "es3-plain.es3"))
	for _, start := range []string{"{", "[", " ", "\t\r\n ", "\xEF\xBB\xBF{", "\x1f\x8b\x08"} {
		iv := append([]byte(start), make([]byte, blockSize-len(start))...)
		file, err := encrypt(text, "password", bytes.NewReader(iv))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(file, []byte(start)) {
			t.Fatalf("the file does not begin with %q", start)
		}
		doc, err := FormatES3.Parse("x.es3", file)
		if err != nil {
			t.Errorf("IV beginning %q: %v", start, err)
			continue
		}
		if len(doc.Variables()) != 10 || len(doc.(*Document).layers) != 1 {
			t.Errorf("IV beginning %q: %d variables", start, len(doc.Variables()))
		}
		if formatOf("x.dat", file) != kindES3 {
			t.Errorf("IV beginning %q: not detected as Easy Save", start)
		}
	}
	// A JSON text of whole blocks with a mistake in it is still JSON, not ciphertext.
	broken := append([]byte(`{"a":`), bytes.Repeat([]byte{' '}, 27)...)
	if got := formatOf("x.json", broken); got != kindJSON {
		t.Errorf("a broken JSON of 32 bytes is taken by %s", got)
	}
}
