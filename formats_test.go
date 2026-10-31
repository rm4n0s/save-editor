package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/glutter/desktop"
	"github.com/rm4n0s/save-editor/internal/model"
	"github.com/rm4n0s/save-editor/internal/renpysave"
)

func TestSupportedFormats(t *testing.T) {
	formats := supportedFormats()
	if len(formats) == 0 || formats[0].Name != "Ren'Py" {
		t.Fatalf("formats %v; Ren'Py is the first", formatNames(formats))
	}
	for _, f := range formats {
		if f.Name == "" || len(f.Patterns) == 0 || f.Detect == nil || f.Parse == nil {
			t.Errorf("format %+v is incomplete", f)
		}
	}
}

func TestOpenDocument(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	renpy, err := os.ReadFile(filepath.Join(fixtures, "renpy8.save"))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	// Ren'Py's detection is by contents, so the extension does not matter.
	if doc, format, err := openDocument(ctx, supportedFormats(), write("slot.dat", renpy)); err != nil {
		t.Errorf("a Ren'Py save called slot.dat: %v", err)
	} else if _, ok := doc.(*renpysave.Document); !ok || format.Name != "Ren'Py" {
		t.Errorf("opened %T as %s", doc, format.Name)
	}
	tests := []struct {
		name    string
		ctx     context.Context
		path    string
		formats []model.Format
		route   string
		message string
	}{
		{"a missing file", ctx, filepath.Join(dir, "missing.save"), supportedFormats(), "openDocument.FileUnreadable", ""},
		{"a cancelled context", cancelled, write("a.save", renpy), supportedFormats(), "openDocument.FileUnreadable", ""},
		{"a file no format knows", ctx, write("notes.txt", []byte("hello")), supportedFormats(),
			"openDocument->detectFormat.FormatUnknown", "This file is not a save of a supported engine"},
		{"an empty file", ctx, write("empty.save", nil), supportedFormats(),
			"openDocument->detectFormat.FormatUnknown", "This file is not a save of a supported engine"},
		{"no formats", ctx, write("b.save", renpy), nil, "openDocument->detectFormat.FormatUnknown", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := openDocument(tt.ctx, tt.formats, tt.path)
			appErr, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("expected *errors.Error, got %T: %v", err, err)
			}
			if !appErr.HasRoute(tt.route) {
				t.Errorf("unexpected failure path: %s", appErr.Route())
			}
			if tt.message != "" && appErr.Message != tt.message {
				t.Errorf("message %q, want %q", appErr.Message, tt.message)
			}
		})
	}
}

func TestDetectFormatTakesTheFirstThatSaysYes(t *testing.T) {
	yes := func(string, []byte) bool { return true }
	no := func(string, []byte) bool { return false }
	formats := []model.Format{{Name: "A", Detect: no}, {Name: "B", Detect: yes}, {Name: "C", Detect: yes}}
	got, err := detectFormat(formats, "x", nil)
	if err != nil || got.Name != "B" {
		t.Errorf("detected %q, %v", got.Name, err)
	}
}

func TestFilters(t *testing.T) {
	formats := []model.Format{
		{Name: "Ren'Py", Patterns: []string{"*.save"}},
		{Name: "RPG Maker MV", Patterns: []string{"*.rpgsave", "*.rmmzsave"}},
	}
	got := openFilters(formats)
	want := []desktop.FileFilter{
		{Name: "All supported saves", Patterns: []string{"*.save", "*.SAVE", "*.rpgsave", "*.RPGSAVE", "*.rmmzsave", "*.RMMZSAVE"}},
		{Name: "Ren'Py saves", Patterns: []string{"*.save", "*.SAVE"}},
		{Name: "RPG Maker MV saves", Patterns: []string{"*.rpgsave", "*.RPGSAVE", "*.rmmzsave", "*.RMMZSAVE"}},
		{Name: "All files", Patterns: []string{"*"}},
	}
	if len(got) != len(want) {
		t.Fatalf("%d filters, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Name != want[i].Name || !slices.Equal(got[i].Patterns, want[i].Patterns) {
			t.Errorf("filter %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	save := saveFilters(formats[1])
	if len(save) != 1 || save[0].Name != "RPG Maker MV saves" || !slices.Equal(save[0].Patterns, want[2].Patterns) {
		t.Errorf("save filters %+v", save)
	}
	if got := dialogPatterns([]string{"*"}); !slices.Equal(got, []string{"*"}) {
		t.Errorf("dialogPatterns(*) = %v", got)
	}
}

func TestFormatNames(t *testing.T) {
	if got := formatNames([]model.Format{{Name: "A"}, {Name: "B"}}); got != "A, B" {
		t.Errorf("formatNames = %q", got)
	}
}

func TestEncryptedEasySaveWithAnotherPasswordIsRefusedWithAMessage(t *testing.T) {
	path := filepath.Join("internal", "unityjson", "testdata", "es3-other-password.es3")
	_, _, err := openDocument(context.Background(), supportedFormats(), path)
	appErr, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("expected *errors.Error, got %T: %v", err, err)
	}
	if !appErr.HasRoute("openDocument->parseES3.UnityJSONEncrypted") || !strings.Contains(appErr.Message, "password other than the default") {
		t.Errorf("route %s, message %q", appErr.Route(), appErr.Message)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range supportedFormats() {
		if f.Detect("es3-other-password.es3", data) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d formats detect the file", n)
	}
	// The same bytes under a name that is not .es3 cannot be told from random bytes.
	other := filepath.Join(t.TempDir(), "slot.dat")
	if err := os.WriteFile(other, data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = openDocument(context.Background(), supportedFormats(), other)
	assertTag(t, err, tagFormatUnknown)
}

func assertTag(t *testing.T, err error, tag string) {
	t.Helper()
	appErr, ok := errors.FromError(err)
	if !ok || appErr.Tag != tag {
		t.Errorf("error %v, want tag %s", err, tag)
	}
}
