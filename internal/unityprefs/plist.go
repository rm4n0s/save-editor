package unityprefs

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/big"
	"slices"
	"strconv"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// The binary property list (bplist00) is Apple's format; Python's plistlib documents it
// and is the reference the tests check against. The file is: the 8 bytes "bplist00", the
// objects, an offset table (the position of each object, numbered from 0), and a 32-byte
// trailer (5 unused bytes, a sort version, the size of an offset, the size of an object
// number, the count of objects, the number of the top object, the position of the offset
// table). Objects begin with a byte whose high nibble is the type and whose low nibble is a
// size or a count (15 means that an integer object with the count follows).

const plistMagic = "bplist00"

type plistKind uint8

const (
	plistNull plistKind = iota
	plistFalse
	plistTrue
	plistInt
	plistReal
	plistDate
	plistData
	plistASCII
	plistUTF16
	plistUID
	plistArray
	plistSet
	plistDict
)

// plistObject is an object of the file with the bytes it occupies.
type plistObject struct {
	kind plistKind
	// off and end are the bytes of the object; chunkEnd is where the next object, or the
	// offset table, begins, so that gaps between objects are kept.
	off, end, chunkEnd int

	n    *big.Int // plistInt and plistUID
	size int      // the bytes of the number of a plistInt or plistReal
	f    float64  // plistReal and plistDate
	s    string   // plistASCII and plistUTF16
	b    []byte   // plistData
	// refs are the objects of an array or set, or the keys and then the values of a dict;
	// refsOff is where they begin in the file.
	refs    []int
	refsOff int
	// uses is how many places refer to the object: the top of the file and the references
	// of the containers.
	uses int

	// value is the value of a container, made once so that a container reached from two
	// places is the same value in both. A number, string or bool has a value for each place
	// that refers to it, since an edit changes one place only.
	value *model.Value
}

// plist is a parsed binary property list.
type plist struct {
	data             []byte
	refSize, offSize int
	objects          []*plistObject
	top              int
	tableOff         int
	// order lists the objects by position.
	order []int
}

// plistRef is the data of the site of a number, string or bool: the object that holds the
// value, and the place that refers to it, which is the reference at position pos in the
// container parent. An object that more than one place refers to (a writer keeps one object
// for equal numbers and strings) is not changed by an edit of one place: the edit makes a new
// object and points the place at it.
type plistRef struct{ index, parent, pos int }

// parsePlist reads the structure of a binary property list.
func parsePlist(data []byte) (*plist, error) {
	if len(data) < len(plistMagic)+1+32 || !bytes.HasPrefix(data, []byte(plistMagic)) {
		return nil, errors.New(tagPlistInvalid, "the file is too short to be a binary property list", "size", len(data))
	}
	trailer := data[len(data)-32:]
	p := &plist{data: data, offSize: int(trailer[6]), refSize: int(trailer[7])}
	numObjects := binary.BigEndian.Uint64(trailer[8:16])
	top := binary.BigEndian.Uint64(trailer[16:24])
	tableOff := binary.BigEndian.Uint64(trailer[24:32])
	if p.offSize < 1 || p.offSize > 8 || p.refSize < 1 || p.refSize > 8 {
		return nil, errors.New(tagPlistInvalid, "the sizes in the trailer are invalid", "offsetSize", p.offSize, "refSize", p.refSize)
	}
	limit := uint64(len(data) - 32)
	if numObjects == 0 || numObjects > limit || top >= numObjects || tableOff < 9 || tableOff > limit ||
		(limit-tableOff)/uint64(p.offSize) < numObjects {
		return nil, errors.New(tagPlistInvalid, "the trailer does not describe the file",
			"objects", numObjects, "top", top, "tableOffset", tableOff)
	}
	p.top, p.tableOff = int(top), int(tableOff)
	n := int(numObjects)
	p.objects = make([]*plistObject, n)
	offsets := make([]int, n)
	for i := range n {
		o := beUint(data[p.tableOff+i*p.offSize : p.tableOff+(i+1)*p.offSize])
		if o < uint64(len(plistMagic)) || o >= uint64(p.tableOff) {
			return nil, errors.New(tagPlistInvalid, "an object is outside the file", "object", i, "offset", o)
		}
		offsets[i] = int(o)
	}
	for i := range n {
		o, err := p.parseObject(offsets[i])
		if err != nil {
			return nil, err
		}
		p.objects[i] = o
	}
	p.order = make([]int, n)
	for i := range p.order {
		p.order[i] = i
	}
	slices.SortFunc(p.order, func(a, b int) int { return p.objects[a].off - p.objects[b].off })
	for k, i := range p.order {
		o := p.objects[i]
		o.chunkEnd = p.tableOff
		if k+1 < n {
			o.chunkEnd = p.objects[p.order[k+1]].off
		}
		if o.end > o.chunkEnd || o.off == o.chunkEnd {
			return nil, errors.New(tagPlistInvalid, "two objects overlap", "object", i, "offset", o.off)
		}
	}
	p.objects[p.top].uses++
	for _, o := range p.objects {
		for _, r := range o.refs {
			p.objects[r].uses++
		}
	}
	return p, nil
}

