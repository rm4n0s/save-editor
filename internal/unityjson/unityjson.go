// Package unityjson reads and edits the JSON saves of Unity games, as two model formats.
//
// FormatJSON is a plain JSON text: an object or an array, as JsonUtility, Json.NET and most
// hand-made saves write them. FormatES3 is an Easy Save 3 file ("ES3", the *.es3 files of the
// Unity asset by Moodkie): JSON in ES3's dialect, in which every key holds {"__type": ...,
// "value": ...}, optionally compressed with gzip and optionally encrypted with AES. Both are
// JSON underneath and use internal/jsondoc, which replaces only the bytes of the edited values;
// the ES3 layers around the text are made again.
//
// The layout of an encrypted ES3 file is, as public descriptions agree (the ES3 guide at
// docs.moodkie.com for the 128-bit AES key and gzip; the README and the code of the tools
// github.com/Chaobs/unity-es3-cracker and github.com/fakeyw/EasySave3Tool for the rest):
// the first 16 bytes are the IV, the key is PBKDF2-HMAC-SHA1 of the password with the IV as
// salt, 100 iterations, 16 bytes, the cipher is AES-CBC with PKCS7 padding. The password that
// Easy Save's default settings carry, "password", is tried on every encrypted file; a file with
// another password is refused. None of these sources is Moodkie's code, which is closed: the
// layout is unverified against Easy Save itself.
package unityjson

import (
	"bytes"
	"math/big"
	"path"
	"strconv"
	"strings"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/jsondoc"
	"github.com/rm4n0s/save-editor/internal/model"
)

// FormatJSON is a plain JSON save: a JSON object or array that is not an Easy Save file.
var FormatJSON = model.Format{
	Name:     "Unity JSON",
	Patterns: []string{"*.json", "*.txt"},
	Detect:   detectJSON,
	Parse:    parseJSON,
}

// FormatES3 is an Easy Save 3 file: ES3's JSON, with or without gzip and AES.
var FormatES3 = model.Format{
	Name:     "Easy Save 3",
	Patterns: []string{"*.es3"},
	Detect:   detectES3,
	Parse:    parseES3,
}

// Document is a parsed JSON or Easy Save 3 save.
type Document struct {
	es3    bool
	raw    []byte
	json   *jsondoc.Doc
	layers []layer
	vars   []model.Variable
}

// Variables are the keys of the JSON object (or the items of the array), in the order of the
// file. In an Easy Save file each is the value of an entry.
func (d *Document) Variables() []model.Variable { return d.vars }

// Details are the facts that the file tells about itself.
func (d *Document) Details() []model.Detail {
	layout := "JSON text"
	if d.es3 {
		layout = "Easy Save 3 (JSON)"
	}
	out := []model.Detail{{Label: "Layout", Text: layout}}
	if d.es3 {
		gz, aes := "none", "none"
		for _, l := range d.layers {
			switch l.kind {
			case layerGzip:
				gz = "gzip"
			default:
				aes = "AES-128 (the default password)"
			}
		}
		out = append(out, model.Detail{Label: "Compression", Text: gz}, model.Detail{Label: "Encryption", Text: aes,
			Tooltip: "The key comes from the password \"password\" that Easy Save's settings carry by default"})
	}
	return append(out, model.Detail{Label: "Entries", Text: strconv.Itoa(len(d.vars))})
}

// Encode is the file with the edits made. With no edits it is the file as it was. Otherwise
// the JSON text is patched and the layers of the file are made again, with a new IV.
func (d *Document) Encode(edits []model.Edit) ([]byte, error) {
	if len(edits) == 0 {
		return bytes.Clone(d.raw), nil
	}
	text, err := d.json.Patch(edits)
	if err != nil {
		return nil, err
	}
	return wrap(text, d.layers)
}

