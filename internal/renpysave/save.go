// Package renpysave reads and writes Ren'Py save files. A save is a zip file with the pickled
// game state ("log"), a screenshot, the save's name and metadata, and from Ren'Py 7.6 and 8.1
// on, signatures of the log made with the player's key.
package renpysave

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/pickle"
)

// The names of the files in a save, as Ren'Py's loadsave.SaveRecord writes them.
const (
	entryScreenshot   = "screenshot.png"
	entryExtraInfo    = "extra_info"
	entryJSON         = "json"
	entryRenpyVersion = "renpy_version"
	entryLog          = "log"
	entrySignatures   = "signatures"
)

// Save is a parsed save file.
type Save struct {
	zip *zip.Reader

	// Log is the pickle of the (roots, rollback log) tuple.
	Log *pickle.Document
	// Roots is the dict of the variables of the stores, by "store.name".
	Roots *pickle.Value
	// Screenshot is the PNG that the save and load screens show, or nil.
	Screenshot []byte
	// ExtraInfo is the save's name, the extra_info of renpy.save.
	ExtraInfo string
	// Metadata is the save's JSON: _save_name, _renpy_version, _version, _ctime,
	// _game_runtime and what the game added. Numbers are json.Number.
	Metadata map[string]any
	// RenpyVersion is the version of Ren'Py that wrote the save.
	RenpyVersion string
	// Signatures is the text of the signatures file, empty when there is none.
	Signatures string
}

// Variable is one variable of a store.
type Variable struct {
	// Key is the key in the roots, such as "store.money" or "store.mystore.x".
	Key string
	// Name is the key without the "store." of the default store, such as "money" or
	// "mystore.x".
	Name string
	// Internal is a variable of Ren'Py or of a library, whose name starts with an
	// underscore.
	Internal bool
	Slot     pickle.Slot
}

// Open reads the save file at path.
func Open(ctx context.Context, path string) (*Save, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.NewErr(tagSaveUnreadable, err).SetMetadata("path", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.NewErr(tagSaveUnreadable, err).SetMetadata("path", path)
	}
	return Parse(data)
}

// Parse reads a save from the bytes of its file.
func Parse(data []byte) (*Save, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.NewErr(tagSaveNotZip, err)
	}
	s := &Save{zip: zr}
	var logData, metadata []byte
	for _, f := range zr.File {
		var target *[]byte
		var text *string
		switch f.Name {
		case entryLog:
			target = &logData
		case entryScreenshot:
			target = &s.Screenshot
		case entryJSON:
			target = &metadata
		case entryExtraInfo:
			text = &s.ExtraInfo
		case entryRenpyVersion:
			text = &s.RenpyVersion
		case entrySignatures:
			text = &s.Signatures
		default:
			continue
		}
		b, err := readEntry(f)
		if err != nil {
			return nil, err
		}
		if target != nil {
			*target = b
		} else {
			*text = string(b)
		}
	}
	if len(metadata) > 0 {
		dec := json.NewDecoder(bytes.NewReader(metadata))
		dec.UseNumber()
		// The metadata is only shown; a save whose JSON is broken still loads in Ren'Py,
		// so it opens here too.
		if dec.Decode(&s.Metadata) != nil {
			s.Metadata = nil
		}
	}
	if logData == nil {
		return nil, errors.New(tagSaveLogMissing, "the file has no log: it is not a Ren'Py save")
	}
	doc, err := pickle.Parse(logData)
	if err != nil {
		return nil, err
	}
	top := doc.Root.Value
	if top.Kind != pickle.KindTuple || len(top.Items) < 1 || top.Items[0].Value.Kind != pickle.KindDict {
		return nil, errors.New(tagSaveRootsMissing, "the log does not start with the dict of the variables",
			"type", top.TypeName())
	}
	s.Log = doc
	s.Roots = top.Items[0].Value
	return s, nil
}

func readEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, errors.NewErr(tagSaveEntryUnreadable, err).SetMetadata("entry", f.Name)
	}
	defer func() { _ = rc.Close() }() // a read-only zip entry; io.ReadAll has seen any error
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, errors.NewErr(tagSaveEntryUnreadable, err).SetMetadata("entry", f.Name)
	}
	return b, nil
}

