// Package lcf reads and edits the save files of RPG Maker 2000 and 2003 (SaveNN.lsd), and of
// EasyRPG Player, which writes the same format.
//
// The file is EasyRPG's "LCF" format: a header, then chunks that hold structs, arrays of structs
// and plain values. The ids, names and types of the chunks of the save structures come from
// EasyRPG's liblcf (https://github.com/EasyRPG/liblcf, MIT license), which the schema table in
// schema.go credits. A chunk that the table does not know (a patched or custom game, Maniac
// Patch, an EasyRPG extension) is kept byte for byte and shown as bytes with its id.
//
// An edit replaces the data of one chunk (or one element of a vector, which has a fixed size) and
// the sizes of the chunks around it; every other byte of the file is kept.
package lcf

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"slices"
	"time"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

const (
	tagNotLCF       = "NotLcfSave"
	tagCorrupt      = "CorruptSave"
	tagForeignSite  = "ForeignSite"
	tagKindMismatch = "KindMismatch"
	tagOutOfRange   = "OutOfRange"
	tagInvalidEdit  = "InvalidEdit"
	headerText      = "LcfSaveData"
	headerLen       = 1 + len(headerText)
)

// Format is the format of the save files of RPG Maker 2000 and 2003.
var Format = model.Format{
	Name:     "RPG Maker 2000/2003",
	Patterns: []string{"*.lsd"},
	Detect:   Detect,
	Parse:    Parse,
}

// Detect reports whether data starts with the header of an LCF save: the number 11 and
// "LcfSaveData". The name is not looked at.
func Detect(_ string, data []byte) bool {
	return len(data) >= headerLen && data[0] == byte(len(headerText)) && string(data[1:headerLen]) == headerText
}

// Document is a parsed save file.
type Document struct {
	data    []byte
	root    *chunk
	codec   *codec
	is2k3   bool
	unknown int
	vars    []model.Variable
	details []model.Detail
	sites   int // the number of sites made, which is also the last ID
}

// leaf is the Ref of a Site: the place of a value in the file.
type leaf struct {
	doc  *Document
	site *model.Site
	c    *chunk
	elem int // the index in a vector, or -1 for the whole data of the chunk
	kind model.Kind
}

// Parse reads a save file. The bytes are copied.
func Parse(name string, data []byte) (model.Document, error) {
	if !Detect(name, data) {
		return nil, errors.New(tagNotLCF,
			"This is not an RPG Maker 2000/2003 save: it does not start with \""+headerText+"\"",
			"name", name, "size", len(data))
	}
	data = slices.Clone(data)
	d := &Document{data: data}
	root := &chunk{typ: ftStruct, def: schema["Save"], start: headerLen, sizeStart: headerLen, dataStart: headerLen, end: len(data)}
	p := &parser{data: data}
	kids, _, st := p.chunks(root.def, headerLen, len(data), root)
	if st == stBad && len(kids) == 0 {
		return nil, errors.New(tagCorrupt, "The save is damaged: the first chunk after the header does not fit in the file",
			"name", name, "size", len(data))
	}
	root.kids = kids
	d.root = root
	d.analyze()
	d.build()
	return d, nil
}

// analyze finds what the file says about itself: the edition of the game, the unknown chunks and
// the code page of the strings.
func (d *Document) analyze() {
	var strs [][]byte
	cp := 0
	d.root.walk(func(c *chunk) {
		if c == d.root {
			return
		}
		if c.f == nil {
			d.unknown++
			return
		}
		if c.f.is2k3 {
			d.is2k3 = true
		}
		if c.typ == ftString {
			strs = append(strs, d.data[c.dataStart:c.end])
		}
		if c.f.name == "codepage" && c.typ == ftInt32 && c.parent != nil && c.parent.f != nil && c.parent.f.name == "easyrpg_data" {
			cp = int(int32(berAt(d.data, c)))
		}
	})
	if c, ok := newCodec(cp, "the save says so: EasyRPG Player wrote its code page in easyrpg_data"); ok && cp > 0 {
		d.codec = c
		return
	}
	guess, why := guessCodepage(strs)
	if cp > 0 {
		why = fmt.Sprintf("the save names code page %d, which is not supported; %s", cp, why)
	}
	d.codec, _ = newCodec(guess, why)
}

