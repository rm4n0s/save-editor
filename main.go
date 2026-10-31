// Command save-editor edits the variables of game save files: open a save, change numbers,
// text and switches, and save it again. Ren'Py saves are signed with your Ren'Py key so that
// the game loads them without asking.
//
// Run it with: go run . [save file]
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/rm4n0s/glutter/foundation"
	m "github.com/rm4n0s/glutter/material"
	"github.com/rm4n0s/save-editor/internal/renpysave"
)

func main() {
	path := ""
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	// Glutter installs no signal handlers: Ctrl+C and a plain kill close the window.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := m.RunAppContext(ctx, NewEditorApp(path, defaultKeysPath()), m.RunAppOpts{
		AppID: "io.github.rm4n0s.SaveEditor",
		Window: m.WindowConfig{
			Title:   "Save Editor",
			Size:    m.Size{Width: 1120, Height: 740},
			MinSize: m.Size{Width: 760, Height: 480},
		},
	})
	stop()
	if err != nil {
		slog.Error("the app stopped", "err", err)
		os.Exit(1)
	}
}

// renpyFolder is the folder where Ren'Py keeps the saves of every game and the tokens, as
// renpy.main.path_to_saves finds it; empty when the home folder is unknown.
func renpyFolder() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch foundation.DefaultTargetPlatform() {
	case foundation.TargetPlatformWindows:
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "RenPy")
		}
		return filepath.Join(home, "RenPy")
	case foundation.TargetPlatformMacOS:
		return filepath.Join(home, "Library", "RenPy")
	default:
		return filepath.Join(home, ".renpy")
	}
}

// defaultKeysPath is the security_keys.txt of the user's Ren'Py.
func defaultKeysPath() string {
	dir := renpyFolder()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "tokens", renpysave.KeysFileName)
}
