package renpysave

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

func openSave(t *testing.T, name string) *Document {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("Open(%s): %v", name, err)
	}
	return s
}

func loadKeys(t *testing.T, name string) *KeyRing {
	t.Helper()
	k, err := LoadKeyRing(context.Background(), filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("LoadKeyRing(%s): %v", name, err)
	}
	return k
}

func variable(t *testing.T, s *Document, name string) model.Variable {
	t.Helper()
	for _, v := range s.Variables() {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no variable %q", name)
	return model.Variable{}
}

// entries are the names and bodies of the files of a zip, in order.
func entries(t *testing.T, data []byte) ([]string, map[string][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	bodies := map[string][]byte{}
	for _, f := range zr.File {
		b, err := readEntry(f)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, f.Name)
		bodies[f.Name] = b
	}
	return names, bodies
}

func TestOpenReadsTheParts(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		screenshot bool
		protocol   int
	}{
		{"renpy8.save", "8.3.4.24120703", true, 5},
		{"renpy7.save", "7.4.11.2266", false, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openSave(t, tt.name)
			if s.SaveName() != "Chapter 2" {
				t.Errorf("save name %q", s.SaveName())
			}
			if s.RenpyVersion != tt.version {
				t.Errorf("version %q, want %q", s.RenpyVersion, tt.version)
			}
			if got := bytes.HasPrefix(s.Screenshot(), []byte("\x89PNG")); got != tt.screenshot {
				t.Errorf("screenshot %v, want %v", got, tt.screenshot)
			}
			if s.Log.Protocol != tt.protocol {
				t.Errorf("protocol %d, want %d", s.Log.Protocol, tt.protocol)
			}
			if got := s.Metadata["_game_runtime"]; got != json.Number("3723.25") {
				t.Errorf("_game_runtime = %v", got)
			}
			vars := s.Variables()
			if len(vars) != 17 {
				t.Errorf("%d variables, want 17", len(vars))
			}
			if vars[0].Name != "_internal" || !vars[0].Internal || vars[1].Name != "big" || vars[1].Internal {
				t.Errorf("the variables are not sorted with _internal marked: %+v %+v", vars[0], vars[1])
			}
			if v := variable(t, s, "money"); v.Slot.Value.Repr() != "150" || v.Slot.Site == nil {
				t.Errorf("money = %+v", v)
			}
		})
	}
}

func TestSignatureStatus(t *testing.T) {
	mine := loadKeys(t, "security_keys.txt")
	other := loadKeys(t, "other_keys.txt")
	tests := []struct {
		name string
		save string
		ring *KeyRing
		want SignatureStatus
	}{
		{"signed with the key of the ring", "renpy8.save", mine, SignedTrusted},
		{"signed with another key", "renpy8.save", other, SignedUntrusted},
		{"without a ring", "renpy8.save", nil, SignedUntrusted},
		{"a key the ring trusts", "friend.save", mine, SignedTrusted},
		{"a trusted key only verifies", "friend.save", other, SignedUntrusted},
		{"no signatures", "renpy7.save", mine, Unsigned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := openSave(t, tt.save).SignatureStatus(tt.ring); got != tt.want {
				t.Errorf("status %v, want %v", got, tt.want)
			}
		})
	}
	s := openSave(t, "renpy8.save")
	changed := append(bytes.Clone(s.Log.Data), 0)
	if got := VerifySignatures(changed, s.Signatures, mine); got != SignedInvalid {
		t.Errorf("a changed log is %v, want %v", got, SignedInvalid)
	}
}

