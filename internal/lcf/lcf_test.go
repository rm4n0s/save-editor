package lcf

import (
	"bytes"
	"context"
	"encoding/hex"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// fixtures are made by testdata/gen.py.
var fixtures = []struct {
	file     string
	game     string
	encoding string // the name of the codec
	pyEnc    string // the name of the encoding in Python
	hero     string
	actor1   string
	unknown  int
}{
	{"rm2k_sjis.lsd", "RPG Maker 2000", "Shift-JIS (code page 932)", "cp932", "勇者アリス", "アリス", 3},
	{"rm2k3_cp1252.lsd", "RPG Maker 2003", "Windows-1252 (Western European)", "cp1252", "René Zoë", "Chloé", 3},
	{"rm2k3_cp1251.lsd", "RPG Maker 2003", "Windows-1251 (Cyrillic)", "cp1251", "Алиса", "Борис", 3},
	{"rm2k3_easyrpg_cp1250.lsd", "RPG Maker 2003", "Windows-1250 (Central European)", "cp1250", "Łukasz", "Żaneta", 3},
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parseFixture(t testing.TB, name string) *Document {
	t.Helper()
	d, err := Parse(name, readFixture(t, name))
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return d.(*Document)
}

// flatten writes every primitive of the document as "path = value", in the format of
// testdata/gen.py dump.
func flatten(d model.Document) []string {
	var lines []string
	var walk func(path string, v *model.Value)
	walk = func(path string, v *model.Value) {
		switch v.Kind {
		case model.KindList, model.KindObject, model.KindMap:
			for _, c := range v.Children {
				walk(path+"."+c.Label, c.Slot.Value)
			}
		default:
			lines = append(lines, path+" = "+flatValue(v))
		}
	}
	for _, v := range d.Variables() {
		walk(v.Name, v.Slot.Value)
	}
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, ".")
	}
	return lines
}

func flatValue(v *model.Value) string {
	switch v.Kind {
	case model.KindInt:
		return v.Int.String()
	case model.KindBool:
		return strconv.FormatBool(v.Bool)
	case model.KindFloat:
		return model.FormatFloat(v.Float, false)
	case model.KindString:
		return strconv.QuoteToASCII(v.Str)
	case model.KindBytes:
		return "bytes " + hex.EncodeToString(v.Bytes)
	}
	return "?"
}

// slotAt finds the slot at a path of labels such as "actors.[1].name".
func slotAt(t testing.TB, d model.Document, path string) model.Slot {
	t.Helper()
	parts := strings.Split(path, ".")
	for _, v := range d.Variables() {
		if v.Name != parts[0] {
			continue
		}
		slot := v.Slot
		for _, label := range parts[1:] {
			found := false
			for _, c := range slot.Value.Children {
				if c.Label == label {
					slot, found = c.Slot, true
					break
				}
			}
			if !found {
				t.Fatalf("no %q in %s", label, path)
			}
		}
		return slot
	}
	t.Fatalf("no variable %q", parts[0])
	return model.Slot{}
}

func intValue(n int64) *model.Value  { return &model.Value{Kind: model.KindInt, Int: big.NewInt(n)} }
func strValue(s string) *model.Value { return &model.Value{Kind: model.KindString, Str: s} }
func boolValue(b bool) *model.Value  { return &model.Value{Kind: model.KindBool, Bool: b} }
func floatValue(f float64) *model.Value {
	return &model.Value{Kind: model.KindFloat, Float: f}
}

func detail(d model.Document, label string) string {
	for _, x := range d.Details() {
		if x.Label == label {
			return x.Text
		}
	}
	return "<none>"
}

func TestSchema(t *testing.T) {
	for _, tc := range []struct {
		strct string
		id    int
		name  string
		typ   fieldType
	}{
		{"Save", 0x64, "title", ftStruct},
		{"Save", 0x6C, "actors", ftArray},
		{"Save", 0x72, "common_events", ftArray},
		{"Save", 0xC8, "easyrpg_data", ftStruct},
		{"SaveTitle", 0x0B, "hero_name", ftString},
		{"SaveTitle", 0x01, "timestamp", ftDouble},
		{"SaveSystem", 0x1F, "switches_count", ftCount},
		{"SaveSystem", 0x20, "switches", ftVecBool},
		{"SaveSystem", 0x22, "variables", ftVecInt32},
		{"SaveSystem", 0x6F, "transition_out", ftInt8},
		{"SaveActor", 0x34, "skills", ftVecInt16},
		{"SaveActor", 0x50, "battle_commands", ftVecInt32},
		{"SaveInventory", 0x0D, "item_counts", ftVecUInt8},
		{"SavePicture", 0x1B, "flags", ftFlags},
		{"SavePartyLocation", 0x0C, "position_x", ftInt32}, // from SaveMapEventBase
		{"SaveMapEvent", 0x6C, "parallel_event_execstate", ftStruct},
		{"SaveMapEvent", 0x29, "move_route", ftStruct},
		{"SaveEasyRpgData", 0x02, "codepage", ftInt32},
	} {
		f := schema[tc.strct].byID[tc.id]
		if f == nil || f.name != tc.name || f.typ != tc.typ {
			t.Errorf("%s 0x%02X = %+v, want %s of type %d", tc.strct, tc.id, f, tc.name, tc.typ)
		}
	}
	for name, hasID := range map[string]bool{
		"SavePicture": true, "SaveActor": true, "SaveTarget": true, "SaveEventExecFrame": true,
		"SaveMapEvent": true, "SaveCommonEvent": true, "SaveEasyRpgWindow": true,
		"SaveEasyRpgText": false, "SaveInventory": false, "Music": false,
	} {
		if schema[name].hasID != hasID {
			t.Errorf("%s hasID = %v, want %v", name, schema[name].hasID, hasID)
		}
	}
	if !schema["SaveActor"].byID[0x50].is2k3 || schema["SaveActor"].byID[0x01].is2k3 {
		t.Error("is2k3 flags are wrong in SaveActor")
	}
	// The save structures of liblcf are all there.
	for _, name := range []string{"SaveTitle", "SaveSystem", "SaveScreen", "SavePicture", "SavePartyLocation",
		"SaveVehicleLocation", "SaveActor", "SaveInventory", "SaveTarget", "SaveEventExecFrame",
		"SaveEventExecState", "SaveMapEventBase", "SaveMapEvent", "SaveMapInfo", "SaveCommonEvent",
		"SavePanorama", "Save"} {
		if schema[name] == nil {
			t.Errorf("struct %s is missing", name)
		}
	}
}

