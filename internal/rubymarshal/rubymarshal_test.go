package rubymarshal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
	"github.com/rm4n0s/save-editor/internal/rubymarshal/rmtest"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parseFixture(t *testing.T, name string, opts Options) *Doc {
	t.Helper()
	doc, err := Parse(readFixture(t, name), opts)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return doc
}

// at follows labels from a dump's root.
func at(t *testing.T, doc *Doc, dump int, labels ...string) model.Slot {
	t.Helper()
	s := doc.Dumps[dump].Root
	for _, l := range labels {
		found := false
		for _, c := range s.Value.Children {
			if c.Label == l {
				s = c.Slot
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no child %q in %s (%v)", l, s.Value.Repr(), labels)
		}
	}
	return s
}

func TestParseSample(t *testing.T) {
	doc := parseFixture(t, "sample.marshal", Options{})
	if len(doc.Dumps) != 3 || doc.Rest != len(doc.Data) {
		t.Fatalf("dumps = %d, rest = %d", len(doc.Dumps), doc.Rest)
	}
	for i, d := range doc.Dumps {
		if d.Start == 0 && i > 0 || d.End <= d.Start {
			t.Errorf("dump %d spans %d..%d", i, d.Start, d.End)
		}
	}
	tests := []struct {
		name   string
		dump   int
		path   []string
		kind   model.Kind
		typ    string
		repr   string
		editor bool
	}{
		{"nil", 0, []string{":nils", "[0]"}, model.KindNull, "NilClass", "nil", false},
		{"true", 0, []string{":nils", "[1]"}, model.KindBool, "TrueClass", "true", true},
		{"false", 0, []string{":nils", "[2]"}, model.KindBool, "FalseClass", "false", true},
		{"zero", 0, []string{":ints", "[0]"}, model.KindInt, "Fixnum", "0", true},
		{"122", 0, []string{":ints", "[2]"}, model.KindInt, "Fixnum", "122", true},
		{"123", 0, []string{":ints", "[3]"}, model.KindInt, "Fixnum", "123", true},
		{"2^30-1", 0, []string{":ints", "[9]"}, model.KindInt, "Fixnum", "1073741823", true},
		{"-124", 0, []string{":ints", "[12]"}, model.KindInt, "Fixnum", "-124", true},
		{"-2^30", 0, []string{":ints", "[17]"}, model.KindInt, "Fixnum", "-1073741824", true},
		{"2^30", 0, []string{":bigs", "[0]"}, model.KindInt, "Bignum", "1073741824", true},
		{"-2^30-1", 0, []string{":bigs", "[1]"}, model.KindInt, "Bignum", "-1073741825", true},
		{"2^64+5", 0, []string{":bigs", "[2]"}, model.KindInt, "Bignum", "18446744073709551621", true},
		{"-2^100", 0, []string{":bigs", "[3]"}, model.KindInt, "Bignum", "-1267650600228229401496703205376", true},
		{"float 100", 0, []string{":floats", "[0]"}, model.KindFloat, "Float", "100.0", true},
		{"float link", 0, []string{":floats", "[2]"}, model.KindFloat, "Float", "0.25", true},
		{"float inf", 0, []string{":floats", "[7]"}, model.KindFloat, "Float", "inf", true},
		{"float nan", 0, []string{":floats", "[9]"}, model.KindFloat, "Float", "nan", true},
		{"float with NUL", 0, []string{":floats", "[11]"}, model.KindFloat, "Float", "0.0", true},
		{"utf8", 0, []string{":strings", "[0]"}, model.KindString, "String", `"héllo"`, true},
		{"utf8 link", 0, []string{":strings", "[1]"}, model.KindString, "String", `"héllo"`, true},
		{"ascii", 0, []string{":strings", "[2]"}, model.KindString, "String", `"plain"`, true},
		{"shift_jis", 0, []string{":strings", "[3]"}, model.KindString, "String", `"日本語"`, true},
		{"shift_jis again", 0, []string{":strings", "[4]"}, model.KindString, "String", `"勇者"`, true},
		{"latin1", 0, []string{":strings", "[5]"}, model.KindString, "String", `"café"`, true},
		{"no flag is binary text", 0, []string{":strings", "[6]"}, model.KindString, "String", `"legacy"`, true},
		{"binary bytes", 0, []string{":strings", "[7]"}, model.KindBytes, "String", "2 bytes", false},
		{"empty", 0, []string{":strings", "[8]"}, model.KindString, "String", `""`, true},
		{"symbol", 0, []string{":symbols", "[2]"}, model.KindString, "Symbol", `"a"`, false},
		{"color", 1, []string{"@color"}, model.KindObject, "Color", "Color (4 parts)", false},
		{"color red", 1, []string{"@color", "red"}, model.KindFloat, "Float", "255.0", true},
		{"tone gray", 1, []string{"@tone", "gray"}, model.KindFloat, "Float", "0.0", true},
		{"rect height", 1, []string{"@rect", "height"}, model.KindInt, "Integer", "480", true},
		{"table", 1, []string{"@table"}, model.KindObject, "Table", "Table (4 parts)", false},
		{"table xsize", 1, []string{"@table", "xsize"}, model.KindInt, "Integer", "3", false},
		{"table data", 1, []string{"@table", "data"}, model.KindBytes, "int16 cells", "12 bytes", false},
		{"unknown u", 1, []string{"@time"}, model.KindBytes, "Time", "8 bytes", false},
		{"hash value", 1, []string{"@keys", "Array (3 items)"}, model.KindBool, "TrueClass", "true", true},
		{"hash string key", 1, []string{"@keys", `"k"`}, model.KindList, "Array", "Array (0 items)", false},
		{"hash int key", 1, []string{"@keys", "7"}, model.KindNull, "NilClass", "nil", false},
		{"gold", 1, []string{"@actors", "[0]", "@hp"}, model.KindInt, "Fixnum", "450", true},
		{"party", 1, nil, model.KindObject, "Game_Party", "Game_Party (9 parts)", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := at(t, doc, tt.dump, tt.path...)
			if s.Value.Kind != tt.kind || s.Value.Type != tt.typ || s.Value.Repr() != tt.repr {
				t.Errorf("got %v %s %s, want %v %s %s", s.Value.Kind, s.Value.Type, s.Value.Repr(), tt.kind, tt.typ, tt.repr)
			}
			if (s.Site != nil) != tt.editor {
				t.Errorf("site = %v, want a site: %v", s.Site, tt.editor)
			}
		})
	}
}