func TestEncode(t *testing.T) {
	mine := loadKeys(t, "security_keys.txt")
	other := loadKeys(t, "other_keys.txt")
	name := "New name <&>"
	tests := []struct {
		name       string
		save       string
		opts       func(s *Document) EncodeOpts
		money      string
		signatures SignatureStatus
		names      []string
		saveName   string
	}{
		{"no change", "renpy8.save", func(*Document) EncodeOpts { return EncodeOpts{} }, "150", SignedTrusted,
			[]string{"screenshot.png", "extra_info", "json", "renpy_version", "log", "signatures"}, "Chapter 2"},
		{"an edit drops the signatures", "renpy8.save", func(s *Document) EncodeOpts {
			return EncodeOpts{Edits: moneyEdit(t, s, 5000)}
		}, "5000", Unsigned, []string{"screenshot.png", "extra_info", "json", "renpy_version", "log"}, "Chapter 2"},
		{"an edit signed with the ring", "renpy8.save", func(s *Document) EncodeOpts {
			return EncodeOpts{Edits: moneyEdit(t, s, 5000), Keys: mine}
		}, "5000", SignedTrusted, []string{"screenshot.png", "extra_info", "json", "renpy_version", "log", "signatures"}, "Chapter 2"},
		{"a friend's save signed with the ring", "friend.save", func(*Document) EncodeOpts {
			return EncodeOpts{Keys: mine}
		}, "150", SignedTrusted, []string{"extra_info", "json", "renpy_version", "log", "signatures"}, "Chapter 2"},
		{"an unsigned save gets signatures", "renpy7.save", func(s *Document) EncodeOpts {
			return EncodeOpts{Edits: moneyEdit(t, s, 1), Keys: mine}
		}, "1", SignedTrusted, []string{"extra_info", "json", "renpy_version", "log", "signatures"}, "Chapter 2"},
		{"a ring without a signing key keeps nothing", "renpy8.save", func(s *Document) EncodeOpts {
			ring, err := ParseKeyRing("verifying-key " + mustLine(t, "security_keys.txt", "verifying-key"))
			if err != nil {
				t.Fatal(err)
			}
			return EncodeOpts{Edits: moneyEdit(t, s, 2), Keys: ring}
		}, "2", Unsigned, []string{"screenshot.png", "extra_info", "json", "renpy_version", "log"}, "Chapter 2"},
		{"a new name", "renpy8.save", func(*Document) EncodeOpts { return EncodeOpts{SaveName: &name} }, "150", SignedTrusted,
			[]string{"screenshot.png", "extra_info", "json", "renpy_version", "log", "signatures"}, name},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openSave(t, tt.save)
			out, err := s.EncodeWith(tt.opts(s))
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			again, err := Parse(out)
			if err != nil {
				t.Fatalf("the new save does not parse: %v", err)
			}
			if got := variable(t, again, "money").Slot.Value.Repr(); got != tt.money {
				t.Errorf("money = %s, want %s", got, tt.money)
			}
			if got := again.SignatureStatus(mine); got != tt.signatures {
				t.Errorf("signatures %v, want %v", got, tt.signatures)
			}
			if tt.signatures == SignedTrusted && again.SignatureStatus(other) != SignedUntrusted {
				t.Errorf("another ring finds %v", again.SignatureStatus(other))
			}
			names, bodies := entries(t, out)
			if !equal(names, tt.names) {
				t.Errorf("entries %v, want %v", names, tt.names)
			}
			if !bytes.Equal(again.Screenshot(), s.Screenshot()) {
				t.Error("the screenshot changed")
			}
			if again.SaveName() != tt.saveName || again.Metadata["_save_name"] != tt.saveName {
				t.Errorf("name %q, json name %v, want %q", again.SaveName(), again.Metadata["_save_name"], tt.saveName)
			}
			if again.Metadata["_game_runtime"] != json.Number("3723.25") || again.Metadata["_version"] != "1.0" {
				t.Errorf("the metadata lost its other keys: %s", bodies["json"])
			}
		})
	}
}

func moneyEdit(t *testing.T, s *Document, n int64) []model.Edit {
	t.Helper()
	return []model.Edit{{Site: variable(t, s, "money").Slot.Site, Value: &model.Value{Kind: model.KindInt, Int: big.NewInt(n)}}}
}