func TestBER(t *testing.T) {
	for _, tc := range []struct {
		n   uint32
		enc string
	}{
		{0, "00"}, {1, "01"}, {127, "7f"}, {128, "8100"}, {300, "822c"}, {16383, "ff7f"},
		{16384, "818000"}, {70000, "84a270"}, {math.MaxInt32, "87ffffff7f"},
		{1 << 31, "8880808000"}, {math.MaxUint32, "8fffffff7f"},
	} {
		got := appendBER(nil, tc.n)
		if hex.EncodeToString(got) != tc.enc {
			t.Errorf("appendBER(%d) = %x, want %s", tc.n, got, tc.enc)
		}
		v, n, ok := readBER(got, 0, len(got))
		if !ok || v != tc.n || n != len(got) {
			t.Errorf("readBER(%x) = %d, %d, %v", got, v, n, ok)
		}
	}
	for _, bad := range []string{"", "80", "ff", "8080808080", "ffffffffff00"} {
		b, _ := hex.DecodeString(bad)
		if _, _, ok := readBER(b, 0, len(b)); ok {
			t.Errorf("readBER(%q) succeeded", bad)
		}
	}
	// A number is not read past the limit.
	if _, _, ok := readBER([]byte{0x81, 0x00}, 0, 1); ok {
		t.Error("readBER read past its limit")
	}
}

func TestDetect(t *testing.T) {
	good := readFixture(t, "rm2k_sjis.lsd")
	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		{"Save01.lsd", good, true},
		{"anything.bin", good, true},
		{"Save01.lsd", []byte("\x0bLcfSaveData"), true},
		{"Save01.lsd", []byte("\x0aLcfMapUnit"), false},
		{"Save01.lsd", []byte("\x0bLcfDataBase"), false},
		{"Save01.lsd", []byte("\x0bLcfSaveDat"), false},
		{"Save01.lsd", []byte("\x0cLcfSaveData"), false},
		{"Save01.lsd", nil, false},
		{"Save01.lsd", []byte("{}"), false},
	} {
		if got := Detect(tc.name, tc.data); got != tc.want {
			t.Errorf("Detect(%q, %q) = %v, want %v", tc.name, tc.data[:min(len(tc.data), 12)], got, tc.want)
		}
	}
	if !Format.Detect("x", good) || Format.Name == "" || len(Format.Patterns) == 0 || Format.Parse == nil {
		t.Error("Format is incomplete")
	}
}

func TestParseFixtures(t *testing.T) {
	topNames := []string{"title", "system", "screen", "pictures", "party_location", "boat_location",
		"ship_location", "airship_location", "actors", "inventory", "targets", "map_info", "panorama",
		"foreground_event_execstate", "common_events", "unknown_0x7F"}
	for _, fx := range fixtures {
		t.Run(fx.file, func(t *testing.T) {
			d := parseFixture(t, fx.file)
			var names []string
			for _, v := range d.Variables() {
				names = append(names, v.Name)
				if v.Name == "common_events" || v.Name == "foreground_event_execstate" {
					if !v.Internal {
						t.Errorf("%s is not internal", v.Name)
					}
				}
			}
			// The chunks are in the order of their ids: the unknown 0x7F is after 0x72 and before
			// easyrpg_data (0xC8).
			want := slices.Clone(topNames)
			if fx.file == "rm2k3_easyrpg_cp1250.lsd" {
				want = append(want, "easyrpg_data")
			}
			if !slices.Equal(names, want) {
				t.Errorf("variables = %v, want %v", names, want)
			}
			if detail(d, "Game") != fx.game {
				t.Errorf("Game = %q, want %q", detail(d, "Game"), fx.game)
			}
			if detail(d, "Text encoding") != fx.encoding {
				t.Errorf("Text encoding = %q, want %q", detail(d, "Text encoding"), fx.encoding)
			}
			for label, want := range map[string]string{
				"Hero": fx.hero, "Level": "12", "HP": "345", "Gold": "1500", "Switches": "12",
				"Variables": "10", "Saved": "2023-03-15 18:00:00", "Unknown chunks": strconv.Itoa(fx.unknown),
			} {
				if got := detail(d, label); got != want {
					t.Errorf("%s = %q, want %q", label, got, want)
				}
			}
			if got := slotAt(t, d, "actors.[1].name").Value.Str; got != fx.actor1 {
				t.Errorf("actor 1 = %q, want %q", got, fx.actor1)
			}
			if tail := d.tail(); tail != 0 {
				t.Errorf("%d bytes unread", tail)
			}
		})
	}
}

