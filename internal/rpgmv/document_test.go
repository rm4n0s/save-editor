package rpgmv

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// parse reads a file with the format of its extension.
func parse(t *testing.T, name string, data []byte) *Document {
	t.Helper()
	var d *Document
	var err error
	if strings.HasPrefix(name, "mz-") {
		d, err = ParseMZ(name, data)
	} else {
		d, err = ParseMV(name, data)
	}
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return d
}

var fixtures = []struct {
	name string
	mv   bool
}{
	{"mv-file1.rpgsave", true}, {"mv-global.rpgsave", true}, {"mv-config.rpgsave", true},
	{"mz-file1.rmmzsave", false}, {"mz-global.rmmzsave", false}, {"mz-config.rmmzsave", false},
}

func TestDetect(t *testing.T) {
	for _, f := range fixtures {
		data := fixture(t, f.name)
		if got := FormatMV.Detect(f.name, data); got != f.mv {
			t.Errorf("%s: FormatMV.Detect = %v", f.name, got)
		}
		if got := FormatMZ.Detect(f.name, data); got != !f.mv {
			t.Errorf("%s: FormatMZ.Detect = %v", f.name, got)
		}
	}
	notSaves := map[string][]byte{
		"empty":        nil,
		"text":         []byte("Hello, world! This is not a save."),
		"json":         []byte(`{"a":1}`),
		"zip":          []byte("PK\x03\x04\x14\x00\x00\x00\x08\x00"),
		"base64":       []byte("SGVsbG8gd29ybGQ="),
		"base64 of lz": []byte("N4Ig5gpgdgrgNgQwM4gFwHQE4BcB2UBXAS0rAgQAIA"),
		"zlib bytes":   []byte("x\x9c\x4b\xcb\xcf\x07\x00\x02\x82\x01\x45"),
		"all ones":     bytes.Repeat([]byte{0xFF}, 100),
	}
	for name, data := range notSaves {
		if FormatMV.Detect("x.rpgsave", data) || FormatMZ.Detect("x.rmmzsave", data) {
			t.Errorf("%s was detected as a save", name)
		}
	}
	// The name does not matter when the contents tell.
	if !FormatMV.Detect("anything.bin", fixture(t, "mv-file1.rpgsave")) {
		t.Error("MV not detected under another name")
	}
	if FormatMV.Name != "RPG Maker MV" || FormatMZ.Name != "RPG Maker MZ" ||
		FormatMV.Patterns[0] != "*.rpgsave" || FormatMZ.Patterns[0] != "*.rmmzsave" {
		t.Error("format names or patterns")
	}
}

// TestLayers checks the layers against the JSON text that the real engines packed (the
// ".json" files, made by gen.js from the JsonEx of each engine).
func TestLayers(t *testing.T) {
	for _, f := range fixtures {
		data := fixture(t, f.name)
		want := fixture(t, f.name+".json")
		if f.mv {
			got, layout, err := decodeMV(data)
			if err != nil || layout != lzMV || !bytes.Equal(got, want) {
				t.Errorf("%s: decodeMV: %v, layout %v, equal %v", f.name, err, layout, bytes.Equal(got, want))
			}
			// Packing the text again gives the engine's bytes: the same compression.
			if again := encodeMV(want, lzMV); !bytes.Equal(again, data) {
				t.Errorf("%s: encodeMV differs from the engine's file", f.name)
			}
			continue
		}
		got, err := decodeMZ(data, maxInflated)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: decodeMZ: %v, equal %v", f.name, err, bytes.Equal(got, want))
		}
		// The bytes of another deflate are not the engine's, but the engine's header and
		// text encoding are kept, and Go reads them back.
		again, err := encodeMZ(want, 1)
		if err != nil || !bytes.HasPrefix(again, data[:2]) {
			t.Errorf("%s: encodeMZ: %v, starts %x, want %x", f.name, err, again[:2], data[:2])
		}
		back, err := decodeMZ(again, maxInflated)
		if err != nil || !bytes.Equal(back, want) {
			t.Errorf("%s: decodeMZ of encodeMZ: %v", f.name, err)
		}
	}
}

func TestLayerOfLZStringFourteen(t *testing.T) {
	// A game that replaced the library with lz-string 1.4 or later has the other layout.
	want := fixture(t, "mv-file1.rpgsave.json")
	data := []byte(lzCompressToBase64(toUnits(want), lzDigits))
	d, err := ParseMV("file1.rpgsave", data)
	if err != nil {
		t.Fatal(err)
	}
	if d.layout != lzDigits || !FormatMV.Detect("file1.rpgsave", data) {
		t.Errorf("layout %v", d.layout)
	}
	out, err := d.Encode(nil)
	if err != nil || !bytes.Equal(out, data) {
		t.Error("no edits")
	}
	site := at(t, d, "system", "_saveCount").Site
	out, err = d.Encode([]model.Edit{{Site: site, Value: intValue(99)}})
	if err != nil {
		t.Fatal(err)
	}
	d2, err := ParseMV("file1.rpgsave", out)
	if err != nil || d2.layout != lzDigits {
		t.Fatalf("%v layout %v", err, d2.layout)
	}
	if n := at(t, d2, "system", "_saveCount").Value.Int; n.Int64() != 99 {
		t.Errorf("_saveCount = %v", n)
	}
}

