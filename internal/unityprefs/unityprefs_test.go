package unityprefs

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

var fixtures = []string{"linux.prefs", "mac-plistlib.plist", "mac-float32.plist"}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parseFixture(t *testing.T, name string) (*Document, []byte) {
	t.Helper()
	data := readFixture(t, name)
	doc, err := Format.Parse(name, data)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return doc.(*Document), data
}

func intV(n int64) *model.Value { return &model.Value{Kind: model.KindInt, Int: big.NewInt(n)} }
func floatV(f float64) *model.Value {
	return &model.Value{Kind: model.KindFloat, Float: f, Float32: true}
}
func strV(s string) *model.Value { return &model.Value{Kind: model.KindString, Str: s} }
func boolV(b bool) *model.Value  { return &model.Value{Kind: model.KindBool, Bool: b} }

func variable(t *testing.T, doc model.Document, name string) model.Variable {
	t.Helper()
	for _, v := range doc.Variables() {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no variable %q", name)
	return model.Variable{}
}

// tagged is a value in the form that load.py prints.
func tagged(v *model.Value) any {
	switch v.Kind {
	case model.KindBool:
		return map[string]any{"t": "bool", "v": fmt.Sprint(v.Bool)}
	case model.KindInt:
		return map[string]any{"t": "int", "v": v.Int.String()}
	case model.KindFloat:
		return map[string]any{"t": "float", "v": fmt.Sprintf("%016x", math.Float64bits(v.Float))}
	case model.KindString:
		return map[string]any{"t": "string", "v": v.Str}
	case model.KindBytes:
		if v.Type == "string" {
			return map[string]any{"t": "bytes", "v": fmt.Sprintf("%x", v.Bytes)}
		}
		return map[string]any{"t": "data", "v": fmt.Sprintf("%x", v.Bytes)}
	case model.KindOpaque:
		return map[string]any{"t": v.Type, "v": ""}
	}
	t := "dict"
	if v.Kind == model.KindList {
		t = "array"
	}
	var c []any
	for _, ch := range v.Children {
		c = append(c, []any{ch.Label, tagged(ch.Slot.Value)})
	}
	return map[string]any{"t": t, "c": c}
}

func taggedVars(doc model.Document) []any {
	var out []any
	for _, v := range doc.Variables() {
		out = append(out, []any{v.Name, tagged(v.Slot.Value)})
	}
	return out
}

// normalize makes the value what json.Unmarshal would have made of it.
func normalize(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// pythonLoad runs load.py on the data; it skips the test when python3 is missing.
func pythonLoad(t *testing.T, name string, data []byte) any {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("testdata", "load.py"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(py, "-I", script, path).Output()
	if err != nil {
		t.Fatalf("load.py: %v", err)
	}
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDetect(t *testing.T) {
	bom := append([]byte("\xef\xbb\xbf<?xml version=\"1.0\"?>\n"), readFixture(t, "linux.prefs")...)
	truncated := readFixture(t, "mac-float32.plist")[:60]
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"linux prefs", readFixture(t, "linux.prefs"), true},
		{"linux prefs with a declaration and BOM", bom, true},
		{"plist by plistlib", readFixture(t, "mac-plistlib.plist"), true},
		{"plist by hand", readFixture(t, "mac-float32.plist"), true},
		{"plist of an array", readFixture(t, "mac-array-root.plist"), false},
		{"truncated plist", truncated, false},
		{"another xml", []byte("<?xml version=\"1.0\"?><settings/>"), false},
		{"unity_prefs after other elements", []byte("<a><unity_prefs/></a>"), false},
		{"empty", nil, false},
		{"text", []byte("hello"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Format.Detect("prefs", tt.data); got != tt.want {
				t.Errorf("Detect = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseValues(t *testing.T) {
	type want struct {
		kind     model.Kind
		repr     string
		internal bool
		editable bool
	}
	tests := []struct {
		fixture string
		vars    map[string]want
		order   []string
	}{
		{"linux.prefs", map[string]want{
			"DataJson":                         {model.KindString, `"{\"SettingsFullScreen\":\"1\",\"SettingsResolution\":\"1366x768\"}"`, false, true},
			"Screenmanager Resolution Width":   {model.KindInt, "1366", true, true},
			"UnitySelectMonitor":               {model.KindInt, "-1", true, true},
			"UnityGraphicsQuality":             {model.KindInt, "5", true, true},
			"Gold":                             {model.KindInt, "1500", false, true},
			"Volume":                           {model.KindFloat, "0.8", false, true},
			"Gravity":                          {model.KindFloat, "-9.81", false, true},
			"Tiny":                             {model.KindFloat, "1e-05", false, true},
			"PlayerName":                       {model.KindString, `"Ünity Hero ✓"`, false, true},
			"Empty":                            {model.KindString, `""`, false, true},
			"Notes":                            {model.KindString, `""`, false, true},
			"a&b":                              {model.KindInt, "7", false, true},
			"Binary":                           {model.KindBytes, "3 bytes", false, false},
			"Screenmanager Is Fullscreen mode": {model.KindInt, "1", true, true},
		}, []string{"DataJson", "Screenmanager Is Fullscreen mode", "Screenmanager Resolution Height"}},
		{"mac-float32.plist", map[string]want{
			"Volume":   {model.KindFloat, "0.8", false, true},
			"Gold":     {model.KindInt, "1500", false, true},
			"Name":     {model.KindString, `"Hero"`, false, true},
			"Alias":    {model.KindString, `"Hero"`, false, true},
			"Cheat":    {model.KindBool, "true", false, true},
			"Neg":      {model.KindInt, "-5", false, true},
			"Big":      {model.KindInt, "3000000000", false, true},
			"Greeting": {model.KindString, `"Grüße ✓"`, false, true},
			"Nested":   {model.KindMap, "dict (1 item)", false, false},
			"List":     {model.KindList, "array (3 items)", false, false},
		}, []string{"Volume", "Gold", "Name", "Alias", "Cheat"}},
		{"mac-plistlib.plist", map[string]want{
			"Gold":                       {model.KindInt, "1500", false, true},
			"Volume":                     {model.KindFloat, "0.8", false, true},
			"Greeting":                   {model.KindString, `"Grüße ✓ 😀"`, false, true},
			"Huge":                       {model.KindInt, "9223372036854775813", false, false},
			"Blob":                       {model.KindBytes, "3 bytes", false, false},
			"When":                       {model.KindOpaque, "<2020-05-17T12:30:15Z>", false, false},
			"unity.player_session_count": {model.KindInt, "12", true, true},
			"UnityGraphicsQuality":       {model.KindInt, "5", true, true},
		}, []string{"Gold", "Volume", "PlayerName"}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			doc, _ := parseFixture(t, tt.fixture)
			vars := doc.Variables()
			for i, name := range tt.order {
				if vars[i].Name != name {
					t.Errorf("variable %d = %q, want %q", i, vars[i].Name, name)
				}
			}
			for name, w := range tt.vars {
				v := variable(t, doc, name)
				if v.Slot.Value.Kind != w.kind || v.Slot.Value.Repr() != w.repr {
					t.Errorf("%s = %v %s, want %v %s", name, v.Slot.Value.Kind, v.Slot.Value.Repr(), w.kind, w.repr)
				}
				if v.Internal != w.internal {
					t.Errorf("%s internal = %v", name, v.Internal)
				}
				if (v.Slot.Site != nil) != w.editable {
					t.Errorf("%s editable = %v", name, v.Slot.Site != nil)
				}
			}
		})
	}
}

func TestSiteBounds(t *testing.T) {
	doc, _ := parseFixture(t, "mac-float32.plist")
	gold := variable(t, doc, "Gold").Slot.Site
	if gold.IntMin.Cmp(int32Min) != 0 || gold.IntMax.Cmp(int32Max) != 0 {
		t.Errorf("Gold bounds = %v..%v", gold.IntMin, gold.IntMax)
	}
	big := variable(t, doc, "Big").Slot.Site
	if big.IntMin.Cmp(int64Min) != 0 || big.IntMax.Cmp(int64Max) != 0 {
		t.Errorf("Big bounds = %v..%v", big.IntMin, big.IntMax)
	}
	if v := variable(t, doc, "Volume").Slot.Value; !v.Float32 {
		t.Error("a 4-byte real should be a Float32")
	}
	doc2, _ := parseFixture(t, "mac-plistlib.plist")
	if v := variable(t, doc2, "Volume").Slot.Value; v.Float32 {
		t.Error("an 8-byte real is not a Float32")
	}
	doc3, _ := parseFixture(t, "linux.prefs")
	if v := variable(t, doc3, "Volume").Slot.Value; !v.Float32 {
		t.Error("a float of the XML file is a Float32")
	}
	if s := variable(t, doc3, "PlayerName").Slot.Site; s.CheckText == nil || s.CheckText("a\xffb") == nil || s.CheckText("ok") != nil {
		t.Error("a string site should check for UTF-8")
	}
}

func TestAgainstPython(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			doc, data := parseFixture(t, name)
			want := pythonLoad(t, name, data)
			if got := normalize(t, taggedVars(doc)); !reflect.DeepEqual(got, want) {
				t.Errorf("Go and Python read different values\nGo:     %v\nPython: %v", got, want)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			doc, data := parseFixture(t, name)
			out, err := doc.Encode(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out, data) {
				t.Error("Encode with no edits changed the file")
			}
			// An edit that gives every site its own value keeps the values.
			var edits []model.Edit
			for _, v := range doc.Variables() {
				if v.Slot.Site != nil {
					edits = append(edits, model.Edit{Site: v.Slot.Site, Value: v.Slot.Value})
				}
			}
			out, err = doc.Encode(edits)
			if err != nil {
				t.Fatal(err)
			}
			again, err := Format.Parse(name, out)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := normalize(t, taggedVars(again)), normalize(t, taggedVars(doc)); !reflect.DeepEqual(got, want) {
				t.Errorf("values changed\ngot  %v\nwant %v", got, want)
			}
			if got := pythonLoad(t, name, out); !reflect.DeepEqual(normalize(t, taggedVars(again)), got) {
				t.Errorf("Python reads other values from the output: %v", got)
			}
		})
	}
}

func TestEdits(t *testing.T) {
	huge := strings.Repeat("long text ", 10) + "é✓😀"
	tests := []struct {
		fixture string
		edits   map[string]*model.Value
		// text is checked in the output of an XML file; golden is the exact byte change.
		contains []string
	}{
		{"linux.prefs", map[string]*model.Value{
			"Gold": intV(-2147483648), "Volume": floatV(0.25), "Gravity": floatV(123456789.0), "Tiny": floatV(0.000123),
			"PlayerName": strV("Zoë ✓"), "Empty": strV("filled"), "Notes": strV("a note"),
		}, []string{
			`type="int">-2147483648</pref>`, `type="float">0.25</pref>`, `type="float">123456790</pref>`, `type="float">0.000123</pref>`,
			`type="string">` + b64("Zoë ✓") + `</pref>`, `name="Empty" type="string">` + b64("filled") + `</pref>`,
			`<pref name="Notes" type="string">` + b64("a note") + `</pref>`,
		}},
		{"linux.prefs", map[string]*model.Value{"Gold": intV(2147483647), "PlayerName": strV(huge), "Empty": strV("")}, nil},
		{"mac-float32.plist", map[string]*model.Value{
			"Volume": floatV(0.5), "Gold": intV(70000), "Cheat": boolV(false), "Neg": intV(300), "Big": intV(-3000000000),
			"Greeting": strV("plain ascii now"),
		}, nil},
		{"mac-float32.plist", map[string]*model.Value{
			"Gold": intV(0), "Greeting": strV(huge), "Big": intV(5), "Cheat": boolV(true), "Neg": intV(-2147483648),
		}, nil},
		{"mac-plistlib.plist", map[string]*model.Value{
			"Gold": intV(2000000000), "Volume": floatV(1e300), "PlayerName": strV("Éric"), "Greeting": strV(""),
			"Long": strV("short"), "Flag": boolV(false), "Negative": intV(5), "Big": intV(1), "unity.player_session_count": intV(99),
		}, nil},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("%s %d", tt.fixture, i), func(t *testing.T) {
			doc, data := parseFixture(t, tt.fixture)
			var edits []model.Edit
			for name, v := range tt.edits {
				edits = append(edits, model.Edit{Site: variable(t, doc, name).Slot.Site, Value: v})
			}
			out, err := doc.Encode(edits)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range tt.contains {
				if !strings.Contains(string(out), s) {
					t.Errorf("the output has no %q", s)
				}
			}
			again, err := Format.Parse(tt.fixture, out)
			if err != nil {
				t.Fatal(err)
			}
			// Every variable has its old value, except the edited ones, which have the new value.
			for _, v := range doc.Variables() {
				if v.Name == "List" {
					// List refers to the objects of Volume and Gold, which the writer shared;
					// an edit of Volume or Gold changes that place only.
					list := variable(t, again, "List").Slot.Value
					for k, name := range []string{"Volume", "Gold"} {
						if !reflect.DeepEqual(tagged(list.Children[k].Slot.Value), tagged(variable(t, doc, name).Slot.Value)) {
							t.Errorf("List[%d] changed with %s", k, name)
						}
					}
					continue
				}
				want := tagged(v.Slot.Value)
				if nv, ok := tt.edits[v.Name]; ok {
					nv2 := *nv
					nv2.Type, nv2.Float32 = v.Slot.Value.Type, v.Slot.Value.Float32
					if nv.Kind == model.KindFloat && v.Slot.Value.Float32 {
						nv2.Float = float64(float32(nv.Float))
					}
					want = tagged(&nv2)
				}
				got := variable(t, again, v.Name).Slot.Value
				if !reflect.DeepEqual(normalize(t, tagged(got)), normalize(t, want)) {
					t.Errorf("%s = %v, want %v", v.Name, tagged(got), want)
				}
			}
			if len(again.Variables()) != len(doc.Variables()) {
				t.Errorf("%d variables, want %d", len(again.Variables()), len(doc.Variables()))
			}
			if got := pythonLoad(t, tt.fixture, out); !reflect.DeepEqual(normalize(t, taggedVars(again)), got) {
				t.Errorf("Python reads other values from the output\nGo:     %v\nPython: %v", taggedVars(again), got)
			}
			if bytes.Equal(out, data) {
				t.Error("the file did not change")
			}
		})
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// TestXMLKeepsOtherBytes checks that an XML edit changes only the text of the element.
func TestXMLKeepsOtherBytes(t *testing.T) {
	doc, data := parseFixture(t, "linux.prefs")
	out, err := doc.Encode([]model.Edit{{Site: variable(t, doc, "Gold").Slot.Site, Value: intV(1501)}})
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(data, []byte(`name="Gold" type="int">1500<`), []byte(`name="Gold" type="int">1501<`), 1)
	if !bytes.Equal(out, want) {
		t.Errorf("unexpected change:\n%s", out)
	}
}

// TestPlistKeepsObjects checks that a plist edit keeps the bytes of the objects it does not edit.
func TestPlistKeepsObjects(t *testing.T) {
	doc, data := parseFixture(t, "mac-float32.plist")
	out, err := doc.Encode([]model.Edit{{Site: variable(t, doc, "Gold").Slot.Site, Value: intV(70000)}})
	if err != nil {
		t.Fatal(err)
	}
	// The object of 1500 ("11 05 dc") is shared with List[1], so it is kept, and a new object
	// of 70000 ("12 00 01 11 70", 5 bytes) with its 2 bytes in the offset table is added.
	if len(out) != len(data)+7 {
		t.Errorf("size = %d, want %d", len(out), len(data)+7)
	}
	if !bytes.Contains(out, []byte{0x11, 0x05, 0xdc}) {
		t.Error("the shared object was changed")
	}
	// The 4-byte real, the UTF-16 string and the gap between the objects are kept.
	for _, keep := range []string{"\x22\x3f\x4c\xcc\xcd", "\x00\xfc\x00\xdf"} {
		if strings.Contains(string(data), keep) && !strings.Contains(string(out), keep) {
			t.Errorf("the bytes %q were lost", keep)
		}
	}
	if !bytes.HasPrefix(out, []byte("bplist00")) || !bytes.Contains(out, []byte{0x12, 0x00, 0x01, 0x11, 0x70}) {
		t.Error("the new integer is not in the output")
	}
}

func TestOffsetTableGrows(t *testing.T) {
	// A string long enough to need a 2-byte offset in a file whose offsets are 1 byte.
	doc, _ := parseFixture(t, "mac-plistlib.plist")
	long := strings.Repeat("ab", 400)
	out, err := doc.Encode([]model.Edit{{Site: variable(t, doc, "PlayerName").Slot.Site, Value: strV(long)}})
	if err != nil {
		t.Fatal(err)
	}
	if out[len(out)-32+6] < 2 {
		t.Errorf("offset size = %d, want at least 2", out[len(out)-32+6])
	}
	again, err := Format.Parse("x.plist", out)
	if err != nil {
		t.Fatal(err)
	}
	if got := variable(t, again, "PlayerName").Slot.Value.Str; got != long {
		t.Error("the long string was not stored")
	}
	if got := pythonLoad(t, "x.plist", out); !reflect.DeepEqual(normalize(t, taggedVars(again)), got) {
		t.Error("Python reads other values")
	}
}

func TestRefusedEdits(t *testing.T) {
	other, _ := parseFixture(t, "linux.prefs")
	for _, name := range []string{"linux.prefs", "mac-float32.plist"} {
		doc, _ := parseFixture(t, name)
		get := func(v string) *model.Site { return variable(t, doc, v).Slot.Site }
		gold := get("Gold")
		volume := get("Volume")
		str := "Greeting"
		if name == "linux.prefs" {
			str = "PlayerName"
		}
		tests := []struct {
			name  string
			edits []model.Edit
			route string
		}{
			{"a site of another document", []model.Edit{{Site: other.Variables()[1].Slot.Site, Value: intV(1)}}, "UnityPrefsEditSite"},
			{"no site", []model.Edit{{Value: intV(1)}}, "UnityPrefsEditSite"},
			{"a made up site", []model.Edit{{Site: &model.Site{ID: 0}, Value: intV(1)}}, "UnityPrefsEditSite"},
			{"the same site twice", []model.Edit{{Site: gold, Value: intV(1)}, {Site: gold, Value: intV(2)}}, "UnityPrefsEditSite"},
			{"no value", []model.Edit{{Site: gold}}, "UnityPrefsEditKind"},
			{"a string for an int", []model.Edit{{Site: gold, Value: strV("1")}}, "UnityPrefsEditKind"},
			{"an int for a float", []model.Edit{{Site: volume, Value: intV(1)}}, "UnityPrefsEditKind"},
			{"above int32", []model.Edit{{Site: gold, Value: intV(math.MaxInt32 + 1)}}, "UnityPrefsEditRange"},
			{"below int32", []model.Edit{{Site: gold, Value: intV(math.MinInt32 - 1)}}, "UnityPrefsEditRange"},
			{"a nil int", []model.Edit{{Site: gold, Value: &model.Value{Kind: model.KindInt}}}, "UnityPrefsEditRange"},
			{"NaN", []model.Edit{{Site: volume, Value: floatV(math.NaN())}}, "UnityPrefsEditRange"},
			{"infinity", []model.Edit{{Site: volume, Value: floatV(math.Inf(1))}}, "UnityPrefsEditRange"},
			{"too large for float32", []model.Edit{{Site: volume, Value: floatV(1e39)}}, "UnityPrefsEditRange"},
			{"not UTF-8", []model.Edit{{Site: get(str), Value: strV("a\xffb")}}, "UnityPrefsEditText"},
		}
		for _, tt := range tests {
			t.Run(name+" "+tt.name, func(t *testing.T) {
				_, err := doc.Encode(tt.edits)
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
}

// TestSharedObjectsAreEditedOnePlaceAtATime: a writer keeps one object for equal strings and
// numbers, even when one is a key. An edit of a place changes that place only.
func TestSharedObjectsAreEditedOnePlaceAtATime(t *testing.T) {
	doc, _ := parseFixture(t, "mac-float32.plist")
	out, err := doc.Encode([]model.Edit{
		{Site: variable(t, doc, "Name").Slot.Site, Value: strV("Zed")},
		{Site: variable(t, doc, "Gold").Slot.Site, Value: intV(1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := Format.Parse("x.plist", out)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"Name": `"Zed"`, "Alias": `"Hero"`, "Gold": "1"} {
		if got := variable(t, again, name).Slot.Value.Repr(); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
	// The key "Hero" of Nested, which shares the object, is as it was.
	if got := variable(t, again, "Nested").Slot.Value.Children[0].Label; got != "Hero" {
		t.Errorf("the key of Nested is %q", got)
	}
	if got := variable(t, again, "List").Slot.Value.Children[1].Slot.Value.Repr(); got != "1500" {
		t.Errorf("List[1] = %s", got)
	}
	if got := pythonLoad(t, "x.plist", out); !reflect.DeepEqual(normalize(t, taggedVars(again)), got) {
		t.Error("Python reads other values")
	}
}

// TestReferencesGrow: when the new objects need more bytes for a reference than the file has,
// every container is written again with the larger references.
func TestReferencesGrow(t *testing.T) {
	// A dictionary of 254 keys that all refer to one integer: 256 objects, 1-byte references.
	const keys = 254
	var objs [][]byte
	objs = append(objs, nil) // the top dictionary
	for i := range keys {
		objs = append(objs, append([]byte{0x50 | 4}, fmt.Sprintf("k%03d", i)...))
	}
	objs = append(objs, []byte{0x10, 0x05})
	top := []byte{0xDF, 0x10, keys}
	for i := range keys {
		top = append(top, byte(1+i))
	}
	for range keys {
		top = append(top, byte(keys+1))
	}
	objs[0] = top
	data := []byte(plistMagic)
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = len(data)
		data = append(data, o...)
	}
	tableOff := len(data)
	for _, o := range offsets {
		data = append(data, byte(o>>8), byte(o))
	}
	trailer := make([]byte, 32)
	trailer[6], trailer[7] = 2, 1
	trailer[15] = 0
	binary.BigEndian.PutUint64(trailer[8:], uint64(len(objs)))
	binary.BigEndian.PutUint64(trailer[24:], uint64(tableOff))
	data = append(data, trailer...)

	doc, err := Format.Parse("grow.plist", data)
	if err != nil {
		t.Fatal(err)
	}
	if got := pythonLoad(t, "grow.plist", data); !reflect.DeepEqual(normalize(t, taggedVars(doc)), got) {
		t.Fatal("Python reads other values from the file made by the test")
	}
	out, err := doc.Encode([]model.Edit{{Site: variable(t, doc, "k007").Slot.Site, Value: intV(9)}})
	if err != nil {
		t.Fatal(err)
	}
	if out[len(out)-32+7] != 2 {
		t.Errorf("reference size = %d, want 2", out[len(out)-32+7])
	}
	again, err := Format.Parse("grow.plist", out)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range again.Variables() {
		want := "5"
		if v.Name == "k007" {
			want = "9"
		}
		if got := v.Slot.Value.Repr(); got != want {
			t.Errorf("%s = %s, want %s", v.Name, got, want)
		}
	}
	if got := pythonLoad(t, "grow.plist", out); !reflect.DeepEqual(normalize(t, taggedVars(again)), got) {
		t.Error("Python reads other values from the output")
	}
}

func TestPlistRefusals(t *testing.T) {
	doc, _ := parseFixture(t, "mac-float32.plist")
	// The bool of a plist has an edit that keeps its kind only.
	cheat := variable(t, doc, "Cheat").Slot.Site
	if _, err := doc.Encode([]model.Edit{{Site: cheat, Value: intV(1)}}); err == nil {
		t.Error("an int for a bool was accepted")
	}
	// A real of 4 bytes cannot hold a number beyond float32.
	if _, err := doc.Encode([]model.Edit{{Site: variable(t, doc, "Volume").Slot.Site, Value: floatV(1e39)}}); err == nil {
		t.Error("1e39 in a 4-byte real was accepted")
	}
	// A real of 8 bytes can.
	d2, _ := parseFixture(t, "mac-plistlib.plist")
	if _, err := d2.Encode([]model.Edit{{Site: variable(t, d2, "Volume").Slot.Site, Value: floatV(1e39)}}); err != nil {
		t.Errorf("1e39 in an 8-byte real: %v", err)
	}
}

func TestParseFailures(t *testing.T) {
	plist := readFixture(t, "mac-float32.plist")
	withTrailer := func(offset, value int) []byte {
		b := bytes.Clone(plist)
		b[len(b)-32+offset] = byte(value)
		return b
	}
	arrayRoot := readFixture(t, "mac-array-root.plist")
	tests := []struct {
		name  string
		data  string
		route string
	}{
		{"nothing", "", "parse.UnityPrefsUnknownLayout"},
		{"text", "hello", "parse.UnityPrefsUnknownLayout"},
		{"xml that stops", "<unity_prefs version_major=\"1\"><pref name=\"a\" type=\"int\">1</unity_prefs>", "parseXML->readPref.UnityPrefsXMLInvalid"},
		{"xml with a bad tag", "<unity_prefs><pref name=\"a\" type=\"int\">1</pref></x>", "parseXML.UnityPrefsXMLInvalid"},
		{"xml without a root", "<?xml version=\"1.0\"?><!-- <unity_prefs -->", "parseXML.UnityPrefsXMLRoot"},
		{"unfinished", "<unity_prefs", "parseXML.UnityPrefsXMLInvalid"},
		{"array root", string(arrayRoot), "parsePlistDocument.UnityPlistRoot"},
		{"short plist", string(plist[:30]), "parsePlist.UnityPlistInvalid"},
		{"bad offset size", string(withTrailer(6, 0)), "parsePlist.UnityPlistInvalid"},
		{"bad object count", string(withTrailer(15, 0)), "parsePlist.UnityPlistInvalid"},
		{"bad top object", string(withTrailer(23, 200)), "parsePlist.UnityPlistInvalid"},
		{"bad table offset", string(withTrailer(31, 0)), "parsePlist.UnityPlistInvalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Format.Parse("x", []byte(tt.data))
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

// TestPlistObjectFailures corrupts single objects of a plist.
func TestPlistObjectFailures(t *testing.T) {
	// One object (the top, an empty dictionary), made by hand.
	build := func(obj []byte) []byte {
		b := append([]byte("bplist00"), obj...)
		table := len(b)
		b = append(b, 8)
		tr := make([]byte, 32)
		tr[6], tr[7] = 1, 1
		tr[15] = 1
		tr[31] = byte(table)
		return append(b, tr...)
	}
	tests := []struct {
		name  string
		obj   []byte
		route string
	}{
		{"unknown marker", []byte{0x0F}, "parseObject.UnityPlistInvalid"},
		{"too large integer", []byte{0x15, 0, 0}, "parseObject.UnityPlistInvalid"},
		{"cut integer", []byte{0x12, 0}, "parseObject.UnityPlistInvalid"},
		{"real of 1 byte", []byte{0x20, 0}, "parseObject.UnityPlistInvalid"},
		{"short date", []byte{0x33, 0}, "parseObject.UnityPlistInvalid"},
		{"cut string", []byte{0x55, 'a'}, "parseObject.UnityPlistInvalid"},
		{"cut data", []byte{0x4F, 0x10, 9}, "parseObject.UnityPlistInvalid"},
		{"count that is not an integer", []byte{0x5F, 0x50}, "readCount.UnityPlistInvalid"},
		{"cut count", []byte{0x5F, 0x12, 0}, "readCount.UnityPlistInvalid"},
		{"cut UID", []byte{0x83, 0}, "parseObject.UnityPlistInvalid"},
		{"cut array", []byte{0xA3, 0, 0}, "parseObject.UnityPlistInvalid"},
		{"reference to nothing", []byte{0xA1, 9}, "parseObject.UnityPlistInvalid"},
		{"string marker 7", []byte{0x71, 'a'}, "parseObject.UnityPlistInvalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parsePlist(build(tt.obj))
			appErr, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("expected *errors.Error, got %T: %v", err, err)
			}
			if !appErr.HasRoute(tt.route) {
				t.Errorf("unexpected failure path: %s", appErr.Route())
			}
		})
	}
	// The same builder with a good object parses.
	if _, err := parsePlist(build([]byte{0xD0})); err != nil {
		t.Errorf("an empty dictionary: %v", err)
	}
}

func TestOverlappingObjects(t *testing.T) {
	// Two objects at the same position.
	b := append([]byte("bplist00"), 0xD0)
	table := len(b)
	b = append(b, 8, 8)
	tr := make([]byte, 32)
	tr[6], tr[7], tr[15], tr[31] = 1, 1, 2, byte(table)
	_, err := parsePlist(append(b, tr...))
	appErr, ok := errors.FromError(err)
	if !ok || !appErr.HasRoute("parsePlist.UnityPlistInvalid") {
		t.Errorf("overlap: %v", err)
	}
}

func TestCheckTextRoute(t *testing.T) {
	err := checkUTF8("a\xffb")
	appErr, ok := errors.FromError(err)
	if !ok || !appErr.HasRoute("checkUTF8.UnityPrefsEditText") {
		t.Errorf("checkUTF8 = %v", err)
	}
}

func TestFormatFloat32(t *testing.T) {
	tests := []struct {
		f    float64
		want string
	}{
		{0, "0"}, {1, "1"}, {0.8, "0.8"}, {-9.81, "-9.81"}, {0.00001, "0.00001"}, {1e-7, "1E-07"},
		{123456789, "123456790"}, {1e20, "1E+20"}, {16777216, "16777216"}, {math.MaxFloat32, "3.4028235E+38"},
	}
	for _, tt := range tests {
		if got := formatFloat32(tt.f); got != tt.want {
			t.Errorf("formatFloat32(%v) = %q, want %q", tt.f, got, tt.want)
		}
	}
}

func TestDetails(t *testing.T) {
	for _, name := range fixtures {
		doc, _ := parseFixture(t, name)
		if len(doc.Details()) == 0 || doc.Details()[0].Label != "Layout" {
			t.Errorf("%s: details = %v", name, doc.Details())
		}
	}
}

func TestDuplicateNames(t *testing.T) {
	data := `<unity_prefs version_major="1" version_minor="1"><pref name="a" type="int">1</pref><pref name="a" type="int">2</pref></unity_prefs>`
	doc, err := Format.Parse("prefs", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	vars := doc.Variables()
	if len(vars) != 2 || vars[0].Name != "a" || vars[1].Name != "a (2)" {
		t.Errorf("names = %v", vars)
	}
}

func TestOddXML(t *testing.T) {
	data := `<unity_prefs version_major="1" version_minor="1"><pref name="a" type="int">x</pref>` +
		`<pref name="b" type="color">1</pref><pref name="c" type="string">***</pref><pref name="d" type="int"><x/></pref>` +
		`<pref name="e" type="float">nope</pref><other/><pref name="f" type="string">gA==</pref></unity_prefs>`
	doc, err := Format.Parse("prefs", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Kind{model.KindOpaque, model.KindOpaque, model.KindOpaque, model.KindOpaque, model.KindOpaque, model.KindBytes}
	for i, v := range doc.Variables() {
		if v.Slot.Value.Kind != want[i] || v.Slot.Site != nil {
			t.Errorf("%s: kind %v site %v", v.Name, v.Slot.Value.Kind, v.Slot.Site)
		}
	}
}

// TestDamagedFilesDoNotPanic parses every fixture with one byte changed or the end cut off:
// the result may be an error, or a document that encodes, but never a panic.
func TestDamagedFilesDoNotPanic(t *testing.T) {
	for _, name := range fixtures {
		data := readFixture(t, name)
		try := func(b []byte) {
			doc, err := Format.Parse(name, b)
			if err != nil {
				return
			}
			var edits []model.Edit
			for _, v := range doc.Variables() {
				if v.Slot.Site != nil {
					edits = append(edits, model.Edit{Site: v.Slot.Site, Value: v.Slot.Value})
				}
			}
			if _, err := doc.Encode(edits); err != nil {
				t.Logf("%s: encode: %v", name, err)
			}
		}
		for i := range data {
			for _, c := range []byte{0x00, 0xFF, 0x0F} {
				b := bytes.Clone(data)
				b[i] = c
				try(b)
			}
			try(data[:i])
		}
	}
}