func TestParseValues(t *testing.T) {
	d := parseFixture(t, "rm2k3_cp1252.lsd")
	for _, tc := range []struct {
		path string
		kind model.Kind
		typ  string
		text string
	}{
		{"title.hero_name", model.KindString, "String", `"René Zoë"`},
		{"title.timestamp", model.KindFloat, "Double", "45000.75"},
		{"system.variables.0006", model.KindInt, "Int32", "70000"},
		{"system.variables.0008", model.KindInt, "Int32", "-2147483648"},
		{"system.variables.0009", model.KindInt, "Int32", "2147483647"},
		{"system.switches.0001", model.KindBool, "Boolean", "true"},
		{"system.switches.0002", model.KindBool, "Boolean", "false"},
		{"system.transition_out", model.KindInt, "Int8", "-1"},
		{"system.atb_mode", model.KindInt, "Int32", "1"},
		{"system.title_music.name", model.KindString, "String", `"Thème"`},
		{"pictures.[3].flags", model.KindInt, "Flags", "33"},
		{"pictures.[1].spritesheet_cols", model.KindInt, "Int32", "4"},
		{"actors.[1].skills.[2]", model.KindInt, "Int16", "300"},
		{"actors.[1].battle_commands.[3]", model.KindInt, "Int32", "-1"},
		{"actors.[2].two_weapon", model.KindBool, "Boolean", "false"},
		{"inventory.item_counts.[2]", model.KindInt, "UInt8", "99"},
		{"inventory.steps", model.KindInt, "Int32", "40000"},
		{"map_info.events.[2].position_y", model.KindInt, "Int32", "4"},
		{"map_info.events.[1].parallel_event_execstate.stack.[1].event_id", model.KindInt, "Int32", "1"},
		{"party_location.pan_current_x", model.KindInt, "Int32", "-1"},
		{"party_location.map_id", model.KindInt, "Int32", "5"},
		{"unknown_0x7F", model.KindBytes, "unknown chunk 0x7F", "4 bytes"},
		{"system.unknown_0xF1", model.KindBytes, "unknown chunk 0xF1", "8 bytes"},
		{"actors.[1].unknown_0x77", model.KindBytes, "unknown chunk 0x77", "2 bytes"},
		{"map_info.events.[1].move_route.move_commands", model.KindBytes, "Bytes", "2 bytes"},
	} {
		slot := slotAt(t, d, tc.path)
		v := slot.Value
		if v.Kind != tc.kind || v.Type != tc.typ || strings.TrimPrefix(v.Repr(), "") != tc.text {
			t.Errorf("%s = %v %q %s, want %v %q %s", tc.path, v.Kind, v.Type, v.Repr(), tc.kind, tc.typ, tc.text)
		}
		if (slot.Site != nil) != (tc.kind != model.KindBytes) {
			t.Errorf("%s: site = %v", tc.path, slot.Site)
		}
	}
	if v := slotAt(t, d, "actors").Value; v.Kind != model.KindList || v.Type != "Array<SaveActor>" || len(v.Children) != 2 {
		t.Errorf("actors = %v", v.Repr())
	}
	// The counts of the vectors are not shown.
	for _, c := range slotAt(t, d, "system").Value.Children {
		if strings.HasSuffix(c.Label, "_count") && c.Label != "frame_count" && c.Label != "save_count" {
			t.Errorf("the count %s is shown", c.Label)
		}
	}
	// Every site has its own ID.
	ids := map[int]bool{}
	var walk func(v *model.Value, s *model.Site)
	walk = func(v *model.Value, s *model.Site) {
		if s != nil {
			if ids[s.ID] {
				t.Fatalf("site %d is used twice", s.ID)
			}
			ids[s.ID] = true
		}
		for _, c := range v.Children {
			walk(c.Slot.Value, c.Slot.Site)
		}
	}
	for _, v := range d.Variables() {
		walk(v.Slot.Value, v.Slot.Site)
	}
	if len(ids) < 100 {
		t.Errorf("only %d sites", len(ids))
	}
	if site := slotAt(t, d, "system.variables.0001").Site; site.IntMin.Int64() != math.MinInt32 || site.IntMax.Int64() != math.MaxInt32 {
		t.Errorf("variable bounds = %v..%v", site.IntMin, site.IntMax)
	}
	if site := slotAt(t, d, "inventory.item_counts.[0]").Site; site.IntMin.Int64() != 0 || site.IntMax.Int64() != 255 {
		t.Errorf("item count bounds = %v..%v", site.IntMin, site.IntMax)
	}
	if site := slotAt(t, d, "actors.[1].skills.[0]").Site; site.IntMin.Int64() != -32768 || site.IntMax.Int64() != 32767 {
		t.Errorf("skill bounds = %v..%v", site.IntMin, site.IntMax)
	}
}

func TestRoundTrip(t *testing.T) {
	for _, fx := range fixtures {
		data := readFixture(t, fx.file)
		d := parseFixture(t, fx.file)
		got, err := d.Encode(nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("%s: Encode(nil) differs from the file", fx.file)
		}
		// Edits that change nothing leave the file as it was.
		same := slotAt(t, d, "title.hero_level")
		got, err = d.Encode([]model.Edit{{Site: same.Site, Value: intValue(12)}})
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("%s: an edit to the same value changed the file (%v)", fx.file, err)
		}
	}
}

