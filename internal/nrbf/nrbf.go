// Package nrbf reads and edits .NET BinaryFormatter files, as a model.Format.
//
// Unity games that save with System.Runtime.Serialization.Formatters.Binary.BinaryFormatter
// write the .NET Remoting Binary Format ([MS-NRBF], open specification of Microsoft:
// https://learn.microsoft.com/openspecs/windows_protocols/ms-nrbf), under any file name
// (.dat, .sav, .save, .bin, ...). A file begins with a SerializedStreamHeader record, which
// is how this package recognizes it.
//
// The classes of the stream are shown by name with their members; List, Dictionary,
// Hashtable and HashSet are shown as their items. A file is edited in place: a primitive
// value or a string is replaced where it is. Nothing else in the format stores offsets or
// sizes (a string has a length of its own, which is written again), so the rest of the file
// is kept byte for byte.
//
// Not supported: method call and return records (they are messages, not saves), and the
// ClassWithMembers and SystemClassWithMembers records, which leave out the types of the
// members so that the values cannot be read without the program that wrote them
// (BinaryFormatter itself never writes them).
package nrbf

import (
	"bytes"
	"encoding/binary"
	"slices"
	"strconv"
	"strings"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// Format is the .NET BinaryFormatter stream.
var Format = model.Format{
	Name:     "Unity / .NET BinaryFormatter",
	Patterns: []string{"*.dat", "*.sav", "*.save", "*.bin", "*.bytes"},
	Detect:   detect,
	Parse:    parse,
}

// Document is a parsed BinaryFormatter stream.
type Document struct {
	data    []byte
	vars    []model.Variable
	details []model.Detail
	// sites are the sites of the document by ID.
	sites []*model.Site
}

// Variables are the members of the root object; when the root is not a class, the one root
// value.
func (d *Document) Variables() []model.Variable { return d.vars }

// Details are the root class, the library it comes from, and the libraries of the stream.
func (d *Document) Details() []model.Detail { return d.details }

// detect looks for the SerializedStreamHeader: record type 0, the ids of the root and of the
// headers, and the version 1.0, then the type of a record that can come first.
func detect(_ string, data []byte) bool {
	if len(data) < 18 || data[0] != byte(recHeader) {
		return false
	}
	if binary.LittleEndian.Uint32(data[9:]) != 1 || binary.LittleEndian.Uint32(data[13:]) != 0 {
		return false
	}
	switch recordType(data[17]) {
	case recLibrary, recClassWithID, recSystemClassTypes, recClassTypes, recObjectString, recBinaryArray,
		recArraySinglePrimitive, recArraySingleObject, recArraySingleString, recSystemClassMembers, recClassMembers:
		return true
	}
	return false
}

func parse(_ string, data []byte) (model.Document, error) {
	r, end, err := parseStream(data)
	if err != nil {
		return nil, err
	}
	d := &Document{data: data}
	b := &builder{d: d, r: r, slots: map[*object]*model.Slot{}}
	root, ok := r.objects[r.rootID]
	if !ok {
		return nil, errors.New(tagReference, "the root object is not in the stream", "id", r.rootID)
	}
	slot, err := b.object(root)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	if root.kind == objClass && slot.Value.Kind == model.KindObject {
		for _, c := range slot.Value.Children {
			d.vars = append(d.vars, model.Variable{Name: uniqueName(c.Label, used), Slot: c.Slot})
		}
	} else {
		d.vars = []model.Variable{{Name: "root", Slot: *slot}}
	}
	d.details = b.details(root, len(r.objects), len(data)-end)
	return d, nil
}

// details are the facts about the stream.
func (b *builder) details(root *object, objects, trailing int) []model.Detail {
	r := b.r
	out := []model.Detail{{Label: "Layout", Text: "BinaryFormatter stream (MS-NRBF 1.0)"}}
	if root.kind == objClass {
		out = append(out, model.Detail{Label: "Top-level class", Text: shortTypeName(root.meta.name), Tooltip: root.meta.name})
		lib := "system library"
		tip := ""
		if name, ok := r.libs[root.meta.libraryID]; ok {
			lib, tip = simpleAssembly(name), name
		}
		out = append(out, model.Detail{Label: "Library of the class", Text: lib, Tooltip: tip})
	} else {
		out = append(out, model.Detail{Label: "Top-level object", Text: b.slotType(root)})
	}
	if len(r.libOrder) > 0 {
		var short, full []string
		for _, id := range r.libOrder {
			short = append(short, simpleAssembly(r.libs[id]))
			full = append(full, r.libs[id])
		}
		out = append(out, model.Detail{Label: "Libraries", Text: strings.Join(short, ", "), Tooltip: strings.Join(full, "\n")})
	}
	out = append(out, model.Detail{Label: "Objects", Text: strconv.Itoa(objects)})
	if trailing > 0 {
		out = append(out, model.Detail{Label: "Bytes after the end", Text: strconv.Itoa(trailing)})
	}
	return out
}

// simpleAssembly is the name of an assembly without its version and key.
func simpleAssembly(full string) string {
	name, _, _ := strings.Cut(full, ",")
	return strings.TrimSpace(name)
}

// uniqueName makes name different from the names in used.
func uniqueName(name string, used map[string]bool) string {
	if !used[name] {
		used[name] = true
		return name
	}
	for n := 2; ; n++ {
		candidate := name + " (" + strconv.Itoa(n) + ")"
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

// Encode is the stream with the edits made: each edited value is written in place of the
// old one, and every other byte is kept.
func (d *Document) Encode(edits []model.Edit) ([]byte, error) {
	if len(edits) == 0 {
		return bytes.Clone(d.data), nil
	}
	type change struct {
		span  span
		bytes []byte
	}
	changes := make([]change, 0, len(edits))
	seen := map[int]bool{}
	for _, e := range edits {
		s := e.Site
		if s == nil || s.ID < 0 || s.ID >= len(d.sites) || d.sites[s.ID] != s {
			return nil, errors.New(tagEditSite, "the site is not from this document")
		}
		if seen[s.ID] {
			return nil, errors.New(tagEditSite, "two edits of the same site", "site", s.ID)
		}
		seen[s.ID] = true
		ref := s.Ref.(*siteRef)
		if e.Value == nil || e.Value.Kind != ref.kind {
			return nil, errors.New(tagEditKind, "the new value has another kind than the old one", "site", s.ID, "want", ref.kind.String())
		}
		nb, err := ref.encode(e)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change{ref.span, nb})
	}
	slices.SortFunc(changes, func(a, b change) int { return a.span.start - b.span.start })
	out := make([]byte, 0, len(d.data)+64)
	pos := 0
	for _, c := range changes {
		out = append(out, d.data[pos:c.span.start]...)
		out = append(out, c.bytes...)
		pos = c.span.end
	}
	return append(out, d.data[pos:]...), nil
}

const (
	tagEditSite  = "NRBFEditSite"
	tagEditKind  = "NRBFEditKind"
	tagEditRange = "NRBFEditRange"
	tagEditText  = "NRBFEditText"
)