// beUint is a big-endian unsigned number of up to 8 bytes.
func beUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

// parseObject reads the object at off.
func (p *plist) parseObject(off int) (*plistObject, error) {
	data, limit := p.data, p.tableOff
	o := &plistObject{off: off}
	marker := data[off]
	hi, lo := marker>>4, int(marker&0xF)
	pos := off + 1
	// need checks that n more bytes of the object exist.
	need := func(n int) bool { return n >= 0 && n <= limit-pos }
	switch hi {
	case 0:
		switch lo {
		case 0:
			o.kind = plistNull
		case 8:
			o.kind = plistFalse
		case 9:
			o.kind = plistTrue
		default:
			return nil, errors.New(tagPlistInvalid, "unknown object marker", "offset", off, "marker", marker)
		}
	case 1:
		size := 1 << lo
		if lo > 4 || !need(size) {
			return nil, errors.New(tagPlistInvalid, "an integer is cut short or too large", "offset", off)
		}
		o.kind, o.size = plistInt, size
		o.n = plistInteger(data[pos:pos+size], true)
		pos += size
	case 2:
		size := 1 << lo
		if (lo != 2 && lo != 3) || !need(size) {
			return nil, errors.New(tagPlistInvalid, "a real is cut short or has an unknown size", "offset", off)
		}
		o.kind, o.size = plistReal, size
		if size == 4 {
			o.f = float64(math.Float32frombits(binary.BigEndian.Uint32(data[pos:])))
		} else {
			o.f = math.Float64frombits(binary.BigEndian.Uint64(data[pos:]))
		}
		pos += size
	case 3:
		if lo != 3 || !need(8) {
			return nil, errors.New(tagPlistInvalid, "a date is cut short or has an unknown size", "offset", off)
		}
		o.kind = plistDate
		o.f = math.Float64frombits(binary.BigEndian.Uint64(data[pos:]))
		pos += 8
	case 4, 5, 6:
		count, next, err := p.readCount(lo, pos)
		if err != nil {
			return nil, err
		}
		pos = next
		width := 1
		if hi == 6 {
			width = 2
		}
		if count > (limit-pos)/width {
			return nil, errors.New(tagPlistInvalid, "a string or data is cut short", "offset", off, "count", count)
		}
		body := data[pos : pos+count*width]
		pos += count * width
		switch hi {
		case 4:
			o.kind, o.b = plistData, body
		case 5:
			o.kind, o.s = plistASCII, string(body)
		default:
			units := make([]uint16, count)
			for i := range units {
				units[i] = binary.BigEndian.Uint16(body[2*i:])
			}
			o.kind, o.s = plistUTF16, string(utf16.Decode(units))
		}
	case 8:
		size := lo + 1
		if !need(size) {
			return nil, errors.New(tagPlistInvalid, "a UID is cut short", "offset", off)
		}
		o.kind = plistUID
		o.n = plistInteger(data[pos:pos+size], false)
		pos += size
	case 0xA, 0xB, 0xC, 0xD:
		count, next, err := p.readCount(lo, pos)
		if err != nil {
			return nil, err
		}
		pos = next
		o.refsOff = pos
		total := count
		o.kind = plistArray
		switch hi {
		case 0xB, 0xC:
			o.kind = plistSet
		case 0xD:
			o.kind = plistDict
			total = 2 * count
		}
		if count > limit || total > (limit-pos)/p.refSize {
			return nil, errors.New(tagPlistInvalid, "a container is cut short", "offset", off, "count", count)
		}
		o.refs = make([]int, total)
		for i := range o.refs {
			r := beUint(data[pos : pos+p.refSize])
			if r >= uint64(len(p.objects)) {
				return nil, errors.New(tagPlistInvalid, "a reference to an object that does not exist", "offset", off, "ref", r)
			}
			o.refs[i] = int(r)
			pos += p.refSize
		}
	default:
		return nil, errors.New(tagPlistInvalid, "unknown object marker", "offset", off, "marker", marker)
	}
	o.end = pos
	return o, nil
}