// python3 runs testdata/gen.py dump; the test is skipped when python3 is not installed.
func pythonDump(t *testing.T, path, encoding string) []string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, py, "-I", filepath.Join("testdata", "gen.py"), "dump", path, encoding).Output()
	if err != nil {
		t.Fatalf("gen.py dump %s: %v", path, err)
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

func TestAgainstPython(t *testing.T) {
	for _, fx := range fixtures {
		t.Run(fx.file, func(t *testing.T) {
			d := parseFixture(t, fx.file)
			want := pythonDump(t, filepath.Join("testdata", fx.file), fx.pyEnc)
			got := flatten(d)
			if !slices.Equal(got, want) {
				for i := range max(len(got), len(want)) {
					g, w := "", ""
					if i < len(got) {
						g = got[i]
					}
					if i < len(want) {
						w = want[i]
					}
					if g != w {
						t.Fatalf("line %d: Go has %q, Python has %q", i, g, w)
					}
				}
			}
		})
	}
}

// edits are the edits of TestEdits; each is made to one of the fixtures.
var editCases = []struct {
	name    string
	file    string
	path    string
	value   *model.Value
	grow    int // how much the file grows
	pyCheck bool
}{
	{"int, same size", "rm2k_sjis.lsd", "title.hero_level", intValue(99), 0, true},
	{"int, grows to 2 bytes", "rm2k_sjis.lsd", "title.hero_level", intValue(300), 1, true},
	{"int, grows to 5 bytes", "rm2k_sjis.lsd", "title.hero_hp", intValue(-5), 3, true},
	{"int, shrinks", "rm2k_sjis.lsd", "inventory.gold", intValue(7), -1, true},
	{"int, max", "rm2k_sjis.lsd", "inventory.steps", intValue(math.MaxInt32), 2, true},
	{"int, min", "rm2k_sjis.lsd", "inventory.steps", intValue(math.MinInt32), 2, true},
	{"int in a nested array", "rm2k3_cp1252.lsd", "map_info.events.[1].parallel_event_execstate.stack.[1].event_id", intValue(100000), 2, true},
	{"int in an array element", "rm2k3_cp1252.lsd", "map_info.events.[2].position_y", intValue(70000), 2, true},
	{"int8", "rm2k_sjis.lsd", "system.transition_out", intValue(127), 0, true},
	{"flags", "rm2k_sjis.lsd", "pictures.[1].flags", intValue(255), 0, true},
	{"bool true", "rm2k_sjis.lsd", "system.switches.0002", boolValue(true), 0, true},
	{"bool false", "rm2k_sjis.lsd", "system.switches.0001", boolValue(false), 0, true},
	{"bool field", "rm2k_sjis.lsd", "system.save_allowed", boolValue(false), 0, true},
	{"variable", "rm2k_sjis.lsd", "system.variables.0007", intValue(2147483647), 0, true},
	{"variable to zero", "rm2k_sjis.lsd", "system.variables.0006", intValue(0), 0, true},
	{"int16 element", "rm2k3_cp1252.lsd", "actors.[1].skills.[0]", intValue(-32768), 0, true},
	{"uint8 element", "rm2k_sjis.lsd", "inventory.item_counts.[1]", intValue(255), 0, true},
	{"battle command", "rm2k3_cp1252.lsd", "actors.[2].battle_commands.[6]", intValue(9), 0, true},
	{"double", "rm2k_sjis.lsd", "title.timestamp", floatValue(45001.5), 0, true},
	{"double in an array element", "rm2k_sjis.lsd", "pictures.[1].start_y", floatValue(0.1), 0, true},
	{"shift-jis string, longer", "rm2k_sjis.lsd", "title.hero_name", strValue("魔王ベルゼブブ"), 4, true},
	{"shift-jis string, shorter", "rm2k_sjis.lsd", "actors.[1].name", strValue("A"), -5, true},
	{"shift-jis string, empty", "rm2k_sjis.lsd", "system.title_music.name", strValue(""), -8, true},
	{"shift-jis string, from empty", "rm2k_sjis.lsd", "actors.[2].title", strValue("勇者"), 4, true},
	{"windows-1252 string", "rm2k3_cp1252.lsd", "title.hero_name", strValue("Zoë Ñandú ©"), 3, true},
	{"windows-1251 string", "rm2k3_cp1251.lsd", "actors.[1].title", strValue("Мастер меча"), 8, true},
	{"windows-1250 string", "rm2k3_easyrpg_cp1250.lsd", "title.hero_name", strValue("Łódź Żółć"), 3, true},
	{"string in the third level", "rm2k3_cp1252.lsd", "system.title_music.name", strValue("Thème principal"), 10, true},
}

func TestEdits(t *testing.T) {
	for _, tc := range editCases {
		t.Run(tc.name, func(t *testing.T) {
			data := readFixture(t, tc.file)
			d := parseFixture(t, tc.file)
			before := flatten(d)
			slot := slotAt(t, d, tc.path)
			out, err := d.Encode([]model.Edit{{Site: slot.Site, Value: tc.value}})
			if err != nil {
				t.Fatal(err)
			}
			if len(out)-len(data) != tc.grow {
				t.Errorf("the file grew by %d bytes, want %d", len(out)-len(data), tc.grow)
			}
			re, err := Parse(tc.file, out)
			if err != nil {
				t.Fatal(err)
			}
			after := flatten(re)
			if len(after) != len(before) {
				t.Fatalf("%d values after, %d before", len(after), len(before))
			}
			changed := 0
			for i := range before {
				if before[i] != after[i] {
					changed++
					want := strings.SplitN(before[i], " = ", 2)[0] + " = " + flatValue(tc.value)
					if tc.value.Kind == model.KindString {
						want = strings.SplitN(before[i], " = ", 2)[0] + " = " + strconv.QuoteToASCII(tc.value.Str)
					}
					if after[i] != want {
						t.Errorf("%q, want %q", after[i], want)
					}
					if !strings.HasPrefix(before[i], tc.path+" = ") {
						t.Errorf("%q changed", before[i])
					}
				}
			}
			if changed != 1 {
				t.Errorf("%d values changed, want 1", changed)
			}
			// The unknown chunks are kept as they were.
			for _, p := range []string{"unknown_0x7F", "system.unknown_0xF1", "actors.[1].unknown_0x77"} {
				a, b := slotAt(t, d, p).Value.Bytes, slotAt(t, re, p).Value.Bytes
				if !bytes.Equal(a, b) {
					t.Errorf("%s changed", p)
				}
			}
			// Editing back gives the original file when the BER numbers were the shortest.
			back, err := re.(*Document).Encode([]model.Edit{{Site: slotAt(t, re, tc.path).Site, Value: slot.Value}})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(back, data) {
				t.Errorf("editing the value back does not give the original file")
			}
			if tc.pyCheck {
				dir := t.TempDir()
				p := filepath.Join(dir, "out.lsd")
				if err := os.WriteFile(p, out, 0o600); err != nil {
					t.Fatal(err)
				}
				enc := ""
				for _, fx := range fixtures {
					if fx.file == tc.file {
						enc = fx.pyEnc
					}
				}
				py := pythonDump(t, p, enc)
				if !slices.Equal(py, after) {
					t.Errorf("Python reads the edited file differently")
				}
			}
		})
	}
}

func TestSeveralEdits(t *testing.T) {
	data := readFixture(t, "rm2k_sjis.lsd")
	d := parseFixture(t, "rm2k_sjis.lsd")
	edits := []model.Edit{
		{Site: slotAt(t, d, "title.hero_name").Site, Value: strValue("ABC")},
		{Site: slotAt(t, d, "actors.[1].level").Site, Value: intValue(1 << 20)},
		{Site: slotAt(t, d, "actors.[2].name").Site, Value: strValue("ロング・ネーム")},
		{Site: slotAt(t, d, "system.variables.0001").Site, Value: intValue(-1)},
		{Site: slotAt(t, d, "system.variables.0002").Site, Value: intValue(-2)},
		{Site: slotAt(t, d, "system.switches.0005").Site, Value: boolValue(true)},
		{Site: slotAt(t, d, "inventory.gold").Site, Value: intValue(99999)},
	}
	out, err := d.Encode(edits)
	if err != nil {
		t.Fatal(err)
	}
	before := flatten(d)
	re, err := Parse("x", out)
	if err != nil {
		t.Fatal(err)
	}
	after := flatten(re)
	diff := 0
	for i := range before {
		if before[i] != after[i] {
			diff++
		}
	}
	if diff != len(edits) {
		t.Errorf("%d values changed, want %d", diff, len(edits))
	}
	if got := slotAt(t, re, "actors.[2].name").Value.Str; got != "ロング・ネーム" {
		t.Errorf("name = %q", got)
	}
	// Encode does not change the document.
	again, err := d.Encode(nil)
	if err != nil || !bytes.Equal(again, data) {
		t.Errorf("Encode changed the document (%v)", err)
	}
	// Two edits of one site: the last one wins.
	site := slotAt(t, d, "title.hero_level").Site
	out, err = d.Encode([]model.Edit{{Site: site, Value: intValue(5)}, {Site: site, Value: intValue(6)}})
	if err != nil {
		t.Fatal(err)
	}
	if got := slotAt(t, mustParse(t, out), "title.hero_level").Value.Int.Int64(); got != 6 {
		t.Errorf("level = %d, want 6", got)
	}
}

func mustParse(t testing.TB, data []byte) model.Document {
	t.Helper()
	d, err := Parse("x", data)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestEncodeRefused(t *testing.T) {
	d := parseFixture(t, "rm2k_sjis.lsd")
	other := parseFixture(t, "rm2k_sjis.lsd")
	level := slotAt(t, d, "title.hero_level").Site
	big1 := new(big.Int).Lsh(big.NewInt(1), 40)
	for _, tc := range []struct {
		name  string
		edit  model.Edit
		route string
		tag   string
	}{
		{"no site", model.Edit{Value: intValue(1)}, "Encode->Document.plan.ForeignSite", tagForeignSite},
		{"site of another document", model.Edit{Site: slotAt(t, other, "title.hero_level").Site, Value: intValue(1)}, "Encode->Document.plan.ForeignSite", tagForeignSite},
		{"made-up site", model.Edit{Site: &model.Site{ID: 1, Ref: 5}, Value: intValue(1)}, "Encode->Document.plan.ForeignSite", tagForeignSite},
		{"copy of a site", model.Edit{Site: &model.Site{ID: level.ID, Ref: level.Ref}, Value: intValue(1)}, "Encode->Document.plan.ForeignSite", tagForeignSite},
		{"string for an int", model.Edit{Site: level, Value: strValue("1")}, "Encode->Document.plan.KindMismatch", tagKindMismatch},
		{"bool for an int", model.Edit{Site: level, Value: boolValue(true)}, "Encode->Document.plan.KindMismatch", tagKindMismatch},
		{"no value", model.Edit{Site: level}, "Encode->Document.plan.KindMismatch", tagKindMismatch},
		{"int for a string", model.Edit{Site: slotAt(t, d, "title.hero_name").Site, Value: intValue(1)}, "Encode->Document.plan.KindMismatch", tagKindMismatch},
		{"int for a float", model.Edit{Site: slotAt(t, d, "title.timestamp").Site, Value: intValue(1)}, "Encode->Document.plan.KindMismatch", tagKindMismatch},
		{"float for an int", model.Edit{Site: level, Value: floatValue(1)}, "Encode->Document.plan.KindMismatch", tagKindMismatch},
		{"missing number", model.Edit{Site: level, Value: &model.Value{Kind: model.KindInt}}, "Encode->Document.plan.InvalidEdit", tagInvalidEdit},
		{"int too big", model.Edit{Site: level, Value: intValue(math.MaxInt32 + 1)}, "Encode->Document.plan.OutOfRange", tagOutOfRange},
		{"int too small", model.Edit{Site: level, Value: intValue(math.MinInt32 - 1)}, "Encode->Document.plan.OutOfRange", tagOutOfRange},
		{"huge int", model.Edit{Site: level, Value: &model.Value{Kind: model.KindInt, Int: big1}}, "Encode->Document.plan.OutOfRange", tagOutOfRange},
		{"int8 too big", model.Edit{Site: slotAt(t, d, "system.transition_out").Site, Value: intValue(128)}, "Encode->Document.plan.OutOfRange", tagOutOfRange},
		{"int8 too small", model.Edit{Site: slotAt(t, d, "system.transition_out").Site, Value: intValue(-129)}, "Encode->Document.plan.OutOfRange", tagOutOfRange},
		{"flags too big", model.Edit{Site: slotAt(t, d, "pictures.[1].flags").Site, Value: intValue(256)}, "Encode->Document.plan.OutOfRange", tagOutOfRange},
		{"uint8 negative", model.Edit{Site: slotAt(t, d, "inventory.item_counts.[0]").Site, Value: intValue(-1)}, "Encode->Document.plan.OutOfRange", tagOutOfRange},
		{"int16 too big", model.Edit{Site: slotAt(t, d, "actors.[1].skills.[0]").Site, Value: intValue(32768)}, "Encode->Document.plan.OutOfRange", tagOutOfRange},
		{"variable too big", model.Edit{Site: slotAt(t, d, "system.variables.0001").Site, Value: intValue(1 << 31)}, "Encode->Document.plan.OutOfRange", tagOutOfRange},
		{"NaN", model.Edit{Site: slotAt(t, d, "title.timestamp").Site, Value: floatValue(math.NaN())}, "Encode->Document.plan.InvalidEdit", tagInvalidEdit},
		{"Inf", model.Edit{Site: slotAt(t, d, "title.timestamp").Site, Value: floatValue(math.Inf(1))}, "Encode->Document.plan.InvalidEdit", tagInvalidEdit},
		{"text with an accent in Shift-JIS", model.Edit{Site: slotAt(t, d, "title.hero_name").Site, Value: strValue("René")}, "Encode->Document.plan->codec.encode.TextNotStorable", tagTextNotStorable},
		{"text with an emoji in Shift-JIS", model.Edit{Site: slotAt(t, d, "actors.[1].name").Site, Value: strValue("A😀")}, "Encode->Document.plan->codec.encode.TextNotStorable", tagTextNotStorable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := d.Encode([]model.Edit{tc.edit})
			if err == nil || out != nil {
				t.Fatalf("Encode = %d bytes, %v", len(out), err)
			}
			e, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("not an *errors.Error: %T", err)
			}
			if e.Tag != tc.tag || !e.HasRoute(tc.route) {
				t.Errorf("tag %s, route %s; want %s, %s", e.Tag, e.Route(), tc.tag, tc.route)
			}
			if e.Message == "" {
				t.Error("no message")
			}
		})
	}
	// A refused edit in a list refuses all of it.
	if _, err := d.Encode([]model.Edit{
		{Site: level, Value: intValue(3)},
		{Site: level, Value: intValue(math.MaxInt32 + 1)},
	}); err == nil {
		t.Error("an edit list with a bad edit was accepted")
	}
}