// at finds a slot by the variable name and the labels below it.
func at(t *testing.T, d *Document, steps ...string) model.Slot {
	t.Helper()
	var cur model.Slot
	found := false
	for _, v := range d.Variables() {
		if v.Name == steps[0] {
			cur, found = v.Slot, true
		}
	}
	if !found {
		t.Fatalf("no variable %q", steps[0])
	}
	for _, s := range steps[1:] {
		next := false
		for _, c := range cur.Value.Children {
			if c.Label == s {
				cur, next = c.Slot, true
				break
			}
		}
		if !next {
			t.Fatalf("no %q under %v", s, steps)
		}
	}
	return cur
}

func intValue(n int64) *model.Value { return &model.Value{Kind: model.KindInt, Int: big.NewInt(n)} }

func TestVariablesAndPresentation(t *testing.T) {
	for _, name := range []string{"mv-file1.rpgsave", "mz-file1.rmmzsave"} {
		t.Run(name, func(t *testing.T) {
			d := parse(t, name, fixture(t, name))
			var names []string
			for _, v := range d.Variables() {
				names = append(names, v.Name)
				if v.Internal {
					t.Errorf("%s is internal", v.Name)
				}
			}
			want := "system screen timer switches variables selfSwitches actors party map player quest"
			if strings.Join(names, " ") != want {
				t.Errorf("variables: %v", names)
			}
			system := at(t, d, "system").Value
			if system.Kind != model.KindObject || system.Type != "Game_System" {
				t.Errorf("system is %v %q", system.Kind, system.Type)
			}
			for _, c := range system.Children {
				if strings.HasPrefix(c.Label, "@") {
					t.Errorf("bookkeeping key %q is shown", c.Label)
				}
			}
			if system.Children[0].Label != "_saveEnabled" || len(system.Children) != 9 {
				t.Errorf("system has %d children, first %q", len(system.Children), system.Children[0].Label)
			}
			// The array is the list, not a wrapper with "@a".
			data := at(t, d, "switches", "_data").Value
			if data.Kind != model.KindList || len(data.Children) != 6 || data.Children[1].Label != "[1]" {
				t.Errorf("switches._data is %v with %d children", data.Kind, len(data.Children))
			}
			if data.Children[0].Slot.Value.Kind != model.KindNull || !data.Children[1].Slot.Value.Bool {
				t.Error("switches._data values")
			}
			// A plain object is a map; its keys are labels.
			self := at(t, d, "selfSwitches", "_data").Value
			if self.Kind != model.KindMap || self.Children[0].Label != "1,2,A" {
				t.Errorf("selfSwitches._data is %v", self.Kind)
			}
			// Non-ASCII text and nested arrays.
			if got := at(t, d, "actors", "_data", "[1]", "_name").Value.Str; got != "Ünï 😀 ジュリア" {
				t.Errorf("name %q", got)
			}
			if got := at(t, d, "actors", "_data", "[1]", "_profile").Value.Str; got != "He said \"hi\"\n\\o/" {
				t.Errorf("profile %q", got)
			}
			if got := at(t, d, "quest", "done", "[2]", "[1]", "[0]").Value.Int; got.Int64() != 2 {
				t.Errorf("nested array: %v", got)
			}
			// The bounds are the safe range of JavaScript.
			site := at(t, d, "variables", "_data", "[6]").Site
			max := big.NewInt(1<<53 - 1)
			if site.IntMax.Cmp(max) != 0 || site.IntMin.Cmp(new(big.Int).Neg(max)) != 0 {
				t.Errorf("bounds %v %v", site.IntMin, site.IntMax)
			}
			if got := at(t, d, "variables", "_data", "[6]").Value.Int; got.Cmp(max) != 0 {
				t.Errorf("max safe integer: %v", got)
			}
			if k := at(t, d, "screen", "_zoomScale").Value; k.Kind != model.KindFloat || k.Float != 1.5 {
				t.Errorf("float %v", k)
			}
		})
	}
}

func TestReferences(t *testing.T) {
	d := parse(t, "mv-file1.rpgsave", fixture(t, "mv-file1.rpgsave"))
	party := at(t, d, "party").Value
	if party.Type != "Game_Party" {
		t.Fatalf("party is %q", party.Type)
	}
	// "party" is {"@r": 21}, a reference to the party met first under the first actor.
	if inActor := at(t, d, "actors", "_data", "[1]", "_party").Value; inActor != party {
		t.Error("the party of the actor is not the party variable")
	}
	// The circle: the party's leader is the actor that holds the party.
	actor := at(t, d, "actors", "_data", "[1]").Value
	if at(t, d, "party", "_leaderActor").Value != actor {
		t.Error("_leaderActor is not the actor")
	}
	if quest := at(t, d, "quest").Value; at(t, d, "quest", "self").Value != quest {
		t.Error("quest.self is not the quest")
	}
	// A shared array and a shared object.
	if at(t, d, "actors", "_data", "[2]", "_friends").Value != at(t, d, "actors", "_data", "[1]", "_skills").Value {
		t.Error("_friends is not _skills")
	}
	item := at(t, d, "actors", "_data", "[1]", "_equips", "[2]")
	if item2 := at(t, d, "actors", "_data", "[2]", "_equips", "[2]"); item2.Value != item.Value {
		t.Error("the shared item is two values")
	}
	// The sites of the shared parts are the same too.
	id1, id2 := at(t, d, "actors", "_data", "[1]", "_equips", "[2]", "_itemId"), at(t, d, "actors", "_data", "[2]", "_equips", "[2]", "_itemId")
	if id1.Site == nil || id1.Site != id2.Site {
		t.Error("the shared item has two sites")
	}
	// MZ writes the object twice and has no references: two values.
	mz := parse(t, "mz-file1.rmmzsave", fixture(t, "mz-file1.rmmzsave"))
	if at(t, mz, "actors", "_data", "[1]", "_equips", "[2]").Value == at(t, mz, "actors", "_data", "[2]", "_equips", "[2]").Value {
		t.Error("MZ has shared values")
	}
}