func TestSiteBounds(t *testing.T) {
	doc := parseFixture(t, "sample.marshal", Options{})
	fix := at(t, doc, 0, ":ints", "[1]").Site
	if fix.IntMin.Int64() != -1<<30 || fix.IntMax.Int64() != 1<<30-1 {
		t.Errorf("Fixnum bounds = %v..%v", fix.IntMin, fix.IntMax)
	}
	if b := at(t, doc, 0, ":bigs", "[0]").Site; b.IntMin != nil || b.IntMax != nil {
		t.Errorf("Bignum bounds = %v..%v, want none", b.IntMin, b.IntMax)
	}
	if r := at(t, doc, 1, "@rect", "x").Site; r.IntMin.Int64() != math.MinInt32 || r.IntMax.Int64() != math.MaxInt32 {
		t.Errorf("Rect bounds = %v..%v", r.IntMin, r.IntMax)
	}
	ascii := at(t, doc, 0, ":strings", "[2]").Site
	sjis := at(t, doc, 0, ":strings", "[3]").Site
	utf := at(t, doc, 0, ":strings", "[0]").Site
	for _, tt := range []struct {
		name string
		site *model.Site
		text string
		ok   bool
	}{
		{"ascii accepts ascii", ascii, "plain text", true},
		{"ascii refuses accents", ascii, "café", false},
		{"shift_jis accepts kana", sjis, "ひらがな", true},
		{"shift_jis refuses emoji", sjis, "😀", false},
		{"utf-8 accepts emoji", utf, "😀", true},
		{"utf-8 refuses invalid", utf, "a\xffb", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.site.CheckText(tt.text)
			if (err == nil) != tt.ok {
				t.Fatalf("CheckText(%q) = %v", tt.text, err)
			}
			if err != nil {
				appErr, ok := errors.FromError(err)
				if !ok || !appErr.HasRoute("textEncoding.check->textEncoding.encode."+TagUnstorableText) {
					t.Errorf("unexpected failure path: %v", err)
				}
			}
		})
	}
}

