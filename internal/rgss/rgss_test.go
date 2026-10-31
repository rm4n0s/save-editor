package rgss

import (
	"bytes"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
	"github.com/rm4n0s/save-editor/internal/rubymarshal"
	"github.com/rm4n0s/save-editor/internal/rubymarshal/rmtest"
)

var fixtures = []struct {
	file   string
	format model.Format
	vars   []string
}{
	{"xp.rxdata", FormatXP, []string{"characters", "Graphics.frame_count", "$game_system", "$game_switches",
		"$game_variables", "$game_self_switches", "$game_screen", "$game_actors", "$game_party", "$game_troop",
		"$game_map", "$game_player"}},
	{"xp-extra.rxdata", FormatXP, []string{"characters", "Graphics.frame_count", "$game_system", "$game_switches",
		"$game_variables", "$game_self_switches", "$game_screen", "$game_actors", "$game_party", "$game_troop",
		"$game_map", "$game_player", "extra 1", "extra 2"}},
	{"vx.rvdata", FormatVX, []string{"characters", "Graphics.frame_count", "last_bgm", "last_bgs", "$game_system",
		"$game_message", "$game_switches", "$game_variables", "$game_self_switches", "$game_actors", "$game_party",
		"$game_troop", "$game_map", "$game_player"}},
	{"ace.rvdata2", FormatVXAce, []string{"header", "$game_system", "$game_timer", "$game_message", "$game_switches",
		"$game_variables", "$game_self_switches", "$game_actors", "$game_party", "$game_troop", "$game_map",
		"$game_player", "contents[:custom]"}},
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func openFixture(t *testing.T, file string) (*Document, model.Format) {
	t.Helper()
	for _, f := range fixtures {
		if f.file == file {
			doc, err := f.format.Parse(file, readFixture(t, file))
			if err != nil {
				t.Fatalf("Parse(%s): %v", file, err)
			}
			return doc.(*Document), f.format
		}
	}
	t.Fatalf("no fixture %s", file)
	return nil, model.Format{}
}

// find follows labels from the variable called name.
func find(t *testing.T, d *Document, name string, labels ...string) model.Slot {
	t.Helper()
	var s model.Slot
	found := false
	for _, v := range d.Variables() {
		if v.Name == name {
			s, found = v.Slot, true
		}
	}
	if !found {
		t.Fatalf("no variable %q", name)
	}
	for _, l := range labels {
		ok := false
		for _, c := range s.Value.Children {
			if c.Label == l {
				s, ok = c.Slot, true
				break
			}
		}
		if !ok {
			t.Fatalf("no child %q under %s", l, name)
		}
	}
	return s
}

func TestDetect(t *testing.T) {
	for _, fx := range fixtures {
		data := readFixture(t, fx.file)
		for _, f := range Formats {
			want := f.Name == fx.format.Name
			if got := f.Detect(fx.file, data); got != want {
				t.Errorf("%s: %s.Detect = %v, want %v", fx.file, f.Name, got, want)
			}
			// The contents decide, not the name.
			if got := f.Detect("Save1.dat", data); got != want {
				t.Errorf("%s as Save1.dat: %s.Detect = %v, want %v", fx.file, f.Name, got, want)
			}
		}
	}
	xp := readFixture(t, "xp.rxdata")
	renamed := bytes.Replace(xp, []byte("Game_System"), []byte("Game_Systen"), 1)
	tests := []struct {
		name string
		file string
		data []byte
		want map[string]bool
	}{
		{"nothing", "a.rxdata", nil, nil},
		{"text", "a.rxdata", []byte("hello"), nil},
		{"a header only", "a.rxdata", []byte("\x04\x08"), nil},
		{"a save cut in the third dump", "a.rxdata", xp[:60], nil},
		{"one dump", "Actors.rxdata", []byte("\x04\x08[\x00"), nil},
		{"a script changed the order, by name", "Save1.rxdata", renamed, map[string]bool{"RPG Maker XP": true}},
		{"a script changed the order, by name", "Save1.RVDATA", renamed, map[string]bool{"RPG Maker VX": true}},
		{"a script changed the order, other name", "Save1.dat", renamed, nil},
		{"two hashes by name", "Save1.rvdata2", []byte("\x04\x08{\x00\x04\x08{\x00"), map[string]bool{"RPG Maker VX Ace": true}},
		{"two hashes, other name", "Save1.rxdata", []byte("\x04\x08{\x00\x04\x08{\x00"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name+" "+tt.file, func(t *testing.T) {
			for _, f := range Formats {
				if got := f.Detect(tt.file, tt.data); got != tt.want[f.Name] {
					t.Errorf("%s.Detect = %v", f.Name, got)
				}
			}
		})
	}
}

func TestFormats(t *testing.T) {
	for i, want := range [][2]string{{"RPG Maker XP", "*.rxdata"}, {"RPG Maker VX", "*.rvdata"}, {"RPG Maker VX Ace", "*.rvdata2"}} {
		if Formats[i].Name != want[0] || !reflect.DeepEqual(Formats[i].Patterns, []string{want[1]}) {
			t.Errorf("Formats[%d] = %s %v", i, Formats[i].Name, Formats[i].Patterns)
		}
	}
}

func TestVariables(t *testing.T) {
	for _, fx := range fixtures {
		t.Run(fx.file, func(t *testing.T) {
			d, _ := openFixture(t, fx.file)
			var names []string
			internal := map[string]bool{}
			for _, v := range d.Variables() {
				names = append(names, v.Name)
				internal[v.Name] = v.Internal
			}
			if !reflect.DeepEqual(names, fx.vars) {
				t.Errorf("variables = %q, want %q", names, fx.vars)
			}
			for _, n := range []string{"characters", "last_bgm", "last_bgs", "$game_message", "header"} {
				if got, has := internal[n]; has && !got {
					t.Errorf("%s is not internal", n)
				}
			}
			for _, n := range []string{"$game_switches", "$game_variables", "$game_party", "extra 1"} {
				if internal[n] {
					t.Errorf("%s is internal", n)
				}
			}
		})
	}
}

func TestGameValues(t *testing.T) {
	xp, _ := openFixture(t, "xp.rxdata")
	ace, _ := openFixture(t, "ace.rvdata2")
	tests := []struct {
		name   string
		doc    *Document
		v      string
		labels []string
		kind   model.Kind
		typ    string
		repr   string
	}{
		{"switch", xp, "$game_switches", []string{"@data", "[1]"}, model.KindBool, "TrueClass", "true"},
		{"variable bignum", xp, "$game_variables", []string{"@data", "[3]"}, model.KindInt, "Bignum", "1099511627776"},
		{"variable negative", xp, "$game_variables", []string{"@data", "[4]"}, model.KindInt, "Fixnum", "-5"},
		{"gold", xp, "$game_party", []string{"@gold"}, model.KindInt, "Fixnum", "1234"},
		{"actor name", xp, "$game_actors", []string{"@data", "[1]", "@name"}, model.KindString, "String", `"Aluxes"`},
		{"zoom", xp, "$game_screen", []string{"@pictures", "[1]", "@zoom_x"}, model.KindFloat, "Float", "100.0"},
		{"tone", xp, "$game_screen", []string{"@tone", "green"}, model.KindFloat, "Float", "-34.0"},
		{"self switch", xp, "$game_self_switches", []string{"@data", "Array (3 items)"}, model.KindBool, "TrueClass", "true"},
		{"frame count", xp, "Graphics.frame_count", nil, model.KindInt, "Fixnum", "144000"},
		{"ace nickname", ace, "$game_actors", []string{"@data", "[1]", "@nickname"}, model.KindString, "String", `"勇者"`},
		{"ace name", ace, "$game_actors", []string{"@data", "[2]", "@name"}, model.KindString, "String", `"ラルフ"`},
		{"ace quest", ace, "contents[:custom]", []string{":quest"}, model.KindString, "String", `"Find the key"`},
		{"ace header playtime", ace, "header", []string{":playtime_s"}, model.KindString, "String", `"01:00:00"`},
		{"ace table", ace, "$game_map", []string{"@map", "@data"}, model.KindObject, "Table", "Table (4 parts)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := find(t, tt.doc, tt.v, tt.labels...)
			if s.Value.Kind != tt.kind || s.Value.Type != tt.typ || s.Value.Repr() != tt.repr {
				t.Errorf("got %v %s %s", s.Value.Kind, s.Value.Type, s.Value.Repr())
			}
		})
	}
	// Strings that the dump shares are one value with one site.
	a := find(t, xp, "$game_actors", "@data", "[1]", "@character_name")
	b := find(t, xp, "$game_actors", "@data", "[2]", "@character_name")
	if a.Value != b.Value || a.Site == nil || a.Site != b.Site {
		t.Error("the shared graphic name is not one value")
	}
	a = find(t, ace, "$game_actors", "@data", "[1]", "@character_name")
	b = find(t, ace, "$game_actors", "@data", "[2]", "@character_name")
	if a.Value != b.Value || a.Site == nil || a.Site != b.Site {
		t.Error("the shared graphic name of Ace is not one value")
	}
	if l := find(t, ace, "$game_party", "@leader"); l.Value != find(t, ace, "$game_actors", "@data", "[1]").Value {
		t.Error("the leader is not the first actor")
	}
}

func TestDetails(t *testing.T) {
	tests := []struct {
		file string
		want map[string]string
	}{
		{"xp.rxdata", map[string]string{"Engine": "RPG Maker XP", "Play time": "1:00:00",
			"Party": "2: 001-Fighter01, 002-Fighter02", "Saved": "7 times"}},
		{"xp-extra.rxdata", map[string]string{"Extra data": "2 more dump(s) written by the game's scripts"}},
		{"vx.rvdata", map[string]string{"Engine": "RPG Maker VX", "Play time": "1:00:00",
			"Party": "2: Actor1, Actor2", "Saved": "3 times"}},
		{"ace.rvdata2", map[string]string{"Engine": "RPG Maker VX Ace", "Play time": "01:00:00",
			"Party": "2: Actor1, Actor1", "Saved": "12 times", "Extra data": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			d, _ := openFixture(t, tt.file)
			got := map[string]model.Detail{}
			for _, det := range d.Details() {
				got[det.Label] = det
			}
			for label, text := range tt.want {
				det, ok := got[label]
				if label == "Extra data" && text == "" {
					// Ace's contents keys are not extra dumps.
					if ok {
						t.Errorf("Extra data = %q", det.Text)
					}
					continue
				}
				if !ok || det.Text != text {
					t.Errorf("%s = %q, want %q", label, det.Text, text)
				}
			}
		})
	}
	d, _ := openFixture(t, "xp.rxdata")
	for _, det := range d.Details() {
		if det.Label == "Play time" && !strings.Contains(det.Tooltip, "144000 frames at 40") {
			t.Errorf("tooltip = %q", det.Tooltip)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, fx := range fixtures {
		t.Run(fx.file, func(t *testing.T) {
			d, _ := openFixture(t, fx.file)
			got, err := d.Encode(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, readFixture(t, fx.file)) {
				t.Error("Encode with no edits changed the file")
			}
		})
	}
}

func TestAgainstPython(t *testing.T) {
	py := rmtest.Python(t)
	for _, fx := range fixtures {
		t.Run(fx.file, func(t *testing.T) {
			file, err := filepath.Abs(filepath.Join("testdata", fx.file))
			if err != nil {
				t.Fatal(err)
			}
			d, _ := openFixture(t, fx.file)
			want := rmtest.PythonTrees(t, py, file)[file]
			if got := tree(t, d); !reflect.DeepEqual(got, want) {
				t.Errorf("the tree differs from the Python reader's")
			}
		})
	}
}

func tree(t *testing.T, d *Document) []any {
	t.Helper()
	var roots []*model.Value
	for _, dump := range d.doc.Dumps {
		roots = append(roots, dump.Root.Value)
	}
	return rmtest.Tree(t, roots)
}

func big10(s string) *big.Int {
	n, _ := new(big.Int).SetString(s, 10)
	return n
}

func TestEdits(t *testing.T) {
	str := func(s string) *model.Value { return &model.Value{Kind: model.KindString, Str: s} }
	num := func(s string) *model.Value { return &model.Value{Kind: model.KindInt, Int: big10(s)} }
	flt := func(f float64) *model.Value { return &model.Value{Kind: model.KindFloat, Float: f} }
	boolean := func(b bool) *model.Value { return &model.Value{Kind: model.KindBool, Bool: b} }
	tests := []struct {
		name   string
		file   string
		v      string
		labels []string
		value  *model.Value
		// then reads the edited document and returns a problem, or "".
		then func(t *testing.T, d *Document) string
	}{
		{"switch off", "xp.rxdata", "$game_switches", []string{"@data", "[1]"}, boolean(false), nil},
		{"variable bignum small", "xp.rxdata", "$game_variables", []string{"@data", "[3]"}, num("7"), nil},
		{"variable bignum huge", "xp.rxdata", "$game_variables", []string{"@data", "[3]"}, num("-123456789012345678901234567890"), nil},
		{"variable fixnum", "xp.rxdata", "$game_variables", []string{"@data", "[2]"}, num("1073741823"), nil},
		{"gold", "xp.rxdata", "$game_party", []string{"@gold"}, num("99999"), nil},
		{"frame count", "xp.rxdata", "Graphics.frame_count", nil, num("288000"), func(t *testing.T, d *Document) string {
			for _, det := range d.Details() {
				if det.Label == "Play time" && det.Text != "2:00:00" {
					return "play time " + det.Text
				}
			}
			return ""
		}},
		{"actor name, shared nowhere", "xp.rxdata", "$game_actors", []string{"@data", "[1]", "@name"}, str("Aluxes the Brave"), nil},
		{"shared graphic", "xp.rxdata", "$game_actors", []string{"@data", "[1]", "@character_name"}, str("003-Fighter03"), func(t *testing.T, d *Document) string {
			if got := find(t, d, "$game_actors", "@data", "[2]", "@character_name").Value.Str; got != "003-Fighter03" {
				return "the link reads " + got
			}
			return ""
		}},
		{"zoom shared", "xp.rxdata", "$game_screen", []string{"@pictures", "[1]", "@zoom_x"}, flt(150.5), func(t *testing.T, d *Document) string {
			if got := find(t, d, "$game_screen", "@pictures", "[1]", "@zoom_y").Value.Float; got != 150.5 {
				return "the link reads " + big.NewFloat(got).String()
			}
			return ""
		}},
		{"tone", "xp.rxdata", "$game_screen", []string{"@tone", "red"}, flt(-255), nil},
		{"self switch", "xp.rxdata", "$game_self_switches", []string{"@data", "Array (3 items)"}, boolean(false), nil},
		{"extra string", "xp-extra.rxdata", "extra 2", nil, str("a longer extra than before"), nil},
		{"extra hash", "xp-extra.rxdata", "extra 1", []string{`"quest"`}, num("4"), nil},
		{"vx bgm", "vx.rvdata", "last_bgm", []string{"@volume"}, num("55"), nil},
		{"vx name", "vx.rvdata", "$game_actors", []string{"@data", "[2]", "@name"}, str("バジル"), nil},
		{"ace utf-8 name", "ace.rvdata2", "$game_actors", []string{"@data", "[1]", "@name"}, str("Erik the Red 🐉"), nil},
		{"ace shift_jis nickname", "ace.rvdata2", "$game_actors", []string{"@data", "[1]", "@nickname"}, str("ひらがなとカタカナ"), func(t *testing.T, d *Document) string {
			// The second nickname reads the encoding name through a link: it must be unharmed.
			if got := find(t, d, "$game_actors", "@data", "[2]", "@nickname").Value.Str; got != "騎士" {
				return "nick2 = " + got
			}
			return ""
		}},
		{"ace shift_jis nickname 2", "ace.rvdata2", "$game_actors", []string{"@data", "[2]", "@nickname"}, str("王"), nil},
		{"ace shared ascii graphic", "ace.rvdata2", "$game_actors", []string{"@data", "[1]", "@character_name"}, str("Actor3"), func(t *testing.T, d *Document) string {
			if got := find(t, d, "$game_actors", "@data", "[2]", "@character_name").Value.Str; got != "Actor3" {
				return "the link reads " + got
			}
			return ""
		}},
		{"ace playtime", "ace.rvdata2", "header", []string{":playtime_s"}, str("99:59:59"), func(t *testing.T, d *Document) string {
			for _, det := range d.Details() {
				if det.Label == "Play time" && det.Text != "99:59:59" {
					return "play time " + det.Text
				}
			}
			return ""
		}},
		{"ace float", "ace.rvdata2", "$game_map", []string{"@display_x"}, flt(12.25), nil},
		{"ace variable", "ace.rvdata2", "$game_variables", []string{"@data", "[5]"}, num("-1073741824"), nil},
		{"ace switch", "ace.rvdata2", "$game_switches", []string{"@data", "[2]"}, boolean(true), nil},
		{"ace custom", "ace.rvdata2", "contents[:custom]", []string{":quest"}, str("Done"), nil},
	}
	py := ""
	if p := tryPython(); p != "" {
		py = p
	}
	dir := t.TempDir()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, format := openFixture(t, tt.file)
			target := find(t, d, tt.v, tt.labels...)
			out, err := d.Encode([]model.Edit{{Site: target.Site, Value: tt.value}})
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(out, readFixture(t, tt.file)) {
				t.Fatal("the edit changed nothing")
			}
			if !format.Detect(tt.file, out) {
				t.Error("the edited file is not detected")
			}
			parsed, err := format.Parse(tt.file, out)
			if err != nil {
				t.Fatalf("the edited file does not parse: %v", err)
			}
			after := parsed.(*Document)
			// Every leaf is as it was, but the edited value and what links to it.
			assertOnlyChanged(t, d, after, target.Value, tt.value)
			if tt.then != nil {
				if problem := tt.then(t, after); problem != "" {
					t.Error(problem)
				}
			}
			if py != "" {
				file := filepath.Join(dir, strings.ReplaceAll(tt.name, " ", "_")+filepath.Ext(tt.file))
				if err := os.WriteFile(file, out, 0o600); err != nil {
					t.Fatal(err)
				}
				want := rmtest.PythonTrees(t, py, file)[file]
				if got := tree(t, after); !reflect.DeepEqual(got, want) {
					t.Error("Python reads the edited file differently from Go")
				}
			}
		})
	}
}

