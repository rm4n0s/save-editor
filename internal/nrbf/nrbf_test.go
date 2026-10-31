package nrbf

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

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

// at is the slot at a path of labels: a variable, then the labels of children.
func at(t *testing.T, doc model.Document, path ...string) model.Slot {
	t.Helper()
	var slot model.Slot
	found := false
	for _, v := range doc.Variables() {
		if v.Name == path[0] {
			slot, found = v.Slot, true
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

func intV(n *big.Int) *model.Value { return &model.Value{Kind: model.KindInt, Int: n} }
func i64(n int64) *model.Value     { return intV(big.NewInt(n)) }
func fltV(f float64) *model.Value  { return &model.Value{Kind: model.KindFloat, Float: f} }
func strV(s string) *model.Value   { return &model.Value{Kind: model.KindString, Str: s} }
func boolV(b bool) *model.Value    { return &model.Value{Kind: model.KindBool, Bool: b} }

// place is a primitive of the tree: where it is, what it shows and the site that holds it.
type place struct {
	repr string
	site *model.Site
}

// places lists the primitives of the tree by the path of their labels. A container that is
// reached again is not entered again, so a cycle ends.
func places(doc model.Document) map[string]place {
	out := map[string]place{}
	seen := map[*model.Value]bool{}
	var walk func(path string, s model.Slot)
	walk = func(path string, s model.Slot) {
		v := s.Value
		if len(v.Children) == 0 || seen[v] {
			out[path] = place{v.Repr(), s.Site}
			return
		}
		seen[v] = true
		out[path] = place{v.Repr(), s.Site}
		for _, c := range v.Children {
			walk(path+"/"+c.Label, c.Slot)
		}
	}
	for _, v := range doc.Variables() {
		walk(v.Name, v.Slot)
	}
	return out
}

// repr is what the value shows after the edit that gives it new contents.
func repr(old, edit *model.Value) string {
	v := *old
	switch edit.Kind {
	case model.KindBool:
		v.Bool = edit.Bool
	case model.KindInt:
		v.Int = edit.Int
	case model.KindFloat:
		v.Float = edit.Float
		if v.Float32 {
			v.Float = float64(float32(edit.Float))
		}
	case model.KindString:
		v.Str = edit.Str
	}
	return v.Repr()
}

func TestDetect(t *testing.T) {
	own := []string{"player.sav", "extra.sav", "array-root.sav", "string-root.sav", "spec-address.bin"}
	for _, name := range own {
		if !Format.Detect(name, readFixture(t, name)) {
			t.Errorf("%s is not detected", name)
		}
	}
	// No fixture of another format, and no script or note of another package, is taken.
	others, err := filepath.Glob(filepath.Join("..", "*", "testdata", "*"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, path := range others {
		if filepath.Dir(path) == filepath.Join("..", "nrbf", "testdata") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		count++
		for _, name := range []string{filepath.Base(path), "player.sav", "x.dat", "x.bin", "x.save"} {
			if Format.Detect(name, data) {
				t.Errorf("%s is detected under the name %s", path, name)
			}
		}
	}
	if count < 30 {
		t.Errorf("only %d fixtures of other formats were checked", count)
	}
	player := readFixture(t, "player.sav")
	with := func(f func(b []byte)) []byte {
		b := bytes.Clone(player)
		f(b)
		return b
	}
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"text", []byte("hello, this is not a save")},
		{"GVAS magic", []byte("GVAS\x02\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")},
		{"zeros", make([]byte, 64)},
		{"a header only", player[:17]},
		{"another major version", with(func(b []byte) { b[9] = 2 })},
		{"another minor version", with(func(b []byte) { b[13] = 1 })},
		{"a record that cannot come first", with(func(b []byte) { b[17] = byte(recMessageEnd) })},
		{"a method call first", with(func(b []byte) { b[17] = byte(recMethodCall) })},
		{"not a header", with(func(b []byte) { b[0] = 1 })},
	}
	for _, tt := range tests {
		if Format.Detect("player.sav", tt.data) {
			t.Errorf("%s is detected", tt.name)
		}
	}
}

func TestParsePlayer(t *testing.T) {
	doc, _ := parseFixture(t, "player.sav")
	want := []struct {
		path []string
		kind model.Kind
		typ  string
		repr string
		edit bool
	}{
		{[]string{"Name"}, model.KindString, "string", `"Ünity Hero"`, true},
		{[]string{"level"}, model.KindInt, "int", "42", true},
		{[]string{"health"}, model.KindFloat, "float", "87.5", true},
		{[]string{"gold"}, model.KindInt, "long", "123456789012", true},
		{[]string{"speed"}, model.KindFloat, "double", "3.75", true},
		{[]string{"isAlive"}, model.KindBool, "bool", "true", true},
		{[]string{"grade"}, model.KindString, "char", `"A"`, true},
		{[]string{"rank"}, model.KindInt, "byte", "200", true},
		{[]string{"delta"}, model.KindInt, "sbyte", "-5", true},
		{[]string{"small"}, model.KindInt, "short", "-1234", true},
		{[]string{"flags"}, model.KindInt, "ushort", "65000", true},
		{[]string{"count"}, model.KindInt, "uint", "4000000000", true},
		{[]string{"big"}, model.KindInt, "ulong", "18446744073709551000", true},
		{[]string{"rating"}, model.KindString, "decimal", `"12.50"`, true},
		{[]string{"created"}, model.KindOpaque, "DateTime", "<2024-03-05T12:00:00.0000000Z>", false},
		{[]string{"playTime"}, model.KindOpaque, "TimeSpan", "<01:02:03>", false},
		{[]string{"nickname"}, model.KindString, "string", `"Ünity Hero"`, true},
		{[]string{"home"}, model.KindObject, "Address", "Address (2 parts)", false},
		{[]string{"home", "street"}, model.KindString, "string", `"1 Main St"`, true},
		{[]string{"home", "number"}, model.KindInt, "int", "12", true},
		{[]string{"inventory"}, model.KindList, "List<int>", "List<int> (5 items)", false},
		{[]string{"inventory", "[4]"}, model.KindInt, "int", "50", true},
		{[]string{"scores"}, model.KindMap, "Dictionary<string, int>", "Dictionary<string, int> (2 items)", false},
		{[]string{"scores", "losses"}, model.KindInt, "int", "5", true},
		{[]string{"tags"}, model.KindList, "string[]", "string[] (3 items)", false},
		{[]string{"tags", "[0]"}, model.KindString, "string", `"fast"`, true},
		{[]string{"tags", "[2]"}, model.KindNull, "null", "null", false},
		{[]string{"grid"}, model.KindList, "int[,]", "int[,] (6 items)", false},
		{[]string{"grid", "[1,2]"}, model.KindInt, "int", "6", true},
		{[]string{"points", "[1]", "y"}, model.KindFloat, "float", "4.5", true},
		{[]string{"blob"}, model.KindBytes, "byte[]", "5 bytes", false},
		{[]string{"ratios", "[2]"}, model.KindFloat, "float", "2.5", true},
		{[]string{"nothing"}, model.KindNull, "null", "null", false},
		{[]string{"boxed"}, model.KindInt, "int", "77", true},
		{[]string{"missing"}, model.KindNull, "null", "null", false},
		{[]string{"names"}, model.KindList, "List<string>", "List<string> (2 items)", false},
		{[]string{"extras", "[1]"}, model.KindString, "string", `"x"`, true},
		{[]string{"table"}, model.KindMap, "Hashtable", "Hashtable (2 items)", false},
		{[]string{"table", "k2"}, model.KindFloat, "double", "2.5", true},
		{[]string{"seen"}, model.KindList, "HashSet<int>", "HashSet<int> (3 items)", false},
		{[]string{"seen", "[1]"}, model.KindInt, "int", "8", true},
	}
	for _, w := range want {
		s := at(t, doc, w.path...)
		if s.Value.Kind != w.kind || s.Value.TypeName() != w.typ || s.Value.Repr() != w.repr || (s.Site != nil) != w.edit {
			t.Errorf("%v = %v %s %s (site %v), want %v %s %s (site %v)", w.path, s.Value.Kind, s.Value.TypeName(), s.Value.Repr(),
				s.Site != nil, w.kind, w.typ, w.repr, w.edit)
		}
	}
	if n := len(doc.Variables()); n != 33 {
		t.Errorf("%d variables, want 33", n)
	}
	// A field behind an automatic property has the name of the property.
	if doc.Variables()[0].Name != "Name" {
		t.Errorf("the first variable is %q", doc.Variables()[0].Name)
	}
	// A list shows its items, not its capacity: List<int> has 8 items in its array and a size of 5.
	if n := len(at(t, doc, "inventory").Value.Children); n != 5 {
		t.Errorf("the list has %d items", n)
	}
	if got := at(t, doc, "home").Value.QualifiedType; got != "Game.Address" {
		t.Errorf("the qualified name is %q", got)
	}
	// The tooltip of the details has the full names.
	details := map[string]model.Detail{}
	for _, d := range doc.Details() {
		details[d.Label] = d
	}
	if details["Top-level class"].Text != "PlayerData" || details["Top-level class"].Tooltip != "Game.PlayerData" ||
		details["Library of the class"].Text != "Assembly-CSharp" || !strings.Contains(details["Libraries"].Tooltip, "Version=0.0.0.0") ||
		details["Objects"].Text != "33" {
		t.Errorf("details %+v", details)
	}
}

func TestParseOtherFixtures(t *testing.T) {
	extra, _ := parseFixture(t, "extra.sav")
	// A class that is its own parent is one value.
	parent := at(t, extra, "parent")
	if parent.Value.Kind != model.KindObject || at(t, extra, "parent", "parent").Value != parent.Value {
		t.Error("the parent of the root is not the object itself")
	}
	if at(t, extra, "name").Site != at(t, extra, "twin").Site || at(t, extra, "name").Site == nil {
		t.Error("two members that hold one string do not share a site")
	}
	for path, want := range map[string]string{
		"jagged/[0]/[2]": "3", "jagged/[1]/[0]": "4", "offset/[5]": "7", "offset/[7]": "9", "accent": `"é"`,
		"version/_Build": "3", "title": "null",
	} {
		if got := at(t, extra, strings.Split(path, "/")...).Value.Repr(); got != want {
			t.Errorf("%s = %s, want %s", path, got, want)
		}
	}
	if n := len(at(t, extra, "nulls").Value.Children); n != 300 {
		t.Errorf("%d nulls", n)
	}
	if n := len(at(t, extra, "empty").Value.Children); n != 0 {
		t.Errorf("%d items in an empty array", n)
	}
	if at(t, extra, "jagged").Value.TypeName() != "int[][]" || at(t, extra, "version").Value.TypeName() != "Version" {
		t.Error("type names of the extra fixture")
	}
	arr, _ := parseFixture(t, "array-root.sav")
	if v := arr.Variables(); len(v) != 1 || v[0].Name != "root" || v[0].Slot.Value.Repr() != "int[] (3 items)" {
		t.Errorf("array root: %+v", v)
	}
	str, _ := parseFixture(t, "string-root.sav")
	if v := str.Variables(); len(v) != 1 || v[0].Slot.Value.Str != "hello" || v[0].Slot.Site == nil {
		t.Errorf("string root: %+v", v)
	}
	// The example of the specification: a class of four strings.
	spec, _ := parseFixture(t, "spec-address.bin")
	var got []string
	for _, v := range spec.Variables() {
		got = append(got, v.Name+"="+v.Slot.Value.Str)
	}
	if want := []string{"Street=One Microsoft Way", "City=Redmond", "State=WA", "Zip=98054"}; !reflect.DeepEqual(got, want) {
		t.Errorf("spec-address.bin: %v", got)
	}
}

func TestSharedValues(t *testing.T) {
	doc, _ := parseFixture(t, "player.sav")
	if at(t, doc, "home").Value != at(t, doc, "work").Value {
		t.Error("home and work are two values")
	}
	if at(t, doc, "home", "street").Site != at(t, doc, "work", "street").Site {
		t.Error("the street has two sites")
	}
	if at(t, doc, "Name").Value != at(t, doc, "nickname").Value || at(t, doc, "Name").Site != at(t, doc, "nickname").Site {
		t.Error("Name and nickname are two values")
	}
	if at(t, doc, "tags", "[0]").Site != at(t, doc, "names", "[1]").Site {
		t.Error("a string of a string[] and of a List<string> do not share a site")
	}
	// Editing a shared value changes it in every place.
	out, err := doc.Encode([]model.Edit{
		{Site: at(t, doc, "Name").Site, Value: strV("Zed")},
		{Site: at(t, doc, "home", "street").Site, Value: strV("2 Side St")},
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := Format.Parse("player.sav", out)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range [][]string{{"Name"}, {"nickname"}} {
		if got := at(t, again, path...).Value.Str; got != "Zed" {
			t.Errorf("%v = %q", path, got)
		}
	}
	for _, path := range [][]string{{"home", "street"}, {"work", "street"}} {
		if got := at(t, again, path...).Value.Str; got != "2 Side St" {
			t.Errorf("%v = %q", path, got)
		}
	}
	if bytes.Count(out, []byte("Zed")) != 1 {
		t.Error("a shared string is written more than once")
	}
}

func TestRoundTrip(t *testing.T) {
	for _, name := range []string{"player.sav", "extra.sav", "array-root.sav", "string-root.sav", "spec-address.bin"} {
		doc, data := parseFixture(t, name)
		for _, edits := range [][]model.Edit{nil, {}} {
			out, err := doc.Encode(edits)
			if err != nil || !bytes.Equal(out, data) {
				t.Errorf("%s: Encode with no edits: %v, equal %v", name, err, bytes.Equal(out, data))
			}
			if len(out) > 0 {
				out[0] ^= 0xFF
			}
		}
		if again, _ := doc.Encode(nil); !bytes.Equal(again, data) {
			t.Errorf("%s: Encode changed the document", name)
		}
	}
}

var long = strings.Repeat("long text ", 20) + "é✓😀"

var edits = []struct {
	name    string
	fixture string
	path    []string
	value   *model.Value
}{
	{"string", "player.sav", []string{"home", "street"}, strV("Zoë ✓ 😀")},
	{"string to empty", "player.sav", []string{"home", "street"}, strV("")},
	{"string needing a longer length", "player.sav", []string{"home", "street"}, strV(long)},
	{"shared string", "player.sav", []string{"Name"}, strV(long)},
	{"string in an array", "player.sav", []string{"tags", "[0]"}, strV("slow")},
	{"string in a list", "player.sav", []string{"names", "[0]"}, strV("b")},
	{"string in object[]", "player.sav", []string{"extras", "[1]"}, strV("y")},
	{"string root", "string-root.sav", []string{"root"}, strV(long)},
	{"spec string", "spec-address.bin", []string{"City"}, strV("Seattle")},
	{"int32", "player.sav", []string{"level"}, i64(-2147483648)},
	{"boxed int32", "player.sav", []string{"boxed"}, i64(99)},
	{"int32 of a list", "player.sav", []string{"inventory", "[0]"}, i64(1)},
	{"int32 of a dictionary", "player.sav", []string{"scores", "wins"}, i64(0)},
	{"int32 of a hash set", "player.sav", []string{"seen", "[2]"}, i64(10)},
	{"int32 of a grid", "player.sav", []string{"grid", "[0,1]"}, i64(-7)},
	{"int32 of a jagged array", "extra.sav", []string{"jagged", "[0]", "[1]"}, i64(22)},
	{"int32 of an offset array", "extra.sav", []string{"offset", "[6]"}, i64(88)},
	{"int32 of a system class", "extra.sav", []string{"version", "_Revision"}, i64(9)},
	{"int32 of the root array", "array-root.sav", []string{"root", "[2]"}, i64(5)},
	{"int64", "player.sav", []string{"gold"}, i64(math.MinInt64)},
	{"byte", "player.sav", []string{"rank"}, i64(0)},
	{"sbyte", "player.sav", []string{"delta"}, i64(-128)},
	{"int16", "player.sav", []string{"small"}, i64(32767)},
	{"uint16", "player.sav", []string{"flags"}, i64(0)},
	{"uint32", "player.sav", []string{"count"}, i64(math.MaxUint32)},
	{"uint64", "player.sav", []string{"big"}, intV(new(big.Int).SetUint64(math.MaxUint64))},
	{"float32", "player.sav", []string{"health"}, fltV(0.1)},
	{"float32 in an array", "player.sav", []string{"ratios", "[1]"}, fltV(-1e-30)},
	{"float32 in a class of an array", "player.sav", []string{"points", "[0]", "x"}, fltV(16777217)},
	{"float64", "player.sav", []string{"speed"}, fltV(1e300)},
	{"float64 in a hashtable", "player.sav", []string{"table", "k2"}, fltV(-0.5)},
	{"bool", "player.sav", []string{"isAlive"}, boolV(false)},
	{"char of two bytes", "player.sav", []string{"grade"}, strV("é")},
	{"char of three bytes", "player.sav", []string{"grade"}, strV("€")},
	{"char of one byte over two", "extra.sav", []string{"accent"}, strV("e")},
	{"decimal", "player.sav", []string{"rating"}, strV("-0.0000000000000000000000000001")},
	{"decimal of the largest size", "player.sav", []string{"rating"}, strV("79228162514264337593543950335")},
	{"decimal of a whole number", "player.sav", []string{"rating"}, strV("7")},
}

func TestEdits(t *testing.T) {
	for _, tt := range edits {
		t.Run(tt.name, func(t *testing.T) {
			doc, data := parseFixture(t, tt.fixture)
			slot := at(t, doc, tt.path...)
			if slot.Site == nil || slot.Value.Kind != tt.value.Kind {
				t.Fatalf("the old value is %v, site %v", slot.Value.Kind, slot.Site)
			}
			before := places(doc)
			out, err := doc.Encode([]model.Edit{{Site: slot.Site, Value: tt.value}})
			if err != nil {
				t.Fatal(err)
			}
			// Only the bytes of the value changed: the start and the end are as they were.
			span := slot.Site.Ref.(*siteRef).span
			tail := len(data) - span.end
			if !bytes.Equal(out[:span.start], data[:span.start]) || !bytes.Equal(out[len(out)-tail:], data[span.end:]) {
				t.Error("bytes outside the edited value changed")
			}
			again, err := Format.Parse(tt.fixture, out)
			if err != nil {
				t.Fatalf("the output does not read back: %v", err)
			}
			// Every place of the edited site has the new value and every other has its old one.
			wantRepr := repr(slot.Value, tt.value)
			after := places(again)
			if len(after) != len(before) {
				t.Errorf("%d places, want %d", len(after), len(before))
			}
			for path, old := range before {
				want := old.repr
				if old.site == slot.Site {
					want = wantRepr
				}
				if got := after[path].repr; got != want {
					t.Errorf("%s = %s, want %s", path, got, want)
				}
			}
			if bytes.Equal(out, data) && repr(slot.Value, slot.Value) != wantRepr {
				t.Error("the file did not change")
			}
		})
	}
}

func TestSeveralEditsAtOnce(t *testing.T) {
	doc, data := parseFixture(t, "player.sav")
	// In an order that is not the order of the file, with a change of length in the middle.
	list := []model.Edit{
		{Site: at(t, doc, "seen", "[0]").Site, Value: i64(70)},
		{Site: at(t, doc, "Name").Site, Value: strV(long)},
		{Site: at(t, doc, "grade").Site, Value: strV("€")},
		{Site: at(t, doc, "level").Site, Value: i64(1)},
		{Site: at(t, doc, "points", "[1]", "x").Site, Value: fltV(9)},
		{Site: at(t, doc, "rating").Site, Value: strV("1")},
	}
	out, err := doc.Encode(list)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Format.Parse("player.sav", out)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"seen/[0]": "70", "Name": strconvQuote(long), "nickname": strconvQuote(long), "grade": `"€"`,
		"level": "1", "points/[1]/x": "9.0", "rating": `"1"`, "seen/[1]": "8", "gold": "123456789012"} {
		if got := places(again)[path].repr; got != want {
			t.Errorf("%s = %s, want %s", path, got, want)
		}
	}
	if bytes.Equal(out, data) {
		t.Error("nothing changed")
	}
}

func strconvQuote(s string) string { return (&model.Value{Kind: model.KindString, Str: s}).Repr() }

func TestBounds(t *testing.T) {
	doc, _ := parseFixture(t, "player.sav")
	u64max := new(big.Int).SetUint64(math.MaxUint64)
	tests := []struct {
		name   string
		lo, hi *big.Int
	}{
		{"level", big.NewInt(math.MinInt32), big.NewInt(math.MaxInt32)},
		{"gold", big.NewInt(math.MinInt64), big.NewInt(math.MaxInt64)},
		{"rank", big.NewInt(0), big.NewInt(255)},
		{"delta", big.NewInt(-128), big.NewInt(127)},
		{"small", big.NewInt(math.MinInt16), big.NewInt(math.MaxInt16)},
		{"flags", big.NewInt(0), big.NewInt(65535)},
		{"count", big.NewInt(0), big.NewInt(math.MaxUint32)},
		{"big", big.NewInt(0), u64max},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := at(t, doc, tt.name).Site
			if site.IntMin.Cmp(tt.lo) != 0 || site.IntMax.Cmp(tt.hi) != 0 {
				t.Errorf("bounds %v to %v, want %v to %v", site.IntMin, site.IntMax, tt.lo, tt.hi)
			}
			for _, ok := range []*big.Int{tt.lo, tt.hi} {
				out, err := doc.Encode([]model.Edit{{Site: site, Value: intV(ok)}})
				if err != nil {
					t.Fatalf("%v: %v", ok, err)
				}
				again, err := Format.Parse("player.sav", out)
				if err != nil {
					t.Fatal(err)
				}
				if got := at(t, again, tt.name).Value.Int; got.Cmp(ok) != 0 {
					t.Errorf("%v reads back as %v", ok, got)
				}
			}
			for _, bad := range []*big.Int{new(big.Int).Sub(tt.lo, big.NewInt(1)), new(big.Int).Add(tt.hi, big.NewInt(1)), nil} {
				_, err := doc.Encode([]model.Edit{{Site: site, Value: &model.Value{Kind: model.KindInt, Int: bad}}})
				assertRoute(t, err, "Encode->siteRef.encode.NRBFEditRange")
			}
		})
	}
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

func TestRefusedEdits(t *testing.T) {
	doc, _ := parseFixture(t, "player.sav")
	other, _ := parseFixture(t, "player.sav")
	level, health, name, grade, rating := at(t, doc, "level").Site, at(t, doc, "health").Site, at(t, doc, "Name").Site,
		at(t, doc, "grade").Site, at(t, doc, "rating").Site
	tests := []struct {
		name  string
		edits []model.Edit
		route string
	}{
		{"a site of another document", []model.Edit{{Site: at(t, other, "level").Site, Value: i64(1)}}, "Encode.NRBFEditSite"},
		{"no site", []model.Edit{{Value: i64(1)}}, "Encode.NRBFEditSite"},
		{"a site that is not in the document", []model.Edit{{Site: &model.Site{ID: 1 << 20}, Value: i64(1)}}, "Encode.NRBFEditSite"},
		{"the same site twice", []model.Edit{{Site: level, Value: i64(1)}, {Site: level, Value: i64(2)}}, "Encode.NRBFEditSite"},
		{"no value", []model.Edit{{Site: level}}, "Encode.NRBFEditKind"},
		{"a string for an int", []model.Edit{{Site: level, Value: strV("1")}}, "Encode.NRBFEditKind"},
		{"an int for a float", []model.Edit{{Site: health, Value: i64(1)}}, "Encode.NRBFEditKind"},
		{"a bool for a string", []model.Edit{{Site: name, Value: boolV(true)}}, "Encode.NRBFEditKind"},
		{"an int out of range", []model.Edit{{Site: level, Value: i64(1 << 31)}}, "Encode->siteRef.encode.NRBFEditRange"},
		{"a float32 that is too large", []model.Edit{{Site: health, Value: fltV(1e39)}}, "Encode->siteRef.encode.NRBFEditRange"},
		{"a string that is not UTF-8", []model.Edit{{Site: name, Value: strV("a\xffb")}}, "Encode->siteRef.encode.NRBFEditText"},
		{"two characters", []model.Edit{{Site: grade, Value: strV("ab")}}, "Encode->siteRef.encode->checkChar.NRBFEditText"},
		{"no character", []model.Edit{{Site: grade, Value: strV("")}}, "Encode->siteRef.encode->checkChar.NRBFEditText"},
		{"a character beyond the plane", []model.Edit{{Site: grade, Value: strV("😀")}}, "Encode->siteRef.encode->checkChar.NRBFEditText"},
		{"a decimal with letters", []model.Edit{{Site: rating, Value: strV("1e5")}}, "Encode->siteRef.encode->checkDecimal.NRBFEditText"},
		{"a decimal with too many digits", []model.Edit{{Site: rating, Value: strV("0." + strings.Repeat("1", 29))}}, "Encode->siteRef.encode->checkDecimal.NRBFEditText"},
		{"a decimal that is too large", []model.Edit{{Site: rating, Value: strV("79228162514264337593543950336")}}, "Encode->siteRef.encode->checkDecimal.NRBFEditText"},
		{"a decimal that is a point", []model.Edit{{Site: rating, Value: strV(".")}}, "Encode->siteRef.encode->checkDecimal.NRBFEditText"},
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
	// The messages that a site's CheckText gives are for the user.
	for _, tt := range []struct {
		site *model.Site
		text string
		want string
	}{
		{grade, "ab", "Enter exactly one character"},
		{grade, "😀", "Enter a character of the Basic Multilingual Plane (not an emoji)"},
		{rating, "abc", "Enter a decimal number such as -12.50"},
		{name, "a\xff", "The text is not valid UTF-8"},
	} {
		err := tt.site.CheckText(tt.text)
		if err == nil || err.Error() != tt.want {
			t.Errorf("CheckText(%q) = %v, want %q", tt.text, err, tt.want)
		}
		assertRoute(t, err, ".NRBFEditText")
	}
	for _, ok := range []string{"é", "€", "z"} {
		if err := grade.CheckText(ok); err != nil {
			t.Errorf("CheckText(%q): %v", ok, err)
		}
	}
}

// The helpers that write streams for the tests of damaged files.

func i32le(n int32) []byte { return binary.LittleEndian.AppendUint32(nil, uint32(n)) }

func lp(s string) []byte { return append([]byte{byte(len(s))}, s...) }

func hdr(root int32) []byte {
	return append(append(append([]byte{byte(recHeader)}, i32le(root)...), i32le(-1)...), append(i32le(1), i32le(0)...)...)
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// sysClass is a SystemClassWithMembersAndTypes of members that are all object.
func sysClass(id int32, name string, members ...string) []byte {
	out := cat([]byte{byte(recSystemClassTypes)}, i32le(id), lp(name), i32le(int32(len(members))))
	for _, m := range members {
		out = append(out, lp(m)...)
	}
	return append(out, bytes.Repeat([]byte{byte(binObject)}, len(members))...)
}

func ref(id int32) []byte { return append([]byte{byte(recMemberReference)}, i32le(id)...) }

var end = []byte{byte(recMessageEnd)}

func TestParseErrors(t *testing.T) {
	player := readFixture(t, "player.sav")
	chain := hdr(1)
	for i := int32(1); i <= 20000; i++ {
		chain = append(chain, sysClass(i, "N", "next")...)
		if i < 20000 {
			chain = append(chain, ref(i+1)...)
		} else {
			chain = append(chain, byte(recObjectNull))
		}
	}
	chain = append(chain, end...)
	var bomb []byte
	bomb = append(bomb, hdr(1)...)
	for i := int32(1); i <= 6; i++ {
		bomb = append(bomb, cat([]byte{byte(recArraySingleObject)}, i32le(i), i32le(1<<20), []byte{byte(recObjectNullMultiple)}, i32le(1<<20))...)
	}
	bomb = append(bomb, end...)
	tests := []struct {
		name  string
		data  []byte
		tag   string
		route string
	}{
		{"empty", nil, "NRBFTruncated", "parse->parseStream->reader.u8->reader.need.NRBFTruncated"},
		{"cut in the middle", player[:200], "NRBFTruncated", "parse->parseStream->reader.readObject"},
		{"no message end", player[:len(player)-1], "NRBFTruncated", "reader.need.NRBFTruncated"},
		{"not a header", append([]byte{1}, player[1:]...), "NRBFHeader", "parse->parseStream.NRBFHeader"},
		{"another version", cat([]byte{0}, i32le(1), i32le(0), i32le(2), i32le(0), end), "NRBFHeader", "parse->parseStream.NRBFHeader"},
		{"a record that cannot be first", cat(hdr(1), []byte{byte(recMemberReference)}, i32le(1), end), "NRBFRecordInvalid", "parse->parseStream.NRBFRecordInvalid"},
		{"an unknown record", cat(hdr(1), []byte{99}, end), "NRBFRecordInvalid", "parse->parseStream.NRBFRecordInvalid"},
		{"a class without types", cat(hdr(1), []byte{byte(recClassMembers)}), "NRBFUnsupported", "parse->parseStream.NRBFUnsupported"},
		{"a method call", cat(hdr(1), []byte{byte(recMethodCall)}), "NRBFUnsupported", "parse->parseStream.NRBFUnsupported"},
		{"a method call in a member", cat(hdr(1), sysClass(1, "C", "a"), []byte{byte(recMethodReturn)}, end), "NRBFUnsupported", "reader.readRecordValue.NRBFUnsupported"},
		{"a class without types in a member", cat(hdr(1), sysClass(1, "C", "a"), []byte{byte(recSystemClassMembers)}, end), "NRBFUnsupported", "reader.readRecordValue.NRBFUnsupported"},
		{"a record that cannot be a member", cat(hdr(1), sysClass(1, "C", "a"), []byte{byte(recMessageEnd)}), "NRBFRecordInvalid", "reader.readRecordValue.NRBFRecordInvalid"},
		{"the root is missing", cat(hdr(5), sysClass(1, "C", "a"), []byte{byte(recObjectNull)}, end), "NRBFReference", "parse.NRBFReference"},
		{"a reference to nothing", cat(hdr(1), sysClass(1, "C", "a"), ref(9), end), "NRBFReference", "builder.slot.NRBFReference"},
		{"two objects with one id", cat(hdr(1), sysClass(1, "C", "a"), []byte{byte(recObjectNull)}, sysClass(1, "D", "a"), []byte{byte(recObjectNull)}, end), "NRBFReference", "reader.readClass.NRBFReference"},
		{"two strings with one id", cat(hdr(1), []byte{byte(recObjectString)}, i32le(1), lp("a"), []byte{byte(recObjectString)}, i32le(1), lp("b"), end), "NRBFReference", "reader.readObject->reader.register.NRBFReference"},
		{"a class id of an unknown class", cat(hdr(1), []byte{byte(recClassWithID)}, i32le(1), i32le(77), end), "NRBFReference", "reader.readClass.NRBFReference"},
		{"two libraries with one id", cat(hdr(1), []byte{byte(recLibrary)}, i32le(2), lp("A"), []byte{byte(recLibrary)}, i32le(2), lp("B"), end), "NRBFReference", "reader.readLibrary.NRBFReference"},
		{"a negative count of members", cat(hdr(1), []byte{byte(recSystemClassTypes)}, i32le(1), lp("C"), i32le(-1), end), "NRBFValueInvalid", "reader.readClass.NRBFValueInvalid"},
		{"an unknown primitive type", cat(hdr(1), []byte{byte(recArraySinglePrimitive)}, i32le(1), i32le(1), []byte{4, 0}, end), "NRBFValueInvalid", "reader.readPrimType.NRBFValueInvalid"},
		{"an unknown binary type", cat(hdr(1), []byte{byte(recSystemClassTypes)}, i32le(1), lp("C"), i32le(1), lp("a"), []byte{9}, end), "NRBFValueInvalid", "reader.readSlotInfo.NRBFValueInvalid"},
		{"a negative array", cat(hdr(1), []byte{byte(recArraySinglePrimitive)}, i32le(1), i32le(-1), []byte{byte(primInt32)}, end), "NRBFValueInvalid", "reader.readArraySinglePrimitive.NRBFValueInvalid"},
		{"an unknown kind of array", cat(hdr(1), []byte{byte(recBinaryArray)}, i32le(1), []byte{9}, end), "NRBFValueInvalid", "reader.readBinaryArray.NRBFValueInvalid"},
		{"a string with a length that does not fit", cat(hdr(1), []byte{byte(recObjectString)}, i32le(1), []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x7F}, end), "NRBFValueInvalid", "reader.lengthPrefixed.NRBFValueInvalid"},
		{"a negative run of nulls", cat(hdr(1), []byte{byte(recArraySingleObject)}, i32le(1), i32le(2), []byte{byte(recObjectNullMultiple)}, i32le(-1), end), "NRBFValueInvalid", "reader.readRecordValue.NRBFValueInvalid"},
		{"a run of nulls that is too long", cat(hdr(1), []byte{byte(recArraySingleObject)}, i32le(1), i32le(2), []byte{byte(recObjectNullMultiple)}, i32le(3), end), "NRBFValueInvalid", "reader.readSlots.NRBFValueInvalid"},
		{"an array that is too long", cat(hdr(1), []byte{byte(recArraySingleObject)}, i32le(1), i32le(1<<21), end), "NRBFTooLarge", "reader.readArraySingle.NRBFTooLarge"},
		{"a rectangular array that is too large", cat(hdr(1), []byte{byte(recBinaryArray)}, i32le(1), []byte{2}, i32le(2), i32le(1<<15), i32le(1<<15), []byte{byte(binObject)}, end), "NRBFTooLarge", "reader.readBinaryArray.NRBFTooLarge"},
		{"more items than can be held", bomb, "NRBFTooLarge", "reader.count.NRBFTooLarge"},
		{"objects nested too deeply", chain, "NRBFTooLarge", "builder.object.NRBFTooLarge"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Format.Parse("x.dat", tt.data)
			e, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("expected *errors.Error, got %T: %v", err, err)
			}
			if e.Tag != tt.tag {
				t.Errorf("tag %s, want %s", e.Tag, tt.tag)
			}
			if !e.HasRoute(tt.route) {
				t.Errorf("route %s, want %s", e.Route(), tt.route)
			}
		})
	}
}

// TestDamagedFilesNeverPanic reads every prefix of the fixtures and the fixtures with each byte
// changed: an error or a document, never a panic, and a document encodes without edits.
func TestDamagedFilesNeverPanic(t *testing.T) {
	for _, name := range []string{"player.sav", "extra.sav", "spec-address.bin", "array-root.sav", "string-root.sav"} {
		data := readFixture(t, name)
		read := func(b []byte) {
			Format.Detect(name, b)
			doc, err := Format.Parse(name, b)
			if err != nil {
				return
			}
			doc.Details()
			for _, v := range doc.Variables() {
				_ = v.Slot.Value.Repr()
			}
			if out, err := doc.Encode(nil); err != nil || !bytes.Equal(out, b) {
				t.Errorf("%s: Encode(nil) of a damaged file: %v", name, err)
			}
		}
		for n := range len(data) {
			read(data[:n])
		}
		for i := range data {
			for _, x := range []byte{0x01, 0x80, 0xFF} {
				damaged := bytes.Clone(data)
				damaged[i] ^= x
				read(damaged)
			}
		}
	}
}

func TestShortTypeName(t *testing.T) {
	mscorlib := "mscorlib, Version=4.0.0.0, Culture=neutral, PublicKeyToken=b77a5c561934e089"
	tests := []struct{ in, want string }{
		{"Game.Models.Address", "Address"},
		{"Outer+Inner", "Inner"},
		{"System.Int32", "int"},
		{"System.String", "string"},
		{"System.Int32[]", "int[]"},
		{"System.Int32[,]", "int[,]"},
		{"System.Int32[][]", "int[][]"},
		{"Game.Vec2[]", "Vec2[]"},
		{"System.Collections.Generic.List`1[[System.Int32, " + mscorlib + "]]", "List<int>"},
		{"System.Collections.Generic.Dictionary`2[[System.String, " + mscorlib + "],[System.Int32, " + mscorlib + "]]", "Dictionary<string, int>"},
		{"System.Collections.Generic.List`1[[Game.Item, Assembly-CSharp, Version=0.0.0.0, Culture=neutral, PublicKeyToken=null]]", "List<Item>"},
		{"System.Collections.Generic.List`1[[System.Collections.Generic.List`1[[System.Int32, " + mscorlib + "]], " + mscorlib + "]]", "List<List<int>>"},
		{"System.Collections.Generic.List`1[[System.Int32, " + mscorlib + "]][]", "List<int>[]"},
		{"System.Collections.Hashtable", "Hashtable"},
		{"Weird`", "Weird"},
		{"Weird`1[[", "Weird"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := shortTypeName(tt.in); got != tt.want {
			t.Errorf("shortTypeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTexts(t *testing.T) {
	for _, tt := range []struct {
		name string
		got  string
		want string
	}{
		{"label of a backing field", memberLabel("<Name>k__BackingField"), "Name"},
		{"label of a field", memberLabel("level"), "level"},
		{"label of an odd field", memberLabel("<Name>"), "<Name>"},
		{"a time span of 1 h 2 min 3 s", timeSpanText(37230000000), "01:02:03"},
		{"a time span of a day and a tick", timeSpanText(ticksPerDay + 1), "1.00:00:00.0000001"},
		{"a negative time span", timeSpanText(-ticksPerDay * 2), "-2.00:00:00"},
		{"the smallest time span", timeSpanText(math.MinInt64), "-10675199.02:48:05.4775808"},
		{"zero time span", timeSpanText(0), "00:00:00"},
		{"a date", dateTimeText(0), "0001-01-01T00:00:00.0000000"},
		{"a UTC date", dateTimeText(1<<62 | 2*ticksPerDay), "0001-01-03T00:00:00.0000000Z"},
		{"a local date", dateTimeText(2<<62 | 36000000000), "0001-01-01T01:00:00.0000000 (local)"},
		{"a name made unique", func() string {
			used := map[string]bool{}
			uniqueName("a", used)
			return uniqueName("a", used)
		}(), "a (2)"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestUnusualShapes(t *testing.T) {
	big := cat(hdr(1), []byte{byte(recArraySinglePrimitive)}, i32le(1), i32le(70000), []byte{byte(primInt32)}, make([]byte, 70000*4), end)
	doc, err := Format.Parse("x.dat", big)
	if err != nil {
		t.Fatal(err)
	}
	if v := at(t, doc, "root").Value; v.Kind != model.KindOpaque || v.Repr() != "<int[] with 70000 items>" {
		t.Errorf("a large primitive array is %v %s", v.Kind, v.Repr())
	}
	// A class that has the name of a collection but not its members is shown as it is.
	odd := cat(hdr(1), sysClass(1, "System.Collections.Generic.List`1[[System.Int32]]", "other"), []byte{byte(recObjectNull)}, end)
	doc, err = Format.Parse("x.dat", odd)
	if err != nil {
		t.Fatal(err)
	}
	if v := at(t, doc, "other").Value; v.Kind != model.KindNull {
		t.Errorf("other = %v", v.Kind)
	}
	// A list whose size is larger than its array is shown as the class.
	list := cat(hdr(1), []byte{byte(recSystemClassTypes)}, i32le(1), lp("System.Collections.Generic.List`1[[System.Int32]]"), i32le(2),
		lp("_items"), lp("_size"), []byte{byte(binPrimitiveArray), byte(binPrimitive)}, []byte{byte(primInt32), byte(primInt32)},
		ref(2), i32le(9),
		[]byte{byte(recArraySinglePrimitive)}, i32le(2), i32le(1), []byte{byte(primInt32)}, i32le(5), end)
	doc, err = Format.Parse("x.dat", list)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Variables()[0].Name; got != "_items" {
		t.Errorf("a list with a size beyond its array is shown as %q", got)
	}
}