// Variables are the variables of the roots whose keys are strings, sorted by name.
func (s *Save) Variables() []Variable {
	var out []Variable
	seen := map[string]int{}
	for _, e := range s.Roots.Entries {
		k := e.Key.Value
		if k.Kind != pickle.KindString && k.Kind != pickle.KindByteString {
			continue
		}
		name := strings.TrimPrefix(k.Str, "store.")
		internal := false
		for part := range strings.SplitSeq(name, ".") {
			if strings.HasPrefix(part, "_") {
				internal = true
			}
		}
		v := Variable{Key: k.Str, Name: name, Internal: internal, Slot: e.Value}
		if i, ok := seen[k.Str]; ok {
			out[i] = v // the last value of a key is the one Python keeps
			continue
		}
		seen[k.Str] = len(out)
		out = append(out, v)
	}
	slices.SortStableFunc(out, func(a, b Variable) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// SaveName is the name the save and load screens show.
func (s *Save) SaveName() string { return s.ExtraInfo }

// SignatureStatus checks the save's signatures against its log; ring may be nil.
func (s *Save) SignatureStatus(ring *KeyRing) SignatureStatus {
	return VerifySignatures(s.Log.Data, s.Signatures, ring)
}

// EncodeOpts are the changes Encode makes.
type EncodeOpts struct {
	// Edits are the new values of variables.
	Edits []pickle.Edit
	// SaveName, when set, is the new name of the save.
	SaveName *string
	// Keys, when it can sign, signs the new log. Without it, the signatures of a changed log
	// are removed: Ren'Py refuses a save whose signatures do not match, and asks once
	// before loading one without signatures.
	Keys *KeyRing
}

// Encode returns the bytes of the save file with the changes. Every file of the zip that
// does not change is copied as it is, in its place.
func (s *Save) Encode(opts EncodeOpts) ([]byte, error) {
	logData, err := s.Log.Patch(opts.Edits)
	if err != nil {
		return nil, err
	}
	logChanged := !bytes.Equal(logData, s.Log.Data)
	// The old signatures stay only with the log they sign.
	signatures, keepSignatures := "", !logChanged
	if opts.Keys != nil && opts.Keys.CanSign() {
		if signatures, err = opts.Keys.Sign(logData); err != nil {
			return nil, err
		}
		keepSignatures = false
	}
	var metadata []byte
	if opts.SaveName != nil && s.Metadata != nil {
		m := make(map[string]any, len(s.Metadata)+1)
		for k, v := range s.Metadata {
			m[k] = v
		}
		m["_save_name"] = *opts.SaveName
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(m); err != nil {
			return nil, errors.NewErr(tagSaveEncodeFailed, err)
		}
		metadata = bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	}

	var out bytes.Buffer
	w := zip.NewWriter(&out)
	wroteSignatures := false
	// put writes a new body for the entry f, with its name, method and time.
	put := func(f *zip.File, body []byte) error {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: f.Name, Method: f.Method, Modified: f.Modified})
		if err != nil {
			return errors.NewErr(tagSaveEncodeFailed, err).SetMetadata("entry", f.Name)
		}
		if _, err := fw.Write(body); err != nil {
			return errors.NewErr(tagSaveEncodeFailed, err).SetMetadata("entry", f.Name)
		}
		return nil
	}
	for _, f := range s.zip.File {
		var err error
		switch {
		case f.Name == entryLog && logChanged:
			err = put(f, logData)
		case f.Name == entrySignatures && keepSignatures:
			err = copyEntry(w, f)
			wroteSignatures = true
		case f.Name == entrySignatures:
			if signatures != "" {
				err = put(f, []byte(signatures))
				wroteSignatures = true
			}
		case f.Name == entryExtraInfo && opts.SaveName != nil:
			err = put(f, []byte(*opts.SaveName))
		case f.Name == entryJSON && metadata != nil:
			err = put(f, metadata)
		default:
			err = copyEntry(w, f)
		}
		if err != nil {
			return nil, err
		}
	}
	if !wroteSignatures && signatures != "" && !keepSignatures {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: entrySignatures, Method: zip.Deflate})
		if err != nil {
			return nil, errors.NewErr(tagSaveEncodeFailed, err).SetMetadata("entry", entrySignatures)
		}
		if _, err := io.WriteString(fw, signatures); err != nil {
			return nil, errors.NewErr(tagSaveEncodeFailed, err).SetMetadata("entry", entrySignatures)
		}
	}
	if err := w.Close(); err != nil {
		return nil, errors.NewErr(tagSaveEncodeFailed, err)
	}
	return out.Bytes(), nil
}

func copyEntry(w *zip.Writer, f *zip.File) error {
	if err := w.Copy(f); err != nil {
		return errors.NewErr(tagSaveEncodeFailed, err).SetMetadata("entry", f.Name)
	}
	return nil
}

// WriteFile writes data to path through a temporary file in the same folder, so a failure
// leaves the old file as it was. With backup, the old file is first copied to path + ".bak",
// unless that file exists already (it keeps the save as it was before the first edit).
func WriteFile(ctx context.Context, path string, data []byte, backup bool) error {
	if err := ctx.Err(); err != nil {
		return errors.NewErr(tagSaveWriteFailed, err).SetMetadata("path", path)
	}
	if backup {
		old, err := os.ReadFile(path)
		switch {
		case err == nil:
			if _, statErr := os.Stat(path + ".bak"); os.IsNotExist(statErr) {
				if err := os.WriteFile(path+".bak", old, 0o644); err != nil {
					return errors.NewErr(tagSaveBackupFailed, err).SetMetadata("path", path+".bak")
				}
			}
		case !os.IsNotExist(err):
			return errors.NewErr(tagSaveBackupFailed, err).SetMetadata("path", path)
		}
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return errors.NewErr(tagSaveWriteFailed, err).SetMetadata("path", path)
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name()) // the write failed already; the temporary file is only litter
		return errors.NewErr(tagSaveWriteFailed, err).SetMetadata("path", path)
	}
	return nil
}

// The tags of the errors that this file makes.
const (
	tagSaveBackupFailed    = "SaveBackupFailed"
	tagSaveEncodeFailed    = "SaveEncodeFailed"
	tagSaveEntryUnreadable = "SaveEntryUnreadable"
	tagSaveLogMissing      = "SaveLogMissing"
	tagSaveNotZip          = "SaveNotZip"
	tagSaveRootsMissing    = "SaveRootsMissing"
	tagSaveUnreadable      = "SaveUnreadable"
	tagSaveWriteFailed     = "SaveWriteFailed"
)