// berAt is the number in the data of a chunk of type ftInt32.
func berAt(data []byte, c *chunk) uint32 {
	if c.end == c.dataStart {
		return 0
	}
	v, _, _ := readBER(data, c.dataStart, c.end)
	return v
}

var (
	minInt32  = big.NewInt(math.MinInt32)
	maxInt32  = big.NewInt(math.MaxInt32)
	minInt16  = big.NewInt(math.MinInt16)
	maxInt16  = big.NewInt(math.MaxInt16)
	minInt8   = big.NewInt(math.MinInt8)
	maxInt8   = big.NewInt(math.MaxInt8)
	zero      = big.NewInt(0)
	maxUInt8  = big.NewInt(math.MaxUint8)
	maxUInt32 = big.NewInt(math.MaxUint32)
)

var typeNames = map[fieldType]string{
	ftInt32: "Int32", ftBool: "Boolean", ftString: "String", ftDouble: "Double", ftInt8: "Int8",
	ftUInt8: "UInt8", ftUInt32: "UInt32", ftFlags: "Flags", ftVecBool: "Vector<Boolean>",
	ftVecInt16: "Vector<Int16>", ftVecInt32: "Vector<Int32>", ftVecUInt8: "Vector<UInt8>",
	ftBytes: "Bytes",
}

// internalVariables are the variables of the top level that hold the state of the engine rather
// than of the game.
var internalVariables = map[string]bool{
	"foreground_event_execstate": true,
	"common_events":              true,
}

// build makes the tree of values, the variables and the details.
func (d *Document) build() {
	seen := map[string]int{}
	for _, c := range d.root.kids {
		name := d.label(c)
		seen[name]++
		if seen[name] > 1 {
			name = fmt.Sprintf("%s #%d", name, seen[name])
		}
		d.vars = append(d.vars, model.Variable{Name: name, Internal: internalVariables[name], Slot: d.slot(c)})
	}
	d.makeDetails()
}

// label is the name the tree shows for a chunk.
func (d *Document) label(c *chunk) string {
	if c.f == nil {
		return fmt.Sprintf("unknown_0x%02X", c.id)
	}
	return c.f.name
}

// newSite makes the site of a value.
func (d *Document) newSite(c *chunk, elem int, kind model.Kind, lo, hi *big.Int) *model.Site {
	d.sites++
	l := &leaf{doc: d, c: c, elem: elem, kind: kind}
	s := &model.Site{ID: d.sites, IntMin: lo, IntMax: hi, Ref: l}
	if kind == model.KindString {
		s.CheckText = d.codec.check
	}
	l.site = s
	return s
}

