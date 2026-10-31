package gvas

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

var fixtures = []string{"ue4.sav", "ue5.sav", "ue54.sav"}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// line describes one value in the same words as the .expected files that gen.py writes.
func line(v *model.Value) string {
	switch v.Kind {
	case model.KindBool:
		return "bool " + strconv.FormatBool(v.Bool)
	case model.KindInt:
		return "int " + v.Int.String()
	case model.KindFloat:
		return "float " + model.FormatFloat(v.Float, v.Float32)
	case model.KindString:
		return "str " + jsonString(v.Str)
	case model.KindBytes:
		return "bytes " + string(hexOf(v.Bytes))
	case model.KindList:
		return "list " + strconv.Itoa(len(v.Children))
	case model.KindMap:
		return "map " + strconv.Itoa(len(v.Children))
	case model.KindObject:
		return "object " + v.Type
	}
	return "?" + v.Kind.String()
}

func hexOf(b []byte) []byte {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return out
}

type entry struct {
	path string
	slot model.Slot
}

func walk(path string, s model.Slot, out *[]entry) {
	*out = append(*out, entry{path, s})
	for _, c := range s.Value.Children {
		walk(path+"/"+c.Label, c.Slot, out)
	}
}

func entries(d model.Document) []entry {
	var out []entry
	for _, v := range d.Variables() {
		walk(v.Name, v.Slot, &out)
	}
	return out
}

func dump(d model.Document) []string {
	var out []string
	for _, e := range entries(d) {
		out = append(out, e.path+"\t"+line(e.slot.Value))
	}
	return out
}

func expected(t *testing.T, name string) []string {
	t.Helper()
	raw := readFixture(t, name+".expected")
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

func diffLines(t *testing.T, got, want []string) {
	t.Helper()
	for i := 0; i < len(got) || i < len(want); i++ {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			t.Errorf("line %d:\n got  %q\n want %q", i, g, w)
			return
		}
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"gvas", []byte("GVAS\x02\x00\x00\x00"), true},
		{"fixture", readFixture(t, "ue4.sav"), true},
		{"short", []byte("GVA"), false},
		{"empty", nil, false},
		{"renpy", []byte("PK\x03\x04"), false},
		{"lowercase", []byte("gvas...."), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Format.Detect("x.sav", tt.data); got != tt.want {
				t.Errorf("Detect = %v, want %v", got, tt.want)
			}
		})
	}
	if Format.Name != "Unreal Engine" {
		t.Errorf("name = %q", Format.Name)
	}
}

func TestParseFixtures(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			d, err := Format.Parse(name, readFixture(t, name))
			if err != nil {
				t.Fatal(err)
			}
			diffLines(t, dump(d), expected(t, name))
		})
	}
}

func TestDetails(t *testing.T) {
	tests := []struct {
		file   string
		engine string
		pkg    string
		layout string
		class  string
		custom string
	}{
		{"ue4.sav", "4.27.2", "UE4 522", "classic property tags", "/Game/Blueprints/BP_Save.BP_Save_C", "2"},
		{"ue5.sav", "5.1.1", "UE4 522, UE5 1008", "classic property tags", "/Game/BP_Save5.BP_Save5_C", "2"},
		{"ue54.sav", "5.4.2", "UE4 522, UE5 1012", "property tags with type names (Unreal Engine 5.4 and later)", "/Game/BP_Save54.BP_Save54_C", "2"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			d, err := Parse(tt.file, readFixture(t, tt.file))
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, det := range d.Details() {
				got[det.Label] = det.Text
			}
			want := map[string]string{"Engine": tt.engine, "Package version": tt.pkg, "Layout": tt.layout,
				"Save game class": tt.class, "Custom versions": tt.custom}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
			if _, ok := got["Trailing bytes"]; ok {
				t.Errorf("a normal trailer is listed as trailing bytes")
			}
		})
	}
}

func TestTrailingBytesKept(t *testing.T) {
	data := append(readFixture(t, "ue4.sav"), 1, 2, 3)
	d, err := Parse("x", data)
	if err != nil {
		t.Fatal(err)
	}
	got := false
	for _, det := range d.Details() {
		if det.Label == "Trailing bytes" {
			got = true
		}
	}
	if !got {
		t.Error("extra trailing bytes are not listed")
	}
	out, err := d.Encode(nil)
	if err != nil || !bytes.Equal(out, data) {
		t.Errorf("round trip changed the file: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			data := readFixture(t, name)
			d, err := Parse(name, data)
			if err != nil {
				t.Fatal(err)
			}
			out, err := d.Encode(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out, data) {
				t.Error("Encode with no edits changed the bytes")
			}
		})
	}
}