func TestDetails(t *testing.T) {
	tests := []struct {
		fixture string
		name    string
		want    map[string]string
	}{
		{"mv-file1.rpgsave", "file1.rpgsave", map[string]string{"Packing": "LZString, Base64", "Save slot": "1", "Save count": "12", "Play time": "01:01:01", "Game version ID": "4711"}},
		{"mz-file1.rmmzsave", "File12.rmmzsave", map[string]string{"Packing": "zlib, written as UTF-8 text", "Save slot": "12", "Save count": "12", "Play time": "01:01:01", "Game version ID": "4711"}},
		{"mv-global.rpgsave", "global.rpgsave", map[string]string{"Packing": "LZString, Base64", "File": "Global information about the save slots", "Save slots": "1"}},
		{"mz-config.rmmzsave", "config.rmmzsave", map[string]string{"Packing": "zlib, written as UTF-8 text", "File": "Game options"}},
		{"mv-config.rpgsave", "other.rpgsave", map[string]string{"Packing": "LZString, Base64"}},
	}
	for _, tt := range tests {
		d := parse(t, tt.fixture, fixture(t, tt.fixture))
		d.name = tt.name
		got := map[string]string{}
		for _, x := range d.Details() {
			got[x.Label] = x.Text
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: details %v\nwant %v", tt.fixture, got, tt.want)
		}
	}
}

func TestGlobalAndConfig(t *testing.T) {
	g := parse(t, "mv-global.rpgsave", fixture(t, "mv-global.rpgsave"))
	vars := g.Variables()
	if len(vars) != 2 || vars[0].Name != "[0]" || vars[1].Name != "[1]" || vars[1].Slot.Value.Kind != model.KindMap {
		t.Fatalf("global variables: %+v", vars)
	}
	if got := at(t, g, "[1]", "characters", "[1]", "[0]").Value.Str; got != "Actor2" {
		t.Errorf("characters: %q", got)
	}
	c := parse(t, "mz-config.rmmzsave", fixture(t, "mz-config.rmmzsave"))
	if at(t, c, "bgmVolume").Value.Int.Int64() != 80 || !at(t, c, "commandRemember").Value.Bool {
		t.Error("config values")
	}
}

// dump is the object graph in the form of engine.js's dump: a container met again is
// {"$ref": n} where n counts the containers in the order they were first met.
func dump(v *model.Value, seen map[*model.Value]int) any {
	switch v.Kind {
	case model.KindNull:
		return nil
	case model.KindBool:
		return v.Bool
	case model.KindInt:
		f, _ := new(big.Float).SetInt(v.Int).Float64()
		return f
	case model.KindFloat:
		return v.Float
	case model.KindString:
		return v.Str
	case model.KindOpaque:
		return map[string]any{"$opaque": v.Str}
	}
	if n, ok := seen[v]; ok {
		return map[string]any{"$ref": float64(n)}
	}
	seen[v] = len(seen)
	if v.Kind == model.KindList {
		out := make([]any, len(v.Children))
		for i, c := range v.Children {
			out[i] = dump(c.Slot.Value, seen)
		}
		return out
	}
	out := map[string]any{}
	if v.Kind == model.KindObject {
		out["@"] = v.Type
	}
	for _, c := range v.Children {
		out[c.Label] = dump(c.Slot.Value, seen)
	}
	return out
}

// dumpOf is the dump of the whole save, through a JSON round trip so that it compares with
// what node prints.
func dumpOf(t *testing.T, d *Document) any {
	t.Helper()
	b, err := json.Marshal(dump(d.json.Root.Value, map[*model.Value]int{}))
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// engineDirs are the checkouts of the engines' scripts that the tests of edits check against.
func engineDir(t *testing.T, mv bool) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	env := "RPGMZ_CORESCRIPT"
	if mv {
		env = "RPGMV_CORESCRIPT"
	}
	dir := os.Getenv(env)
	if dir == "" {
		t.Skip(env + " is not set (see testdata/engine.js)")
	}
	return dir
}

