package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/glutter/gluttertest"
	m "github.com/rm4n0s/glutter/material"
)

// withInternals are the fixtures that have variables of the engine, which the page hides
// behind the Internals chip. The others have none, and the chip is not shown.
var withInternals = map[string]bool{
	"renpy8.save": true, "friend.save": true, "renpy7.save": true,
	"rm2k3_cp1251.lsd": true, "rm2k3_cp1252.lsd": true, "rm2k3_easyrpg_cp1250.lsd": true, "rm2k_sjis.lsd": true,
	"xp.rxdata": true, "xp-extra.rxdata": true, "vx.rvdata": true, "ace.rvdata2": true,
	"linux.prefs": true, "mac-plistlib.plist": true,
}

// copyFixture copies a fixture of the repository into a folder of the test and returns the path.
// The prefs file of Linux gets the name that Unity gives it.
func copyFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(path)
	if name == "linux.prefs" {
		name = "prefs" // Unity's file on Linux has this name
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestEveryFormatOpensInTheApp opens a fixture of each format in the app and checks what the
// page shows: the format and the details of the document, the Internals chip only when there
// are internal variables, the picture of the save only for Ren'Py, and no Ren'Py parts (the
// save name and the signature card) for any other format.
func TestEveryFormatOpensInTheApp(t *testing.T) {
	for _, fx := range registeredFixtures {
		t.Run(filepath.Base(fx.path), func(t *testing.T) {
			path := copyFixture(t, fx.path)
			keys := ""
			if fx.format == fRenpy {
				keys = testKeys
			}
			ts, _ := newTester(t, path, keys)
			s := state(ts)
			if s.format.Name != fx.format {
				t.Fatalf("the page opened it as %q", s.format.Name)
			}
			base := filepath.Base(fx.path)
			if got := ts.Exists(gluttertest.ByText("Internals")); got != withInternals[base] {
				t.Errorf("the Internals chip is shown: %v, want %v", got, withInternals[base])
			}
			hasInternal := false
			for _, v := range s.doc.Variables() {
				hasInternal = hasInternal || v.Internal
			}
			if hasInternal != withInternals[base] {
				t.Errorf("the document has internal variables: %v, want %v", hasInternal, withInternals[base])
			}
			// Only Ren'Py's renpy8.save has a picture of the game.
			if got := ts.Exists(gluttertest.ByType[*m.Image]()); got != (base == "renpy8.save") {
				t.Errorf("a picture is shown: %v", got)
			}
			renpy := fx.format == fRenpy
			if got := ts.Exists(gluttertest.ByKey(m.NewValueKey("save name"))); got != renpy {
				t.Errorf("the save name is shown: %v", got)
			}
			if got := ts.Exists(gluttertest.ByTextContaining("Sign with")) || ts.Exists(gluttertest.ByTextContaining("Not signed")) ||
				ts.Exists(gluttertest.ByTextContaining("Signed")); got != renpy {
				t.Errorf("the signature card is shown: %v", got)
			}
			if !ts.Exists(gluttertest.ByText("Format")) || !ts.Exists(gluttertest.ByText(fx.format)) {
				t.Error("the format is not shown")
			}
			if n := len(s.doc.Details()); n == 0 {
				t.Error("the document has no details")
			}
			if !strings.Contains(s.status, "Opened") {
				t.Errorf("status %q", s.status)
			}
		})
	}
}

// TestSaveAsOffersTheFormatOfTheDocument: the dialog of Save as is made from the open document's
// format (the fake dialog cannot report its filters, so they are checked as the page makes them),
// and a save made through it is read by the same format.
func TestSaveAsOffersTheFormatOfTheDocument(t *testing.T) {
	for _, fx := range registeredFixtures {
		if fx.intPath == nil {
			continue
		}
		t.Run(filepath.Base(fx.path), func(t *testing.T) {
			path := copyFixture(t, fx.path)
			keys := ""
			if fx.format == fRenpy {
				keys = testKeys
			}
			ts, fd := newTester(t, path, keys)
			s := state(ts)
			filters := saveFilters(s.format)
			if len(filters) != 1 || filters[0].Name != fx.format+" saves" {
				t.Fatalf("filters %+v", filters)
			}
			matches := false
			for _, p := range filters[0].Patterns {
				if ok, _ := filepath.Match(p, filepath.Base(path)); ok {
					matches = true
				}
			}
			if !matches {
				t.Errorf("no pattern of %v matches %s", filters[0].Patterns, filepath.Base(path))
			}
			id := fx.intPath[0]
			for _, label := range fx.intPath[1:] {
				ts.Tap(gluttertest.Descendant(gluttertest.ByKey(m.NewValueKey(id)), gluttertest.ByTooltip("Show the parts")))
				ts.Pump()
				id += "\x00" + label
			}
			ts.EnterText(fieldOf(id), "5")
			ts.PumpAndSettle()
			ts.Tap(gluttertest.ByTooltip("Save as (Ctrl+Shift+S)"))
			fd.WaitFor("the file dialog", func() bool { return fd.PendingFileChoosers() == 1 })
			dir := filepath.Join(filepath.Dir(path), "out")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, filepath.Base(path))
			fd.AnswerFileChooser(target)
			waitFor(t, ts, "the save", func() bool { return s.path == target && !s.busy })
			if s.format.Name != fx.format {
				t.Errorf("the format after saving is %q", s.format.Name)
			}
			doc, format, err := openDocument(t.Context(), supportedFormats(), target)
			if err != nil {
				t.Fatalf("the saved file does not open: %v", err)
			}
			if format.Name != fx.format || len(doc.Variables()) != len(s.doc.Variables()) {
				t.Errorf("the saved file is %q with %d variables", format.Name, len(doc.Variables()))
			}
			if _, err := os.Stat(target + ".bak"); !os.IsNotExist(err) {
				t.Error("save as made a backup")
			}
			orig, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			first, err := os.ReadFile(fx.path)
			if err != nil {
				t.Fatal(err)
			}
			if string(orig) != string(first) {
				t.Error("the first file changed")
			}
		})
	}
}

