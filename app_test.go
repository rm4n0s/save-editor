package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rm4n0s/glutter/foundation"
	"github.com/rm4n0s/glutter/gluttertest"
	m "github.com/rm4n0s/glutter/material"
	"github.com/rm4n0s/save-editor/internal/renpysave"
)

const (
	fixtures = "internal/renpysave/testdata"
	testKeys = fixtures + "/security_keys.txt"
)

// useLinux makes the framework behave as on the Linux desktop: the tests default to Android.
func useLinux(t *testing.T) {
	t.Helper()
	if !foundation.DebugMode {
		t.Skip("the platform override is a debug-only feature")
	}
	foundation.SetDebugDefaultTargetPlatformOverride(foundation.Some(foundation.TargetPlatformLinux))
	t.Cleanup(func() { foundation.SetDebugDefaultTargetPlatformOverride(foundation.None[foundation.TargetPlatform]()) })
}

// copySave copies a save of the fixtures into a folder of the test.
func copySave(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "1-1.save")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// newTester starts the app with the save at path (none when empty) and the keys at keysPath,
// and waits until both are read.
func newTester(t *testing.T, path, keysPath string) (*gluttertest.Tester, *gluttertest.FakeDesktop) {
	t.Helper()
	useLinux(t)
	ts := gluttertest.New(t, gluttertest.Opts{Size: m.Size{Width: 1120, Height: 740}})
	fd := ts.FakeDesktop()
	ts.PumpWidget(NewEditorApp(path, keysPath))
	waitFor(t, ts, "the save and the keys", func() bool {
		s := state(ts)
		return (path == "" || s.save != nil) && (keysPath == "" || s.keys != nil)
	})
	return ts, fd
}

func state(ts *gluttertest.Tester) *editorState {
	return gluttertest.StateOf[*editorState](ts, gluttertest.ByType[*EditorPage]())
}