func TestSharedValues(t *testing.T) {
	doc := parseFixture(t, "sample.marshal", Options{})
	for _, tt := range []struct {
		name string
		a, b model.Slot
	}{
		{"string", at(t, doc, 0, ":strings", "[0]"), at(t, doc, 0, ":strings", "[1]")},
		{"float", at(t, doc, 0, ":floats", "[1]"), at(t, doc, 0, ":floats", "[2]")},
		{"name and class", at(t, doc, 1, "@actors", "[0]", "@name"), at(t, doc, 1, "@actors", "[0]", "@class_name")},
		{"zoom", at(t, doc, 1, "@actors", "[0]", "@zoom"), at(t, doc, 1, "@actors", "[0]", "@zoom2")},
		{"actor", at(t, doc, 1, "@actors", "[0]"), at(t, doc, 1, "@actors", "[1]")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.a.Value != tt.b.Value || tt.a.Site != tt.b.Site {
				t.Errorf("the link is not the same value and site")
			}
		})
	}
	if at(t, doc, 0, ":floats", "[0]").Value == at(t, doc, 0, ":floats", "[1]").Value {
		t.Error("two floats that are different objects are the same value")
	}
}

func TestTypeCodes(t *testing.T) {
	doc := parseFixture(t, "types.marshal", Options{DefaultEncoding: "UTF-8"})
	tests := []struct {
		dump   int
		path   []string
		kind   model.Kind
		typ    string
		repr   string
		editor bool
	}{
		{0, nil, model.KindNull, "NilClass", "nil", false},
		{1, nil, model.KindBool, "TrueClass", "true", true},
		{2, nil, model.KindBool, "FalseClass", "false", true},
		{3, nil, model.KindInt, "Fixnum", "1000000", true},
		{4, []string{"[1]"}, model.KindString, "Symbol", `"x"`, false},
		{4, []string{"[2]"}, model.KindString, "Symbol", `"y"`, false},
		{5, nil, model.KindInt, "Bignum", "5", true},
		{6, nil, model.KindOpaque, "Class", "<Game_Map>", false},
		{7, nil, model.KindOpaque, "Module", "<Comparable>", false},
		{8, nil, model.KindOpaque, "Module", "<Kernel>", false},
		{9, []string{"(data)", "[1]"}, model.KindInt, "Fixnum", "2", true},
		{10, []string{":a"}, model.KindInt, "Fixnum", "1", true},
		{10, []string{"(default)"}, model.KindInt, "Fixnum", "9", true},
		{11, []string{"@y"}, model.KindInt, "Fixnum", "4", true},
		{12, nil, model.KindOpaque, "Regexp", "</a.c/im>", false},
		{13, nil, model.KindString, "String", `"x"`, true},
		{14, []string{"right"}, model.KindString, "String", `"r"`, true},
		{15, nil, model.KindString, "MyString", `"sub"`, true},
		{16, []string{"[0]"}, model.KindInt, "Fixnum", "1", true},
		{17, []string{"data"}, model.KindBytes, "int16 cells", "2 bytes", false},
		{18, []string{"(marshal_dump)", ":k"}, model.KindBool, "TrueClass", "true", true},
		{19, []string{"@a"}, model.KindInt, "Fixnum", "1", true},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.dump)+strings.Join(tt.path, "/"), func(t *testing.T) {
			s := at(t, doc, tt.dump, tt.path...)
			if s.Value.Kind != tt.kind || s.Value.Type != tt.typ || s.Value.Repr() != tt.repr {
				t.Errorf("got %v %s %s, want %v %s %s", s.Value.Kind, s.Value.Type, s.Value.Repr(), tt.kind, tt.typ, tt.repr)
			}
			if (s.Site != nil) != tt.editor {
				t.Errorf("site = %v, want a site: %v", s.Site, tt.editor)
			}
		})
	}
	if got := doc.Dumps[4].Root.Value.Type; got != "Array" {
		t.Errorf("type = %s", got)
	}
	// The numbering in the last dump: an Array (0), a Float (1) and a Hash (2) whose key is
	// a String (3); the value links to 3.
	last := doc.Dumps[len(doc.Dumps)-1].Root
	if len(last.Value.Children) != 3 {
		t.Fatalf("last = %s", last.Value.Repr())
	}
	if last.Value.Children[0].Slot.Value != last.Value.Children[1].Slot.Value {
		t.Error("the Float link is a different value")
	}
	hash := last.Value.Children[2].Slot.Value
	if len(hash.Children) != 1 || hash.Children[0].Label != `"key"` || hash.Children[0].Slot.Value.Str != "key" {
		t.Errorf("hash = %+v", hash.Children)
	}
}

func TestRoundTripWithoutEdits(t *testing.T) {
	for _, name := range []string{"sample.marshal", "types.marshal"} {
		t.Run(name, func(t *testing.T) {
			doc := parseFixture(t, name, Options{})
			got, err := doc.Encode(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, doc.Data) {
				t.Error("Encode with no edits changed the bytes")
			}
		})
	}
}