// engineDump has the engine's own code read the file and returns the object graph it makes.
func engineDump(t *testing.T, mv bool, data []byte) any {
	t.Helper()
	dir := engineDir(t, mv)
	kind, ext := "mz", ".rmmzsave"
	if mv {
		kind, ext = "mv", ".rpgsave"
	}
	file := filepath.Join(t.TempDir(), "file1"+ext)
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join("testdata", "engine.js"), "dump", kind, dir, file)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node engine.js: %v\n%s", err, stderr.String())
	}
	var graph any
	if err := json.Unmarshal(out, &graph); err != nil {
		t.Fatal(err)
	}
	return graph
}

func TestFixturesAgainstTheEngine(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			data := fixture(t, f.name)
			engineDir(t, f.mv)
			d := parse(t, f.name, data)
			if got, want := dumpOf(t, d), engineDump(t, f.mv, data); !reflect.DeepEqual(got, want) {
				t.Errorf("the tree is not what the engine reads\ngot  %v\nwant %v", got, want)
			}
		})
	}
}

func TestEncodeWithoutEdits(t *testing.T) {
	for _, f := range fixtures {
		data := fixture(t, f.name)
		d := parse(t, f.name, data)
		out, err := d.Encode(nil)
		if err != nil || !bytes.Equal(out, data) {
			t.Errorf("%s: Encode(nil) differs: %v", f.name, err)
		}
		out[0] ^= 0xFF
		if again, _ := d.Encode([]model.Edit{}); !bytes.Equal(again, data) {
			t.Errorf("%s: Encode changed the document", f.name)
		}
	}
}

type edit struct {
	name    string
	fixture string
	path    []string
	value   model.Value
}

var edits = []edit{
	{"int", "mv-file1.rpgsave", []string{"party", "_gold"}, model.Value{Kind: model.KindInt, Int: big.NewInt(99999999)}},
	{"int negative", "mv-file1.rpgsave", []string{"variables", "_data", "[2]"}, model.Value{Kind: model.KindInt, Int: big.NewInt(-9007199254740991)}},
	{"int in actor", "mv-file1.rpgsave", []string{"actors", "_data", "[2]", "_level"}, model.Value{Kind: model.KindInt, Int: big.NewInt(99)}},
	{"float", "mv-file1.rpgsave", []string{"screen", "_zoomScale"}, model.Value{Kind: model.KindFloat, Float: 0.1}},
	{"float whole", "mv-file1.rpgsave", []string{"map", "_displayX"}, model.Value{Kind: model.KindFloat, Float: 3}},
	{"string", "mv-file1.rpgsave", []string{"actors", "_data", "[2]", "_name"}, model.Value{Kind: model.KindString, Str: "Zoë \"the\" 😀\n\\ ✓"}},
	{"string non-ASCII over non-ASCII", "mv-file1.rpgsave", []string{"actors", "_data", "[1]", "_name"}, model.Value{Kind: model.KindString, Str: "Ελένη 😀😀"}},
	{"bool", "mv-file1.rpgsave", []string{"timer", "_working"}, model.Value{Kind: model.KindBool, Bool: true}},
	{"switch", "mv-file1.rpgsave", []string{"switches", "_data", "[2]"}, model.Value{Kind: model.KindBool, Bool: true}},
	{"shared item", "mv-file1.rpgsave", []string{"actors", "_data", "[1]", "_equips", "[2]", "_itemId"}, model.Value{Kind: model.KindInt, Int: big.NewInt(42)}},
	{"plugin string", "mv-file1.rpgsave", []string{"quest", "name"}, model.Value{Kind: model.KindString, Str: ""}},
	{"nested array", "mv-file1.rpgsave", []string{"quest", "done", "[2]", "[1]", "[1]"}, model.Value{Kind: model.KindInt, Int: big.NewInt(300)}},
	{"global string", "mv-global.rpgsave", []string{"[1]", "title"}, model.Value{Kind: model.KindString, Str: "Another title"}},
	{"global timestamp", "mv-global.rpgsave", []string{"[1]", "timestamp"}, model.Value{Kind: model.KindInt, Int: big.NewInt(1800000000000)}},
	{"config", "mv-config.rpgsave", []string{"bgmVolume"}, model.Value{Kind: model.KindInt, Int: big.NewInt(0)}},
	{"config bool", "mv-config.rpgsave", []string{"alwaysDash"}, model.Value{Kind: model.KindBool, Bool: true}},
	{"MZ int", "mz-file1.rmmzsave", []string{"party", "_gold"}, model.Value{Kind: model.KindInt, Int: big.NewInt(99999999)}},
	{"MZ float", "mz-file1.rmmzsave", []string{"screen", "_zoomScale"}, model.Value{Kind: model.KindFloat, Float: -2.5e-7}},
	{"MZ string", "mz-file1.rmmzsave", []string{"actors", "_data", "[1]", "_name"}, model.Value{Kind: model.KindString, Str: "Ünï 😀 é€"}},
	{"MZ bool", "mz-file1.rmmzsave", []string{"timer", "_working"}, model.Value{Kind: model.KindBool, Bool: true}},
	{"MZ switch", "mz-file1.rmmzsave", []string{"switches", "_data", "[5]"}, model.Value{Kind: model.KindBool, Bool: true}},
	{"MZ first of two equal items", "mz-file1.rmmzsave", []string{"actors", "_data", "[1]", "_equips", "[2]", "_itemId"}, model.Value{Kind: model.KindInt, Int: big.NewInt(5)}},
	{"MZ global", "mz-global.rmmzsave", []string{"[1]", "title"}, model.Value{Kind: model.KindString, Str: "x"}},
	{"MZ config", "mz-config.rmmzsave", []string{"seVolume"}, model.Value{Kind: model.KindInt, Int: big.NewInt(55)}},
}