// newValue makes the value of an edit from text, in the kind of the old value.
func newValue(t *testing.T, old *model.Value, text string) *model.Value {
	t.Helper()
	v := *old
	v.Children = nil
	switch old.Kind {
	case model.KindInt:
		n, ok := new(big.Int).SetString(text, 10)
		if !ok {
			t.Fatalf("bad int %q", text)
		}
		v.Int = n
	case model.KindFloat:
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			t.Fatal(err)
		}
		v.Float = f
	case model.KindBool:
		v.Bool = text == "true"
	case model.KindString:
		v.Str = text
	default:
		t.Fatalf("cannot edit kind %v", old.Kind)
	}
	return &v
}

func find(t *testing.T, d model.Document, path string) entry {
	t.Helper()
	for _, e := range entries(d) {
		if e.path == path {
			return e
		}
	}
	t.Fatalf("no value at %q", path)
	return entry{}
}

func TestEdits(t *testing.T) {
	tests := []struct {
		file, path, text string
		want             string // the new line of the value
	}{
		{"ue4.sav", "Level", "-5", "int -5"},
		{"ue4.sav", "Level", "2147483647", "int 2147483647"},
		{"ue4.sav", "Gold", "-9223372036854775808", "int -9223372036854775808"},
		{"ue4.sav", "Small", "127", "int 127"},
		{"ue4.sav", "Short", "-32768", "int -32768"},
		{"ue4.sav", "Port", "0", "int 0"},
		{"ue4.sav", "Flags", "4294967295", "int 4294967295"},
		{"ue4.sav", "Big", "18446744073709551615", "int 18446744073709551615"},
		{"ue4.sav", "Raw", "255", "int 255"},
		{"ue4.sav", "bAlive", "false", "bool false"},
		{"ue4.sav", "bMuted", "true", "bool true"},
		{"ue4.sav", "Speed", "-3.5", "float -3.5"},
		{"ue4.sav", "Precise", "1e100", "float 1e+100"},
		{"ue4.sav", "PlayerName", "Alexander the Great", "str \"Alexander the Great\""},
		{"ue4.sav", "PlayerName", "", "str \"\""},
		{"ue4.sav", "PlayerName", "\u65e5\u672c", "str \"\u65e5\u672c\""},
		{"ue4.sav", "Empty", "now set", "str \"now set\""},
		{"ue4.sav", "Greeting", "plain", "str \"plain\""},
		{"ue4.sav", "Greeting", "\U0001F600 smile", "str \"\U0001F600 smile\""},
		{"ue4.sav", "NickName", "Robert", "str \"Robert\""},
		{"ue4.sav", "Color", "EColor::Blue", "str \"EColor::Blue\""},
		{"ue4.sav", "Mode", "EMode::Easy", "str \"EMode::Easy\""},
		{"ue4.sav", "Title", "Goodbye, cruel world", "str \"Goodbye, cruel world\""},
		{"ue4.sav", "Plain", "X", "str \"X\""},
		{"ue4.sav", "Target", "/Game/Maps/Level2.Level2", "str \"/Game/Maps/Level2.Level2\""},
		{"ue4.sav", "Soft/asset", "/Game/Items/Axe.Axe", "str \"/Game/Items/Axe.Axe\""},
		{"ue4.sav", "Soft/subpath", "Sub", "str \"Sub\""},
		{"ue4.sav", "Pos/x", "100.25", "float 100.25"},
		{"ue4.sav", "Rot/yaw", "-45", "float -45.0"},
		{"ue4.sav", "Q/w", "0.5", "float 0.5"},
		{"ue4.sav", "Pos2/y", "8", "float 8.0"},
		{"ue4.sav", "Tint/g", "0.125", "float 0.125"},
		{"ue4.sav", "Col/r", "255", "int 255"},
		{"ue4.sav", "Col/b", "0", "int 0"},
		{"ue4.sav", "Id", "0123456789abcdef0123456789ABCDEF", "str \"0123456789ABCDEF0123456789ABCDEF\""},
		{"ue4.sav", "When", "1", "int 1"},
		{"ue4.sav", "Dur", "-1", "int -1"},
		{"ue4.sav", "Cell/y", "40", "int 40"},
		{"ue4.sav", "Inventory/Gold", "99", "int 99"},
		{"ue4.sav", "Inventory/Items/[0]", "A much longer item name", "str \"A much longer item name\""},
		{"ue4.sav", "Inventory/Owner/Name", "Bartholomew", "str \"Bartholomew\""},
		{"ue4.sav", "Inventory/Owner/Pos/z", "9", "float 9.0"},
		{"ue4.sav", "Arr[1]", "22", "int 22"},
		{"ue4.sav", "Scores/[1]", "-20", "int -20"},
		{"ue4.sav", "Names/[1]", "\u00fcberlong", "str \"\u00fcberlong\""},
		{"ue4.sav", "Bytes/[2]", "250", "int 250"},
		{"ue4.sav", "Colors/[0]", "EColor::Green", "str \"EColor::Green\""},
		{"ue4.sav", "Switches/[0]", "false", "bool false"},
		{"ue4.sav", "Floats/[1]", "7.5", "float 7.5"},
		{"ue4.sav", "Objs/[1]", "/Game/C.C", "str \"/Game/C.C\""},
		{"ue4.sav", "Points/[0]/Name", "first point of the day", "str \"first point of the day\""},
		{"ue4.sav", "Points/[1]/X", "20", "int 20"},
		{"ue4.sav", "Positions/[1]/z", "60.5", "float 60.5"},
		{"ue4.sav", "Seen/[0]", "100", "int 100"},
		{"ue4.sav", "Tags/[1]", "yes indeed", "str \"yes indeed\""},
		{"ue4.sav", "Counts/apples", "30", "int 30"},
		{"ue4.sav", "Names2/2", "dos", "str \"dos\""},
		{"ue4.sav", "ById/FFEEDDCCBBAA99887766554433221100", "5", "int 5"},
		{"ue4.sav", "Slots/a/Name", "a longer name", "str \"a longer name\""},
		{"ue4.sav", "Slots/b/Count", "300", "int 300"},
		{"ue4.sav", "StructKeys/[0]/key/A", "11", "int 11"},
		{"ue4.sav", "StructKeys/[1]/value", "21", "int 21"},
		{"ue4.sav", "EnumMap/EColor::Blue", "9", "int 9"},
		{"ue5.sav", "Pos/x", "1e300", "float 1e+300"},
		{"ue5.sav", "Rot/roll", "0.1", "float 0.1"},
		{"ue5.sav", "Positions/[0]/y", "-2.5", "float -2.5"},
		{"ue5.sav", "Points/[0]/X", "5", "int 5"},
		{"ue5.sav", "PlayerName", "plain ascii", "str \"plain ascii\""},
		{"ue5.sav", "Soft/package", "/Game/Other", "str \"/Game/Other\""},
		{"ue5.sav", "Soft/subpath", "S", "str \"S\""},
		{"ue5.sav", "Title", "Bye", "str \"Bye\""},
		{"ue5.sav", "Color", "EColor::Red", "str \"EColor::Red\""},
		{"ue5.sav", "Counts/a", "5", "int 5"},
		{"ue5.sav", "Seen/[1]", "-6", "int -6"},
		{"ue54.sav", "bAlive", "false", "bool false"},
		{"ue54.sav", "bMuted", "true", "bool true"},
		{"ue54.sav", "Level", "10", "int 10"},
		{"ue54.sav", "PlayerName", "A longer hero name", "str \"A longer hero name\""},
		{"ue54.sav", "Raw", "8", "int 8"},
		{"ue54.sav", "Color", "EColor::Blue", "str \"EColor::Blue\""},
		{"ue54.sav", "Mode", "EMode::Easy", "str \"EMode::Easy\""},
		{"ue54.sav", "Pos/z", "3.5", "float 3.5"},
		{"ue54.sav", "Col/g", "1", "int 1"},
		{"ue54.sav", "Inventory/Name", "Bartholomew", "str \"Bartholomew\""},
		{"ue54.sav", "Scores/[0]", "11", "int 11"},
		{"ue54.sav", "Points/[1]/X", "22", "int 22"},
		{"ue54.sav", "Positions/[0]/x", "-1.5", "float -1.5"},
		{"ue54.sav", "Slots/a/Count", "4", "int 4"},
		{"ue54.sav", "Keys/[0]/key/A", "12", "int 12"},
		{"ue54.sav", "Keys/[0]/value", "11", "int 11"},
		{"ue54.sav", "Tags/[0]", "a much longer tag", "str \"a much longer tag\""},
		{"ue54.sav", "Arr[1]", "3", "int 3"},
		{"ue54.sav", "Speed", "0.75", "float 0.75"},
	}
	for _, tt := range tests {
		t.Run(tt.file+"/"+tt.path+"="+tt.text, func(t *testing.T) {
			data := readFixture(t, tt.file)
			d, err := Parse(tt.file, data)
			if err != nil {
				t.Fatal(err)
			}
			e := find(t, d, tt.path)
			if e.slot.Site == nil {
				t.Fatalf("%s has no site", tt.path)
			}
			out, err := d.Encode([]model.Edit{{Site: e.slot.Site, Value: newValue(t, e.slot.Value, tt.text)}})
			if err != nil {
				t.Fatal(err)
			}
			again, err := d.Encode([]model.Edit{{Site: e.slot.Site, Value: newValue(t, e.slot.Value, tt.text)}})
			if err != nil || !bytes.Equal(out, again) {
				t.Fatal("Encode changed the document or is not repeatable")
			}
			d2, err := Parse(tt.file, out)
			if err != nil {
				t.Fatal(err)
			}
			want := expected(t, tt.file)
			found := false
			for i, l := range want {
				if strings.HasPrefix(l, tt.path+"\t") {
					want[i] = tt.path + "\t" + tt.want
					found = true
				}
			}
			if !found {
				t.Fatalf("%s is not in the expected file", tt.path)
			}
			diffLines(t, dump(d2), want)
			// The original document still holds the old value.
			diffLines(t, dump(d), expected(t, tt.file))
			if len(out) == len(data) && bytes.Equal(out, data) {
				t.Error("the edit changed nothing")
			}
		})
	}
}