// slot is the value of a chunk, with its site when it has one.
func (d *Document) slot(c *chunk) model.Slot {
	raw := d.data[c.dataStart:c.end]
	typeName := typeNames[c.typ]
	switch c.typ {
	case ftInt32:
		v := &model.Value{Kind: model.KindInt, Type: typeName, Int: big.NewInt(int64(int32(berAt(d.data, c))))}
		return model.Slot{Value: v, Site: d.newSite(c, -1, model.KindInt, minInt32, maxInt32)}
	case ftUInt32:
		n := int64(raw[0]) | int64(raw[1])<<8 | int64(raw[2])<<16 | int64(raw[3])<<24
		v := &model.Value{Kind: model.KindInt, Type: typeName, Int: big.NewInt(n)}
		return model.Slot{Value: v, Site: d.newSite(c, -1, model.KindInt, zero, maxUInt32)}
	case ftInt8:
		v := &model.Value{Kind: model.KindInt, Type: typeName, Int: big.NewInt(int64(int8(raw[0])))}
		return model.Slot{Value: v, Site: d.newSite(c, -1, model.KindInt, minInt8, maxInt8)}
	case ftUInt8, ftFlags:
		v := &model.Value{Kind: model.KindInt, Type: typeName, Int: big.NewInt(int64(raw[0]))}
		return model.Slot{Value: v, Site: d.newSite(c, -1, model.KindInt, zero, maxUInt8)}
	case ftBool:
		v := &model.Value{Kind: model.KindBool, Type: typeName, Bool: raw[0] != 0}
		return model.Slot{Value: v, Site: d.newSite(c, -1, model.KindBool, nil, nil)}
	case ftDouble:
		bits := uint64(0)
		for i := 7; i >= 0; i-- {
			bits = bits<<8 | uint64(raw[i])
		}
		v := &model.Value{Kind: model.KindFloat, Type: typeName, Float: math.Float64frombits(bits)}
		return model.Slot{Value: v, Site: d.newSite(c, -1, model.KindFloat, nil, nil)}
	case ftString:
		v := &model.Value{Kind: model.KindString, Type: typeName, Str: d.codec.decode(raw)}
		return model.Slot{Value: v, Site: d.newSite(c, -1, model.KindString, nil, nil)}
	case ftVecBool, ftVecInt16, ftVecInt32, ftVecUInt8:
		return model.Slot{Value: d.vector(c, raw)}
	case ftStruct:
		v := &model.Value{Kind: model.KindObject, Type: c.def.name}
		for _, k := range c.kids {
			if k.typ == ftCount {
				continue
			}
			v.Children = append(v.Children, model.Child{Label: d.label(k), Slot: d.slot(k)})
		}
		return model.Slot{Value: v}
	case ftArray:
		v := &model.Value{Kind: model.KindList, Type: "Array<" + c.def.name + ">"}
		for i, e := range c.elems {
			el := &model.Value{Kind: model.KindObject, Type: c.def.name}
			for _, k := range e.kids {
				if k.typ == ftCount {
					continue
				}
				el.Children = append(el.Children, model.Child{Label: d.label(k), Slot: d.slot(k)})
			}
			label := fmt.Sprintf("[%d]", i)
			if c.def.hasID {
				label = fmt.Sprintf("[%d]", e.id)
			}
			v.Children = append(v.Children, model.Child{Label: label, Slot: model.Slot{Value: el}})
		}
		return model.Slot{Value: v}
	default:
		t := typeName
		if c.f == nil {
			t = fmt.Sprintf("unknown chunk 0x%02X", c.id)
		}
		return model.Slot{Value: &model.Value{Kind: model.KindBytes, Type: t, Bytes: slices.Clone(raw)}}
	}
}

// vector is the list of the elements of a vector chunk.
func (d *Document) vector(c *chunk, raw []byte) *model.Value {
	v := &model.Value{Kind: model.KindList, Type: typeNames[c.typ]}
	numbered := c.f != nil && (c.f.name == "switches" || c.f.name == "variables")
	width := map[fieldType]int{ftVecBool: 1, ftVecUInt8: 1, ftVecInt16: 2, ftVecInt32: 4}[c.typ]
	n := len(raw) / width
	v.Children = make([]model.Child, 0, n)
	for i := 0; i < n; i++ {
		b := raw[i*width : (i+1)*width]
		var el *model.Value
		var site *model.Site
		switch c.typ {
		case ftVecBool:
			el = &model.Value{Kind: model.KindBool, Type: "Boolean", Bool: b[0] != 0}
			site = d.newSite(c, i, model.KindBool, nil, nil)
		case ftVecUInt8:
			el = &model.Value{Kind: model.KindInt, Type: "UInt8", Int: big.NewInt(int64(b[0]))}
			site = d.newSite(c, i, model.KindInt, zero, maxUInt8)
		case ftVecInt16:
			el = &model.Value{Kind: model.KindInt, Type: "Int16", Int: big.NewInt(int64(int16(uint16(b[0]) | uint16(b[1])<<8)))}
			site = d.newSite(c, i, model.KindInt, minInt16, maxInt16)
		default:
			el = &model.Value{Kind: model.KindInt, Type: "Int32", Int: big.NewInt(int64(int32(uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24)))}
			site = d.newSite(c, i, model.KindInt, minInt32, maxInt32)
		}
		label := fmt.Sprintf("[%d]", i)
		if numbered {
			// RPG Maker numbers its switches and variables from 1, in four digits.
			label = fmt.Sprintf("%04d", i+1)
		}
		v.Children = append(v.Children, model.Child{Label: label, Slot: model.Slot{Value: el, Site: site}})
	}
	return v
}

// Variables are the structs of the save, in file order.
func (d *Document) Variables() []model.Variable { return d.vars }

