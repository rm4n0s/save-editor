// Package gvas reads and edits Unreal Engine SaveGame files (*.sav, magic "GVAS"), the files that
// UGameplayStatics::SaveGameToSlot writes. It supports Unreal Engine 4 and 5, including the
// property tags of 5.4 and later.
//
// Like the other formats of the editor, a save is edited in place: Encode copies the original
// bytes, replaces the bytes of each edited value, and fixes the size fields of the properties
// that contain it. A value the package cannot decode is kept as bytes.
//
// Games that wrap the GVAS data in their own compression or encryption are not supported: the
// file has to start with "GVAS".
package gvas

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// Format is the Unreal Engine SaveGame format.
var Format = model.Format{
	Name:     "Unreal Engine",
	Patterns: []string{"*.sav"},
	Detect: func(_ string, data []byte) bool {
		return len(data) >= 4 && string(data[:4]) == magic
	},
	Parse: func(name string, data []byte) (model.Document, error) {
		d, err := Parse(name, data)
		if err != nil {
			return nil, err
		}
		return d, nil
	},
}

// Document is a parsed SaveGame file.
type Document struct {
	data     []byte
	hdr      header
	root     *span
	vars     []model.Variable
	trailing []byte
}

// Parse reads a SaveGame file. The name is not used.
func Parse(_ string, data []byte) (*Document, error) {
	d := &Document{data: bytes.Clone(data)}
	r := newReader(d.data, d)
	if err := r.readHeader(); err != nil {
		return nil, err
	}
	d.hdr = r.v
	d.root = &span{start: r.pos, sizeAt: -1}
	kids, err := r.readPropList(d.root)
	if err != nil {
		return nil, err
	}
	d.root.end = r.pos
	d.trailing = d.data[r.pos:]
	for _, k := range kids {
		d.vars = append(d.vars, model.Variable{Name: k.Label, Slot: k.Slot})
	}
	return d, nil
}

// Variables are the top-level properties of the save, in file order.
func (d *Document) Variables() []model.Variable { return d.vars }

// Details are the facts of the header of the save.
func (d *Document) Details() []model.Detail {
	h := &d.hdr
	pkg := "UE4 " + strconv.Itoa(int(h.ue4Version))
	if h.ue5Version >= 0 {
		pkg += ", UE5 " + strconv.Itoa(int(h.ue5Version))
	}
	layout := "classic property tags"
	if h.propertyTag() {
		layout = "property tags with type names (Unreal Engine 5.4 and later)"
	}
	var customs []string
	for _, c := range h.customs {
		customs = append(customs, guidText(c.guid[:])+" = "+strconv.Itoa(int(c.version)))
	}
	out := []model.Detail{
		{Label: "Engine", Text: strconv.Itoa(int(h.major)) + "." + strconv.Itoa(int(h.minor)) + "." + strconv.Itoa(int(h.patch)),
			Tooltip: h.engineText()},
		{Label: "Save game version", Text: strconv.Itoa(int(h.saveGameVersion))},
		{Label: "Package version", Text: pkg},
		{Label: "Custom versions", Text: strconv.Itoa(len(h.customs)), Tooltip: strings.Join(customs, "\n")},
		{Label: "Save game class", Text: h.className},
		{Label: "Layout", Text: layout},
	}
	if len(d.trailing) != 4 || !bytes.Equal(d.trailing, make([]byte, 4)) {
		out = append(out, model.Detail{Label: "Trailing bytes", Text: strconv.Itoa(len(d.trailing)),
			Tooltip: "Bytes after the last property; kept as they are."})
	}
	return out
}

// Encode is the file with the edits made: the original bytes with the edited values replaced,
// and the sizes of the properties that contain them fixed.
func (d *Document) Encode(edits []model.Edit) ([]byte, error) {
	repl := make(map[*span][]byte, len(edits))
	for _, e := range edits {
		sp, err := d.leafOf(e)
		if err != nil {
			return nil, err
		}
		b, err := sp.leaf.encode(d.data[sp.start:sp.end], e.Value)
		if err != nil {
			return nil, err
		}
		repl[sp] = b
	}
	out := make([]byte, 0, len(d.data))
	out = append(out, d.data[:d.root.start]...)
	out = append(out, d.emit(d.root, repl)...)
	out = append(out, d.data[d.root.end:]...)
	return out, nil
}

// leafOf finds the span of the edit's site and checks the edit against it.
func (d *Document) leafOf(e model.Edit) (*span, error) {
	if e.Site == nil || e.Value == nil {
		return nil, errors.New(tagUnknownSite, "an edit has no site or no value")
	}
	sp, ok := e.Site.Ref.(*span)
	if !ok || sp == nil || sp.leaf == nil || sp.leaf.doc != d || sp.leaf.site != e.Site {
		return nil, errors.New(tagUnknownSite, "an edit is for a site that is not of this save", "site", e.Site.ID)
	}
	if e.Value.Kind != sp.leaf.kind {
		return nil, errors.New(tagWrongKind, "an edit changes the kind of a value",
			"site", e.Site.ID, "want", sp.leaf.kind.String(), "got", e.Value.Kind.String())
	}
	switch e.Value.Kind {
	case model.KindInt:
		n := e.Value.Int
		if n == nil {
			return nil, errors.New(tagBadValue, "a whole number has no value", "site", e.Site.ID)
		}
		if (e.Site.IntMin != nil && n.Cmp(e.Site.IntMin) < 0) || (e.Site.IntMax != nil && n.Cmp(e.Site.IntMax) > 0) {
			return nil, errors.New(tagOutOfRange, "the number is outside what the field can hold",
				"site", e.Site.ID, "value", n.String(), "min", e.Site.IntMin.String(), "max", e.Site.IntMax.String())
		}
	case model.KindString:
		if e.Site.CheckText != nil {
			if err := e.Site.CheckText(e.Value.Str); err != nil {
				return nil, err
			}
		}
	}
	return sp, nil
}