func TestCheckText(t *testing.T) {
	for _, tc := range []struct {
		file, path, text string
		ok               bool
		message          string
	}{
		{"rm2k_sjis.lsd", "title.hero_name", "勇者アリス ABC ｱｲｳ １２３", true, ""},
		{"rm2k_sjis.lsd", "title.hero_name", "", true, ""},
		{"rm2k_sjis.lsd", "title.hero_name", "René", false, "Shift-JIS"},
		{"rm2k_sjis.lsd", "title.hero_name", "😀", false, "Shift-JIS"},
		{"rm2k_sjis.lsd", "title.hero_name", "Привет Ωmega", true, ""}, // JIS X 0208 has Greek and Cyrillic
		{"rm2k3_cp1252.lsd", "title.hero_name", "Zoë €", true, ""},
		{"rm2k3_cp1252.lsd", "title.hero_name", "勇者", false, "Windows-1252"},
		{"rm2k3_cp1252.lsd", "title.hero_name", "Łódź", false, "Windows-1252"},
		{"rm2k3_cp1251.lsd", "title.hero_name", "Привет, мир", true, ""},
		{"rm2k3_cp1251.lsd", "title.hero_name", "é", false, "Windows-1251"},
		{"rm2k3_easyrpg_cp1250.lsd", "title.hero_name", "Łódź", true, ""},
		{"rm2k3_easyrpg_cp1250.lsd", "title.hero_name", "Ёж", false, "Windows-1250"},
		{"rm2k3_cp1252.lsd", "title.hero_name", "a�b", false, "Windows-1252"},
	} {
		d := parseFixture(t, tc.file)
		site := slotAt(t, d, tc.path).Site
		err := site.CheckText(tc.text)
		if tc.ok != (err == nil) {
			t.Errorf("%s CheckText(%q) = %v", tc.file, tc.text, err)
			continue
		}
		if err != nil {
			e, ok := errors.FromError(err)
			if !ok || e.Tag != tagTextNotStorable || !e.HasRoute("codec.check->codec.encode.TextNotStorable") {
				t.Errorf("%s CheckText(%q): %v", tc.file, tc.text, err)
			} else if !strings.Contains(e.Message, tc.message) {
				t.Errorf("message %q does not name %q", e.Message, tc.message)
			}
		}
		// What CheckText accepts, Encode writes, and it reads back.
		if tc.ok {
			out, err := d.Encode([]model.Edit{{Site: site, Value: strValue(tc.text)}})
			if err != nil {
				t.Errorf("%s Encode(%q): %v", tc.file, tc.text, err)
				continue
			}
			if got := slotAt(t, mustParse(t, out), tc.path).Value.Str; got != tc.text {
				t.Errorf("%s: %q reads back as %q", tc.file, tc.text, got)
			}
		}
	}
}