// goTree is the tree of the doc in the form of the comparison with Python.
func goTree(t *testing.T, doc *Doc) []any {
	t.Helper()
	var roots []*model.Value
	for _, d := range doc.Dumps {
		roots = append(roots, d.Root.Value)
	}
	return rmtest.Tree(t, roots)
}

func TestSampleAgainstPython(t *testing.T) {
	py := rmtest.Python(t)
	file, err := filepath.Abs(filepath.Join("testdata", "sample.marshal"))
	if err != nil {
		t.Fatal(err)
	}
	want := rmtest.PythonTrees(t, py, file)[file]
	got := goTree(t, parseFixture(t, "sample.marshal", Options{}))
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", " ")
		w, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("the tree differs from the Python reader's\n got: %s\nwant: %s", g, w)
	}
}

// flatten lists the leaves of a dump by path; a shared value is listed at every place.
func flatten(doc *Doc) (paths []string, values map[string]*model.Value) {
	values = map[string]*model.Value{}
	var walk func(prefix string, v *model.Value)
	walk = func(prefix string, v *model.Value) {
		if !v.HasChildren() {
			paths = append(paths, prefix)
			values[prefix] = v
			return
		}
		for _, c := range v.Children {
			walk(prefix+"/"+c.Label, c.Slot.Value)
		}
	}
	for i, d := range doc.Dumps {
		walk(strconv.Itoa(i), d.Root.Value)
	}
	return paths, values
}

func leafRepr(v *model.Value) string {
	if v.Kind == model.KindFloat {
		return strconv.FormatUint(math.Float64bits(v.Float), 16)
	}
	return v.Type + ":" + v.Repr()
}

type editCase struct {
	name  string
	dump  int
	path  []string
	value *model.Value
	// kindAfter is the Type the value must have after the edit (it must not change).
	typeAfter string
}

func intV(n *big.Int) *model.Value { return &model.Value{Kind: model.KindInt, Int: n} }
func bigV(s string) *model.Value {
	n, _ := new(big.Int).SetString(s, 10)
	return intV(n)
}
func strV(s string) *model.Value    { return &model.Value{Kind: model.KindString, Str: s} }
func floatV(f float64) *model.Value { return &model.Value{Kind: model.KindFloat, Float: f} }
func boolV(b bool) *model.Value     { return &model.Value{Kind: model.KindBool, Bool: b} }

var editCases = []editCase{
	{"flip true", 0, []string{":nils", "[1]"}, boolV(false), "FalseClass"},
	{"flip false", 0, []string{":nils", "[2]"}, boolV(true), "TrueClass"},
	{"fixnum bigger", 0, []string{":ints", "[1]"}, bigV("100000"), "Fixnum"},
	{"fixnum smaller", 0, []string{":ints", "[9]"}, bigV("-7"), "Fixnum"},
	{"fixnum to 2^30-1", 0, []string{":ints", "[0]"}, bigV("1073741823"), "Fixnum"},
	{"fixnum to -2^30", 0, []string{":ints", "[0]"}, bigV("-1073741824"), "Fixnum"},
	{"bignum to small stays bignum", 0, []string{":bigs", "[0]"}, bigV("3"), "Bignum"},
	{"bignum to zero", 0, []string{":bigs", "[2]"}, bigV("0"), "Bignum"},
	{"bignum to larger negative", 0, []string{":bigs", "[3]"}, bigV("-340282366920938463463374607431768211456"), "Bignum"},
	{"float 100", 0, []string{":floats", "[0]"}, floatV(0.1), "Float"},
	{"float shared", 0, []string{":floats", "[1]"}, floatV(2.5e-9), "Float"},
	{"float to inf", 0, []string{":floats", "[3]"}, floatV(math.Inf(-1)), "Float"},
	{"float to nan", 0, []string{":floats", "[3]"}, floatV(math.NaN()), "Float"},
	{"float whole", 0, []string{":floats", "[5]"}, floatV(1500), "Float"},
	{"float large", 0, []string{":floats", "[5]"}, floatV(123456789012345680000), "Float"},
	{"float negative zero", 0, []string{":floats", "[5]"}, floatV(math.Copysign(0, -1)), "Float"},
	{"string longer", 0, []string{":strings", "[0]"}, strV("a much longer text, with ünïcode ✓"), "String"},
	{"string empty", 0, []string{":strings", "[0]"}, strV(""), "String"},
	{"string from empty", 0, []string{":strings", "[8]"}, strV("now filled"), "String"},
	{"string ascii", 0, []string{":strings", "[2]"}, strV("other"), "String"},
	{"string shift_jis", 0, []string{":strings", "[3]"}, strV("カタカナと漢字"), "String"},
	{"string latin1", 0, []string{":strings", "[5]"}, strV("naïve"), "String"},
	{"string 1.8 style", 0, []string{":strings", "[6]"}, strV("日本"), "String"},
	{"string 200 bytes", 0, []string{":strings", "[2]"}, strV(strings.Repeat("x", 200)), "String"},
	{"name shared by an object", 1, []string{"@actors", "[0]", "@name"}, strV("Erika"), "String"},
	{"object field", 1, []string{"@actors", "[1]", "@hp"}, bigV("9999"), "Fixnum"},
	{"color", 1, []string{"@color", "green"}, floatV(12.5), "Float"},
	{"tone", 1, []string{"@tone", "gray"}, floatV(255), "Float"},
	{"rect", 1, []string{"@rect", "width"}, bigV("-1"), "Integer"},
	{"hash value", 1, []string{"@keys", "Array (3 items)"}, boolV(false), "FalseClass"},
}