// tryPython is the reference Python, or "" when it is not available.
func tryPython() string {
	py := os.Getenv("RUBYMARSHAL_PYTHON")
	if py == "" {
		py = "python3"
	}
	return pythonWithPackage(py)
}

// assertOnlyChanged checks that every leaf of after is as in before, except where the old
// value was (and it is the new one, whole or in the parts that the kind keeps).
func assertOnlyChanged(t *testing.T, before, after *Document, old, edited *model.Value) {
	t.Helper()
	var walk func(path string, a, b *model.Value)
	changed := 0
	walk = func(path string, a, b *model.Value) {
		if a.Kind != b.Kind || (a != old && a.Type != b.Type) || len(a.Children) != len(b.Children) {
			t.Errorf("%s: shape changed: %v %s -> %v %s", path, a.Kind, a.Type, b.Kind, b.Type)
			return
		}
		if a == old {
			changed++
			switch edited.Kind {
			case model.KindString:
				if b.Str != edited.Str {
					t.Errorf("%s = %q", path, b.Str)
				}
			case model.KindInt:
				if b.Int.Cmp(edited.Int) != 0 {
					t.Errorf("%s = %v", path, b.Int)
				}
			case model.KindFloat:
				if b.Float != edited.Float {
					t.Errorf("%s = %v", path, b.Float)
				}
			case model.KindBool:
				if b.Bool != edited.Bool {
					t.Errorf("%s = %v", path, b.Bool)
				}
			}
			return
		}
		if len(a.Children) == 0 && a.Repr() != b.Repr() {
			t.Errorf("%s changed: %s -> %s", path, a.Repr(), b.Repr())
		}
		for i := range a.Children {
			if a.Children[i].Label != b.Children[i].Label {
				t.Errorf("%s: label %q -> %q", path, a.Children[i].Label, b.Children[i].Label)
			}
			walk(path+"/"+a.Children[i].Label, a.Children[i].Slot.Value, b.Children[i].Slot.Value)
		}
		if a.Kind == model.KindBytes && !bytes.Equal(a.Bytes, b.Bytes) {
			t.Errorf("%s: bytes changed", path)
		}
	}
	if len(before.vars) != len(after.vars) {
		t.Fatalf("variables: %d -> %d", len(before.vars), len(after.vars))
	}
	for i := range before.vars {
		walk(before.vars[i].Name, before.vars[i].Slot.Value, after.vars[i].Slot.Value)
	}
	if changed == 0 {
		t.Error("the edited value is not in the tree")
	}
}

