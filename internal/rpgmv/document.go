// Package rpgmv reads and writes the save files of RPG Maker MV (*.rpgsave) and RPG Maker MZ
// (*.rmmzsave).
//
// A save is the JSON of the game's objects (JsonEx.stringify of the contents: system, screen,
// timer, switches, variables, selfSwitches, actors, party, map, player, and what plugins
// add), packed by the engine: in MV with LZString.compressToBase64, in MZ with zlib at level
// 1 whose bytes are written as UTF-8 text. An edit replaces only the bytes of the edited
// values in the JSON text (internal/jsondoc) and packs the text again; the rest of the
// JSON, including the ids and class names that JsonEx writes, is as it was.
package rpgmv

import (
	"compress/zlib"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/rm4n0s/save-editor/internal/jsondoc"
	"github.com/rm4n0s/save-editor/internal/model"
)

// FormatMV is RPG Maker MV's save files.
var FormatMV = model.Format{
	Name:     "RPG Maker MV",
	Patterns: []string{"*.rpgsave"},
	Detect: func(_ string, data []byte) bool {
		_, _, err := decodeMV(data)
		return err == nil
	},
	Parse: func(name string, data []byte) (model.Document, error) { return ParseMV(name, data) },
}

// FormatMZ is RPG Maker MZ's save files.
var FormatMZ = model.Format{
	Name:     "RPG Maker MZ",
	Patterns: []string{"*.rmmzsave"},
	Detect: func(_ string, data []byte) bool {
		_, err := decodeMZ(data, maxInflated)
		return err == nil
	},
	Parse: func(name string, data []byte) (model.Document, error) { return ParseMZ(name, data) },
}

// Document is a parsed MV or MZ save file.
type Document struct {
	mv     bool
	layout lzLayout
	name   string
	raw    []byte
	json   *jsondoc.Doc
	vars   []model.Variable
}

// ParseMV reads an MV save file; name is the base name of the file.
func ParseMV(name string, data []byte) (*Document, error) {
	text, layout, err := decodeMV(data)
	if err != nil {
		return nil, err
	}
	d, err := newDocument(name, data, text, true)
	if err != nil {
		return nil, err
	}
	d.layout = layout
	return d, nil
}

// ParseMZ reads an MZ save file; name is the base name of the file.
func ParseMZ(name string, data []byte) (*Document, error) {
	text, err := decodeMZ(data, maxInflated)
	if err != nil {
		return nil, err
	}
	return newDocument(name, data, text, false)
}

func newDocument(name string, raw, text []byte, mv bool) (*Document, error) {
	j, err := jsondoc.Parse(text)
	if err != nil {
		return nil, err
	}
	present(j.Root, mv)
	d := &Document{mv: mv, name: name, raw: raw, json: j}
	d.vars = variables(j.Root)
	return d, nil
}

// variables are the top-level keys of the contents, or the elements of the list that
// global.rpgsave is.
func variables(root model.Slot) []model.Variable {
	switch root.Value.Kind {
	case model.KindMap, model.KindObject, model.KindList:
	default:
		return []model.Variable{{Name: "value", Slot: root}}
	}
	seen := map[string]int{}
	out := make([]model.Variable, 0, len(root.Value.Children))
	for _, c := range root.Value.Children {
		name := c.Label
		if seen[name]++; seen[name] > 1 {
			name += " #" + strconv.Itoa(seen[name])
		}
		out = append(out, model.Variable{Name: name, Slot: c.Slot})
	}
	return out
}

// Variables are the top-level keys of the contents of the save, in the order of the file.
func (d *Document) Variables() []model.Variable { return d.vars }

var slotName = regexp.MustCompile(`(?i)^file(\d+)\.(rpgsave|rmmzsave)$`)

// Details are the facts that the save tells about itself.
func (d *Document) Details() []model.Detail {
	var out []model.Detail
	packing := "zlib, written as UTF-8 text"
	if d.mv {
		packing = "LZString, Base64"
	}
	out = append(out, model.Detail{Label: "Packing", Text: packing,
		Tooltip: "How the game packs the JSON of the save in the file"})
	base := strings.ToLower(path.Base(d.name))
	switch m := slotName.FindStringSubmatch(base); {
	case m != nil:
		out = append(out, model.Detail{Label: "Save slot", Text: m[1]})
	case strings.HasPrefix(base, "global."):
		out = append(out, model.Detail{Label: "File", Text: "Global information about the save slots"})
	case strings.HasPrefix(base, "config."):
		out = append(out, model.Detail{Label: "File", Text: "Game options"})
	}
	root := d.json.Root.Value
	if root.Kind == model.KindList {
		n := 0
		for _, c := range root.Children {
			if c.Slot.Value.Kind != model.KindNull {
				n++
			}
		}
		out = append(out, model.Detail{Label: "Save slots", Text: strconv.Itoa(n)})
		return out
	}
	system, ok := find(root, "system")
	if !ok {
		return out
	}
	if s, ok := find(system.Value, "_saveCount"); ok {
		if n, ok := intOf(s.Value); ok {
			out = append(out, model.Detail{Label: "Save count", Text: strconv.FormatInt(n, 10),
				Tooltip: "How many times the game saved, system._saveCount"})
		}
	}
	if s, ok := find(system.Value, "_framesOnSave"); ok {
		if n, ok := intOf(s.Value); ok && n >= 0 {
			secs := n / 60
			out = append(out, model.Detail{Label: "Play time", Text: fmt.Sprintf("%02d:%02d:%02d", secs/3600, secs/60%60, secs%60),
				Tooltip: "system._framesOnSave divided by 60 frames a second"})
		}
	}
	if s, ok := find(system.Value, "_versionId"); ok {
		if n, ok := intOf(s.Value); ok {
			out = append(out, model.Detail{Label: "Game version ID", Text: strconv.FormatInt(n, 10),
				Tooltip: "The version ID of the game's data when it saved, system._versionId"})
		}
	}
	return out
}

// Encode is the file with the edits made. With no edits it is the file as it was. Otherwise
// the JSON text is patched and packed again.
func (d *Document) Encode(edits []model.Edit) ([]byte, error) {
	if len(edits) == 0 {
		return append([]byte(nil), d.raw...), nil
	}
	text, err := d.json.Patch(edits)
	if err != nil {
		return nil, err
	}
	if d.mv {
		return encodeMV(text, d.layout), nil
	}
	out, err := encodeMZ(text, zlib.BestSpeed)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// compile-time check
var _ model.Document = (*Document)(nil)