// detectJSON reports whether the file is a JSON object or array that no layer wraps and that
// is not an Easy Save file. The contents decide: a .json name does not make a file JSON, and a
// JSON file may have any name. Only a file that begins as JSON but has a mistake in it, and has
// a .json or .txt name, is taken by its name, so that Parse can show the mistake.
func detectJSON(name string, data []byte) bool {
	if !looksLikeJSON(data) {
		return false
	}
	j, err := jsondoc.Parse(data)
	if err != nil {
		// The contents begin as JSON but are not: with a JSON name, say what is wrong with them.
		ext := strings.ToLower(path.Ext(name))
		return ext == ".json" || ext == ".txt"
	}
	return !isES3Shaped(j.Root.Value)
}

func parseJSON(name string, data []byte) (model.Document, error) {
	if !looksLikeJSON(data) {
		return nil, errors.New(tagNotSave, "This file is not a JSON object or array", "name", name)
	}
	j, err := jsondoc.Parse(data)
	if err != nil {
		return nil, err
	}
	d := &Document{raw: data, json: j}
	d.vars = variables(j.Root.Value, false)
	return d, nil
}

// detectES3 reports whether the file is Easy Save 3's: a JSON text of its shape, or any JSON
// under a gzip or AES layer, or, when only the name can tell, a *.es3 file that has the shape
// of an encrypted one (whole blocks of bytes that are not text) and that the default password
// does not open; Parse refuses that one with a message that says so. A file that does not
// have an .es3 name and whose password is not the default is not recognized at all, since
// random bytes cannot be told from a ciphertext.
func detectES3(name string, data []byte) bool {
	text, layers, res, err := unwrap(data)
	if err != nil {
		return false
	}
	switch res {
	case found:
		j, err := jsondoc.Parse(text)
		if err != nil {
			return false
		}
		return len(layers) > 0 || isES3Shaped(j.Root.Value)
	case encryptedOther:
		return strings.EqualFold(path.Ext(name), ".es3") && looksEncrypted(data)
	}
	return false
}

func parseES3(name string, data []byte) (model.Document, error) {
	text, layers, res, err := unwrap(data)
	if err != nil {
		return nil, err
	}
	switch res {
	case encryptedOther:
		return nil, errors.New(tagEncrypted, "This Easy Save 3 file is encrypted with a password other than the default one (\"password\"), so it cannot be opened",
			"name", name)
	case notSave:
		return nil, errors.New(tagNotSave, "This file is not an Easy Save 3 file", "name", name)
	}
	j, err := jsondoc.Parse(text)
	if err != nil {
		return nil, err
	}
	d := &Document{es3: true, raw: data, json: j, layers: layers}
	d.vars = variables(j.Root.Value, true)
	return d, nil
}

// isES3Shaped reports whether the root is an object whose every key holds an object with a
// "__type" text, which is how Easy Save writes an entry.
func isES3Shaped(root *model.Value) bool {
	if root.Kind != model.KindMap || len(root.Children) == 0 {
		return false
	}
	for _, c := range root.Children {
		if c.Slot.Value.Kind != model.KindMap || !hasType(c.Slot.Value) {
			return false
		}
	}
	return true
}

// hasType reports whether the object has a "__type" that is text.
func hasType(v *model.Value) bool {
	for _, c := range v.Children {
		if c.Label == typeKey && c.Slot.Value.Kind == model.KindString {
			return true
		}
	}
	return false
}

const (
	typeKey  = "__type"
	valueKey = "value"
)

// variables are the top-level rows of the document. In an Easy Save file an entry
// {"__type": T, "value": V} is shown as V of the type T, and the "__type" keys inside the
// values are shown as the types of their objects; they stay in the file and cannot be edited.
func variables(root *model.Value, es3 bool) []model.Variable {
	if es3 {
		presentES3(root)
	}
	seen := map[string]int{}
	out := make([]model.Variable, 0, len(root.Children))
	for _, c := range root.Children {
		name := c.Label
		if seen[name]++; seen[name] > 1 {
			name += " #" + strconv.Itoa(seen[name])
		}
		slot := c.Slot
		if es3 {
			slot = entryValue(c.Slot)
		}
		out = append(out, model.Variable{Name: name, Slot: slot})
	}
	return out
}