func TestCheckTextNames(t *testing.T) {
	d := parseFixture(t, "rm2k_sjis.lsd")
	err := d.vars[0].Slot.Value.Children[1].Slot.Site.CheckText("é😀é ü ö ä ß ñ")
	if err == nil {
		t.Fatal("no error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "'é'") || !strings.Contains(msg, "'😀'") || strings.Count(msg, "'é'") != 1 || !strings.Contains(msg, "...") {
		t.Errorf("message = %q", msg)
	}
}

func TestEncodingFromTheSave(t *testing.T) {
	d := parseFixture(t, "rm2k3_easyrpg_cp1250.lsd")
	if d.codec.cp != 1250 || !strings.Contains(d.codec.reason, "easyrpg_data") {
		t.Errorf("codec = %d %q", d.codec.cp, d.codec.reason)
	}
	// A code page that is not supported is replaced by a guess, and the details say why.
	out, err := d.Encode([]model.Edit{{Site: slotAt(t, d, "easyrpg_data.codepage").Site, Value: intValue(437)}})
	if err != nil {
		t.Fatal(err)
	}
	re := mustParse(t, out).(*Document)
	if re.codec.cp == 437 || !strings.Contains(re.codec.reason, "code page 437") {
		t.Errorf("codec = %d %q", re.codec.cp, re.codec.reason)
	}
	// The file without the code page is guessed from the text.
	g := parseFixture(t, "rm2k3_cp1252.lsd")
	if !strings.Contains(g.codec.reason, "guessed") {
		t.Errorf("reason = %q", g.codec.reason)
	}
}

func TestGuessCodepage(t *testing.T) {
	b := func(hexs string) []byte {
		out, err := hex.DecodeString(strings.ReplaceAll(hexs, " ", ""))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, tc := range []struct {
		name string
		strs [][]byte
		want int
	}{
		{"nothing", nil, 1252},
		{"ASCII", [][]byte{[]byte("Hero"), []byte("Actor1")}, 1252},
		{"Shift-JIS kana", [][]byte{b("83 41 83 8a 83 58")}, 932}, // アリス
		{"Shift-JIS kana and ASCII names", [][]byte{[]byte("System"), b("83 7d 83 8a 83 41")}, 932},
		{"Shift-JIS kanji", [][]byte{b("97 45 8e d2")}, 932},                      // 勇者
		{"Shift-JIS kanji and ASCII", [][]byte{b("97 45 8e d2 20 41")}, 932},      // 勇者 A
		{"Shift-JIS kanji and kana", [][]byte{b("97 45 8e d2 82 cc 8c 95")}, 932}, // 勇者の剣
		{"Western accents", [][]byte{b("52 65 6e e9 65")}, 1252},                  // Renée
		{"Western accents 2", [][]byte{b("5a 6f eb"), b("43 68 6c 6f e9")}, 1252}, // Zoë, Chloé
		{"Western pair of accents", [][]byte{b("c9 e9 20 6f")}, 1252},             // Éé o
		{"Western doubled accent", [][]byte{b("52 e9 e9 71 75 69 6c 69 62 72 65")}, 1252},
		{"Western text", [][]byte{b("4c 27 e9 70 e9 65 20 64 75 20 68 e9 72 6f 73")}, 1252},
		{"Russian", [][]byte{b("c0 eb e8 f1 e0")}, 1251},                            // Алиса
		{"Russian sentence", [][]byte{b("cf f0 e8 e2 e5 f2 2c 20 ec e8 f0")}, 1251}, // Привет, мир
		{"Russian among ASCII names", [][]byte{[]byte("System"), []byte("Actor1"), b("c0 eb e8 f1 e0")}, 1251},
		{"Polish", [][]byte{b("5a 61 bf f3 b3 e6")}, 1250},           // Zażółć
		{"Korean", [][]byte{b("c7 d1 b1 db")}, 949},                  // 한글
		{"Chinese GBK", [][]byte{b("d3 c2 d5 df b4 f3 c2 bd")}, 936}, // 勇者大陆
		{"Chinese Big5", [][]byte{b("ab 69 aa cc")}, 950},            // 勇者
		{"single accent byte", [][]byte{b("e9")}, 1252},
		{"two high bytes", [][]byte{b("e9 e9")}, 1251}, // a one-word Russian name is as likely
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := guessCodepage(tc.strs)
			if got != tc.want {
				t.Errorf("guessCodepage = %d (%s), want %d", got, why, tc.want)
			}
			if why == "" {
				t.Error("no reason")
			}
		})
	}
}

func TestCodepages(t *testing.T) {
	for _, p := range codepages {
		c, ok := newCodec(p.cp, "x")
		if !ok || c.name != p.name {
			t.Fatalf("newCodec(%d) = %v, %v", p.cp, c, ok)
		}
		// ASCII text is the same in every code page.
		b, err := c.encode("Hero 123")
		if err != nil || string(b) != "Hero 123" || c.decode(b) != "Hero 123" {
			t.Errorf("%s: ASCII = %q, %v", p.name, b, err)
		}
	}
	if _, ok := newCodec(437, ""); ok {
		t.Error("code page 437 is not supported")
	}
	c, _ := newCodec(932, "")
	if got := c.decode(nil); got != "" {
		t.Errorf("decode(nil) = %q", got)
	}
	if got := c.decode([]byte{0x83}); got != "�" {
		t.Errorf("decode of a cut character = %q", got)
	}
}

