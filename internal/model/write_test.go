package model

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rm4n0s/errors"
)

func TestWriteFileKeepsTheFirstBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "1-1.save")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, body := range []string{"first edit", "second edit"} {
		if err := WriteFile(ctx, path, []byte(body), true); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	for _, tt := range []struct{ path, want string }{{path, "second edit"}, {path + ".bak", "original"}} {
		got, err := os.ReadFile(tt.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tt.want {
			t.Errorf("%s = %q, want %q", filepath.Base(tt.path), got, tt.want)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want the file's 0600", info.Mode().Perm())
	}
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 {
		t.Errorf("%d files in the folder, want the save and its backup", len(left))
	}
}

func TestWriteFileCreatesAndSkipsTheBackupOfANewFile(t *testing.T) {
	tests := []struct {
		name   string
		backup bool
	}{
		{"with a backup requested", true},
		{"without a backup", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "new.sav")
			if err := WriteFile(context.Background(), path, []byte("data"), tt.backup); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "data" {
				t.Errorf("file = %q, %v", got, err)
			}
			if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
				t.Errorf("a backup of a file that did not exist: %v", err)
			}
		})
	}
}

func TestWriteFileFailures(t *testing.T) {
	dir := t.TempDir()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name  string
		ctx   context.Context
		path  string
		back  bool
		route string
	}{
		{"a folder that does not exist", context.Background(), filepath.Join(dir, "no", "1-1.save"), false, "WriteFile.FileWriteFailed"},
		{"a backup of a folder", context.Background(), dir, true, "WriteFile.FileBackupFailed"},
		{"a cancelled context", cancelled, filepath.Join(dir, "1-1.save"), false, "WriteFile.FileWriteFailed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := WriteFile(tt.ctx, tt.path, []byte("x"), tt.back)
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