func TestEditEachKind(t *testing.T) {
	for _, tt := range editCases {
		t.Run(tt.name, func(t *testing.T) {
			before := parseFixture(t, "sample.marshal", Options{})
			target := at(t, before, tt.dump, tt.path...)
			out, err := before.Encode([]model.Edit{{Site: target.Site, Value: tt.value}})
			if err != nil {
				t.Fatal(err)
			}
			after, err := Parse(out, Options{})
			if err != nil {
				t.Fatalf("the edited file does not parse: %v", err)
			}
			if len(after.Dumps) != len(before.Dumps) {
				t.Fatalf("dumps = %d", len(after.Dumps))
			}
			pathsBefore, valuesBefore := flatten(before)
			pathsAfter, valuesAfter := flatten(after)
			if !reflect.DeepEqual(pathsBefore, pathsAfter) {
				t.Fatal("the shape of the tree changed")
			}
			changed := 0
			for _, p := range pathsBefore {
				was, now := valuesBefore[p], valuesAfter[p]
				if was == target.Value {
					changed++
					if now.Kind != tt.value.Kind || (tt.typeAfter != "" && now.Type != tt.typeAfter) {
						t.Errorf("%s: kind or type changed: %v %s", p, now.Kind, now.Type)
					}
					switch tt.value.Kind {
					case model.KindString:
						if now.Str != tt.value.Str {
							t.Errorf("%s = %q, want %q", p, now.Str, tt.value.Str)
						}
					case model.KindInt:
						if now.Int.Cmp(tt.value.Int) != 0 {
							t.Errorf("%s = %v", p, now.Int)
						}
					case model.KindBool:
						if now.Bool != tt.value.Bool {
							t.Errorf("%s = %v", p, now.Bool)
						}
					case model.KindFloat:
						if math.Float64bits(now.Float) != math.Float64bits(tt.value.Float) && !(math.IsNaN(now.Float) && math.IsNaN(tt.value.Float)) {
							t.Errorf("%s = %v, want %v", p, now.Float, tt.value.Float)
						}
					}
				} else if leafRepr(was) != leafRepr(now) {
					t.Errorf("%s changed: %s -> %s", p, leafRepr(was), leafRepr(now))
				}
			}
			if changed == 0 {
				t.Error("the edited value is not in the tree")
			}
			// The same edit again, to the new file, must give the same value (the sites of
			// the new file are different but the object numbering is the same).
			if tt.name == "float shared" {
				if now := at(t, after, 0, ":floats", "[2]").Value.Float; now != tt.value.Float {
					t.Errorf("the link reads %v", now)
				}
			}
		})
	}
}

func TestEditedFilesAgainstPython(t *testing.T) {
	py := rmtest.Python(t)
	dir := t.TempDir()
	for _, tt := range editCases {
		t.Run(tt.name, func(t *testing.T) {
			doc := parseFixture(t, "sample.marshal", Options{})
			target := at(t, doc, tt.dump, tt.path...)
			out, err := doc.Encode([]model.Edit{{Site: target.Site, Value: tt.value}})
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, strings.ReplaceAll(tt.name, " ", "_")+".marshal")
			if err := os.WriteFile(file, out, 0o600); err != nil {
				t.Fatal(err)
			}
			want := rmtest.PythonTrees(t, py, file)[file]
			parsed, err := Parse(out, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if got := goTree(t, parsed); !reflect.DeepEqual(got, want) {
				t.Errorf("Python reads the edited file differently from Go")
			}
		})
	}
}