// TestSizesFollowEdits checks the size fields themselves, not only that the file reads back.
func TestSizesFollowEdits(t *testing.T) {
	data := readFixture(t, "ue4.sav")
	d, err := Parse("ue4.sav", data)
	if err != nil {
		t.Fatal(err)
	}
	e := find(t, d, "Inventory/Items/[0]")
	v := *e.slot.Value
	v.Str = "Sword of a Thousand Truths"
	out, err := d.Encode([]model.Edit{{Site: e.slot.Site, Value: &v}})
	if err != nil {
		t.Fatal(err)
	}
	grow := len(out) - len(data)
	if grow != len(v.Str)-len("Sword") {
		t.Fatalf("file grew by %d", grow)
	}
	// Both Items and Inventory contain the string, so both sizes grow by the same amount; the
	// size field of every other property is unchanged.
	oldSizes, newSizes := sizeFields(t, data), sizeFields(t, out)
	changed := 0
	for name, o := range oldSizes {
		n := newSizes[name]
		if n != o {
			changed++
			if n-o != uint32(grow) {
				t.Errorf("%s grew by %d, want %d", name, n-o, grow)
			}
		}
	}
	if changed != 2 {
		t.Errorf("%d sizes changed, want 2 (Inventory, Items)", changed)
	}
}