// Details are facts about the save.
func (d *Document) Details() []model.Detail { return d.details }

// find is the value at the path of labels below the top-level variable name, or nil.
func (d *Document) find(path ...string) *model.Value {
	for _, v := range d.vars {
		if v.Name != path[0] {
			continue
		}
		cur := v.Slot.Value
		for _, label := range path[1:] {
			var next *model.Value
			for _, ch := range cur.Children {
				if ch.Label == label {
					next = ch.Slot.Value
					break
				}
			}
			if next == nil {
				return nil
			}
			cur = next
		}
		return cur
	}
	return nil
}

func (d *Document) makeDetails() {
	add := func(label, text, tip string) {
		d.details = append(d.details, model.Detail{Label: label, Text: text, Tooltip: tip})
	}
	if d.is2k3 {
		add("Game", "RPG Maker 2003", "The save has data that only RPG Maker 2003 writes.")
	} else {
		add("Game", "RPG Maker 2000", "The save has no data that only RPG Maker 2003 writes, so it is from RPG Maker 2000 or from 2003 without its battle system data.")
	}
	add("Text encoding", d.codec.name, d.codec.reason)
	if v := d.find("title", "hero_name"); v != nil {
		add("Hero", v.Str, "")
	}
	if v := d.find("title", "hero_level"); v != nil {
		add("Level", v.Int.String(), "")
	}
	if v := d.find("title", "hero_hp"); v != nil {
		add("HP", v.Int.String(), "")
	}
	if v := d.find("title", "timestamp"); v != nil {
		// A Delphi TDateTime: days since 1899-12-30, in the local time of the computer that saved.
		if t := v.Float; t >= 25569 && t < 100000 {
			secs := int64(math.Round((t - 25569) * 86400))
			add("Saved", time.Unix(secs, 0).UTC().Format("2006-01-02 15:04:05"), "The time on the computer that saved the game; the file has no time zone.")
		}
	}
	if v := d.find("inventory", "gold"); v != nil {
		add("Gold", v.Int.String(), "")
	}
	if v := d.find("system", "switches"); v != nil {
		add("Switches", fmt.Sprint(len(v.Children)), "")
	}
	if v := d.find("system", "variables"); v != nil {
		add("Variables", fmt.Sprint(len(v.Children)), "")
	}
	if d.unknown > 0 {
		add("Unknown chunks", fmt.Sprint(d.unknown), "Chunks that this editor has no description of (a patched or custom game); they are kept as they are and shown as bytes.")
	}
	if tail := d.tail(); tail > 0 {
		add("Unread bytes", fmt.Sprint(tail), "Bytes at the end of the file that are not chunks; they are kept as they are.")
	}
}

// tail is the number of bytes after the last chunk that were read.
func (d *Document) tail() int {
	if len(d.root.kids) == 0 {
		return len(d.data) - headerLen
	}
	return len(d.data) - d.root.kids[len(d.root.kids)-1].end
}

// chunkEdit is what an edit does to a chunk: it replaces the data, or some elements of a vector.
type chunkEdit struct {
	whole    []byte
	hasWhole bool
	patches  []patch
}

type patch struct {
	off int
	b   []byte
}

// Encode is the file with the edits made.
func (d *Document) Encode(edits []model.Edit) ([]byte, error) {
	plans := map[*chunk]*chunkEdit{}
	dirty := map[*chunk]bool{}
	for _, e := range edits {
		l, b, err := d.plan(e)
		if err != nil {
			return nil, err
		}
		p := plans[l.c]
		if p == nil {
			p = &chunkEdit{}
			plans[l.c] = p
		}
		if l.elem < 0 {
			p.whole, p.hasWhole = b, true
		} else {
			width := len(b)
			p.patches = append(p.patches, patch{off: l.elem * width, b: b})
		}
		for c := l.c; c != nil; c = c.parent {
			dirty[c] = true
		}
	}
	em := &emitter{data: d.data, dirty: dirty, plans: plans}
	out := bytes.Clone(d.data[:d.root.dataStart])
	return append(out, em.region(d.root.dataStart, d.root.end, d.root.kids)...), nil
}