func TestSeveralEditsAtOnce(t *testing.T) {
	doc := parseFixture(t, "sample.marshal", Options{})
	edits := []model.Edit{
		{Site: at(t, doc, 1, "@actors", "[0]", "@name").Site, Value: strV("Ann")},
		{Site: at(t, doc, 0, ":ints", "[2]").Site, Value: bigV("5")},
		{Site: at(t, doc, 1, "@gold").Site, Value: bigV("77")},
		{Site: at(t, doc, 0, ":bigs", "[4]").Site, Value: bigV("-1")},
		{Site: at(t, doc, 0, ":nils", "[1]").Site, Value: boolV(false)},
	}
	out, err := doc.Encode(edits)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Parse(out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := at(t, after, 1, "@actors", "[1]", "@class_name").Value.Str; got != "Ann" {
		t.Errorf("class_name = %q", got)
	}
	if got := at(t, after, 1, "@gold").Value.Int.Int64(); got != 77 {
		t.Errorf("gold = %d", got)
	}
	if got := at(t, after, 0, ":bigs", "[4]").Value.Int.Int64(); got != -1 {
		t.Errorf("big = %d", got)
	}
	// Doing it in the other order gives the same file.
	slicesReverse(edits)
	again, err := doc.Encode(edits)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, again) {
		t.Error("the order of the edits changes the file")
	}
}

func slicesReverse(e []model.Edit) {
	for i, j := 0, len(e)-1; i < j; i, j = i+1, j-1 {
		e[i], e[j] = e[j], e[i]
	}
}