func TestEmptyPageListsEveryFormat(t *testing.T) {
	ts, _ := newTester(t, "", "")
	for _, f := range supportedFormats() {
		if !ts.Exists(gluttertest.ByTextContaining(f.Name)) {
			t.Errorf("the empty page does not list %q", f.Name)
		}
	}
	if len(supportedFormats()) != 12 {
		t.Errorf("%d formats are registered, want 12", len(supportedFormats()))
	}
}

func TestOneObjectPartIsNotParts(t *testing.T) {
	path := copyFixture(t, "internal/rpgmv/testdata/mv-file1.rpgsave")
	ts, _ := newTester(t, path, "")
	// Game_Actors has one part, _data; Game_Switches too.
	if !ts.Exists(gluttertest.ByText("Game_Actors (1 part)")) || ts.Exists(gluttertest.ByTextContaining("(1 parts)")) {
		t.Error("an object with one part is not shown as (1 part)")
	}
	if !ts.Exists(gluttertest.ByText("Game_Party (10 parts)")) {
		t.Error("an object with several parts is not shown as (n parts)")
	}
	for n, want := range map[int]string{0: "parts", 1: "part", 2: "parts", 11: "parts"} {
		if got := partsWord(n); got != want {
			t.Errorf("partsWord(%d) = %q", n, got)
		}
	}
}

// TestGoldenOfAnotherEngine is the picture of a document that is not Ren'Py's: an RPG Maker MV
// save with a row expanded and a number being edited.
func TestGoldenOfAnotherEngine(t *testing.T) {
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })
	dir := t.TempDir()
	data, err := os.ReadFile("internal/rpgmv/testdata/mv-file1.rpgsave")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "file1.rpgsave")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTester(t, path, "")
	ts.Tap(gluttertest.Descendant(gluttertest.ByKey(m.NewValueKey("party")), gluttertest.ByTooltip("Show the parts")))
	ts.Pump()
	ts.EnterText(fieldOf("party\x00_gold"), "9000")
	editable := gluttertest.Descendant(fieldOf("party\x00_gold"), gluttertest.ByType[*m.EditableText]())
	gluttertest.WidgetAs[*m.EditableText](ts, editable).FocusNode().Unfocus()
	ts.PumpAndSettle()
	ts.MatchesGolden("editor-mv")
}