func TestMalformed(t *testing.T) {
	good := readFixture(t, "rm2k3_cp1252.lsd")
	// A cut file: it opens with what could be read, and writes back as it was.
	for n := headerLen; n < len(good); n++ {
		cut := good[:n]
		d, err := Parse("x", cut)
		if err != nil {
			e, ok := errors.FromError(err)
			if !ok || e.Tag != tagCorrupt || !e.HasRoute("Parse.CorruptSave") {
				t.Fatalf("cut at %d: %v", n, err)
			}
			continue
		}
		out, err := d.Encode(nil)
		if err != nil || !bytes.Equal(out, cut) {
			t.Fatalf("cut at %d: round trip failed (%v)", n, err)
		}
		_ = d.Details()
	}
	// Garbage after the header.
	for _, tail := range [][]byte{{0x64}, {0x64, 0x05, 0x01}, {0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, {0x80}} {
		data := append([]byte("\x0bLcfSaveData"), tail...)
		_, err := Parse("x", data)
		e, ok := errors.FromError(err)
		if !ok || e.Tag != tagCorrupt || !e.HasRoute("Parse.CorruptSave") {
			t.Errorf("Parse(% x) = %v", data, err)
		}
	}
	// The header alone is a save with nothing in it.
	d, err := Parse("x", []byte("\x0bLcfSaveData"))
	if err != nil || len(d.Variables()) != 0 {
		t.Errorf("empty save: %v", err)
	}
	// Not a save at all.
	_, err = Parse("Save01.lsd", []byte("hello"))
	e, ok := errors.FromError(err)
	if !ok || e.Tag != tagNotLCF || !e.HasRoute("Parse.NotLcfSave") {
		t.Errorf("Parse(hello) = %v", err)
	}
	// Bytes after the 0 that ends the chunks are kept.
	data := append(slices.Clone(good), 0, 0, 1, 2, 3)
	d, err = Parse("x", data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Encode(nil)
	if err != nil || !bytes.Equal(out, data) {
		t.Errorf("trailing bytes: %v", err)
	}
	if detail(d, "Unread bytes") != "5" {
		t.Errorf("Unread bytes = %s", detail(d, "Unread bytes"))
	}
}

// chunkB makes a chunk with the shortest BER numbers.
func chunkB(id uint32, data ...byte) []byte {
	out := appendBER(nil, id)
	out = appendBER(out, uint32(len(data)))
	return append(out, data...)
}

func TestOddButValidFiles(t *testing.T) {
	header := []byte("\x0bLcfSaveData")
	cat := func(parts ...[]byte) []byte { return slices.Concat(parts...) }
	for _, tc := range []struct {
		name string
		data []byte
		// check looks at the parsed document.
		check func(t *testing.T, d *Document)
	}{
		{
			"a number with a longer BER than needed",
			// title { hero_level = 12 as 0x80 0x0C }
			cat(header, chunkB(0x64, cat(chunkB(0x0C, 0x80, 0x0C), chunkB(0x0D, 0x05), []byte{0})...)),
			func(t *testing.T, d *Document) {
				if got := slotAt(t, d, "title.hero_level").Value.Int.Int64(); got != 12 {
					t.Errorf("level = %d", got)
				}
			},
		},
		{
			"a number of size 0",
			cat(header, chunkB(0x64, cat(chunkB(0x0C), chunkB(0x0D, 0x05), []byte{0})...)),
			func(t *testing.T, d *Document) {
				if got := slotAt(t, d, "title.hero_level").Value.Int.Int64(); got != 0 {
					t.Errorf("level = %d", got)
				}
			},
		},
		{
			"a number too long for its chunk is bytes",
			cat(header, chunkB(0x64, cat(chunkB(0x0C, 0x81, 0x80, 0x80, 0x80, 0x80, 0x00), []byte{0})...)),
			func(t *testing.T, d *Document) {
				if v := slotAt(t, d, "title.hero_level").Value; v.Kind != model.KindBytes {
					t.Errorf("kind = %v", v.Kind)
				}
			},
		},
		{
			"a bool of 2 bytes is bytes",
			cat(header, chunkB(0x65, cat(chunkB(0x35, 0x01, 0x00), []byte{0})...)),
			func(t *testing.T, d *Document) {
				if v := slotAt(t, d, "system.face_right").Value; v.Kind != model.KindBytes {
					t.Errorf("kind = %v", v.Kind)
				}
			},
		},
		{
			"a struct that does not fit is bytes",
			cat(header, chunkB(0x64, 0x0C, 0x09, 0x01)), // a chunk of 9 bytes in a struct of 3
			func(t *testing.T, d *Document) {
				if v := slotAt(t, d, "title").Value; v.Kind != model.KindBytes || v.Type != "Bytes" {
					t.Errorf("title = %v %s", v.Kind, v.Type)
				}
			},
		},
		{
			"an array with fewer elements than its count is bytes",
			cat(header, chunkB(0x6C, 0x03, 0x01, 0x00)), // 3 actors, 1 id, 1 terminator
			func(t *testing.T, d *Document) {
				if v := slotAt(t, d, "actors").Value; v.Kind != model.KindBytes {
					t.Errorf("actors = %v", v.Kind)
				}
			},
		},
		{
			"an odd Int16 vector is bytes",
			cat(header, chunkB(0x6C, cat([]byte{1, 1}, chunkB(0x34, 1, 2, 3), []byte{0})...)),
			func(t *testing.T, d *Document) {
				if v := slotAt(t, d, "actors.[1].skills").Value; v.Kind != model.KindBytes {
					t.Errorf("skills = %v", v.Kind)
				}
			},
		},
		{
			"two chunks with the same id",
			cat(header, chunkB(0x66, 0), chunkB(0x66, 0)),
			func(t *testing.T, d *Document) {
				vs := d.Variables()
				if len(vs) != 2 || vs[0].Name != "screen" || vs[1].Name != "screen #2" {
					t.Errorf("variables = %v", vs)
				}
			},
		},
		{
			"an empty chunk of a struct",
			cat(header, chunkB(0x70)),
			func(t *testing.T, d *Document) {
				if v := slotAt(t, d, "panorama").Value; v.Kind != model.KindObject || len(v.Children) != 0 {
					t.Errorf("panorama = %v", v.Repr())
				}
			},
		},
		{
			"unknown chunk inside a struct inside an array",
			cat(header, chunkB(0x6C, cat([]byte{1, 7}, chunkB(0x99, 1, 2), chunkB(0x0C, 0x03), []byte{0})...)),
			func(t *testing.T, d *Document) {
				if v := slotAt(t, d, "actors.[7].unknown_0x99").Value; v.Kind != model.KindBytes {
					t.Errorf("unknown = %v", v.Kind)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := Parse("x", tc.data)
			if err != nil {
				t.Fatal(err)
			}
			doc := d.(*Document)
			tc.check(t, doc)
			out, err := doc.Encode(nil)
			if err != nil || !bytes.Equal(out, tc.data) {
				t.Errorf("round trip: %v", err)
			}
		})
	}
	// Editing a field keeps the long BER number next to it.
	data := cat(header, chunkB(0x64, cat(chunkB(0x0C, 0x80, 0x0C), chunkB(0x0D, 0x05), []byte{0})...))
	d := mustParse(t, data).(*Document)
	out, err := d.Encode([]model.Edit{{Site: slotAt(t, d, "title.hero_hp").Site, Value: intValue(200)}})
	if err != nil {
		t.Fatal(err)
	}
	want := cat(header, chunkB(0x64, cat(chunkB(0x0C, 0x80, 0x0C), chunkB(0x0D, 0x81, 0x48), []byte{0})...))
	if !bytes.Equal(out, want) {
		t.Errorf("got % x, want % x", out, want)
	}
	// Editing a field of size 0 gives it a size.
	data = cat(header, chunkB(0x64, cat(chunkB(0x0C), []byte{0})...))
	d = mustParse(t, data).(*Document)
	out, err = d.Encode([]model.Edit{{Site: slotAt(t, d, "title.hero_level").Site, Value: intValue(3)}})
	if err != nil || !bytes.Equal(out, cat(header, chunkB(0x64, cat(chunkB(0x0C, 3), []byte{0})...))) {
		t.Errorf("got % x (%v)", out, err)
	}
	// A struct of 124 bytes grows to 128: its size takes a second byte.
	data = cat(header, chunkB(0x64, cat(chunkB(0x0B, bytes.Repeat([]byte{'a'}, 121)...), []byte{0})...))
	d = mustParse(t, data).(*Document)
	out, err = d.Encode([]model.Edit{{Site: slotAt(t, d, "title.hero_name").Site, Value: strValue(strings.Repeat("a", 125))}})
	if err != nil {
		t.Fatal(err)
	}
	want = cat(header, []byte{0x64, 0x81, 0x00}, chunkB(0x0B, bytes.Repeat([]byte{'a'}, 125)...), []byte{0})
	if !bytes.Equal(out, want) {
		t.Errorf("got % x", out)
	}
	// A string of 127 bytes grows to 128: its size takes a second byte, and so does its struct's.
	out, err = d.Encode([]model.Edit{{Site: slotAt(t, d, "title.hero_name").Site, Value: strValue(strings.Repeat("a", 128))}})
	if err != nil {
		t.Fatal(err)
	}
	want = cat(header, []byte{0x64, 0x81, 0x04, 0x0B, 0x81, 0x00}, bytes.Repeat([]byte{'a'}, 128), []byte{0})
	if !bytes.Equal(out, want) {
		t.Errorf("got % x", out)
	}
	if got := slotAt(t, mustParse(t, out), "title.hero_name").Value.Str; got != strings.Repeat("a", 128) {
		t.Errorf("name has %d letters", len(got))
	}
	// And back: a struct that shrinks under 128 gets a one byte size again.
	big := mustParse(t, out).(*Document)
	back, err := big.Encode([]model.Edit{{Site: slotAt(t, big, "title.hero_name").Site, Value: strValue("a")}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, cat(header, chunkB(0x64, cat(chunkB(0x0B, 'a'), []byte{0})...))) {
		t.Errorf("got % x", back)
	}
}

func TestDetailsOfOdd(t *testing.T) {
	// The time stamp is only shown when it can be one.
	d := mustParse(t, []byte("\x0bLcfSaveData"))
	if detail(d, "Saved") != "<none>" || detail(d, "Hero") != "<none>" || detail(d, "Game") != "RPG Maker 2000" {
		t.Errorf("details = %v", d.Details())
	}
	if detail(d, "Text encoding") != "Windows-1252 (Western European)" {
		t.Errorf("encoding = %v", detail(d, "Text encoding"))
	}
}

func TestDamagedFilesNeverBreak(t *testing.T) {
	for _, fx := range fixtures {
		good := readFixture(t, fx.file)
		for i := range good {
			for _, mask := range []byte{0xFF, 0x80, 0x01} {
				data := slices.Clone(good)
				data[i] ^= mask
				d, err := Parse("x", data)
				if err != nil {
					continue
				}
				out, err := d.Encode(nil)
				if err != nil || !bytes.Equal(out, data) {
					t.Fatalf("%s: byte %d ^ %#x: round trip failed (%v)", fx.file, i, mask, err)
				}
				// Whatever the file says, every value can be shown and every site is its own.
				_ = flatten(d)
				_ = d.Details()
			}
		}
	}
}

func TestUnsignedFields(t *testing.T) {
	header := []byte("\x0bLcfSaveData")
	// map_info { events [ 1: { easyrpg_clone_map_id = 0xFFFFFFFE (4 bytes), keyinput_variable... } ] }
	event := slices.Concat(chunkB(0xCA, 0xFE, 0xFF, 0xFF, 0xFF), chunkB(0x16, 200), []byte{0})
	events := slices.Concat([]byte{1, 1}, event)
	data := slices.Concat(header, chunkB(0x6F, slices.Concat(chunkB(0x0B, events...), []byte{0})...))
	d := mustParse(t, data)
	clone := slotAt(t, d, "map_info.events.[1].easyrpg_clone_map_id")
	if clone.Value.Int.Int64() != 0xFFFFFFFE || clone.Value.Type != "UInt32" || clone.Site.IntMin.Sign() != 0 {
		t.Errorf("clone id = %v (%s)", clone.Value.Repr(), clone.Value.Type)
	}
	out, err := d.Encode([]model.Edit{{Site: clone.Site, Value: intValue(5)}})
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Concat(header, chunkB(0x6F, slices.Concat(chunkB(0x0B, slices.Concat([]byte{1, 1},
		chunkB(0xCA, 5, 0, 0, 0), chunkB(0x16, 200), []byte{0})...), []byte{0})...))
	if !bytes.Equal(out, want) {
		t.Errorf("got % x\nwant % x", out, want)
	}
	for _, n := range []int64{-1, 1 << 32} {
		if _, err := d.Encode([]model.Edit{{Site: clone.Site, Value: intValue(n)}}); err == nil {
			t.Errorf("%d was accepted", n)
		}
	}
}