func TestEncodeFailures(t *testing.T) {
	doc := parseFixture(t, "sample.marshal", Options{})
	other := parseFixture(t, "sample.marshal", Options{})
	fix := at(t, doc, 0, ":ints", "[1]").Site
	ascii := at(t, doc, 0, ":strings", "[2]").Site
	rect := at(t, doc, 1, "@rect", "x").Site
	str := at(t, doc, 0, ":strings", "[0]").Site
	big0 := at(t, doc, 0, ":bigs", "[0]").Site
	tests := []struct {
		name  string
		edits []model.Edit
		route string
	}{
		{"no site", []model.Edit{{Value: boolV(true)}}, "Doc.Encode." + TagForeignSite},
		{"a site of another document", []model.Edit{{Site: at(t, other, 0, ":ints", "[1]").Site, Value: bigV("1")}}, "Doc.Encode." + TagForeignSite},
		{"a site of nobody", []model.Edit{{Site: &model.Site{ID: 1}, Value: bigV("1")}}, "Doc.Encode." + TagForeignSite},
		{"the same value twice", []model.Edit{{Site: fix, Value: bigV("1")}, {Site: fix, Value: bigV("2")}}, "Doc.Encode." + TagDuplicateEdit},
		{"nil value", []model.Edit{{Site: fix}}, "Doc.Encode." + TagWrongKind},
		{"string for a number", []model.Edit{{Site: fix, Value: strV("1")}}, "Doc.Encode." + TagWrongKind},
		{"float for a string", []model.Edit{{Site: str, Value: floatV(1)}}, "Doc.Encode." + TagWrongKind},
		{"int without a number", []model.Edit{{Site: fix, Value: &model.Value{Kind: model.KindInt}}}, "Doc.Encode." + TagWrongKind},
		{"fixnum above 2^30-1", []model.Edit{{Site: fix, Value: bigV("1073741824")}}, "Doc.Encode." + TagOutOfRange},
		{"fixnum below -2^30", []model.Edit{{Site: fix, Value: bigV("-1073741825")}}, "Doc.Encode." + TagOutOfRange},
		{"rect above int32", []model.Edit{{Site: rect, Value: bigV("2147483648")}}, "Doc.Encode." + TagOutOfRange},
		{"ascii string with accents", []model.Edit{{Site: ascii, Value: strV("é")}}, "Doc.Encode->textEncoding.encode." + TagUnstorableText},
		{"bignum is a number", []model.Edit{{Site: big0, Value: boolV(true)}}, "Doc.Encode." + TagWrongKind},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

func TestParseFailures(t *testing.T) {
	h := "\x04\x08"
	tests := []struct {
		name  string
		data  string
		opts  Options
		route string
	}{
		{"empty", "", Options{}, "Parse." + TagNoDump},
		{"no header", "x", Options{}, "Parse." + TagBadHeader},
		{"wrong major", "\x03\x08i\x06", Options{}, "Parse." + TagBadHeader},
		{"newer minor", "\x04\x09i\x06", Options{}, "Parse." + TagBadHeader},
		{"header only", h, Options{}, "Parse->reader.value->reader.valueP->reader.valueCase->reader.byte." + TagTruncated},
		{"garbage after a dump", h + "i\x06" + "zz", Options{}, "Parse." + TagBadHeader},
		{"unknown type", h + "Z", Options{}, "Parse->reader.value->reader.valueP->reader.valueCase." + TagBadType},
		{"fixnum cut", h + "i\x02\x01", Options{}, "reader.long->reader.bytes." + TagTruncated},
		{"string cut", h + "\"\x0aabc", Options{}, "reader.length." + TagBadLength},
		{"array longer than file", h + "[\x7f", Options{}, "reader.length." + TagBadLength},
		{"negative length", h + "\"\xfa", Options{}, "reader.length." + TagBadLength},
		{"object link out of range", h + "[\x06@\x07", Options{}, "reader.valueCase." + TagBadLink},
		{"link to the future", h + "[\x06@\x06", Options{}, "reader.valueCase." + TagBadLink},
		{"symbol link out of range", h + ";\x06", Options{}, "reader.symbol." + TagBadLink},
		{"class name not a symbol", h + "o\"\x06a\x00", Options{}, "reader.symbol." + TagBadType},
		{"bignum without sign", h + "l?\x06\x00\x00", Options{}, "reader.bignum." + TagBadBignum},
		{"bignum too long", h + "l+\x7f\x00", Options{}, "reader.bignum." + TagBadLength},
		{"float of text", h + "f\x08abc", Options{}, "reader.float." + TagBadFloat},
		{"float of words", h + "f\x09Infinity", Options{}, "reader.float." + TagBadFloat},
		{"too deep", h + strings.Repeat("[\x06", maxDepth+10) + "0", Options{}, "reader.valueP." + TagTooDeep},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.data), tt.opts)
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

func TestMaxDumps(t *testing.T) {
	doc := parseFixture(t, "sample.marshal", Options{MaxDumps: 2})
	if len(doc.Dumps) != 2 || doc.Rest != doc.Dumps[1].End || doc.Rest >= len(doc.Data) {
		t.Errorf("dumps = %d, rest = %d", len(doc.Dumps), doc.Rest)
	}
}

func TestLongRoundTrip(t *testing.T) {
	values := []int{0, 1, 122, 123, 124, 255, 256, 257, 65535, 65536, 1<<24 - 1, 1 << 24, 1<<30 - 1, 1<<31 - 1,
		-1, -2, -123, -124, -125, -255, -256, -257, -65536, -65537, -(1 << 24), -(1 << 30), -(1 << 31)}
	for _, n := range values {
		enc := appendLong(nil, n)
		r := &reader{data: enc}
		got, err := r.long()
		if err != nil || got != n || r.pos != len(enc) {
			t.Errorf("long(%d): %x -> %d, %v", n, enc, got, err)
		}
	}
	// Examples of marshal.rdoc.
	for n, want := range map[int]string{0: "\x00", 1: "\x06", 122: "\x7f", 123: "\x01\x7b", 255: "\x01\xff", 256: "\x02\x00\x01",
		65535: "\x02\xff\xff", 65536: "\x03\x00\x00\x01", -1: "\xfa", -123: "\x80", -124: "\xff\x84", -256: "\xff\x00",
		-257: "\xfe\xff\xfe", -65536: "\xfe\x00\x00", -65537: "\xfd\xff\xff\xfe"} {
		if got := string(appendLong(nil, n)); got != want {
			t.Errorf("appendLong(%d) = %x, want %x", n, got, want)
		}
	}
}

func TestFloatText(t *testing.T) {
	for _, tt := range []struct {
		f    float64
		text string
	}{
		{0, "0"}, {math.Copysign(0, -1), "-0"}, {1, "1"}, {-1, "-1"}, {100, "1e2"}, {123, "123"}, {1500, "1.5e3"},
		{0.5, "0.5"}, {0.25, "0.25"}, {0.0001, "0.0001"}, {0.00001, "1e-5"}, {1.5e-7, "1.5e-7"}, {3.14159, "3.14159"},
		{1234567.5, "1234567.5"}, {1e100, "1e100"}, {1.7976931348623157e308, "1.7976931348623157e308"},
		{5e-324, "5e-324"}, {math.Inf(1), "inf"}, {math.Inf(-1), "-inf"}, {math.NaN(), "nan"}, {0.1, "0.1"},
		{12345678901234567890, "1.2345678901234567e19"},
	} {
		got := formatFloat(tt.f)
		if got != tt.text {
			t.Errorf("formatFloat(%v) = %q, want %q", tt.f, got, tt.text)
		}
		back, ok := parseFloat([]byte(got))
		if !ok || (back != tt.f && !math.IsNaN(tt.f)) {
			t.Errorf("parseFloat(%q) = %v, %v", got, back, ok)
		}
	}
}

func TestBignumText(t *testing.T) {
	for _, s := range []string{"0", "1", "-1", "65535", "65536", "4294967296", "-4294967296", "18446744073709551616",
		"1606938044258990275541962092341162602522202993782792835301376"} {
		n, _ := new(big.Int).SetString(s, 10)
		enc := appendBignum(n)
		r := &reader{doc: &Doc{}, data: enc[1:], pos: 0}
		slot, _, err := r.bignum(0)
		if err != nil || slot.Value.Int.Cmp(n) != 0 {
			t.Errorf("bignum(%s): %x -> %v, %v", s, enc, slot, err)
		}
	}
	if got := hexs(appendBignum(big.NewInt(0))); got != "6c2b060000" {
		t.Errorf("zero = %s", got)
	}
}

func TestLookupEncoding(t *testing.T) {
	for _, tt := range []struct {
		name string
		kind encKind
	}{
		{"UTF-8", encUTF8}, {"utf-8", encUTF8}, {"US-ASCII", encASCII}, {"ASCII-8BIT", encBinary}, {"BINARY", encBinary},
		{"Shift_JIS", encCharset}, {"Windows-31J", encCharset}, {"EUC-JP", encCharset}, {"Windows-1252", encCharset},
		{"KOI8-R", encUnknown},
	} {
		if got := lookupEncoding(tt.name); got.kind != tt.kind || got.Name() == "" {
			t.Errorf("lookupEncoding(%s) = %v", tt.name, got.kind)
		}
	}
	// Bytes that are not text of the encoding are bytes, not a guess.
	sjis := lookupEncoding("Shift_JIS")
	if _, ok := sjis.decode([]byte{0x81}); ok {
		t.Error("a cut Shift_JIS character decoded")
	}
	if _, err := lookupEncoding("KOI8-R").encode("x"); err == nil {
		t.Error("an unknown encoding stored text")
	}
}

func TestDefaultEncoding(t *testing.T) {
	// "I" with no encoding in a Ruby 1.8 file: text in UTF-8 by convention; a lone byte is bytes.
	data := []byte("\x04\x08[\x07\"\x08\xe2\x82\xac\"\x06\xff")
	for _, enc := range []string{"", "UTF-8"} {
		doc, err := Parse(data, Options{DefaultEncoding: enc})
		if err != nil {
			t.Fatal(err)
		}
		if got := doc.Dumps[0].Root.Value.Children[0].Slot.Value.Str; got != "€" {
			t.Errorf("%q: text = %q", enc, got)
		}
		if got := doc.Dumps[0].Root.Value.Children[1].Slot.Value; got.Kind != model.KindBytes {
			t.Errorf("%q: kind = %v", enc, got.Kind)
		}
	}
	doc, err := Parse(data, Options{DefaultEncoding: "US-ASCII"})
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Dumps[0].Root.Value.Children[0].Slot.Value.Kind; got != model.KindBytes {
		t.Errorf("a euro sign in US-ASCII is %v", got)
	}
}

// A Ruby 1.9 object index counts the encoding name Strings, and 'u' data after its own
// instance variables: a link after both must reach the right object.
func TestObjectNumbering(t *testing.T) {
	// [ "a"(Shift_JIS) 'u' Time with E=false, "b" (Shift_JIS, encoding name linked), @6 ]
	// objects: 0 array, 1 "a", 2 "Shift_JIS", 3 Time (after its ivars), 4 "b", then @4 is "b".
	var b bytes.Buffer
	b.WriteString("\x04\x08[\x0b")
	b.WriteString("I\"\x06a\x06:\rencoding\"\x0eShift_JIS")
	b.WriteString("Iu:\tTime\x06x\x06:\x06EF")
	b.WriteString("I\"\x06b\x06;\x00@\x07")
	b.WriteString("@\x09@\x09")
	b.WriteString("0")
	doc, err := Parse(b.Bytes(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	items := doc.Dumps[0].Root.Value.Children
	if len(items) != 6 {
		t.Fatalf("items = %d", len(items))
	}
	if items[2].Slot.Value.Str != "b" || items[3].Slot.Value != items[2].Slot.Value {
		t.Errorf("link: %v / %v", items[2].Slot.Value.Repr(), items[3].Slot.Value.Repr())
	}
}

func hexs(b []byte) string { return fmt.Sprintf("%x", b) }