// mustLine is the base64 of the first field of the line of kind in a key file of testdata.
func mustLine(t *testing.T, file, kind string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if fields := bytes.Fields(line); len(fields) >= 2 && string(fields[0]) == kind {
			return string(fields[1])
		}
	}
	t.Fatalf("%s has no %s line", file, kind)
	return ""
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func zipOf(t *testing.T, method uint16, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFailures(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	corrupt := zipOf(t, zip.Store, map[string]string{"log": "\x80\x02N."})
	corrupt[bytes.Index(corrupt, []byte("\x80\x02N."))+2] = 'X' // the checksum no longer matches
	tests := []struct {
		name  string
		run   func() error
		route string
	}{
		{"a missing file", func() error {
			_, err := Open(ctx, filepath.Join(dir, "missing.save"))
			return err
		}, "Open.SaveUnreadable"},
		{"not a zip", func() error {
			_, err := Parse([]byte("not a zip"))
			return err
		}, "Parse.SaveNotZip"},
		{"no log", func() error {
			_, err := Parse(zipOf(t, zip.Deflate, map[string]string{"json": "{}"}))
			return err
		}, "Parse.SaveLogMissing"},
		{"a broken entry", func() error {
			_, err := Parse(corrupt)
			return err
		}, "Parse->readEntry.SaveEntryUnreadable"},
		{"a broken pickle", func() error {
			_, err := Parse(zipOf(t, zip.Deflate, map[string]string{"log": "\x80\x02N"}))
			return err
		}, "Parse->Parse->parser.run.PickleNoStop"},
		{"a log that is not (roots, log)", func() error {
			_, err := Parse(zipOf(t, zip.Deflate, map[string]string{"log": "\x80\x02]."}))
			return err
		}, "Parse.SaveRootsMissing"},
		{"an edit of a place of another save", func() error {
			s := openSave(t, "renpy8.save")
			_, err := s.Encode(moneyEdit(t, openSave(t, "renpy8.save"), 1))
			return err
		}, "Document.Encode->Document.EncodeWith->Document.patches.EditSiteUnknown"},
		{"an edit without a site", func() error {
			s := openSave(t, "renpy8.save")
			_, err := s.Encode([]model.Edit{{Value: &model.Value{Kind: model.KindInt, Int: big.NewInt(1)}}})
			return err
		}, "Document.patches.EditSiteUnknown"},
		{"an edit of another kind", func() error {
			s := openSave(t, "renpy8.save")
			_, err := s.Encode([]model.Edit{{Site: variable(t, s, "money").Slot.Site, Value: &model.Value{Kind: model.KindString, Str: "x"}}})
			return err
		}, "Document.patches.EditKindMismatch"},
		{"an edit without a value", func() error {
			s := openSave(t, "renpy8.save")
			_, err := s.Encode([]model.Edit{{Site: variable(t, s, "money").Slot.Site}})
			return err
		}, "Document.patches.EditKindMismatch"},
		{"a number without digits", func() error {
			s := openSave(t, "renpy8.save")
			_, err := s.Encode([]model.Edit{{Site: variable(t, s, "money").Slot.Site, Value: &model.Value{Kind: model.KindInt}}})
			return err
		}, "Document.patches.EditKindMismatch"},
		{"the same place edited twice", func() error {
			s := openSave(t, "renpy8.save")
			_, err := s.Encode(append(moneyEdit(t, s, 1), moneyEdit(t, s, 2)...))
			return err
		}, "Document.Patch.PickleSiteNotEditable"},
		{"a missing key file", func() error {
			_, err := LoadKeyRing(ctx, filepath.Join(dir, "missing.txt"))
			return err
		}, "LoadKeyRing.KeysUnreadable"},
		{"a key file without keys", func() error {
			_, err := ParseKeyRing("# nothing\n\nsignature AAAA\n")
			return err
		}, "ParseKeyRing.KeysMissing"},
		{"a bad signing key", func() error {
			_, err := ParseKeyRing("signing-key AAAA\n")
			return err
		}, "ParseKeyRing->parsePrivateKey.KeyInvalid"},
		{"a raw key out of range", func() error {
			_, err := ParseKeyRing("signing-key " + "//////////////////////////////////////////8=" + "\n")
			return err
		}, "ParseKeyRing->parsePrivateKey.KeyInvalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appErr, ok := errors.FromError(tt.run())
			if !ok {
				t.Fatalf("expected *errors.Error")
			}
			if !appErr.HasRoute(tt.route) {
				t.Errorf("unexpected failure path: %s", appErr.Route())
			}
		})
	}
}