// readCount reads the count of a string or container whose marker has the low nibble lo
// and whose bytes after the marker begin at pos. It returns the count and where the bytes
// after the count begin.
func (p *plist) readCount(lo, pos int) (count, next int, err error) {
	if lo != 0xF {
		return lo, pos, nil
	}
	data := p.data
	if pos >= p.tableOff || data[pos]>>4 != 1 || data[pos]&0xF > 3 {
		return 0, 0, errors.New(tagPlistInvalid, "a count is not an integer object", "offset", pos)
	}
	size := 1 << (data[pos] & 0xF)
	if size > p.tableOff-pos-1 {
		return 0, 0, errors.New(tagPlistInvalid, "a count is cut short", "offset", pos)
	}
	v := beUint(data[pos+1 : pos+1+size])
	if v > uint64(len(data)) {
		return 0, 0, errors.New(tagPlistInvalid, "a count is larger than the file", "offset", pos, "count", v)
	}
	return int(v), pos + 1 + size, nil
}

// plistInteger reads an integer of 1, 2, 4, 8 or 16 bytes: those of 1, 2 and 4 bytes are
// unsigned, the others signed (as in Python's plistlib). A UID is always unsigned.
func plistInteger(b []byte, signed bool) *big.Int {
	n := new(big.Int).SetBytes(b)
	if signed && len(b) >= 8 && b[0]&0x80 != 0 {
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(8*len(b))))
	}
	return n
}

func parsePlistDocument(data []byte) (model.Document, error) {
	p, err := parsePlist(data)
	if err != nil {
		return nil, err
	}
	top := p.objects[p.top]
	if top.kind != plistDict {
		return nil, errors.New(tagPlistRoot, "the top object of the property list is not a dictionary", "object", p.top)
	}
	d := &Document{layout: layoutPlist, data: data, plist: p}
	root := d.plistValue(p.top)
	used := map[string]bool{}
	for _, c := range root.Children {
		d.vars = append(d.vars, model.Variable{
			Name:     uniqueName(c.Label, used),
			Internal: isInternalKey(c.Label),
			Slot:     c.Slot,
		})
	}
	d.details = []model.Detail{
		{Label: "Layout", Text: "macOS preferences file (binary property list)"},
		{Label: "Preferences", Text: strconv.Itoa(len(d.vars))},
		{Label: "Objects", Text: strconv.Itoa(len(p.objects))},
	}
	return d, nil
}

// plistValue is the value of object i. A container is made once, so that a container that is
// reached from two places is the same value in both; any other object is made again for each
// place that refers to it.
func (d *Document) plistValue(i int) *model.Value {
	o := d.plist.objects[i]
	if o.value != nil {
		return o.value
	}
	v := &model.Value{}
	switch o.kind {
	case plistNull:
		v.Kind = model.KindNull
	case plistFalse, plistTrue:
		v.Kind, v.Type, v.Bool = model.KindBool, "bool", o.kind == plistTrue
	case plistInt:
		v.Kind, v.Type, v.Int = model.KindInt, "int", new(big.Int).Set(o.n)
	case plistReal:
		v.Kind, v.Type, v.Float, v.Float32 = model.KindFloat, "float", o.f, o.size == 4
	case plistDate:
		v.Kind, v.Type, v.Str = model.KindOpaque, "date", dateText(o.f)
	case plistData:
		v.Kind, v.Type, v.Bytes = model.KindBytes, "data", o.b
	case plistASCII, plistUTF16:
		if utf8.ValidString(o.s) {
			v.Kind, v.Type, v.Str = model.KindString, "string", o.s
		} else {
			v.Kind, v.Type, v.Bytes = model.KindBytes, "string", []byte(o.s)
		}
	case plistUID:
		v.Kind, v.Type, v.Str = model.KindOpaque, "uid", "uid "+o.n.String()
	case plistArray, plistSet:
		o.value = v
		v.Kind, v.Type = model.KindList, "array"
		if o.kind == plistSet {
			v.Type = "set"
		}
		for k, r := range o.refs {
			v.Children = append(v.Children, model.Child{Label: "[" + strconv.Itoa(k) + "]", Slot: d.plistSlot(r, i, k)})
		}
	case plistDict:
		o.value = v
		v.Kind, v.Type = model.KindMap, "dict"
		half := len(o.refs) / 2
		for k := range half {
			key := d.plistValue(o.refs[k])
			label := key.Str
			if key.Kind != model.KindString {
				label = key.Repr()
			}
			v.Children = append(v.Children, model.Child{Label: label, Slot: d.plistSlot(o.refs[half+k], i, half+k)})
		}
	}
	return v
}