// emit writes the bytes of sp: the original bytes, with the edited leaves below it replaced,
// and, when the length changed, the size field of sp moved by the same amount.
func (d *Document) emit(sp *span, repl map[*span][]byte) []byte {
	if b, ok := repl[sp]; ok {
		return b
	}
	if len(sp.kids) == 0 {
		return d.data[sp.start:sp.end]
	}
	out := make([]byte, 0, sp.end-sp.start)
	cur := sp.start
	for _, k := range sp.kids {
		out = append(out, d.data[cur:k.start]...)
		out = append(out, d.emit(k, repl)...)
		cur = k.end
	}
	out = append(out, d.data[cur:sp.end]...)
	if delta := len(out) - (sp.end - sp.start); sp.sizeAt >= 0 && delta != 0 {
		at := sp.sizeAt - sp.start
		old := int64(binary.LittleEndian.Uint32(out[at:]))
		binary.LittleEndian.PutUint32(out[at:], uint32(old+int64(delta)))
	}
	return out
}

// codecOp says how a leaf's value is stored.
type codecOp uint8

const (
	opInt      codecOp = iota // little-endian integer of width bytes
	opFloat                   // float32 or float64 of width bytes
	opBool                    // a byte that is 0 or 1
	opBoolFlag                // the bool bit of the flags byte of a 5.4 tag
	opString                  // an FString
	opGuid                    // 16 bytes, shown as 32 hexadecimal digits
)

// leaf is a value that can be replaced: where it is stored and how.
type leaf struct {
	doc    *Document
	site   *model.Site
	kind   model.Kind
	op     codecOp
	width  int
	signed bool
}

// addLeaf makes sp (a range of bytes) the span of a value that can be replaced, adds it to
// parent and returns its site.
func (r *reader) addLeaf(parent, sp *span, kind model.Kind, op codecOp, width int, signed bool) *model.Site {
	r.nextID++
	site := &model.Site{ID: r.nextID, Ref: sp}
	switch op {
	case opInt:
		bits := uint(8 * width)
		one := big.NewInt(1)
		if signed {
			site.IntMin = new(big.Int).Neg(new(big.Int).Lsh(one, bits-1))
			site.IntMax = new(big.Int).Sub(new(big.Int).Lsh(one, bits-1), one)
		} else {
			site.IntMin = new(big.Int)
			site.IntMax = new(big.Int).Sub(new(big.Int).Lsh(one, bits), one)
		}
	case opString:
		site.CheckText = checkString
	case opGuid:
		site.CheckText = checkGuid
	}
	sp.leaf = &leaf{doc: r.doc, site: site, kind: kind, op: op, width: width, signed: signed}
	parent.kids = append(parent.kids, sp)
	return site
}

// encode is the bytes of the new value v, which replace orig.
func (l *leaf) encode(orig []byte, v *model.Value) ([]byte, error) {
	switch l.op {
	case opInt:
		n := new(big.Int).Set(v.Int)
		if n.Sign() < 0 {
			n.Add(n, new(big.Int).Lsh(big.NewInt(1), uint(8*l.width)))
		}
		out := make([]byte, l.width)
		n.FillBytes(out)
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		return out, nil
	case opFloat:
		if l.width == 4 {
			f := float32(v.Float)
			if math.IsInf(float64(f), 0) && !math.IsInf(v.Float, 0) {
				return nil, errors.New(tagOutOfRange, "the number is too large for a float32", "site", l.site.ID, "value", v.Float)
			}
			return binary.LittleEndian.AppendUint32(nil, math.Float32bits(f)), nil
		}
		return binary.LittleEndian.AppendUint64(nil, math.Float64bits(v.Float)), nil
	case opBool:
		if v.Bool {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	case opBoolFlag:
		if v.Bool {
			return []byte{orig[0] | flagBoolTrue}, nil
		}
		return []byte{orig[0] &^ flagBoolTrue}, nil
	case opString:
		return encodeFString(v.Str), nil
	default: // opGuid
		b, ok := parseGuid(v.Str)
		if !ok {
			return nil, errors.New(tagBadText, "a GUID is 32 hexadecimal digits", "site", l.site.ID)
		}
		return b, nil
	}
}

// checkString is the CheckText of a string: UE strings end at a null character, and text must
// be valid Unicode.
func checkString(s string) error {
	if strings.ContainsRune(s, 0) || !utf8.ValidString(s) {
		return errors.New(tagBadText, "Use text without null characters")
	}
	return nil
}

// checkGuid is the CheckText of a GUID.
func checkGuid(s string) error {
	if _, ok := parseGuid(s); !ok {
		return errors.New(tagBadText, "Enter 32 hexadecimal digits, such as 00112233445566778899AABBCCDDEEFF")
	}
	return nil
}

// parseGuid reads the text guidText writes; dashes and braces are ignored.
func parseGuid(s string) ([]byte, bool) {
	s = strings.NewReplacer("-", "", "{", "", "}", "").Replace(strings.TrimSpace(s))
	if len(s) != 32 {
		return nil, false
	}
	out := make([]byte, 16)
	for w := 0; w < 4; w++ {
		v, err := strconv.ParseUint(s[8*w:8*w+8], 16, 32)
		if err != nil {
			return nil, false
		}
		binary.LittleEndian.PutUint32(out[4*w:], uint32(v))
	}
	return out, true
}

func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
func le64(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }
