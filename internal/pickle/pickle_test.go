package pickle

import (
	"bytes"
	"encoding/json"
	"maps"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/rm4n0s/errors"
)

func readSample(t *testing.T, name string) *Document {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return doc
}

// roots is the dict of the (roots, log) tuple a save pickles.
func roots(t *testing.T, doc *Document) *Value {
	t.Helper()
	top := doc.Root.Value
	if top.Kind != KindTuple || len(top.Items) != 2 || top.Items[0].Value.Kind != KindDict {
		t.Fatalf("the pickle is not a (dict, log) tuple: %s", top.Repr())
	}
	return top.Items[0].Value
}

func lookup(t *testing.T, v *Value, key string) Slot {
	t.Helper()
	s, ok := v.Lookup(key)
	if !ok {
		t.Fatalf("no key %q", key)
	}
	return s
}

// child finds the child of v with the label.
func child(t *testing.T, v *Value, label string) Slot {
	t.Helper()
	for _, c := range v.Children() {
		if c.Label == label {
			return c.Slot
		}
	}
	t.Fatalf("%s has no child %q", v.Repr(), label)
	return Slot{}
}

func protocols() []string {
	var out []string
	for p := 0; p <= 5; p++ {
		out = append(out, "sample-p"+strconv.Itoa(p)+".pickle")
	}
	return out
}

func TestParseReadsEveryProtocol(t *testing.T) {
	for _, name := range protocols() {
		t.Run(name, func(t *testing.T) {
			r := roots(t, readSample(t, name))
			big70 := new(big.Int).Lsh(big.NewInt(1), 70)
			for _, tt := range []struct {
				key      string
				repr     string
				typ      string
				editable bool
			}{
				{"store.money", "150", "int", true},
				{"store.big", big70.String(), "int", true},
				{"store.neg", "-129", "int", true},
				{"store.ratio", "0.75", "float", true},
				{"store.player_name", `"AnnAnn"`, "str", true},
				{"store.nickname", `"AnnAnn"`, "str", true},
				{"store.has_key", "True", "bool", true},
				{"store.flag_off", "False", "bool", true},
				{"store.nothing", "None", "None", true},
				{"store.greeting", strconv.Quote("Καλημέρα ☕"), "str", true},
				{"store.inventory", "RevertableList (3 items)", "RevertableList", false},
				{"store.stats", "RevertableDict (2 items)", "RevertableDict", false},
				{"store.hero", "Hero (0 items)", "Hero", false},
				{"store.pair", "tuple (2 items)", "tuple", false},
				{"store.e", "ADVCharacter (0 items)", "ADVCharacter", false},
			} {
				s := lookup(t, r, tt.key)
				if s.Value.TypeName() != tt.typ {
					t.Errorf("%s: type %q, want %q", tt.key, s.Value.TypeName(), tt.typ)
				}
				if s.Editable() != tt.editable {
					t.Errorf("%s: editable %v, want %v", tt.key, s.Editable(), tt.editable)
				}
				if s.Value.IsPrimitive() && s.Value.Repr() != tt.repr {
					t.Errorf("%s: %s, want %s", tt.key, s.Value.Repr(), tt.repr)
				}
			}
			if got := lookup(t, r, "store.player_name").Value; got != lookup(t, r, "store.nickname").Value {
				t.Error("the memo did not give the same value to both names")
			}
			for _, tt := range []struct {
				key, label, repr string
			}{
				{"store.inventory", "[0]", `"sword"`},
				{"store.inventory", "[2]", `"AnnAnn"`},
				{"store.stats", `["dex"]`, "12.5"},
				{"store.hero", ".hp", "30"},
				{"store.hero", ".name", `"AnnAnn"`},
				{"store.seen", "[0]", `"intro"`},
				{"store.pair", "[1]", `"a"`},
				{"store.e", ".name", `"Eileen"`},
			} {
				if got := child(t, lookup(t, r, tt.key).Value, tt.label).Value.Repr(); got != tt.repr {
					t.Errorf("%s%s = %s, want %s", tt.key, tt.label, got, tt.repr)
				}
			}
			if q := lookup(t, r, "store.inventory").Value.QualifiedTypeName(); q != "renpy.revertable.RevertableList" {
				t.Errorf("qualified type %q", q)
			}
		})
	}
}

