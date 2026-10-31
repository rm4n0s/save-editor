package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Format names, as the formats call themselves.
const (
	fRenpy = "Ren'Py"
	fGVAS  = "Unreal Engine"
	fLCF   = "RPG Maker 2000/2003"
	fXP    = "RPG Maker XP"
	fVX    = "RPG Maker VX"
	fVXAce = "RPG Maker VX Ace"
	fMV    = "RPG Maker MV"
	fMZ    = "RPG Maker MZ"
	fPrefs = "Unity PlayerPrefs"
	fNRBF  = "Unity / .NET BinaryFormatter"
	fES3   = "Easy Save 3"
	fJSON  = "Unity JSON"
	// none is a file that no format takes.
	none = ""
)

// everyFile is every file that the packages keep in their testdata folders and the format
// that must detect it, and no other. Saves of each format are listed first; the rest are the
// scripts, the notes and the files that look like a save but are not one.
var everyFile = map[string]string{
	"renpysave/testdata/renpy7.save": fRenpy, "renpysave/testdata/renpy8.save": fRenpy, "renpysave/testdata/friend.save": fRenpy,
	"gvas/testdata/ue4.sav": fGVAS, "gvas/testdata/ue5.sav": fGVAS, "gvas/testdata/ue54.sav": fGVAS,
	"lcf/testdata/rm2k3_cp1251.lsd": fLCF, "lcf/testdata/rm2k3_cp1252.lsd": fLCF,
	"lcf/testdata/rm2k3_easyrpg_cp1250.lsd": fLCF, "lcf/testdata/rm2k_sjis.lsd": fLCF,
	"rgss/testdata/xp.rxdata": fXP, "rgss/testdata/xp-extra.rxdata": fXP, "rgss/testdata/vx.rvdata": fVX, "rgss/testdata/ace.rvdata2": fVXAce,
	"rpgmv/testdata/mv-file1.rpgsave": fMV, "rpgmv/testdata/mv-global.rpgsave": fMV, "rpgmv/testdata/mv-config.rpgsave": fMV,
	"rpgmv/testdata/mz-file1.rmmzsave": fMZ, "rpgmv/testdata/mz-global.rmmzsave": fMZ, "rpgmv/testdata/mz-config.rmmzsave": fMZ,
	"unityprefs/testdata/linux.prefs": fPrefs, "unityprefs/testdata/mac-plistlib.plist": fPrefs, "unityprefs/testdata/mac-float32.plist": fPrefs,
	"nrbf/testdata/player.sav": fNRBF, "nrbf/testdata/extra.sav": fNRBF, "nrbf/testdata/array-root.sav": fNRBF,
	"nrbf/testdata/string-root.sav": fNRBF, "nrbf/testdata/spec-address.bin": fNRBF,
	"unityjson/testdata/es3-plain.es3": fES3, "unityjson/testdata/es3-gzip.es3": fES3, "unityjson/testdata/es3-aes.es3": fES3,
	"unityjson/testdata/es3-aes-gzip.es3":       fES3,
	"unityjson/testdata/es3-other-password.es3": fES3, // detected by its name and shape; opening it is refused with a message
	"unityjson/testdata/plain.json":             fJSON, "unityjson/testdata/root-array.json": fJSON,

	// The JSON texts that the MV and MZ tests keep beside their saves are JSON.
	"rpgmv/testdata/mv-file1.rpgsave.json": fJSON, "rpgmv/testdata/mv-global.rpgsave.json": fJSON,
	"rpgmv/testdata/mv-config.rpgsave.json": fJSON, "rpgmv/testdata/mz-file1.rmmzsave.json": fJSON,
	"rpgmv/testdata/mz-global.rmmzsave.json": fJSON, "rpgmv/testdata/mz-config.rmmzsave.json": fJSON,

	// Not saves: a plist whose top is an array, Ren'Py's key files, notes, pickles, Marshal
	// dumps, and the scripts that make and check the fixtures.
	"unityprefs/testdata/mac-array-root.plist": none,
	"renpysave/testdata/security_keys.txt":     none, "renpysave/testdata/other_keys.txt": none,
	"gvas/testdata/ue4.sav.expected": none, "gvas/testdata/ue5.sav.expected": none, "gvas/testdata/ue54.sav.expected": none,
	"pickle/testdata/large-p5.pickle": none, "pickle/testdata/sample-p0.pickle": none, "pickle/testdata/sample-p1.pickle": none,
	"pickle/testdata/sample-p2.pickle": none, "pickle/testdata/sample-p3.pickle": none, "pickle/testdata/sample-p4.pickle": none,
	"pickle/testdata/sample-p5.pickle": none, "rubymarshal/testdata/sample.marshal": none, "rubymarshal/testdata/types.marshal": none,
}

func TestEveryFixtureIsDetectedByExactlyItsFormat(t *testing.T) {
	root := "internal"
	paths, err := filepath.Glob(filepath.Join(root, "*", "testdata", "*"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	formats := supportedFormats()
	for _, path := range paths {
		rel := strings.TrimPrefix(filepath.ToSlash(path), root+"/")
		want, listed := everyFile[rel]
		if !listed {
			// Scripts of the generators and checkers are not saves.
			if ext := filepath.Ext(rel); ext == ".py" || ext == ".js" {
				want, listed = none, true
			}
		}
		if !listed {
			t.Errorf("%s is not in the table: say which format detects it", rel)
			continue
		}
		seen[rel] = true
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range formats {
			if f.Detect(filepath.Base(path), data) {
				got = append(got, f.Name)
			}
		}
		wantList := []string{want}
		if want == none {
			wantList = nil
		}
		if !slices.Equal(got, wantList) {
			t.Errorf("%s is detected by %q, want %q", rel, got, wantList)
		}
	}
	for rel := range everyFile {
		if !seen[rel] {
			t.Errorf("%s is in the table but not on disk", rel)
		}
	}
	if len(seen) < 60 {
		t.Errorf("only %d files were checked", len(seen))
	}
}

// TestNoFixtureIsTakenUnderAnotherName: the contents decide, so the same bytes under the
// names of the other formats are taken by the same format, or by none.
func TestNoFixtureIsTakenUnderAnotherName(t *testing.T) {
	names := []string{"x.dat", "x.save", "x.sav", "x.bin", "x.json", "x.txt", "x"}
	formats := supportedFormats()
	for rel, want := range everyFile {
		if want == none || strings.HasSuffix(rel, "other-password.es3") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("internal", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			var got []string
			for _, f := range formats {
				if f.Detect(name, data) {
					got = append(got, f.Name)
				}
			}
			if !slices.Equal(got, []string{want}) {
				t.Errorf("%s under the name %s is detected by %q, want %q", rel, name, got, want)
			}
		}
	}
}