// sizeFields finds the size field of the top-level properties and of Items by their names.
func sizeFields(t *testing.T, data []byte) map[string]uint32 {
	t.Helper()
	out := map[string]uint32{}
	for _, name := range []string{"Inventory", "Items", "Gold", "Scores", "Points", "Pos"} {
		pat := append(binary.LittleEndian.AppendUint32(nil, uint32(len(name)+1)), name...)
		pat = append(pat, 0)
		i := bytes.Index(data, pat)
		if i < 0 {
			t.Fatalf("no property %s", name)
		}
		i += len(pat)
		n := int(binary.LittleEndian.Uint32(data[i:]))
		i += 4 + n // type
		out[name] = binary.LittleEndian.Uint32(data[i:])
	}
	return out
}

func TestArrayInnerTagSizeFollowsEdit(t *testing.T) {
	data := readFixture(t, "ue4.sav")
	d, err := Parse("x", data)
	if err != nil {
		t.Fatal(err)
	}
	e := find(t, d, "Points/[0]/Name")
	v := *e.slot.Value
	v.Str = "0123456789"
	out, err := d.Encode([]model.Edit{{Site: e.slot.Site, Value: &v}})
	if err != nil {
		t.Fatal(err)
	}
	// "one" (4 bytes) becomes 11 bytes: +7. The outer size and the inner tag size both grow.
	for _, c := range []struct{ name string }{{"Points"}} {
		old, now := sizeFields(t, data)[c.name], sizeFields(t, out)[c.name]
		if now-old != 7 {
			t.Errorf("%s grew by %d, want 7", c.name, now-old)
		}
	}
	// The inner tag repeats the name and holds the size of the elements.
	inner := append(binary.LittleEndian.AppendUint32(nil, 7), "Points\x00"...)
	inner = append(inner, binary.LittleEndian.AppendUint32(nil, 15)...)
	inner = append(inner, "StructProperty\x00"...)
	oi := bytes.Index(data, inner)
	ni := bytes.Index(out, inner)
	if oi < 0 || ni < 0 {
		t.Fatal("inner tag not found")
	}
	after := ni + len(inner)
	oldInner := binary.LittleEndian.Uint32(data[oi+len(inner):])
	newInner := binary.LittleEndian.Uint32(out[after:])
	if newInner-oldInner != 7 {
		t.Errorf("inner tag grew by %d, want 7", newInner-oldInner)
	}
}

