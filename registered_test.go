package main

import (
	"bytes"
	"context"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rm4n0s/glutter/gluttertest"
	m "github.com/rm4n0s/glutter/material"
	"github.com/rm4n0s/save-editor/internal/model"
)

// registeredFixture is a save of the repository's fixtures and what the editor does with it.
type registeredFixture struct {
	path   string // from the root of the repository
	format string // the name of the format that detects it
	// What the page shows: a detail of the document and a variable of the tree.
	detail, variable string
	// The row of an int that the test changes: the path of labels from a variable, each part
	// of it a row that is expanded first.
	intPath []string
	newInt  int64
}

var registeredFixtures = []registeredFixture{
	{"internal/renpysave/testdata/renpy7.save", "Ren'Py", "", "", nil, 0},
	{"internal/renpysave/testdata/friend.save", "Ren'Py", "", "", nil, 0},
	{"internal/renpysave/testdata/renpy8.save", "Ren'Py", "8.3.4.24120703", "money", []string{"money"}, 4242},
	{"internal/gvas/testdata/ue4.sav", "Unreal Engine", "4.27.2", "bAlive", []string{"Level"}, 77},
	{"internal/gvas/testdata/ue5.sav", "Unreal Engine", "5.1.1", "PlayerName", []string{"Level"}, 77},
	{"internal/gvas/testdata/ue54.sav", "Unreal Engine", "5.4.2", "PlayerName", []string{"Level"}, 77},
	{"internal/lcf/testdata/rm2k3_cp1251.lsd", "RPG Maker 2000/2003", "Windows-1251 (Cyrillic)", "system", []string{"title", "hero_hp"}, 400},
	{"internal/lcf/testdata/rm2k3_cp1252.lsd", "RPG Maker 2000/2003", "Windows-1252 (Western European)", "system", []string{"title", "hero_hp"}, 400},
	{"internal/lcf/testdata/rm2k3_easyrpg_cp1250.lsd", "RPG Maker 2000/2003", "Windows-1250 (Central European)", "system", []string{"title", "hero_hp"}, 400},
	{"internal/lcf/testdata/rm2k_sjis.lsd", "RPG Maker 2000/2003", "Shift-JIS (code page 932)", "system", []string{"title", "hero_hp"}, 400},
	{"internal/rgss/testdata/xp.rxdata", "RPG Maker XP", "7 times", "$game_system", []string{"Graphics.frame_count"}, 1000},
	{"internal/rgss/testdata/xp-extra.rxdata", "RPG Maker XP", "2 more dump(s) written by the game's scripts", "$game_system", []string{"Graphics.frame_count"}, 1000},
	{"internal/rgss/testdata/vx.rvdata", "RPG Maker VX", "3 times", "$game_system", []string{"Graphics.frame_count"}, 1000},
	{"internal/rgss/testdata/ace.rvdata2", "RPG Maker VX Ace", "12 times", "$game_system", []string{"$game_system", "@save_count"}, 13},
	{"internal/unityprefs/testdata/linux.prefs", "Unity PlayerPrefs", "Linux prefs file (XML), version 1.1", "Gold", []string{"Gold"}, 2500},
	{"internal/unityprefs/testdata/mac-plistlib.plist", "Unity PlayerPrefs", "macOS preferences file (binary property list)", "Gold", []string{"Gold"}, 2500},
	{"internal/unityprefs/testdata/mac-float32.plist", "Unity PlayerPrefs", "macOS preferences file (binary property list)", "Gold", []string{"Gold"}, 70000},
	{"internal/nrbf/testdata/player.sav", "Unity / .NET BinaryFormatter", "PlayerData", "level", []string{"level"}, 43},
	{"internal/nrbf/testdata/extra.sav", "Unity / .NET BinaryFormatter", "Node", "accent", nil, 0},
	{"internal/nrbf/testdata/array-root.sav", "Unity / .NET BinaryFormatter", "int[]", "root", []string{"root", "[2]"}, 20},
	{"internal/nrbf/testdata/string-root.sav", "Unity / .NET BinaryFormatter", "string", "root", nil, 0},
	{"internal/nrbf/testdata/spec-address.bin", "Unity / .NET BinaryFormatter", "Address", "City", nil, 0},
	{"internal/unityjson/testdata/plain.json", "Unity JSON", "JSON text", "playerName", []string{"level"}, 13},
	{"internal/unityjson/testdata/root-array.json", "Unity JSON", "JSON text", "[1]", nil, 0},
	{"internal/unityjson/testdata/es3-plain.es3", "Easy Save 3", "Easy Save 3 (JSON)", "playerName", []string{"level"}, 13},
	{"internal/unityjson/testdata/es3-gzip.es3", "Easy Save 3", "gzip", "playerName", []string{"level"}, 13},
	{"internal/unityjson/testdata/es3-aes.es3", "Easy Save 3", "AES-128 (the default password)", "playerName", []string{"level"}, 13},
	{"internal/unityjson/testdata/es3-aes-gzip.es3", "Easy Save 3", "AES-128 (the default password)", "playerName", []string{"level"}, 13},
	{"internal/rpgmv/testdata/mv-file1.rpgsave", "RPG Maker MV", "LZString, Base64", "system", []string{"system", "_saveCount"}, 13},
	{"internal/rpgmv/testdata/mv-global.rpgsave", "RPG Maker MV", "LZString, Base64", "[1]", []string{"[1]", "timestamp"}, 1800000000001},
	{"internal/rpgmv/testdata/mv-config.rpgsave", "RPG Maker MV", "LZString, Base64", "bgmVolume", []string{"bgmVolume"}, 55},
	{"internal/rpgmv/testdata/mz-file1.rmmzsave", "RPG Maker MZ", "zlib, written as UTF-8 text", "system", []string{"system", "_saveCount"}, 13},
	{"internal/rpgmv/testdata/mz-global.rmmzsave", "RPG Maker MZ", "zlib, written as UTF-8 text", "[1]", []string{"[1]", "timestamp"}, 1800000000001},
	{"internal/rpgmv/testdata/mz-config.rmmzsave", "RPG Maker MZ", "zlib, written as UTF-8 text", "bgmVolume", []string{"bgmVolume"}, 55},
}

