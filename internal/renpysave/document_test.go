package renpysave

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rm4n0s/save-editor/internal/model"
)

func TestDetect(t *testing.T) {
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"a Ren'Py 8 save", read("renpy8.save"), true},
		{"a Ren'Py 7 save", read("renpy7.save"), true},
		{"a friend's save", read("friend.save"), true},
		{"a zip without a log", zipOf(t, zip.Deflate, map[string]string{"json": "{}", "renpy_version": "x"}), false},
		{"a zip with only a log", zipOf(t, zip.Deflate, map[string]string{"log": "x"}), false},
		{"a zip with other files", zipOf(t, zip.Deflate, map[string]string{"a.txt": "x"}), false},
		{"not a zip", []byte("not a zip"), false},
		{"nothing", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Format.Detect("1-1.save", tt.data); got != tt.want {
				t.Errorf("Detect = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatParse(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "renpy8.save"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Format.Parse("1-1.save", data)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.(model.Screenshotter); !ok {
		t.Error("the document is not a Screenshotter")
	}
	if _, ok := doc.(*Document); !ok {
		t.Errorf("the document is a %T", doc)
	}
	if _, err := Format.Parse("x.save", []byte("not a zip")); err == nil {
		t.Error("a file that is not a zip was parsed")
	}
}

// find follows the labels from a variable.
func find(t *testing.T, s *Document, name string, labels ...string) model.Slot {
	t.Helper()
	slot := variable(t, s, name).Slot
	for _, l := range labels {
		i := slices.IndexFunc(slot.Value.Children, func(c model.Child) bool { return c.Label == l })
		if i < 0 {
			t.Fatalf("%s has no part %s", name, l)
		}
		slot = slot.Value.Children[i].Slot
	}
	return slot
}

func TestModelValues(t *testing.T) {
	s := openSave(t, "renpy8.save")
	tests := []struct {
		name     string
		labels   []string
		kind     model.Kind
		typeName string
		repr     string
		editable bool
	}{
		{"money", nil, model.KindInt, "int", "150", true},
		{"big", nil, model.KindInt, "int", "1180591620717411303424", true},
		{"ratio", nil, model.KindFloat, "float", "0.75", true},
		{"player_name", nil, model.KindString, "str", `"AnnAnn"`, true},
		{"greeting", nil, model.KindString, "str", `"Καλημέρα ☕"`, true},
		{"has_key", nil, model.KindBool, "bool", "true", true},
		{"nothing", nil, model.KindNull, "None", "None", false},
		{"pair", nil, model.KindList, "tuple", "tuple (2 items)", false},
		{"pair", []string{"[0]"}, model.KindInt, "int", "1", true},
		{"inventory", nil, model.KindObject, "RevertableList", "RevertableList (3 parts)", false},
		{"inventory", []string{"[2]"}, model.KindString, "str", `"AnnAnn"`, true},
		{"stats", nil, model.KindObject, "RevertableDict", "RevertableDict (2 parts)", false},
		{"seen", nil, model.KindObject, "RevertableSet", "RevertableSet (1 part)", false},
		{"hero", nil, model.KindObject, "Hero", "Hero (2 parts)", false},
		{"hero", []string{".hp"}, model.KindInt, "int", "30", true},
	}
	for _, tt := range tests {
		t.Run(tt.name+" "+join(tt.labels), func(t *testing.T) {
			slot := find(t, s, tt.name, tt.labels...)
			v := slot.Value
			if v.Kind != tt.kind || v.TypeName() != tt.typeName || v.Repr() != tt.repr {
				t.Errorf("got %v %s %s, want %v %s %s", v.Kind, v.TypeName(), v.Repr(), tt.kind, tt.typeName, tt.repr)
			}
			if (slot.Site != nil) != tt.editable {
				t.Errorf("site %v, want editable %v", slot.Site, tt.editable)
			}
		})
	}
	if q := find(t, s, "stats").Value.QualifiedTypeName(); q != "renpy.revertable.RevertableDict" {
		t.Errorf("qualified type of stats = %q", q)
	}
	if q := find(t, s, "pair").Value.QualifiedTypeName(); q != "tuple" {
		t.Errorf("qualified type of pair = %q", q)
	}
	if q := find(t, s, "e").Value.QualifiedTypeName(); q != "renpy.character.ADVCharacter" {
		t.Errorf("qualified type of e = %q", q)
	}
}

func join(labels []string) string {
	out := ""
	for _, l := range labels {
		out += l
	}
	return out
}

func TestSitesAreUniqueAndShared(t *testing.T) {
	s := openSave(t, "renpy8.save")
	ids := map[int]*model.Site{}
	var walk func(v *model.Value, seen map[*model.Value]bool)
	walk = func(v *model.Value, seen map[*model.Value]bool) {
		if seen[v] {
			return
		}
		seen[v] = true
		for _, c := range v.Children {
			if site := c.Slot.Site; site != nil {
				if other, ok := ids[site.ID]; ok && other != site {
					t.Errorf("two sites have the ID %d", site.ID)
				}
				ids[site.ID] = site
			}
			walk(c.Slot.Value, seen)
		}
	}
	for _, v := range s.Variables() {
		if site := v.Slot.Site; site != nil {
			if other, ok := ids[site.ID]; ok && other != site {
				t.Errorf("two sites have the ID %d", site.ID)
			}
			ids[site.ID] = site
		}
		walk(v.Slot.Value, map[*model.Value]bool{})
	}
	if len(ids) < 10 {
		t.Errorf("%d sites", len(ids))
	}
	if len(ids) != len(s.sites) {
		t.Errorf("%d sites reached, %d known to the document", len(ids), len(s.sites))
	}
	// The name is one pickle string reached from three places: the values are shared.
	if find(t, s, "player_name").Value != find(t, s, "nickname").Value {
		t.Error("the value reached through the memo is not shared")
	}
}

func TestRecursiveValueTerminates(t *testing.T) {
	// roots = {"store.loop": l}, where l = [l]; the pickle is (roots, None).
	log := "\x80\x02}q\x00U\x0astore.loop]q\x01h\x01a" + "sN\x86."
	data := zipOf(t, zip.Deflate, map[string]string{"log": log, "json": "{}"})
	doc, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	loop := variable(t, doc, "loop").Slot.Value
	if len(loop.Children) != 1 || loop.Children[0].Slot.Value != loop {
		t.Errorf("the list does not contain itself: %+v", loop.Children)
	}
}

func TestPythonTwoByteStringsAreStrings(t *testing.T) {
	// A Python 2 str is a SHORT_BINSTRING.
	log := "\x80\x02}U\x0bstore.titleU\x03abcsN\x86."
	doc, err := Parse(zipOf(t, zip.Deflate, map[string]string{"log": log, "json": "{}"}))
	if err != nil {
		t.Fatal(err)
	}
	v := variable(t, doc, "title")
	if v.Slot.Value.Kind != model.KindString || v.Slot.Site == nil {
		t.Fatalf("title = %+v", v.Slot)
	}
	out, err := doc.Encode([]model.Edit{{Site: v.Slot.Site, Value: &model.Value{Kind: model.KindString, Str: "changed"}}})
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	got := variable(t, again, "title").Slot.Value
	if got.Str != "changed" || got.Kind != model.KindString {
		t.Errorf("title = %s", got.Repr())
	}
}

// snapshot is every variable with its parts, as text, for comparing two documents.
func snapshot(s *Document) []string {
	var out []string
	var walk func(prefix string, v *model.Value, seen map[*model.Value]bool)
	walk = func(prefix string, v *model.Value, seen map[*model.Value]bool) {
		out = append(out, prefix+" "+v.Kind.String()+" "+v.QualifiedTypeName()+" "+v.Repr())
		if seen[v] {
			return
		}
		seen[v] = true
		for _, c := range v.Children {
			walk(prefix+c.Label, c.Slot.Value, seen)
		}
		delete(seen, v)
	}
	for _, v := range s.Variables() {
		walk(v.Name, v.Slot.Value, map[*model.Value]bool{})
	}
	return out
}

func TestEncodeWithoutEditsKeepsEverything(t *testing.T) {
	for _, name := range []string{"renpy8.save", "renpy7.save", "friend.save"} {
		t.Run(name, func(t *testing.T) {
			s := openSave(t, name)
			out, err := s.Encode(nil)
			if err != nil {
				t.Fatal(err)
			}
			again, err := Parse(out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(again.Log.Data, s.Log.Data) || again.Signatures != s.Signatures {
				t.Error("the log or the signatures changed")
			}
			if !slices.Equal(snapshot(again), snapshot(s)) {
				t.Error("the values changed")
			}
		})
	}
}

func TestEditEachKind(t *testing.T) {
	big70, _ := new(big.Int).SetString("-99999999999999999999999", 10)
	tests := []struct {
		name   string
		labels []string
		value  *model.Value
		repr   string
	}{
		{"money", nil, &model.Value{Kind: model.KindInt, Int: big.NewInt(-5)}, "-5"},
		{"money", nil, &model.Value{Kind: model.KindInt, Int: big70}, "-99999999999999999999999"},
		{"ratio", nil, &model.Value{Kind: model.KindFloat, Float: 2.5}, "2.5"},
		{"player_name", nil, &model.Value{Kind: model.KindString, Str: "Bób ☕"}, `"Bób ☕"`},
		{"has_key", nil, &model.Value{Kind: model.KindBool, Bool: false}, "false"},
		{"flag_off", nil, &model.Value{Kind: model.KindBool, Bool: true}, "true"},
		{"hero", []string{".hp"}, &model.Value{Kind: model.KindInt, Int: big.NewInt(99)}, "99"},
		{"inventory", []string{"[0]"}, &model.Value{Kind: model.KindString, Str: "axe"}, `"axe"`},
		{"pair", []string{"[0]"}, &model.Value{Kind: model.KindInt, Int: big.NewInt(7)}, "7"},
	}
	for _, name := range []string{"renpy8.save", "renpy7.save"} {
		for _, tt := range tests {
			t.Run(name+" "+tt.name+join(tt.labels)+" "+tt.repr, func(t *testing.T) {
				s := openSave(t, name)
				before := snapshot(s)
				slot := find(t, s, tt.name, tt.labels...)
				out, err := s.Encode([]model.Edit{{Site: slot.Site, Value: tt.value}})
				if err != nil {
					t.Fatal(err)
				}
				again, err := Parse(out)
				if err != nil {
					t.Fatal(err)
				}
				if got := find(t, again, tt.name, tt.labels...).Value.Repr(); got != tt.repr {
					t.Errorf("%s = %s, want %s", tt.name, got, tt.repr)
				}
				after := snapshot(again)
				if len(after) != len(before) {
					t.Fatalf("%d values, was %d", len(after), len(before))
				}
				changed := 0
				for i := range before {
					if before[i] != after[i] {
						changed++
					}
				}
				if changed != 1 {
					t.Errorf("%d values changed, want 1", changed)
				}
			})
		}
	}
}

func TestEditOneOfTheSharedStrings(t *testing.T) {
	s := openSave(t, "renpy8.save")
	site := find(t, s, "player_name").Site
	out, err := s.Encode([]model.Edit{{Site: site, Value: &model.Value{Kind: model.KindString, Str: "Bob"}}})
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := find(t, again, "player_name").Value.Str; got != "Bob" {
		t.Errorf("player_name = %q", got)
	}
	if got := find(t, again, "nickname").Value.Str; got != "AnnAnn" {
		t.Errorf("nickname = %q, want the shared string unchanged", got)
	}
}

func TestDetails(t *testing.T) {
	t.Setenv("TZ", "UTC")
	s := openSave(t, "renpy8.save")
	got := map[string]string{}
	for _, d := range s.Details() {
		got[d.Label] = d.Text
	}
	for label, want := range map[string]string{
		"Play time": "1:02:03", "Game version": "1.0", "Ren'Py version": "8.3.4.24120703", "Variables": "17",
	} {
		if got[label] != want {
			t.Errorf("%s = %q, want %q", label, got[label], want)
		}
	}
	if got["Saved"] == "" {
		t.Error("no saved time")
	}
}

func TestMetadataText(t *testing.T) {
	tests := []struct {
		got, want string
	}{
		{metadataDuration(json.Number("3723.25")), "1:02:03"},
		{metadataDuration(json.Number("59")), "0:00:59"},
		{metadataDuration("not a number"), ""},
		{metadataTime(json.Number("0")), ""},
		{metadataString("1.0"), "1.0"},
		{metadataString(nil), ""},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%q, want %q", tt.got, tt.want)
		}
	}
}