func TestMultipleEdits(t *testing.T) {
	d, err := Parse("x", readFixture(t, "ue4.sav"))
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := find(t, d, "PlayerName"), find(t, d, "Inventory/Owner/Name"), find(t, d, "Level")
	out, err := d.Encode([]model.Edit{
		{Site: a.slot.Site, Value: newValue(t, a.slot.Value, "\u65e5\u672c\u8a9e")},
		{Site: b.slot.Site, Value: newValue(t, b.slot.Value, "")},
		{Site: c.slot.Site, Value: newValue(t, c.slot.Value, "1")},
	})
	if err != nil {
		t.Fatal(err)
	}
	d2, err := Parse("x", out)
	if err != nil {
		t.Fatal(err)
	}
	want := expected(t, "ue4.sav")
	for i, l := range want {
		switch {
		case strings.HasPrefix(l, "PlayerName\t"):
			want[i] = "PlayerName\tstr \"\u65e5\u672c\u8a9e\""
		case strings.HasPrefix(l, "Inventory/Owner/Name\t"):
			want[i] = "Inventory/Owner/Name\tstr \"\""
		case strings.HasPrefix(l, "Level\t"):
			want[i] = "Level\tint 1"
		}
	}
	diffLines(t, dump(d2), want)
}

func TestEncodingOfStrings(t *testing.T) {
	tests := []struct {
		s    string
		want []byte
	}{
		{"", []byte{0, 0, 0, 0}},
		{"ab", []byte{3, 0, 0, 0, 'a', 'b', 0}},
		{"\u00e9", []byte{0xfe, 0xff, 0xff, 0xff, 0xe9, 0, 0, 0}},
		{"\U0001F600", []byte{0xfd, 0xff, 0xff, 0xff, 0x3d, 0xd8, 0x00, 0xde, 0, 0}},
	}
	for _, tt := range tests {
		if got := encodeFString(tt.s); !bytes.Equal(got, tt.want) {
			t.Errorf("encodeFString(%q) = % x, want % x", tt.s, got, tt.want)
		}
		r := newReader(encodeFString(tt.s), nil)
		got, err := r.fstring()
		if err != nil || got != tt.s {
			t.Errorf("fstring round trip of %q = %q, %v", tt.s, got, err)
		}
	}
	// A string without the null terminator and a Latin-1 string are read as they are.
	r := newReader([]byte{2, 0, 0, 0, 'a', 'b'}, nil)
	if s, err := r.fstring(); err != nil || s != "ab" {
		t.Errorf("unterminated = %q, %v", s, err)
	}
	r = newReader([]byte{2, 0, 0, 0, 0xe9, 0}, nil)
	if s, err := r.fstring(); err != nil || s != "\u00e9" {
		t.Errorf("latin-1 = %q, %v", s, err)
	}
}

func routeOf(t *testing.T, err error) *errors.Error {
	t.Helper()
	e, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("not an *errors.Error: %T %v", err, err)
	}
	return e
}