func TestRegisteredFixturesAreDetectedByTheirOwnFormat(t *testing.T) {
	formats := supportedFormats()
	for _, fx := range registeredFixtures {
		t.Run(filepath.Base(fx.path), func(t *testing.T) {
			data, err := os.ReadFile(fx.path)
			if err != nil {
				t.Fatal(err)
			}
			var detectors []string
			for _, f := range formats {
				if f.Detect(filepath.Base(fx.path), data) {
					detectors = append(detectors, f.Name)
				}
			}
			if !reflect.DeepEqual(detectors, []string{fx.format}) {
				t.Errorf("detected by %q, want only %q", detectors, fx.format)
			}
			doc, format, err := openDocument(context.Background(), formats, fx.path)
			if err != nil {
				t.Fatal(err)
			}
			if format.Name != fx.format || len(doc.Variables()) == 0 || len(doc.Details()) == 0 {
				t.Errorf("opened as %s with %d variables and %d details", format.Name, len(doc.Variables()), len(doc.Details()))
			}
		})
	}
}

// leaves lists the primitive values of the tree, by the path of their labels.
func leaves(vars []model.Variable) map[string]string {
	out := map[string]string{}
	var walk func(path string, v *model.Value, depth int)
	walk = func(path string, v *model.Value, depth int) {
		if len(v.Children) == 0 || depth > 6 {
			out[path] = v.Repr()
			return
		}
		for _, c := range v.Children {
			walk(path+"\x00"+c.Label, c.Slot.Value, depth+1)
		}
	}
	for _, v := range vars {
		walk(v.Name, v.Slot.Value, 0)
	}
	return out
}

func TestRegisteredFixturesOpenAndSaveInTheApp(t *testing.T) {
	for _, fx := range registeredFixtures {
		if fx.intPath == nil {
			continue
		}
		t.Run(filepath.Base(fx.path), func(t *testing.T) {
			data, err := os.ReadFile(fx.path)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), filepath.Base(fx.path))
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			keys := ""
			if fx.format == "Ren'Py" {
				keys = testKeys
			}
			ts, _ := newTester(t, path, keys)
			s := state(ts)
			if s.format.Name != fx.format {
				t.Fatalf("the page opened it as %q", s.format.Name)
			}
			for _, text := range []string{filepath.Base(fx.path), fx.format, fx.detail, fx.variable} {
				if !ts.Exists(gluttertest.ByText(text)) && !ts.Exists(gluttertest.ByTextContaining(text)) {
					t.Errorf("%q is not shown; status %q", text, s.status)
				}
			}
			before := leaves(s.doc.Variables())

			// Expand the rows on the way, then type the new number.
			id := fx.intPath[0]
			for _, label := range fx.intPath[1:] {
				ts.Tap(gluttertest.Descendant(gluttertest.ByKey(m.NewValueKey(id)), gluttertest.ByTooltip("Show the parts")))
				ts.Pump()
				id += "\x00" + label
			}
			ts.EnterText(fieldOf(id), big.NewInt(fx.newInt).String())
			ts.PumpAndSettle()
			if !ts.Exists(gluttertest.ByText("1 change")) {
				t.Fatalf("the change is not counted; status %q", s.status)
			}
			ts.Tap(gluttertest.ByTooltip("Save (Ctrl+S)"))
			waitFor(t, ts, "the save", func() bool { return len(s.edits) == 0 && !s.busy })

			saved, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := s.format.Parse(filepath.Base(path), saved)
			if err != nil {
				t.Fatalf("the saved file does not read back: %v", err)
			}
			want := before
			want[strings.Join(fx.intPath, "\x00")] = big.NewInt(fx.newInt).String()
			after := leaves(doc.Variables())
			if !reflect.DeepEqual(after, want) {
				for k, v := range after {
					if want[k] != v {
						t.Errorf("%q reads back as %s, want %s", k, v, want[k])
					}
				}
				t.Errorf("the saved values differ from the edited ones")
			}
			backup, err := os.ReadFile(path + ".bak")
			if err != nil || !bytes.Equal(backup, data) {
				t.Errorf("the backup is not the first version: %v", err)
			}
		})
	}
}