// waitFor pumps frames until the goroutines of the page have handed their results over.
func waitFor(t *testing.T, ts *gluttertest.Tester, what string, done func() bool) {
	t.Helper()
	for range 500 {
		ts.Pump(10 * time.Millisecond)
		if done() {
			ts.Pump() // builds what the last SetState changed
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("waited for %s: status %q", what, state(ts).status)
}

// fieldOf is the text field in the row of the variable or part with the id.
func fieldOf(id string) gluttertest.Finder {
	return gluttertest.Descendant(gluttertest.ByKey(m.NewValueKey(id)), gluttertest.ByType[*m.TextField]())
}

func reopen(t *testing.T, path string) *renpysave.Save {
	t.Helper()
	s, err := renpysave.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func valueOf(t *testing.T, s *renpysave.Save, name string) string {
	t.Helper()
	for _, v := range s.Variables() {
		if v.Name == name {
			return v.Slot.Value.Repr()
		}
	}
	t.Fatalf("no variable %q", name)
	return ""
}

func TestShowsTheSave(t *testing.T) {
	ts, _ := newTester(t, copySave(t, "renpy8.save"), testKeys)
	if got := state(ts).saveName.Text(); got != "Chapter 2" {
		t.Errorf("the save name field has %q", got)
	}
	for _, text := range []string{"1-1.save", "money", "player_name", "inventory", "Signed with your key", "1:02:03"} {
		if !ts.Exists(gluttertest.ByText(text)) {
			t.Errorf("%q is not shown", text)
		}
	}
	if ts.Exists(gluttertest.ByText("_internal")) {
		t.Error("an internal variable is shown without the filter chip")
	}
	ts.Tap(gluttertest.ByText("Ren'Py internals"))
	ts.Pump()
	if !ts.Exists(gluttertest.ByText("_internal")) {
		t.Error("the filter chip does not show the internal variables")
	}
}

func TestSearch(t *testing.T) {
	tests := []struct {
		query string
		shown []string
		gone  []string
	}{
		{"mon", []string{"money"}, []string{"player_name", "inventory"}},
		{"annann", []string{"player_name", "nickname"}, []string{"money"}},
		{"nothing at all", []string{"No variable matches."}, []string{"money"}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			ts, _ := newTester(t, copySave(t, "renpy8.save"), "")
			ts.EnterText(gluttertest.ByKey(m.NewValueKey("search")), tt.query)
			ts.Pump()
			for _, text := range tt.shown {
				if !ts.Exists(gluttertest.ByText(text)) {
					t.Errorf("%q is not shown", text)
				}
			}
			for _, text := range tt.gone {
				if ts.Exists(gluttertest.ByText(text)) {
					t.Errorf("%q is shown", text)
				}
			}
		})
	}
}

func TestEditAndSave(t *testing.T) {
	tests := []struct {
		name      string
		keys      string
		signature renpysave.SignatureStatus
	}{
		{"signed with the key", testKeys, renpysave.SignedTrusted},
		{"without a key the signatures go", "", renpysave.Unsigned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := copySave(t, "renpy8.save")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			ts, _ := newTester(t, path, tt.keys)
			ts.EnterText(fieldOf("money"), "123456789")
			ts.EnterText(fieldOf("ratio"), "2.25")
			ts.EnterText(fieldOf("player_name"), "Bob")
			ts.PumpAndSettle() // the focused field scrolls into view
			ts.Tap(gluttertest.Descendant(gluttertest.ByKey(m.NewValueKey("has_key")), gluttertest.ByType[*m.Switch]()))
			ts.Pump()
			ts.Tap(gluttertest.Descendant(gluttertest.ByKey(m.NewValueKey("inventory")), gluttertest.ByTooltip("Show the parts")))
			ts.Pump()
			ts.EnterText(fieldOf("inventory\x00[0]"), "axe")
			ts.Pump()
			if !ts.Exists(gluttertest.ByText("5 changes")) {
				t.Fatalf("the count of changes is not shown; status %q", state(ts).status)
			}
			ts.Tap(gluttertest.ByTooltip("Save (Ctrl+S)"))
			waitFor(t, ts, "the save", func() bool { return len(state(ts).edits) == 0 && !state(ts).busy })

			saved := reopen(t, path)
			for _, want := range []struct{ name, repr string }{
				{"money", "123456789"}, {"ratio", "2.25"}, {"player_name", `"Bob"`}, {"nickname", `"AnnAnn"`}, {"has_key", "False"},
			} {
				if got := valueOf(t, saved, want.name); got != want.repr {
					t.Errorf("%s = %s, want %s", want.name, got, want.repr)
				}
			}
			ring, err := renpysave.LoadKeyRing(context.Background(), testKeys)
			if err != nil {
				t.Fatal(err)
			}
			if got := saved.SignatureStatus(ring); got != tt.signature {
				t.Errorf("signature %v, want %v", got, tt.signature)
			}
			backup, err := os.ReadFile(path + ".bak")
			if err != nil || !bytes.Equal(backup, original) {
				t.Errorf("the backup is not the original save: %v", err)
			}
			if !ts.Exists(gluttertest.ByText("No changes")) {
				t.Error("the page still counts changes after saving")
			}
		})
	}
}

func TestInvalidValuesBlockSaving(t *testing.T) {
	path := copySave(t, "renpy8.save")
	ts, _ := newTester(t, path, testKeys)
	ts.EnterText(fieldOf("money"), "lots")
	ts.EnterText(fieldOf("ratio"), "1/2")
	ts.Pump()
	for _, text := range []string{"Enter a whole number", "Enter a number"} {
		if !ts.Exists(gluttertest.ByText(text)) {
			t.Errorf("the error %q is not shown", text)
		}
	}
	ts.Tap(gluttertest.ByTooltip("Save (Ctrl+S)"))
	ts.Pump()
	if got := state(ts).status; got != "Fix the values marked in red before saving." {
		t.Errorf("status %q", got)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Error("the save was written")
	}
}

func TestUndoAndDiscard(t *testing.T) {
	ts, _ := newTester(t, copySave(t, "renpy8.save"), "")
	ts.EnterText(fieldOf("money"), "1")
	ts.EnterText(fieldOf("neg"), "2")
	ts.Pump()
	ts.Tap(gluttertest.Descendant(gluttertest.ByKey(m.NewValueKey("money")), gluttertest.ByTooltip("Undo this change")))
	ts.Pump()
	if s := state(ts); len(s.edits) != 1 || s.fields[s.vars[indexOf(t, s, "money")].Slot.Site.Start].Text() != "150" {
		t.Errorf("undo left %d edits", len(s.edits))
	}
	ts.Tap(gluttertest.ByText("Discard"))
	ts.Pump()
	if s := state(ts); len(s.edits) != 0 || s.fields[s.vars[indexOf(t, s, "neg")].Slot.Site.Start].Text() != "-129" {
		t.Errorf("discard left %d edits", len(s.edits))
	}
	ts.EnterText(fieldOf("money"), "150")
	ts.Pump()
	if n := len(state(ts).edits); n != 0 {
		t.Errorf("typing the old value back is %d edits", n)
	}
}

func indexOf(t *testing.T, s *editorState, name string) int {
	t.Helper()
	for i, v := range s.vars {
		if v.Name == name {
			return i
		}
	}
	t.Fatalf("no variable %q", name)
	return -1
}

func TestSaveAsAndRename(t *testing.T) {
	path := copySave(t, "renpy7.save")
	ts, fd := newTester(t, path, testKeys)
	if !ts.Exists(gluttertest.ByText("Not signed")) {
		t.Error("the Ren'Py 7 save is not shown as unsigned")
	}
	ts.EnterText(gluttertest.ByKey(m.NewValueKey("save name")), "Renamed")
	ts.EnterText(fieldOf("money"), "7")
	ts.Pump()
	ts.Tap(gluttertest.ByTooltip("Save as (Ctrl+Shift+S)"))
	fd.WaitFor("the file dialog", func() bool { return fd.PendingFileChoosers() == 1 })
	target := filepath.Join(filepath.Dir(path), "2-1.save")
	fd.AnswerFileChooser(target)
	waitFor(t, ts, "the save", func() bool { return state(ts).path == target })
	saved := reopen(t, target)
	if got := valueOf(t, saved, "money"); got != "7" {
		t.Errorf("money = %s", got)
	}
	if saved.SaveName() != "Renamed" || saved.Metadata["_save_name"] != "Renamed" {
		t.Errorf("name %q, json name %v", saved.SaveName(), saved.Metadata["_save_name"])
	}
	if _, err := os.Stat(target + ".bak"); !os.IsNotExist(err) {
		t.Error("save as made a backup")
	}
	if got := valueOf(t, reopen(t, path), "money"); got != "150" {
		t.Errorf("the first file changed: money = %s", got)
	}
}

func TestOpenWithTheDialog(t *testing.T) {
	ts, fd := newTester(t, "", "")
	if !ts.Exists(gluttertest.ByText("Open a Ren'Py save")) {
		t.Fatal("the empty page is not shown")
	}
	ts.Tap(gluttertest.ByText("Open save…"))
	fd.WaitFor("the file dialog", func() bool { return fd.PendingFileChoosers() == 1 })
	fd.AnswerFileChooser(copySave(t, "friend.save"))
	waitFor(t, ts, "the save", func() bool { return state(ts).save != nil })
	if !ts.Exists(gluttertest.ByText("Signed on another computer")) {
		t.Error("the signature of a friend's save is not shown")
	}
}

func TestOpenFailureIsShown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.save")
	if err := os.WriteFile(path, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTester(t, "", "")
	state(ts).open(path)
	waitFor(t, ts, "the error", func() bool { return state(ts).statusError })
	if !ts.Exists(gluttertest.ByTextContaining("Cannot open broken.save")) {
		t.Errorf("status %q", state(ts).status)
	}
}

func TestGolden(t *testing.T) {
	// The page shows the folder and the local time of the save: both are fixed here. The
	// test only edits in memory, so it reads the fixture in place.
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })
	ts, _ := newTester(t, filepath.Join(fixtures, "renpy8.save"), testKeys)
	ts.Tap(gluttertest.Descendant(gluttertest.ByKey(m.NewValueKey("hero")), gluttertest.ByTooltip("Show the parts")))
	ts.Pump()
	ts.EnterText(fieldOf("money"), "9000")
	// A blinking caret would make the picture depend on the time.
	editable := gluttertest.Descendant(fieldOf("money"), gluttertest.ByType[*m.EditableText]())
	gluttertest.WidgetAs[*m.EditableText](ts, editable).FocusNode().Unfocus()
	ts.PumpAndSettle()
	ts.MatchesGolden("editor")
}

// TestTypingKeepsTheFocus types into fields one key at a time: the first key marks the row as
// changed, and the field must keep the focus through that rebuild.
func TestTypingKeepsTheFocus(t *testing.T) {
	for _, id := range []string{"money", "nickname", "hero\x00.hp"} {
		t.Run(id, func(t *testing.T) {
			ts, _ := newTester(t, copySave(t, "renpy8.save"), "")
			ts.Tap(gluttertest.Descendant(gluttertest.ByKey(m.NewValueKey("hero")), gluttertest.ByTooltip("Show the parts")))
			ts.Pump()
			editable := gluttertest.Descendant(fieldOf(id), gluttertest.ByType[*m.EditableText]())
			for _, text := range []string{"1", "12", "x", "123"} {
				ts.EnterText(fieldOf(id), text)
				ts.Pump()
				if !gluttertest.WidgetAs[*m.EditableText](ts, editable).FocusNode().HasPrimaryFocus() {
					t.Fatalf("after typing %q the field of %s lost the focus", text, id)
				}
			}
		})
	}
}