// plistSlot is the slot that the reference at position pos of the container parent makes of
// object i. A key of a dictionary is never a slot: editing it would rename the preference.
func (d *Document) plistSlot(i, parent, pos int) model.Slot {
	v := d.plistValue(i)
	o := d.plist.objects[i]
	ref := &plistRef{index: i, parent: parent, pos: pos}
	switch v.Kind {
	case model.KindBool, model.KindFloat:
		return model.Slot{Value: v, Site: d.newSite(ref, nil, nil)}
	case model.KindString:
		return model.Slot{Value: v, Site: d.textSite(ref)}
	case model.KindInt:
		switch {
		case !o.n.IsInt64():
			return model.Slot{Value: v}
		case o.n.Cmp(int32Min) < 0 || o.n.Cmp(int32Max) > 0:
			return model.Slot{Value: v, Site: d.newSite(ref, int64Min, int64Max)}
		}
		return model.Slot{Value: v, Site: d.newSite(ref, int32Min, int32Max)}
	}
	return model.Slot{Value: v}
}

// dateText is a date of a property list: seconds since 2001-01-01 00:00:00 UTC.
func dateText(secs float64) string {
	if math.IsNaN(secs) || math.Abs(secs) > 2.5e11 {
		return "date out of range"
	}
	t := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(secs * float64(time.Second)))
	return t.Format(time.RFC3339Nano)
}

func (d *Document) encodePlist(edits []model.Edit) ([]byte, error) {
	p := d.plist
	n := len(p.objects)
	repl := map[int][]byte{}  // objects replaced where they are
	added := [][]byte{}       // objects made for places that share their object
	moved := map[[2]int]int{} // the new object of the reference at (container, position)
	seen := map[int]bool{}
	for _, e := range edits {
		ref, err := d.siteOf(e, seen)
		if err != nil {
			return nil, err
		}
		r := ref.(*plistRef)
		o := p.objects[r.index]
		newBytes, err := encodePlistLeaf(o, e)
		if err != nil {
			return nil, err
		}
		if o.uses == 1 {
			repl[r.index] = newBytes
			continue
		}
		moved[[2]int{r.parent, r.pos}] = n + len(added)
		added = append(added, newBytes)
	}
	data := p.data
	total := n + len(added)
	refSize := max(p.refSize, byteLen(uint64(total-1)))
	first := p.objects[p.order[0]].off
	out := make([]byte, 0, len(data)+16*(len(repl)+len(added)))
	out = append(out, data[:first]...)
	offsets := make([]int, total)
	for _, i := range p.order {
		o := p.objects[i]
		offsets[i] = len(out)
		switch b, ok := repl[i]; {
		case ok:
			out = append(out, b...)
			out = append(out, data[o.end:o.chunkEnd]...)
		case len(o.refs) > 0 && (refSize != p.refSize || hasMoved(moved, i, len(o.refs))):
			// A container whose references change: the bytes before and after them are kept.
			out = append(out, data[o.off:o.refsOff]...)
			for k, r := range o.refs {
				if m, ok := moved[[2]int{i, k}]; ok {
					r = m
				}
				out = appendBE(out, uint64(r), refSize)
			}
			out = append(out, data[o.end:o.chunkEnd]...)
		default:
			out = append(out, data[o.off:o.chunkEnd]...)
		}
	}
	for j, b := range added {
		offsets[n+j] = len(out)
		out = append(out, b...)
	}
	tableOff := len(out)
	offSize := max(p.offSize, byteLen(uint64(tableOff)))
	for _, o := range offsets {
		out = appendBE(out, uint64(o), offSize)
	}
	oldTableEnd := p.tableOff + n*p.offSize
	out = append(out, data[oldTableEnd:len(data)-32]...)
	trailer := bytes.Clone(data[len(data)-32:])
	trailer[6], trailer[7] = byte(offSize), byte(refSize)
	binary.BigEndian.PutUint64(trailer[8:], uint64(total))
	binary.BigEndian.PutUint64(trailer[24:], uint64(tableOff))
	return append(out, trailer...), nil
}