func TestRefusedEdits(t *testing.T) {
	d, err := Parse("x", readFixture(t, "ue4.sav"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := Parse("x", readFixture(t, "ue4.sav"))
	if err != nil {
		t.Fatal(err)
	}
	level, small, port := find(t, d, "Level"), find(t, d, "Small"), find(t, d, "Port")
	speed, name, id := find(t, d, "Speed"), find(t, d, "PlayerName"), find(t, d, "Id")
	foreign := find(t, other, "Level")
	str := func(s string) *model.Value { return &model.Value{Kind: model.KindString, Str: s} }
	num := func(n int64) *model.Value { return &model.Value{Kind: model.KindInt, Int: big.NewInt(n)} }
	tests := []struct {
		name  string
		edit  model.Edit
		route string
	}{
		{"no site", model.Edit{Value: num(1)}, "Document.Encode->Document.leafOf.GvasUnknownSite"},
		{"no value", model.Edit{Site: level.slot.Site}, "Document.Encode->Document.leafOf.GvasUnknownSite"},
		{"other document", model.Edit{Site: foreign.slot.Site, Value: num(1)}, "Document.Encode->Document.leafOf.GvasUnknownSite"},
		{"foreign ref", model.Edit{Site: &model.Site{ID: 1, Ref: 5}, Value: num(1)}, "Document.leafOf.GvasUnknownSite"},
		{"wrong kind", model.Edit{Site: level.slot.Site, Value: str("5")}, "Document.leafOf.GvasWrongKind"},
		{"float for int", model.Edit{Site: level.slot.Site, Value: &model.Value{Kind: model.KindFloat}}, "GvasWrongKind"},
		{"nil int", model.Edit{Site: level.slot.Site, Value: &model.Value{Kind: model.KindInt}}, "Document.leafOf.GvasBadValue"},
		{"int32 high", model.Edit{Site: level.slot.Site, Value: num(1 << 31)}, "Document.leafOf.GvasOutOfRange"},
		{"int32 low", model.Edit{Site: level.slot.Site, Value: num(-1<<31 - 1)}, "GvasOutOfRange"},
		{"int8 high", model.Edit{Site: small.slot.Site, Value: num(128)}, "GvasOutOfRange"},
		{"uint16 negative", model.Edit{Site: port.slot.Site, Value: num(-1)}, "GvasOutOfRange"},
		{"uint16 high", model.Edit{Site: port.slot.Site, Value: num(65536)}, "GvasOutOfRange"},
		{"float32 overflow", model.Edit{Site: speed.slot.Site, Value: &model.Value{Kind: model.KindFloat, Float: 1e300}},
			"leaf.encode.GvasOutOfRange"},
		{"null in text", model.Edit{Site: name.slot.Site, Value: str("a\x00b")}, "Document.leafOf->checkString.GvasBadText"},
		{"invalid utf-8", model.Edit{Site: name.slot.Site, Value: str("a\xffb")}, "checkString.GvasBadText"},
		{"short guid", model.Edit{Site: id.slot.Site, Value: str("1234")}, "Document.leafOf->checkGuid.GvasBadText"},
		{"non-hex guid", model.Edit{Site: id.slot.Site, Value: str("zz112233445566778899AABBCCDDEEFF")}, "checkGuid.GvasBadText"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := d.Encode([]model.Edit{tt.edit})
			if err == nil {
				t.Fatalf("Encode accepted the edit and wrote %d bytes", len(out))
			}
			e := routeOf(t, err)
			if !e.HasRoute(tt.route) {
				t.Errorf("route = %s, want it to contain %s", e.Route(), tt.route)
			}
		})
	}
	// A float32 edit that rounds to a finite number is accepted.
	if _, err := d.Encode([]model.Edit{{Site: speed.slot.Site, Value: &model.Value{Kind: model.KindFloat, Float: math.MaxFloat32}}}); err != nil {
		t.Errorf("MaxFloat32 refused: %v", err)
	}
}

func TestSiteBounds(t *testing.T) {
	d, err := Parse("x", readFixture(t, "ue4.sav"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path     string
		min, max string
	}{
		{"Small", "-128", "127"},
		{"Short", "-32768", "32767"},
		{"Level", "-2147483648", "2147483647"},
		{"Gold", "-9223372036854775808", "9223372036854775807"},
		{"Raw", "0", "255"},
		{"Port", "0", "65535"},
		{"Flags", "0", "4294967295"},
		{"Big", "0", "18446744073709551615"},
		{"Col/r", "0", "255"},
		{"Cell/x", "-2147483648", "2147483647"},
		{"When", "-9223372036854775808", "9223372036854775807"},
	}
	for _, tt := range tests {
		s := find(t, d, tt.path).slot.Site
		if s == nil || s.IntMin.String() != tt.min || s.IntMax.String() != tt.max {
			t.Errorf("%s bounds = %v, want %s..%s", tt.path, s, tt.min, tt.max)
		}
	}
	// Strings have CheckText, every site has a unique ID, and only primitives have sites.
	ids := map[int]string{}
	for _, e := range entries(d) {
		v := e.slot.Value
		if e.slot.Site == nil {
			if v.IsPrimitive() && v.Kind != model.KindNull {
				t.Errorf("%s is a primitive without a site", e.path)
			}
			continue
		}
		if prev, dup := ids[e.slot.Site.ID]; dup {
			t.Errorf("%s and %s share site ID %d", prev, e.path, e.slot.Site.ID)
		}
		ids[e.slot.Site.ID] = e.path
		if v.Kind == model.KindString && e.slot.Site.CheckText == nil {
			t.Errorf("%s has no CheckText", e.path)
		}
	}
}

func TestFloat32Flag(t *testing.T) {
	d, err := Parse("x", readFixture(t, "ue5.sav"))
	if err != nil {
		t.Fatal(err)
	}
	for path, f32 := range map[string]bool{"Speed": true, "Pos/x": false, "Tint/r": true} {
		if got := find(t, d, path).slot.Value.Float32; got != f32 {
			t.Errorf("%s Float32 = %v, want %v", path, got, f32)
		}
	}
}

func TestKeepsUnreadableValuesAsBytes(t *testing.T) {
	d, err := Parse("x", readFixture(t, "ue4.sav"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"Table", "Mystery", "Odd", "VecMap"} {
		e := find(t, d, p)
		if e.slot.Value.Kind != model.KindBytes || e.slot.Site != nil {
			t.Errorf("%s: kind %v, site %v", p, e.slot.Value.Kind, e.slot.Site)
		}
	}
	// Editing a neighbour leaves them byte for byte.
	lv := find(t, d, "Level")
	data := readFixture(t, "ue4.sav")
	out, err := d.Encode([]model.Edit{{Site: lv.slot.Site, Value: newValue(t, lv.slot.Value, "8")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(data) {
		t.Fatal("length changed")
	}
	diff := 0
	for i := range out {
		if out[i] != data[i] {
			diff++
		}
	}
	if diff != 1 {
		t.Errorf("%d bytes differ, want 1", diff)
	}
}

func TestVariablesAreUnique(t *testing.T) {
	for _, name := range fixtures {
		d, err := Parse(name, readFixture(t, name))
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, v := range d.Variables() {
			if seen[v.Name] {
				t.Errorf("%s: duplicate variable %q", name, v.Name)
			}
			seen[v.Name] = true
		}
	}
}

func TestParseErrors(t *testing.T) {
	good := readFixture(t, "ue4.sav")
	good54 := readFixture(t, "ue54.sav")
	badMagic := append([]byte("GVAX"), good[4:]...)
	// Custom version count that cannot fit in the file.
	hugeCustoms := append([]byte(nil), good...)
	countAt := 4 + 4 + 4 + 6 + 4 + 4 + len("++UE4+Release-4.27") + 1 + 4
	binary.LittleEndian.PutUint32(hugeCustoms[countAt:], 0x7fffffff)

	props := func(tail []byte) []byte {
		i := bytes.Index(good, []byte("\x06\x00\x00\x00Level\x00"))
		return append(append([]byte(nil), good[:i]...), tail...)
	}
	tests := []struct {
		name  string
		data  []byte
		route string
	}{
		{"not gvas", badMagic, "Parse->reader.readHeader.GvasNotGVAS"},
		{"empty", nil, "Parse->reader.readHeader->reader.need.GvasTruncated"},
		{"cut in header", good[:20], "reader.need.GvasTruncated"},
		{"huge custom table", hugeCustoms, "reader.readHeader.GvasBadHeader"},
		{"cut in a property name", good[:bytes.Index(good, []byte("Level"))+2], "reader.fstring->reader.need.GvasTruncated"},
		{"cut in a value", good[:len(good)/2], "reader.readTag->reader.checkSize.GvasBadProperty"},
		{"bad property type", props(append(append([]byte("\x04\x00\x00\x00abc\x00"), "\x04\x00\x00\x00abc\x00"...), make([]byte, 16)...)),
			"reader.readTag.GvasBadProperty"},
		{"bad property name", props([]byte{2, 0, 0, 0, 1, 0, 0, 0, 0, 0}), "reader.readPropList.GvasBadProperty"},
		{"size beyond file", props(append([]byte("\x04\x00\x00\x00Abc\x00\x0c\x00\x00\x00IntProperty\x00\xff\xff\xff\x7f\x00\x00\x00\x00\x00"), 1, 2)),
			"reader.readTag->reader.checkSize.GvasBadProperty"},
		{"5.4 tag with extensions", append(append([]byte(nil), good54[:bytes.Index(good54, []byte("\x07\x00\x00\x00bAlive\x00"))]...),
			"\x04\x00\x00\x00ext\x00\x0c\x00\x00\x00IntProperty\x00\x00\x00\x00\x00\x00\x00\x00\x00\x04"...), "reader.readTagNew.GvasUnsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Parse("x.sav", tt.data)
			if err == nil {
				t.Fatalf("Parse succeeded: %v", dump(d))
			}
			e := routeOf(t, err)
			if !e.HasRoute(tt.route) {
				t.Errorf("route = %s, want it to contain %s", e.Route(), tt.route)
			}
		})
	}
	if _, err := Format.Parse("x.sav", badMagic); err == nil {
		t.Error("Format.Parse accepted a file that is not GVAS")
	}
}

func TestInternalErrors(t *testing.T) {
	r := newReader([]byte{0, 0, 0, 0x80}, nil)
	_, err := r.fstring()
	if e := routeOf(t, err); !e.HasRoute("reader.fstring.GvasBadString") {
		t.Errorf("route = %s", e.Route())
	}
	r = newReader([]byte{9, 0, 0, 0, 'a'}, nil)
	_, err = r.fstring()
	if e := routeOf(t, err); !e.HasRoute("reader.fstring->reader.need.GvasTruncated") {
		t.Errorf("route = %s", e.Route())
	}

	// A value that does not fill the size the tag gives it is kept as bytes.
	r = newReader([]byte{1, 2, 3, 4, 5, 6, 7, 8}, nil)
	_, err = r.readBinary("Guid", binaryStructs["Guid"], &span{}, elemCtx{structSize: 8, hint: -1})
	if err == nil {
		t.Error("a Guid of 8 bytes was accepted")
	}
	if e := routeOf(t, err); !e.HasRoute("reader.readBinary->reader.need.GvasTruncated") {
		t.Errorf("route = %s", e.Route())
	}
	r = newReader(make([]byte, 16), nil)
	_, err = r.readBinary("Guid", binaryStructs["Guid"], &span{}, elemCtx{structSize: 12, hint: -1})
	if e := routeOf(t, err); !e.HasRoute("reader.readBinary->reader.checkStructSize.GvasSizeMismatch") {
		t.Errorf("route = %s", e.Route())
	}
	_, err = floatWidth(3, elemCtx{structSize: 13, hint: -1}, false)
	if e := routeOf(t, err); !e.HasRoute("floatWidth.GvasSizeMismatch") {
		t.Errorf("route = %s", e.Route())
	}

	// readProperty: the body of a bool tag with a size.
	tg := &tag{name: "X", typ: &ptype{name: "IntProperty"}, size: 8}
	r = newReader(make([]byte, 8), nil)
	r.doc = &Document{}
	slot := r.readProperty(tg, 0, &span{})
	if slot.Value.Kind != model.KindBytes || len(slot.Value.Bytes) != 8 {
		t.Errorf("a property that does not fill its size = %v", slot.Value.Kind)
	}
	r = newReader(make([]byte, 8), nil)
	_, err = r.readScalar(&ptype{name: "WeirdProperty"}, &span{}, elemCtx{})
	if e := routeOf(t, err); !e.HasRoute("reader.readScalar.GvasUnsupported") {
		t.Errorf("route = %s", e.Route())
	}
	for _, name := range []string{"ArrayProperty", "SetProperty", "MapProperty"} {
		tg := &tag{name: "X", typ: &ptype{name: name}, size: 0}
		r = newReader(nil, nil)
		if _, err := r.readBody(tg, &span{}); routeOf(t, err).Tag != tagUnsupported {
			t.Errorf("%s without types: %v", name, err)
		}
	}
}

func TestTruncatedAndCorruptedFilesDoNotPanic(t *testing.T) {
	for _, name := range fixtures {
		data := readFixture(t, name)
		for n := 0; n < len(data); n++ {
			if d, err := Parse(name, data[:n]); err == nil {
				// A prefix that parses must also encode.
				if _, err := d.Encode(nil); err != nil {
					t.Fatalf("%s[:%d]: %v", name, n, err)
				}
			}
		}
		for i := range data {
			mut := append([]byte(nil), data...)
			mut[i] ^= 0xff
			if d, err := Parse(name, mut); err == nil {
				out, err := d.Encode(nil)
				if err != nil || !bytes.Equal(out, mut) {
					t.Fatalf("%s with byte %d flipped: round trip failed: %v", name, i, err)
				}
			}
		}
	}
}

func fstringBytes(s string) []byte { return encodeFString(s) }

func TestLargeByteArrayAndDuplicateKeys(t *testing.T) {
	good := readFixture(t, "ue4.sav")
	head := good[:bytes.Index(good, fstringBytes("bAlive"))]
	u32 := func(n int) []byte { return binary.LittleEndian.AppendUint32(nil, uint32(n)) }
	prop := func(name, typ string, hdr, body []byte) []byte {
		out := append(fstringBytes(name), fstringBytes(typ)...)
		out = append(out, u32(len(body))...)
		out = append(out, u32(0)...)
		out = append(out, hdr...)
		out = append(out, 0)
		return append(out, body...)
	}
	big := append(u32(5000), make([]byte, 5000)...)
	mapBody := append(u32(0), u32(2)...)
	for _, k := range []string{"a", "a"} {
		mapBody = append(mapBody, fstringBytes(k)...)
		mapBody = append(mapBody, u32(1)...)
	}
	data := append([]byte(nil), head...)
	data = append(data, prop("Blob", "ArrayProperty", fstringBytes("ByteProperty"), big)...)
	data = append(data, prop("Dups", "MapProperty", append(fstringBytes("StrProperty"), fstringBytes("IntProperty")...), mapBody)...)
	data = append(data, fstringBytes("None")...)
	data = append(data, 0, 0, 0, 0)
	d, err := Parse("x", data)
	if err != nil {
		t.Fatal(err)
	}
	blob := find(t, d, "Blob")
	if blob.slot.Value.Kind != model.KindBytes || len(blob.slot.Value.Bytes) != 5000 {
		t.Errorf("Blob = %v with %d bytes", blob.slot.Value.Kind, len(blob.slot.Value.Bytes))
	}
	want := []string{"Blob\tbytes " + string(hexOf(make([]byte, 5000))), "Dups\tmap 2", "Dups/a\tint 1", "Dups/a (2)\tint 1"}
	diffLines(t, dump(d), want)
	out, err := d.Encode(nil)
	if err != nil || !bytes.Equal(out, data) {
		t.Errorf("round trip: %v", err)
	}
}