// plan checks an edit and returns the bytes it writes.
func (d *Document) plan(e model.Edit) (*leaf, []byte, error) {
	var l *leaf
	if e.Site != nil {
		l, _ = e.Site.Ref.(*leaf)
	}
	if l == nil || l.doc != d || l.site != e.Site {
		return nil, nil, errors.New(tagForeignSite, "This edit is for a value of another file", "site", siteID(e.Site))
	}
	v := e.Value
	if v == nil || v.Kind != l.kind {
		return nil, nil, errors.New(tagKindMismatch, "The new value is not a "+l.kind.String(), "site", l.site.ID, "want", l.kind.String())
	}
	switch l.kind {
	case model.KindInt:
		n := v.Int
		if n == nil {
			return nil, nil, errors.New(tagInvalidEdit, "The new number is missing", "site", l.site.ID)
		}
		if (l.site.IntMin != nil && n.Cmp(l.site.IntMin) < 0) || (l.site.IntMax != nil && n.Cmp(l.site.IntMax) > 0) {
			return nil, nil, errors.New(tagOutOfRange,
				fmt.Sprintf("Enter a whole number from %v to %v", l.site.IntMin, l.site.IntMax),
				"site", l.site.ID, "value", n.String())
		}
		return l, intBytes(l, n.Int64()), nil
	case model.KindBool:
		b := byte(0)
		if v.Bool {
			b = 1
		}
		return l, []byte{b}, nil
	case model.KindFloat:
		if math.IsNaN(v.Float) || math.IsInf(v.Float, 0) {
			return nil, nil, errors.New(tagInvalidEdit, "Enter a finite number", "site", l.site.ID)
		}
		bits := math.Float64bits(v.Float)
		out := make([]byte, 8)
		for i := range out {
			out[i] = byte(bits >> (8 * uint(i)))
		}
		return l, out, nil
	case model.KindString:
		b, err := d.codec.encode(v.Str)
		if err != nil {
			return nil, nil, err
		}
		return l, b, nil
	}
	return nil, nil, errors.New(tagKindMismatch, "This value cannot be edited", "site", l.site.ID)
}

func siteID(s *model.Site) int {
	if s == nil {
		return 0
	}
	return s.ID
}

// intBytes is the data of a whole number for the chunk or the vector element of l.
func intBytes(l *leaf, n int64) []byte {
	le := func(width int) []byte {
		out := make([]byte, width)
		for i := range out {
			out[i] = byte(n >> (8 * uint(i)))
		}
		return out
	}
	if l.elem >= 0 {
		switch l.c.typ {
		case ftVecInt16:
			return le(2)
		case ftVecInt32:
			return le(4)
		default:
			return le(1)
		}
	}
	switch l.c.typ {
	case ftUInt32:
		return le(4)
	case ftInt8, ftUInt8, ftFlags:
		return le(1)
	default:
		return appendBER(nil, uint32(int32(n)))
	}
}

// emitter writes the file again, copying every chunk that no edit touches.
type emitter struct {
	data  []byte
	dirty map[*chunk]bool
	plans map[*chunk]*chunkEdit
}

// region is data[lo:hi], which holds the chunks kids and the bytes between them, with the chunks
// written again.
func (e *emitter) region(lo, hi int, kids []*chunk) []byte {
	var out []byte
	pos := lo
	for _, k := range kids {
		out = append(out, e.data[pos:k.start]...)
		out = append(out, e.chunk(k)...)
		pos = k.end
	}
	return append(out, e.data[pos:hi]...)
}

// chunk is the bytes of a chunk, with its size corrected when the data changed length.
func (e *emitter) chunk(c *chunk) []byte {
	if !e.dirty[c] {
		return e.data[c.start:c.end]
	}
	var body []byte
	plan := e.plans[c]
	switch {
	case plan != nil && plan.hasWhole:
		body = plan.whole
	case plan != nil:
		body = bytes.Clone(e.data[c.dataStart:c.end])
		for _, p := range plan.patches {
			copy(body[p.off:], p.b)
		}
	default:
		body = e.region(c.dataStart, c.end, c.inner())
	}
	if len(body) == c.end-c.dataStart {
		return append(bytes.Clone(e.data[c.start:c.dataStart]), body...)
	}
	out := bytes.Clone(e.data[c.start:c.sizeStart])
	out = appendBER(out, uint32(len(body)))
	return append(out, body...)
}