// setDump puts a value into a dump at a path of labels.
func setDump(t *testing.T, tree any, steps []string, v any) {
	t.Helper()
	for i, s := range steps {
		last := i == len(steps)-1
		switch x := tree.(type) {
		case map[string]any:
			if last {
				x[s] = v
				return
			}
			tree = x[s]
		case []any:
			n, err := strconv.Atoi(strings.Trim(s, "[]"))
			if err != nil || n >= len(x) {
				t.Fatalf("bad step %q", s)
			}
			if last {
				x[n] = v
				return
			}
			tree = x[n]
		default:
			t.Fatalf("cannot step into %T at %q", tree, s)
		}
	}
}

// pathOfSite is the labels from the root to the first place, in the order of dump, where the
// site is the slot of a value.
func pathOfSite(v *model.Value, site *model.Site, seen map[*model.Value]bool) ([]string, bool) {
	if seen[v] {
		return nil, false
	}
	seen[v] = true
	for _, c := range v.Children {
		if c.Slot.Site == site {
			return []string{c.Label}, true
		}
		if rest, ok := pathOfSite(c.Slot.Value, site, seen); ok {
			return append([]string{c.Label}, rest...), true
		}
	}
	return nil, false
}

func TestEdits(t *testing.T) {
	for _, tt := range edits {
		t.Run(tt.name, func(t *testing.T) {
			data := fixture(t, tt.fixture)
			d := parse(t, tt.fixture, data)
			old := at(t, d, tt.path...)
			if old.Site == nil || old.Value.Kind != tt.value.Kind {
				t.Fatalf("the old value is %v with site %v", old.Value.Kind, old.Site)
			}
			before := dumpOf(t, d)
			out, err := d.Encode([]model.Edit{{Site: old.Site, Value: &tt.value}})
			if err != nil {
				t.Fatal(err)
			}
			mv := strings.HasPrefix(tt.fixture, "mv-")
			d2 := parse(t, tt.fixture, out)
			// The edited value changed...
			got := at(t, d2, tt.path...).Value
			if got.Kind != tt.value.Kind || got.Bool != tt.value.Bool || got.Str != tt.value.Str || got.Float != tt.value.Float ||
				(got.Int != nil && got.Int.Cmp(tt.value.Int) != 0) {
				t.Errorf("the edited value reads back as %s", got.Repr())
			}
			// ...and every other value is as it was.
			want := before
			var plain any
			switch tt.value.Kind {
			case model.KindInt:
				plain, _ = new(big.Float).SetInt(tt.value.Int).Float64()
			case model.KindFloat:
				plain = tt.value.Float
			case model.KindString:
				plain = tt.value.Str
			case model.KindBool:
				plain = tt.value.Bool
			}
			// A value met through a reference is stored once, where it is first met; the dump
			// has it there and {"$ref": n} elsewhere.
			firstPath, found := pathOfSite(d.json.Root.Value, old.Site, map[*model.Value]bool{})
			if !found {
				t.Fatal("the site is not in the tree")
			}
			setDump(t, want, firstPath, plain)
			if after := dumpOf(t, d2); !reflect.DeepEqual(after, want) {
				t.Errorf("another value changed\ngot  %v\nwant %v", after, want)
			}
			// The JSON text differs from the original only where the value is.
			text1, text2 := unpack(t, mv, data), unpack(t, mv, out)
			if !sameOutsideEdit(text1, text2, old) {
				t.Errorf("the JSON changed elsewhere:\n%s\n%s", text1, text2)
			}
			// The engine reads it, and sees the same.
			if _, err := exec.LookPath("node"); err == nil && os.Getenv(map[bool]string{true: "RPGMV_CORESCRIPT", false: "RPGMZ_CORESCRIPT"}[mv]) != "" {
				if graph := engineDump(t, mv, out); !reflect.DeepEqual(graph, want) {
					t.Errorf("the engine reads another tree\ngot  %v\nwant %v", graph, want)
				}
			}
		})
	}
}

func unpack(t *testing.T, mv bool, data []byte) []byte {
	t.Helper()
	if mv {
		text, _, err := decodeMV(data)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	text, err := decodeMZ(data, maxInflated)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// sameOutsideEdit reports whether the texts have the same start and the same end around one
// changed token of the site.
func sameOutsideEdit(a, b []byte, old model.Slot) bool {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	j := 0
	for j < len(a)-i && j < len(b)-i && a[len(a)-1-j] == b[len(b)-1-j] {
		j++
	}
	// What is left between is the old token and the new one; neither has a bracket or a
	// colon outside a string, so it is one token.
	return !bytes.ContainsAny(a[i:len(a)-j], "{}[]") && !bytes.ContainsAny(b[i:len(b)-j], "{}[]") && old.Site != nil
}

// TestSeveralEdits changes values of every kind at once, in the order the editor may give them.
func TestSeveralEdits(t *testing.T) {
	for _, name := range []string{"mv-file1.rpgsave", "mz-file1.rmmzsave"} {
		data := fixture(t, name)
		d := parse(t, name, data)
		gold := at(t, d, "party", "_gold")
		actorName := at(t, d, "actors", "_data", "[1]", "_name")
		zoom := at(t, d, "screen", "_zoomScale")
		sw := at(t, d, "switches", "_data", "[3]")
		out, err := d.Encode([]model.Edit{
			{Site: zoom.Site, Value: &model.Value{Kind: model.KindFloat, Float: 2.25}},
			{Site: actorName.Site, Value: &model.Value{Kind: model.KindString, Str: "Ω"}},
			{Site: sw.Site, Value: &model.Value{Kind: model.KindBool, Bool: false}},
			{Site: gold.Site, Value: intValue(1)},
		})
		if err != nil {
			t.Fatal(err)
		}
		d2 := parse(t, name, out)
		if at(t, d2, "party", "_gold").Value.Int.Int64() != 1 || at(t, d2, "actors", "_data", "[1]", "_name").Value.Str != "Ω" ||
			at(t, d2, "screen", "_zoomScale").Value.Float != 2.25 || at(t, d2, "switches", "_data", "[3]").Value.Bool {
			t.Errorf("%s: the edits were not made", name)
		}
	}
}

func TestEncodeRefused(t *testing.T) {
	data := fixture(t, "mv-file1.rpgsave")
	d := parse(t, "mv-file1.rpgsave", data)
	other := parse(t, "mv-file1.rpgsave", data)
	gold := at(t, d, "party", "_gold")
	name := at(t, d, "actors", "_data", "[1]", "_name")
	tests := []struct {
		name  string
		edit  model.Edit
		route string
	}{
		{"site of another document", model.Edit{Site: at(t, other, "party", "_gold").Site, Value: intValue(1)}, "Encode->Doc.Patch.JsonEditSite"},
		{"no site", model.Edit{Value: intValue(1)}, "Encode->Doc.Patch.JsonEditSite"},
		{"wrong kind", model.Edit{Site: gold.Site, Value: &model.Value{Kind: model.KindString, Str: "1"}}, "Encode->Doc.Patch.JsonEditKind"},
		{"int over string", model.Edit{Site: name.Site, Value: intValue(1)}, "Encode->Doc.Patch.JsonEditKind"},
		{"above the safe range", model.Edit{Site: gold.Site, Value: &model.Value{Kind: model.KindInt, Int: big.NewInt(1 << 53)}}, "Encode->Doc.Patch.JsonEditRange"},
		{"below the safe range", model.Edit{Site: gold.Site, Value: &model.Value{Kind: model.KindInt, Int: new(big.Int).Neg(big.NewInt(1 << 53))}}, "Encode->Doc.Patch.JsonEditRange"},
		{"invalid UTF-8", model.Edit{Site: name.Site, Value: &model.Value{Kind: model.KindString, Str: "a\xffb"}}, "Encode->Doc.Patch.JsonEditText"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := d.Encode([]model.Edit{tt.edit})
			if err == nil {
				t.Fatalf("accepted: %d bytes", len(out))
			}
			assertRoute(t, err, tt.route)
		})
	}
}

func TestParseErrors(t *testing.T) {
	zeros := func(n int) []byte { return bytes.Repeat([]byte{'a'}, n) }
	mzBig, err := encodeMZ(zeros(5000), 1)
	if err != nil {
		t.Fatal(err)
	}
	// A corrupt deflate stream that is still text: flip a byte of the compressed bytes, not of
	// their UTF-8.
	var zipped []byte
	for _, r := range string(fixture(t, "mz-file1.rmmzsave")) {
		zipped = append(zipped, byte(r))
	}
	zipped[len(zipped)/2] ^= 0x55
	var corrupt []byte
	for _, b := range zipped {
		corrupt = utf8.AppendRune(corrupt, rune(b))
	}
	tests := []struct {
		name  string
		mv    bool
		data  []byte
		route string
	}{
		{"MV empty", true, nil, "ParseMV->decodeMV->lzDecompressFromBase64.LZStringCorrupt"},
		{"MV not base64", true, []byte("hello world!"), "ParseMV->decodeMV->lzDecompressFromBase64.LZStringCorrupt"},
		{"MV base64 of something else", true, []byte(lzCompressToBase64(lzUnits("not json"), lzMV)), "ParseMV->decodeMV.RpgmvNotMV"},
		{"MV lone surrogate", true, []byte(lzCompressToBase64([]uint16{'{', 0xD800, '}'}, lzMV)), "ParseMV->decodeMV->toUTF8.LZStringSurrogate"},
		{"MV json error", true, []byte(lzCompressToBase64(lzUnits(`{"a":}`), lzMV)), "ParseMV->newDocument->Parse->ParseOptions->parser.parseDocument->parser.parseValue->parser.parseObject->parser.parseValue.JsonSyntax"},
		{"MZ empty", false, nil, "ParseMZ->decodeMZ.RpgmvMZInflate"},
		{"MZ not utf8", false, []byte{'x', 0xFF, 0xFE}, "ParseMZ->decodeMZ.RpgmvMZText"},
		{"MZ char above 255", false, []byte("xĀ"), "ParseMZ->decodeMZ.RpgmvMZText"},
		{"MZ not zlib", false, []byte("hello world"), "ParseMZ->decodeMZ.RpgmvMZInflate"},
		{"MZ corrupt", false, corrupt, "ParseMZ->decodeMZ.RpgmvMZInflate"},
		{"MZ zlib of something else", false, mzBig, "ParseMZ->decodeMZ.RpgmvNotMZ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.mv {
				_, err = ParseMV("x", tt.data)
			} else {
				_, err = ParseMZ("x", tt.data)
			}
			if err == nil {
				t.Fatal("parsed")
			}
			assertRoute(t, err, tt.route)
		})
	}
	// Through the Format.
	if _, err := FormatMV.Parse("file1.rpgsave", []byte("x")); err == nil {
		t.Error("FormatMV.Parse accepted garbage")
	}
	if _, err := FormatMZ.Parse("file1.rmmzsave", []byte("x")); err == nil {
		t.Error("FormatMZ.Parse accepted garbage")
	}
	doc, err := FormatMV.Parse("file1.rpgsave", fixture(t, "mv-file1.rpgsave"))
	if err != nil || len(doc.Variables()) == 0 {
		t.Errorf("FormatMV.Parse: %v", err)
	}
	doc, err = FormatMZ.Parse("file1.rmmzsave", fixture(t, "mz-file1.rmmzsave"))
	if err != nil || len(doc.Variables()) == 0 {
		t.Errorf("FormatMZ.Parse: %v", err)
	}
}

func TestMZLimitsAndDeflate(t *testing.T) {
	big, err := encodeMZ(bytes.Repeat([]byte{'['}, 5000), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = decodeMZ(big, 1000)
	assertRoute(t, err, "decodeMZ.RpgmvMZLarge")
	if _, err := decodeMZ(big, 5000); err != nil {
		t.Errorf("at the limit: %v", err)
	}
	_, err = encodeMZ([]byte("{}"), 42)
	assertRoute(t, err, "encodeMZ.RpgmvMZDeflate")
}

func TestUnresolvedReference(t *testing.T) {
	text := `{"a":{"@r":99,"@c":3},"b":{"@c":4,"@a":[{"@r":4}]},"@c":1}`
	d, err := ParseMV("file1.rpgsave", encodeMV([]byte(text), lzMV))
	if err != nil {
		t.Fatal(err)
	}
	a := at(t, d, "a").Value
	if a.Kind != model.KindOpaque || !strings.Contains(a.Str, "#99") {
		t.Errorf("a is %v %q", a.Kind, a.Str)
	}
	// A list that holds a reference to itself.
	b := at(t, d, "b").Value
	if b.Kind != model.KindList || b.Children[0].Slot.Value != b {
		t.Error("b does not contain itself")
	}
}

func TestBookkeepingKeysThatAreNotBookkeeping(t *testing.T) {
	// Only an integer "@c" and a string "@" are the engine's; anything else is data.
	text := `{"@c":"x","@":5,"@z":1,"o":{"@":"","@c":7}}`
	d, err := ParseMV("file1.rpgsave", encodeMV([]byte(text), lzMV))
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, v := range d.Variables() {
		labels = append(labels, v.Name)
	}
	if strings.Join(labels, ",") != "@c,@,@z,o" {
		t.Errorf("variables %v", labels)
	}
	if o := at(t, d, "o").Value; o.Kind != model.KindMap || len(o.Children) != 1 || o.Children[0].Label != "@" {
		t.Errorf("o: %v", o.Repr())
	}
	// MZ has no ids: "@c" stays a key, "@" names the class.
	mz, err := encodeMZ([]byte(`{"o":{"@c":1,"@":"Foo"},"dup":1,"dup":2}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	d, err = ParseMZ("file1.rmmzsave", mz)
	if err != nil {
		t.Fatal(err)
	}
	if o := at(t, d, "o").Value; o.Kind != model.KindObject || o.Type != "Foo" || len(o.Children) != 1 || o.Children[0].Label != "@c" {
		t.Errorf("o: %v", o.Repr())
	}
	// Names are unique.
	var names []string
	for _, v := range d.Variables() {
		names = append(names, v.Name)
	}
	if strings.Join(names, ",") != "o,dup,dup #2" {
		t.Errorf("names %v", names)
	}
}

func TestRootThatIsNotAContainer(t *testing.T) {
	d, err := ParseMV("file1.rpgsave", []byte(lzCompressToBase64(lzUnits(`[1,2]`), lzMV)))
	if err != nil || len(d.Variables()) != 2 {
		t.Fatalf("%v", err)
	}
	p := &Document{}
	_ = p
	vars := variables(model.Slot{Value: &model.Value{Kind: model.KindInt, Int: big.NewInt(1)}})
	if len(vars) != 1 || vars[0].Name != "value" {
		t.Errorf("%v", vars)
	}
}

func TestLargeSave(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"variables":{"_data":{"@c":2,"@a":[null`)
	for i := range 250000 {
		sb.WriteString("," + strconv.Itoa(i*7919%100000))
	}
	sb.WriteString(`]},"@c":1,"@":"Game_Variables"},"@c":0}`)
	text := []byte(sb.String())
	start := time.Now()
	data := encodeMV(text, lzMV)
	d, err := ParseMV("file1.rpgsave", data)
	if err != nil {
		t.Fatal(err)
	}
	site := at(t, d, "variables", "_data", "[250000]").Site
	out, err := d.Encode([]model.Edit{{Site: site, Value: intValue(-1)}})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("a %d byte save took %v", len(text), took)
	}
	d2, err := ParseMV("file1.rpgsave", out)
	if err != nil || at(t, d2, "variables", "_data", "[250000]").Value.Int.Int64() != -1 {
		t.Errorf("edit lost: %v", err)
	}
	t.Logf("%d bytes of JSON, %d bytes of file, %v", len(text), len(data), time.Since(start))
}

func TestErrorsAreErrors(t *testing.T) {
	_, err := ParseMV("x", nil)
	if _, ok := errors.FromError(err); !ok {
		t.Errorf("%T", err)
	}
}

// TestMZWritesTheZlibStringAsUTF8 pins down how an MZ save is on disk. The engine
// (rmmz_managers.js) makes the file with pako.deflate(json, {to: "string", level: 1}), a string
// of one character per byte of the zlib stream, and fs.writeFileSync(path, string), which
// writes it as UTF-8: a byte of 0x80 or more takes two bytes. It reads the file with
// {encoding: "utf8"} and pako.inflate.
func TestMZWritesTheZlibStringAsUTF8(t *testing.T) {
	for _, name := range []string{"mz-file1.rmmzsave", "mz-global.rmmzsave", "mz-config.rmmzsave"} {
		t.Run(name, func(t *testing.T) {
			file := fixture(t, name)
			text := fixture(t, name+".json")
			// The stream is the characters of the file, one byte each.
			var stream []byte
			high := 0
			for _, r := range string(file) {
				if r > 0xFF {
					t.Fatalf("character %U is not one byte", r)
				}
				if r >= 0x80 {
					high++
				}
				stream = append(stream, byte(r))
			}
			if len(file) != len(stream)+high {
				t.Errorf("the file has %d bytes for %d characters and %d of them 0x80 or more", len(file), len(stream), high)
			}
			// pako's level 1: the zlib header is 0x78 0x01.
			if len(stream) < 2 || stream[0] != 0x78 || stream[1] != 0x01 {
				t.Errorf("the stream starts %x, want 78 01", stream[:2])
			}
			// The stream inflates to the JSON text of the engine.
			zr, err := zlib.NewReader(bytes.NewReader(stream))
			if err != nil {
				t.Fatal(err)
			}
			inflated, err := io.ReadAll(zr)
			if err != nil || !bytes.Equal(inflated, text) {
				t.Errorf("inflated: %v, equal %v", err, bytes.Equal(inflated, text))
			}
			// Our own file is the UTF-8 of the characters, not the bytes of the stream.
			ours, err := encodeMZ(text, zlib.BestSpeed)
			if err != nil {
				t.Fatal(err)
			}
			var chars strings.Builder
			zr2, err := zlib.NewReader(bytes.NewReader(mustStream(t, ours)))
			if err != nil {
				t.Fatal(err)
			}
			if got, err := io.ReadAll(zr2); err != nil || !bytes.Equal(got, text) {
				t.Errorf("our stream inflates to another text: %v", err)
			}
			for _, b := range mustStream(t, ours) {
				chars.WriteRune(rune(b))
			}
			if chars.String() != string(ours) {
				t.Error("our file is not the UTF-8 of one character per byte")
			}
			// The bare bytes of the stream are not a save when one of them is 0x80 or more.
			if high > 0 {
				if _, err := ParseMZ(name, stream); err == nil {
					t.Error("the bare zlib stream was read as a save")
				} else {
					assertRoute(t, err, "ParseMZ->decodeMZ.RpgmvMZText")
				}
			}
		})
	}
}

// mustStream is the bytes of the characters of an MZ file.
func mustStream(t *testing.T, file []byte) []byte {
	t.Helper()
	var out []byte
	for _, r := range string(file) {
		if r > 0xFF {
			t.Fatalf("character %U is not one byte", r)
		}
		out = append(out, byte(r))
	}
	return out
}

// TestDamagedFilesNeverPanic reads every prefix of every fixture, and the fixture with each
// byte changed in turn. Whatever comes out, an error or a document, nothing panics, and a
// document that comes out encodes without edits.
func TestDamagedFilesNeverPanic(t *testing.T) {
	for _, f := range fixtures {
		data := fixture(t, f.name)
		read := func(b []byte) {
			var d *Document
			var err error
			if f.mv {
				FormatMV.Detect(f.name, b)
				d, err = ParseMV(f.name, b)
			} else {
				FormatMZ.Detect(f.name, b)
				d, err = ParseMZ(f.name, b)
			}
			if err != nil {
				return
			}
			d.Details()
			if _, err := d.Encode(nil); err != nil {
				t.Errorf("%s: Encode(nil) of a damaged file: %v", f.name, err)
			}
		}
		for n := range len(data) {
			read(data[:n])
		}
		for i := range data {
			damaged := append([]byte(nil), data...)
			damaged[i] ^= 0x5A
			read(damaged)
		}
	}
}