func TestRefusedEdits(t *testing.T) {
	xp, _ := openFixture(t, "xp.rxdata")
	ace, _ := openFixture(t, "ace.rvdata2")
	other, _ := openFixture(t, "xp.rxdata")
	fixnum := find(t, xp, "$game_party", "@gold").Site
	ascii := find(t, ace, "$game_actors", "@data", "[1]", "@character_name").Site
	sjis := find(t, ace, "$game_actors", "@data", "[1]", "@nickname").Site
	tests := []struct {
		name  string
		doc   *Document
		edits []model.Edit
		route string
	}{
		{"another document's site", xp, []model.Edit{{Site: find(t, other, "$game_party", "@gold").Site,
			Value: &model.Value{Kind: model.KindInt, Int: big.NewInt(1)}}}, "Document.Encode->Doc.Encode." + rubymarshal.TagForeignSite},
		{"above the Fixnum range", xp, []model.Edit{{Site: fixnum,
			Value: &model.Value{Kind: model.KindInt, Int: big10("1073741824")}}}, "Document.Encode->Doc.Encode." + rubymarshal.TagOutOfRange},
		{"a string for a number", xp, []model.Edit{{Site: fixnum,
			Value: &model.Value{Kind: model.KindString, Str: "5"}}}, "Document.Encode->Doc.Encode." + rubymarshal.TagWrongKind},
		{"accents in US-ASCII", ace, []model.Edit{{Site: ascii,
			Value: &model.Value{Kind: model.KindString, Str: "Acteur é"}}}, "Document.Encode->Doc.Encode->textEncoding.encode." + rubymarshal.TagUnstorableText},
		{"emoji in Shift_JIS", ace, []model.Edit{{Site: sjis,
			Value: &model.Value{Kind: model.KindString, Str: "🐉"}}}, "Document.Encode->Doc.Encode->textEncoding.encode." + rubymarshal.TagUnstorableText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.doc.Encode(tt.edits)
			appErr, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("expected *errors.Error, got %T: %v", err, err)
			}
			if !appErr.HasRoute(tt.route) {
				t.Errorf("unexpected failure path: %s", appErr.Route())
			}
		})
	}
	// CheckText tells the user why.
	if err := sjis.CheckText("🐉"); err == nil || !strings.Contains(err.Error(), "Shift_JIS") {
		t.Errorf("CheckText = %v", err)
	}
	if err := ascii.CheckText("é"); err == nil || !strings.Contains(err.Error(), "ASCII") {
		t.Errorf("CheckText = %v", err)
	}
}