// entryValue is the slot of the value of an Easy Save entry, which takes the type of the
// entry; an entry of another shape is shown as it is.
func entryValue(entry model.Slot) model.Slot {
	obj := entry.Value
	if obj.Kind != model.KindMap && obj.Kind != model.KindObject {
		return entry
	}
	var val *model.Child
	for i := range obj.Children {
		if obj.Children[i].Label == valueKey {
			val = &obj.Children[i]
		}
	}
	if val == nil || len(obj.Children) != 1 {
		return entry
	}
	v := val.Slot.Value
	if v.Type == "" {
		v.Type = obj.Type
		if v.Kind == model.KindMap && v.Type != "" {
			v.Kind = model.KindObject
		}
	}
	applyType(val.Slot, obj.Type)
	return val.Slot
}

// presentES3 turns each object that has a "__type" into an object of that type, hiding the
// key, everywhere under the root.
func presentES3(v *model.Value) {
	if v.Kind != model.KindMap && v.Kind != model.KindList {
		return
	}
	kept := v.Children[:0:0]
	for _, c := range v.Children {
		if v.Kind == model.KindMap && c.Label == typeKey && c.Slot.Value.Kind == model.KindString {
			v.Type = shortType(c.Slot.Value.Str)
			continue
		}
		kept = append(kept, c)
	}
	v.Children = kept
	if v.Type != "" && v.Kind == model.KindMap {
		v.Kind = model.KindObject
	}
	for _, c := range v.Children {
		presentES3(c.Slot.Value)
	}
}

// shortType is the name of a type of an Easy Save "__type", which is a type name, possibly
// followed by the assembly: "int,System.Int32" is "int", "UnityEngine.Vector3,UnityEngine"
// is "Vector3", and a generic type is its name without the arguments.
func shortType(s string) string {
	depth, end := 0, len(s)
scan:
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				end = i
				break scan
			}
		}
	}
	name := strings.TrimSpace(s[:end])
	if i := strings.IndexAny(name, "`["); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndexAny(name, ".+"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// intRanges are the bounds of the whole-number types of C#, by the lower case names that
// Easy Save and .NET use.
var intRanges = map[string][2]string{
	"int": {"-2147483648", "2147483647"}, "int32": {"-2147483648", "2147483647"},
	"uint": {"0", "4294967295"}, "uint32": {"0", "4294967295"},
	"long": {"-9223372036854775808", "9223372036854775807"}, "int64": {"-9223372036854775808", "9223372036854775807"},
	"ulong": {"0", "18446744073709551615"}, "uint64": {"0", "18446744073709551615"},
	"short": {"-32768", "32767"}, "int16": {"-32768", "32767"},
	"ushort": {"0", "65535"}, "uint16": {"0", "65535"},
	"byte": {"0", "255"}, "sbyte": {"-128", "127"},
}

// applyType holds a value of a known C# type to what the type can hold: the whole numbers to
// the range of the type, and a float to single precision.
func applyType(s model.Slot, typ string) {
	v := s.Value
	switch v.Kind {
	case model.KindInt:
		if r, ok := intRanges[strings.ToLower(typ)]; ok && s.Site != nil {
			lo, _ := new(big.Int).SetString(r[0], 10)
			hi, _ := new(big.Int).SetString(r[1], 10)
			s.Site.IntMin, s.Site.IntMax = lo, hi
		}
	case model.KindFloat:
		if t := strings.ToLower(typ); t == "float" || t == "single" {
			v.Float32 = true
		}
	}
}

const (
	tagNotSave   = "UnityJSONNotSave"
	tagEncrypted = "UnityJSONEncrypted"
)