// sampleEdits change one slot of each kind: literals, memo GETs, a dict key, and values
// inside containers and objects.
func sampleEdits(t *testing.T, r *Value) []Edit {
	t.Helper()
	str := func(s string) *Value { return &Value{Kind: KindString, Str: s} }
	num := func(i int64) *Value { return &Value{Kind: KindInt, Int: big.NewInt(i)} }
	huge, _ := new(big.Int).SetString("-123456789012345678901234567890", 10)
	return []Edit{
		{lookup(t, r, "store.money").Site, num(999999)},
		{lookup(t, r, "store.big").Site, num(-5)},
		{lookup(t, r, "store.neg").Site, &Value{Kind: KindInt, Int: huge}},
		{lookup(t, r, "store.ratio").Site, &Value{Kind: KindFloat, Float: 1.5}},
		{lookup(t, r, "store.player_name").Site, str("Bob")},
		{lookup(t, r, "store.nickname").Site, str("Zed ☕")},
		{lookup(t, r, "store.has_key").Site, &Value{Kind: KindBool, Bool: false}},
		{lookup(t, r, "store.nothing").Site, num(7)},
		{child(t, lookup(t, r, "store.inventory").Value, "[2]").Site, str("shield")},
		{child(t, lookup(t, r, "store.stats").Value, `["str"]`).Site, num(-1)},
		{child(t, lookup(t, r, "store.hero").Value, ".hp").Site, num(70000)},
		{child(t, lookup(t, r, "store.seen").Value, "[0]").Site, str("ending")},
	}
}

// wantAfterEdits is what Python reads from the sample with sampleEdits applied: every name
// that shared "AnnAnn" and was not edited keeps it.
var wantAfterEdits = map[string]any{
	"store.money":       float64(999999),
	"store.big":         float64(-5),
	"store.neg":         "-123456789012345678901234567890",
	"store.ratio":       1.5,
	"store.player_name": "Bob",
	"store.nickname":    "Zed ☕",
	"store.has_key":     false,
	"store.flag_off":    false,
	"store.nothing":     float64(7),
	"store.greeting":    "Καλημέρα ☕",
	"store.inventory":   []any{"sword", "potion", "shield"},
	"store.stats":       map[string]any{"str": float64(-1), "dex": 12.5},
	"store.seen":        []any{"ending"},
	"store.hero":        map[string]any{"__class__": "Hero", "name": "AnnAnn", "hp": float64(70000)},
	"store.pair":        []any{float64(1), "a"},
	"store.e":           map[string]any{"__class__": "ADVCharacter", "name": "Eileen", "what_args": map[string]any{}},
	"store._internal":   float64(1),
}

func TestPatchChangesOnlyTheEditedSlots(t *testing.T) {
	for _, name := range protocols() {
		t.Run(name, func(t *testing.T) {
			doc := readSample(t, name)
			out, err := doc.Patch(sampleEdits(t, roots(t, doc)))
			if err != nil {
				t.Fatalf("Patch: %v", err)
			}
			again, err := Parse(out)
			if err != nil {
				t.Fatalf("the patched pickle does not parse: %v", err)
			}
			r := roots(t, again)
			for _, tt := range []struct{ key, repr string }{
				{"store.money", "999999"},
				{"store.big", "-5"},
				{"store.neg", "-123456789012345678901234567890"},
				{"store.ratio", "1.5"},
				{"store.player_name", `"Bob"`},
				{"store.nickname", strconv.Quote("Zed ☕")},
				{"store.has_key", "False"},
				{"store.nothing", "7"},
				{"store.flag_off", "False"},
			} {
				if got := lookup(t, r, tt.key).Value.Repr(); got != tt.repr {
					t.Errorf("%s = %s, want %s", tt.key, got, tt.repr)
				}
			}
			if got := child(t, lookup(t, r, "store.hero").Value, ".name").Value.Repr(); got != `"AnnAnn"` {
				t.Errorf("hero.name = %s; the memo entry of the edited name changed", got)
			}
			if again.Protocol != doc.Protocol {
				t.Errorf("protocol %d, want %d", again.Protocol, doc.Protocol)
			}
			want := wantAfterEdits
			if doc.Protocol < 2 {
				// Protocols 0 and 1 pickle a set subclass's members twice, in the base
				// set([...]) and in __getstate__, and the edit changes the second. Ren'Py
				// pickles with protocol 2 or later.
				want = maps.Clone(wantAfterEdits)
				want["store.seen"] = []any{"ending", "intro"}
			}
			checkWithPython(t, out, want)
		})
	}
}

func TestPatchWithoutEditsKeepsTheBytes(t *testing.T) {
	doc := readSample(t, "sample-p5.pickle")
	out, err := doc.Patch(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, doc.Data) {
		t.Error("the pickle changed without edits")
	}
}

func TestPatchAcrossFrames(t *testing.T) {
	doc := readSample(t, "large-p5.pickle")
	if len(doc.frames) < 2 {
		t.Fatalf("the large sample has %d frames; it should have the string outside them", len(doc.frames))
	}
	r := roots(t, doc)
	text := lookup(t, r, "store.long_text")
	if text.Site.MemoEnd == text.Site.End {
		t.Error("the MEMOIZE after the FRAME was not given to the large string")
	}
	edits := append(sampleEdits(t, r), Edit{text.Site, &Value{Kind: KindString, Str: "short"}})
	out, err := doc.Patch(edits)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte{opFrame}) {
		again, err := Parse(out)
		if err != nil {
			t.Fatal(err)
		}
		if len(again.frames) != 0 {
			t.Errorf("%d frames are left", len(again.frames))
		}
	}
	want := map[string]any{"store.long_text": "short"}
	for k, v := range wantAfterEdits {
		want[k] = v
	}
	checkWithPython(t, out, want)
}

// checkWithPython reads the pickle with Python's own unpickler, when python3 is installed,
// and compares the roots.
func checkWithPython(t *testing.T, data []byte, want map[string]any) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Log("python3 is not installed; the pickle is not checked with Python")
		return
	}
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "load.py"))
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Python cannot read the pickle: %v\n%s", err, stderr.String())
	}
	var got struct {
		Roots map[string]any   `json:"roots"`
		Log   []map[string]any `json:"log"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Roots, want) {
		t.Errorf("Python reads\n%v\nwant\n%v", got.Roots, want)
	}
	wantLog := []map[string]any{{"store": map[string]any{"money": float64(100), "player_name": "AnnAnn"}}}
	if !reflect.DeepEqual(got.Log, wantLog) {
		t.Errorf("the rollback log changed: %v", got.Log)
	}
}

// TestPython2Opcodes reads and patches the opcodes that only Python 2 writes: str as STRING,
// BINSTRING and SHORT_BINSTRING, and the text forms of numbers and the memo.
func TestPython2Opcodes(t *testing.T) {
	data := []byte("\x80\x02}q\x00(U\x01aq\x01U\x03abcq\x02U\x01bh\x02U\x01cT\x03\x00\x00\x00xyzU\x01d" +
		"S'it\\'s\\n'\np3\nU\x01eg3\nU\x01fI42\nU\x01gL12345678901234567890L\nU\x01hF2.5\nU\x01iI01\nu.")
	doc, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	r := doc.Root.Value
	for _, tt := range []struct{ key, repr string }{
		{"a", `"abc"`}, {"b", `"abc"`}, {"c", `"xyz"`}, {"d", `"it's\n"`}, {"e", `"it's\n"`},
		{"f", "42"}, {"g", "12345678901234567890"}, {"h", "2.5"}, {"i", "True"},
	} {
		s := lookup(t, r, tt.key)
		if s.Value.Repr() != tt.repr {
			t.Errorf("%s = %s, want %s", tt.key, s.Value.Repr(), tt.repr)
		}
	}
	if k := lookup(t, r, "a").Value.Kind; k != KindByteString {
		t.Errorf("SHORT_BINSTRING is %v, want a byte string", k)
	}
	out, err := doc.Patch([]Edit{
		{lookup(t, r, "a").Site, &Value{Kind: KindByteString, Str: "new"}},
		{lookup(t, r, "e").Site, &Value{Kind: KindByteString, Str: "other"}},
		{lookup(t, r, "g").Site, &Value{Kind: KindInt, Int: big.NewInt(3)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ key, repr string }{{"a", `"new"`}, {"b", `"abc"`}, {"e", `"other"`}, {"d", `"it's\n"`}, {"g", "3"}} {
		if got := lookup(t, again.Root.Value, tt.key).Value.Repr(); got != tt.repr {
			t.Errorf("after the patch %s = %s, want %s", tt.key, got, tt.repr)
		}
	}
	if !bytes.Contains(out, []byte("U\x03abcq\x020U\x03new")) {
		t.Errorf("the memoized literal was not kept for its GET: %q", out)
	}
}

func TestEncodeLongMatchesPython(t *testing.T) {
	// The bytes are pickle.encode_long's.
	tests := []struct {
		n    string
		want []byte
	}{
		{"0", nil},
		{"255", []byte{0xff, 0x00}},
		{"128", []byte{0x80, 0x00}},
		{"127", []byte{0x7f}},
		{"-1", []byte{0xff}},
		{"-128", []byte{0x80}},
		{"-129", []byte{0x7f, 0xff}},
		{"-256", []byte{0x00, 0xff}},
		{"1180591620717411303424", []byte{0, 0, 0, 0, 0, 0, 0, 0, 0x40}},
	}
	for _, tt := range tests {
		n, _ := new(big.Int).SetString(tt.n, 10)
		got := encodeLong(n)
		if !bytes.Equal(got, tt.want) {
			t.Errorf("encodeLong(%s) = %x, want %x", tt.n, got, tt.want)
		}
		if back := decodeLong(got); back.Cmp(n) != 0 {
			t.Errorf("decodeLong(encodeLong(%s)) = %s", tt.n, back)
		}
	}
}

func TestFormatFloatMatchesRepr(t *testing.T) {
	tenth, fifth := 0.1, 0.2
	tests := []struct {
		f    float64
		want string
	}{
		{0, "0.0"}, {1, "1.0"}, {0.75, "0.75"}, {-2.5, "-2.5"}, {1e16, "1e+16"}, {1e15, "1000000000000000.0"},
		{0.0001, "0.0001"}, {0.00001, "1e-05"}, {tenth + fifth, "0.30000000000000004"},
	}
	for _, tt := range tests {
		if got := FormatFloat(tt.f); got != tt.want {
			t.Errorf("FormatFloat(%v) = %q, want %q", tt.f, got, tt.want)
		}
	}
}

func TestParseFailures(t *testing.T) {
	tests := []struct {
		name  string
		data  string
		route string
	}{
		{"empty", "", "Parse->parser.run.PickleNoStop"},
		{"no stop", "\x80\x02N", "Parse->parser.run.PickleNoStop"},
		{"truncated int", "\x80\x02J\x01", "Parse->parser.run->parser.step->parser.read.PickleTruncated"},
		{"truncated string", "\x80\x02X\x09\x00\x00\x00ab.", "Parse->parser.run->parser.step->parser.readSized.PickleTruncated"},
		{"negative length", "T\xff\xff\xff\xff.", "Parse->parser.run->parser.step->parser.readSized.PickleTruncated"},
		{"line without end", "I12", "Parse->parser.run->parser.step->parser.readLine.PickleTruncated"},
		{"unknown opcode", "\x80\x02\xff.", "Parse->parser.run->parser.step.PickleOpcodeUnknown"},
		{"stop on empty stack", ".", "Parse->parser.run->parser.pop.PickleStackUnderflow"},
		{"tuple2 of one", "N\x86.", "Parse->parser.run->parser.step.PickleStackUnderflow"},
		{"setitem of one", "}N(Ns.", "Parse->parser.run->parser.step.PickleStackUnderflow"},
		{"reduce without callable", "N(R.", "Parse->parser.run->parser.step->parser.pop.PickleStackUnderflow"},
		{"build on empty", "(Nb.", "Parse->parser.run->parser.step->parser.top.PickleStackUnderflow"},
		{"memo on empty", "\x94.", "Parse->parser.run->parser.step.PickleStackUnderflow"},
		{"obj without class", "(o.", "Parse->parser.run->parser.step.PickleStackUnderflow"},
		{"appends without mark", "]e.", "Parse->parser.run->parser.step->parser.popMark.PickleMarkMissing"},
		{"get of nothing", "h\x05.", "Parse->parser.run->parser.step.PickleMemoInvalid"},
		{"put without number", "Np\n.", "Parse->parser.run->parser.step.PickleMemoInvalid"},
		{"get without number", "gx\n.", "Parse->parser.run->parser.step.PickleMemoInvalid"},
		{"int without number", "Ixyz\n.", "Parse->parser.run->parser.step.PickleNumberInvalid"},
		{"long without number", "Lxyz\n.", "Parse->parser.run->parser.step.PickleNumberInvalid"},
		{"float without number", "Fxyz\n.", "Parse->parser.run->parser.step.PickleNumberInvalid"},
		{"unquoted string", "Sabc\n.", "Parse->parser.run->parser.step.PickleStringInvalid"},
		{"append to a dict", "}Na.", "Parse->parser.run->parser.step.PickleTargetInvalid"},
		{"additems to a list", "](N\x90.", "Parse->parser.run->parser.step.PickleTargetInvalid"},
		{"setitems on a list", "](NNu.", "Parse->parser.run->parser.step->parser.addEntries.PickleTargetInvalid"},
		{"setitems odd", "}(Nu.", "Parse->parser.run->parser.step->parser.addEntries.PickleTargetInvalid"},
		{"stack global of numbers", "\x80\x04K\x01K\x02\x93.", "Parse->parser.run->parser.step.PickleGlobalInvalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.data))
			appErr, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("expected *errors.Error, got %T: %v", err, err)
			}
			if !appErr.HasRoute(tt.route) {
				t.Errorf("unexpected failure path: %s", appErr.Route())
			}
		})
	}
}

func TestPatchFailures(t *testing.T) {
	doc := readSample(t, "sample-p5.pickle")
	r := roots(t, doc)
	money := lookup(t, r, "store.money").Site
	one := &Value{Kind: KindInt, Int: big.NewInt(1)}
	tests := []struct {
		name  string
		edits []Edit
		route string
	}{
		{"a container", []Edit{{lookup(t, r, "store.inventory").Site, one}}, "Document.Patch.PickleSiteNotEditable"},
		{"outside the pickle", []Edit{{Site{Kind: SiteLiteral, Start: len(doc.Data), End: len(doc.Data) + 1,
			MemoEnd: len(doc.Data) + 1}, one}}, "Document.Patch.PickleSiteNotEditable"},
		{"the same slot twice", []Edit{{money, one}, {money, one}}, "Document.Patch.PickleSiteNotEditable"},
		{"a list as the value", []Edit{{money, &Value{Kind: KindList}}},
			"Document.Patch->Document.encodeLiteral.PickleValueNotPrimitive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := doc.Patch(tt.edits)
			appErr, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("expected *errors.Error, got %T: %v", err, err)
			}
			if !appErr.HasRoute(tt.route) {
				t.Errorf("unexpected failure path: %s", appErr.Route())
			}
		})
	}
}