func TestParseOfATruncatedSave(t *testing.T) {
	xp := readFixture(t, "xp.rxdata")
	_, err := FormatXP.Parse("Save1.rxdata", xp[:len(xp)/2])
	appErr, ok := errors.FromError(err)
	if !ok || appErr.Tag != rubymarshal.TagTruncated && appErr.Tag != rubymarshal.TagBadLength {
		t.Errorf("Parse = %v", err)
	}
}

func TestParseFailure(t *testing.T) {
	for _, f := range Formats {
		_, err := f.Parse("a", []byte("\x04\x08Z"))
		appErr, ok := errors.FromError(err)
		if !ok || !appErr.HasRoute("engine.parse->Parse->reader.value->reader.valueP->reader.valueCase."+rubymarshal.TagBadType) {
			t.Errorf("%s: %v", f.Name, err)
		}
	}
}

func TestDuplicateAndOddContents(t *testing.T) {
	// A header, then a contents hash with :system twice and a key that is not a symbol.
	data := []byte("\x04\x08{\x00\x04\x08{\x08:\x0bsystem0;\x000i\x060")
	doc, err := FormatVXAce.Parse("Save1.rvdata2", data)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, v := range doc.Variables() {
		names = append(names, v.Name)
	}
	want := []string{"header", "$game_system", "$game_system #2", "contents[1]"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("variables = %q, want %q", names, want)
	}
	// A contents that is not a hash, and a third dump.
	doc, err = FormatVXAce.Parse("Save1.rvdata2", []byte("\x04\x08{\x00\x04\x08[\x00\x04\x08i\x06"))
	if err != nil {
		t.Fatal(err)
	}
	names = nil
	for _, v := range doc.Variables() {
		names = append(names, v.Name)
	}
	if want := []string{"header", "contents", "extra 1"}; !reflect.DeepEqual(names, want) {
		t.Errorf("variables = %q, want %q", names, want)
	}
}

func TestClock(t *testing.T) {
	for secs, want := range map[int64]string{0: "0:00:00", 59: "0:00:59", 61: "0:01:01", 3600: "1:00:00", 36061: "10:01:01", 360000: "100:00:00"} {
		if got := clock(secs); got != want {
			t.Errorf("clock(%d) = %q, want %q", secs, got, want)
		}
	}
}

// pythonWithPackage is py if it can import the package rubymarshal, else "".
func pythonWithPackage(py string) string {
	path, err := exec.LookPath(py)
	if err != nil {
		return ""
	}
	if err := exec.Command(path, "-I", "-c", "import rubymarshal").Run(); err != nil {
		return ""
	}
	return path
}