// hasMoved reports whether a reference of the container i, which has count references, is
// to be pointed at a new object.
func hasMoved(moved map[[2]int]int, i, count int) bool {
	for k := range count {
		if _, ok := moved[[2]int{i, k}]; ok {
			return true
		}
	}
	return false
}

// encodePlistLeaf is the object that replaces o to hold the value of the edit.
func encodePlistLeaf(o *plistObject, e model.Edit) ([]byte, error) {
	v := e.Value
	wrongKind := func(want model.Kind) error {
		return errors.New(tagEditKind, "the new value has another kind than the old one", "site", e.Site.ID,
			"want", want.String(), "got", v.Kind.String())
	}
	switch o.kind {
	case plistFalse, plistTrue:
		if v.Kind != model.KindBool {
			return nil, wrongKind(model.KindBool)
		}
		if v.Bool {
			return []byte{0x09}, nil
		}
		return []byte{0x08}, nil
	case plistInt:
		if v.Kind != model.KindInt {
			return nil, wrongKind(model.KindInt)
		}
		if v.Int == nil || v.Int.Cmp(e.Site.IntMin) < 0 || v.Int.Cmp(e.Site.IntMax) > 0 {
			return nil, errors.New(tagEditRange, "The number is out of range", "site", e.Site.ID,
				"min", e.Site.IntMin.String(), "max", e.Site.IntMax.String())
		}
		return encodePlistInt(v.Int.Int64()), nil
	case plistReal:
		if v.Kind != model.KindFloat {
			return nil, wrongKind(model.KindFloat)
		}
		f := v.Float
		if math.IsNaN(f) || math.IsInf(f, 0) || (o.size == 4 && math.Abs(f) > math.MaxFloat32) {
			return nil, errors.New(tagEditRange, "The number must be finite and fit the size of the stored real", "site", e.Site.ID)
		}
		if o.size == 4 {
			return binary.BigEndian.AppendUint32([]byte{0x22}, math.Float32bits(float32(f))), nil
		}
		return binary.BigEndian.AppendUint64([]byte{0x23}, math.Float64bits(f)), nil
	default:
		if v.Kind != model.KindString {
			return nil, wrongKind(model.KindString)
		}
		if !utf8.ValidString(v.Str) {
			return nil, errors.New(tagEditText, "The text is not valid UTF-8", "site", e.Site.ID)
		}
		return encodePlistString(v.Str), nil
	}
}

// encodePlistInt is the shortest integer object: unsigned for 1, 2 and 4 bytes, signed for
// 8 bytes, which is how negative numbers are written.
func encodePlistInt(n int64) []byte {
	switch {
	case n >= 0 && n <= math.MaxUint8:
		return []byte{0x10, byte(n)}
	case n >= 0 && n <= math.MaxUint16:
		return binary.BigEndian.AppendUint16([]byte{0x11}, uint16(n))
	case n >= 0 && n <= math.MaxUint32:
		return binary.BigEndian.AppendUint32([]byte{0x12}, uint32(n))
	}
	return binary.BigEndian.AppendUint64([]byte{0x13}, uint64(n))
}

// encodePlistString is an ASCII string, or a UTF-16 string when the text has other
// characters.
func encodePlistString(s string) []byte {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return append(plistHeader(0x50, len(s)), s...)
	}
	units := utf16.Encode([]rune(s))
	out := plistHeader(0x60, len(units))
	for _, u := range units {
		out = binary.BigEndian.AppendUint16(out, u)
	}
	return out
}

// plistHeader is the marker of a string or container with count items.
func plistHeader(marker byte, count int) []byte {
	if count < 15 {
		return []byte{marker | byte(count)}
	}
	return append([]byte{marker | 0xF}, encodePlistInt(int64(count))...)
}

func byteLen(v uint64) int {
	n := 1
	for v >>= 8; v != 0; v >>= 8 {
		n++
	}
	return n
}

func appendBE(b []byte, v uint64, size int) []byte {
	for i := size - 1; i >= 0; i-- {
		b = append(b, byte(v>>(8*uint(i))))
	}
	return b
}

const (
	tagPlistInvalid = "UnityPlistInvalid"
	tagPlistRoot    = "UnityPlistRoot"
)
