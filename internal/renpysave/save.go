// Package renpysave reads and writes Ren'Py save files, and is the Ren'Py format of the
// editor. A save is a zip file with the pickled game state ("log"), a screenshot, the save's
// name and metadata, and from Ren'Py 7.6 and 8.1 on, signatures of the log made with the
// player's key.
package renpysave

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
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

// Document is a parsed Ren'Py save file. It is a model.Document and a model.Screenshotter;
// the save's name, the signatures and the key ring are Ren'Py's own and are its other
// methods.
type Document struct {
	zip *zip.Reader

	// Log is the pickle of the (roots, rollback log) tuple.
	Log *pickle.Document
	// Roots is the dict of the variables of the stores, by "store.name".
	Roots      *pickle.Value
	screenshot []byte
	// ExtraInfo is the save's name, the extra_info of renpy.save.
	ExtraInfo string
	// Metadata is the save's JSON: _save_name, _renpy_version, _version, _ctime,
	// _game_runtime and what the game added. Numbers are json.Number.
	Metadata map[string]any
	// RenpyVersion is the version of Ren'Py that wrote the save.
	RenpyVersion string
	// Signatures is the text of the signatures file, empty when there is none.
	Signatures string

	vars  []model.Variable
	sites map[*model.Site]siteInfo
}

// Open reads the save file at path.
func Open(ctx context.Context, path string) (*Document, error) {
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
func Parse(data []byte) (*Document, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.NewErr(tagSaveNotZip, err)
	}
	s := &Document{zip: zr}
	var logData, metadata []byte
	for _, f := range zr.File {
		var target *[]byte
		var text *string
		switch f.Name {
		case entryLog:
			target = &logData
		case entryScreenshot:
			target = &s.screenshot
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
	s.convert()
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

// SaveName is the name the save and load screens show.
func (s *Document) SaveName() string { return s.ExtraInfo }

// SignatureStatus checks the save's signatures against its log; ring may be nil.
func (s *Document) SignatureStatus(ring *KeyRing) SignatureStatus {
	return VerifySignatures(s.Log.Data, s.Signatures, ring)
}

// EncodeOpts are the changes Encode makes.
type EncodeOpts struct {
	// Edits are the new values of variables.
	Edits []model.Edit
	// SaveName, when set, is the new name of the save.
	SaveName *string
	// Keys, when it can sign, signs the new log. Without it, the signatures of a changed log
	// are removed: Ren'Py refuses a save whose signatures do not match, and asks once
	// before loading one without signatures.
	Keys *KeyRing
}

// Encode returns the bytes of the save file with the edits made and without signatures if
// the log changed; see EncodeWith to sign it.
func (s *Document) Encode(edits []model.Edit) ([]byte, error) {
	return s.EncodeWith(EncodeOpts{Edits: edits})
}

// EncodeWith returns the bytes of the save file with the changes. Every file of the zip that
// does not change is copied as it is, in its place.
func (s *Document) EncodeWith(opts EncodeOpts) ([]byte, error) {
	patches, err := s.patches(opts.Edits)
	if err != nil {
		return nil, err
	}
	logData, err := s.Log.Patch(patches)
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

// The tags of the errors that this file makes.
const (
	tagSaveEncodeFailed    = "SaveEncodeFailed"
	tagSaveEntryUnreadable = "SaveEntryUnreadable"
	tagSaveLogMissing      = "SaveLogMissing"
	tagSaveNotZip          = "SaveNotZip"
	tagSaveRootsMissing    = "SaveRootsMissing"
	tagSaveUnreadable      = "SaveUnreadable"
)
