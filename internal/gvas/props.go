package gvas

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// ptype is the type of a property, or of the elements of an array, set or map.
type ptype struct {
	name       string // "IntProperty", "StructProperty", ...
	structName string // of a StructProperty; empty when the file does not say
	enumName   string // of a ByteProperty or EnumProperty; empty when none
	kids       []*ptype
}

// tag is the part of a property that comes before its value.
type tag struct {
	name   string
	typ    *ptype
	size   int // size of the value in bytes, as the file says
	index  uint32
	sizeAt int // offset of the size field
	// boolAt is where the value of a BoolProperty is: a byte of its own in the classic tag, the
	// flags byte in the tag of Unreal Engine 5.4 and later (boolFlag).
	boolAt   int
	boolVal  bool
	boolFlag bool
	// binary is the "serialized natively" flag of a 5.4 tag.
	binary bool
}

// elemCtx says how to read the value of an element or of a property.
type elemCtx struct {
	byteLabel  bool // a ByteProperty is an enum name, not a number
	structSize int  // size of the struct as a whole; -1 when the file does not say
	hint       int  // size of one element when the elements share the rest of the property; -1 if unknown
	binary     bool // the struct is serialized natively
}

// maxByteItems is the size above which an array of bytes is kept as bytes rather than as a list
// of numbers.
const maxByteItems = 4096

// span is a range of the original bytes. A span with a leaf is replaced when its value is
// edited; a span with sizeAt is a property or an array inner tag, whose size field is fixed when
// what is inside it changes size.
type span struct {
	start, end int
	sizeAt     int // offset of the u32 size field, -1 when none
	kids       []*span
	leaf       *leaf
}

// readPropList reads properties until the "None" that ends the list. The spans of the
// properties are added to parent.
func (r *reader) readPropList(parent *span) ([]model.Child, error) {
	var kids []model.Child
	seen := map[string]int{}
	for {
		start := r.pos
		name, err := r.fstring()
		if err != nil {
			return nil, err
		}
		if name == "None" {
			return kids, nil
		}
		if !plausibleName(name) {
			return nil, errors.New(tagBadProperty, "a property has an impossible name", "offset", start)
		}
		tg, err := r.readTag(name)
		if err != nil {
			return nil, err
		}
		slot := r.readProperty(tg, start, parent)
		label := name
		if tg.index != 0 {
			label += "[" + strconv.FormatUint(uint64(tg.index), 10) + "]"
		}
		kids = append(kids, model.Child{Label: uniqueLabel(seen, label), Slot: slot})
	}
}

// uniqueLabel returns label, or label with a number when it has been used.
func uniqueLabel(seen map[string]int, label string) string {
	seen[label]++
	if n := seen[label]; n > 1 {
		return label + " (" + strconv.Itoa(n) + ")"
	}
	return label
}

func plausibleName(s string) bool {
	if s == "" || len(s) > 1024 {
		return false
	}
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func plausibleType(s string) bool {
	if len(s) < len("Property") || len(s) > 64 || !strings.HasSuffix(s, "Property") {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// readTag reads what follows the name of a property, up to its value.
func (r *reader) readTag(name string) (*tag, error) {
	tg := &tag{name: name}
	if r.v.propertyTag() {
		return r.readTagNew(tg)
	}
	typeName, err := r.fstring()
	if err != nil {
		return nil, err
	}
	if !plausibleType(typeName) {
		return nil, errors.New(tagBadProperty, "a property has an impossible type", "property", name, "type", typeName)
	}
	t := &ptype{name: typeName}
	tg.typ = t
	tg.sizeAt = r.pos
	size, err := r.u32()
	if err != nil {
		return nil, err
	}
	if tg.index, err = r.u32(); err != nil {
		return nil, err
	}
	switch typeName {
	case "StructProperty":
		if t.structName, err = r.fstring(); err != nil {
			return nil, err
		}
		if err := r.skip(16); err != nil {
			return nil, err
		}
	case "ArrayProperty", "SetProperty":
		if r.v.guidAndInner() {
			inner, err := r.fstring()
			if err != nil {
				return nil, err
			}
			t.kids = []*ptype{{name: inner}}
		}
	case "MapProperty":
		if r.v.guidAndInner() {
			k, err := r.fstring()
			if err != nil {
				return nil, err
			}
			v, err := r.fstring()
			if err != nil {
				return nil, err
			}
			t.kids = []*ptype{{name: k}, {name: v}}
		}
	case "ByteProperty", "EnumProperty":
		e, err := r.fstring()
		if err != nil {
			return nil, err
		}
		if e != "None" {
			t.enumName = e
		}
	case "BoolProperty":
		tg.boolAt = r.pos
		b, err := r.u8()
		if err != nil {
			return nil, err
		}
		tg.boolVal = b != 0
	}
	if r.v.guidAndInner() {
		has, err := r.u8()
		if err != nil {
			return nil, err
		}
		if has != 0 {
			if err := r.skip(16); err != nil {
				return nil, err
			}
		}
	}
	return tg, r.checkSize(tg, size)
}

// checkSize records the size of the value after checking that it fits in the property list.
func (r *reader) checkSize(tg *tag, size uint32) error {
	if int64(size) > int64(r.lim-r.pos) {
		return errors.New(tagBadProperty, "a property is larger than what is left of the file",
			"property", tg.name, "size", size, "offset", r.pos)
	}
	tg.size = int(size)
	return nil
}

// node is one name of a tree of type names (FPropertyTypeName).
type node struct {
	name  string
	inner []*node
}

func (r *reader) readNode(depth int) (*node, error) {
	if depth > 16 {
		return nil, errors.New(tagBadProperty, "a type name is nested too deep", "offset", r.pos)
	}
	name, err := r.fstring()
	if err != nil {
		return nil, err
	}
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	if int64(n)*5 > int64(r.lim-r.pos) {
		return nil, errors.New(tagBadProperty, "a type name has more parts than the file has bytes", "count", n)
	}
	nd := &node{name: name}
	for range n {
		c, err := r.readNode(depth + 1)
		if err != nil {
			return nil, err
		}
		nd.inner = append(nd.inner, c)
	}
	return nd, nil
}

// Flags of the tag of Unreal Engine 5.4 and later (EPropertyTagFlags).
const (
	flagArrayIndex = 0x01
	flagGUID       = 0x02
	flagExtensions = 0x04
	flagBinary     = 0x08
	flagBoolTrue   = 0x10
)

func (r *reader) readTagNew(tg *tag) (*tag, error) {
	root, err := r.readNode(0)
	if err != nil {
		return nil, err
	}
	if !plausibleType(root.name) {
		return nil, errors.New(tagBadProperty, "a property has an impossible type", "property", tg.name, "type", root.name)
	}
	tg.typ = ptypeFromNode(root)
	tg.sizeAt = r.pos
	size, err := r.u32()
	if err != nil {
		return nil, err
	}
	tg.boolAt = r.pos
	flags, err := r.u8()
	if err != nil {
		return nil, err
	}
	tg.boolFlag = true
	tg.boolVal = flags&flagBoolTrue != 0
	tg.binary = flags&flagBinary != 0
	if flags&flagExtensions != 0 {
		return nil, errors.New(tagUnsupported, "a property has extension data, which is not supported", "property", tg.name)
	}
	if flags&flagArrayIndex != 0 {
		if tg.index, err = r.u32(); err != nil {
			return nil, err
		}
	}
	if flags&flagGUID != 0 {
		if err := r.skip(16); err != nil {
			return nil, err
		}
	}
	return tg, r.checkSize(tg, size)
}

// pathName is the last part of "/Script/Engine.Name" or of "Name".
func pathName(s string) string {
	if i := strings.LastIndexAny(s, "./"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func ptypeFromNode(n *node) *ptype {
	t := &ptype{name: n.name}
	switch n.name {
	case "StructProperty":
		if len(n.inner) > 0 {
			t.structName = pathName(n.inner[0].name)
		}
	case "ByteProperty", "EnumProperty":
		if len(n.inner) > 0 {
			t.enumName = pathName(n.inner[0].name)
		}
	case "ArrayProperty", "SetProperty":
		if len(n.inner) > 0 {
			t.kids = []*ptype{ptypeFromNode(n.inner[0])}
		}
	case "MapProperty":
		if len(n.inner) > 1 {
			t.kids = []*ptype{ptypeFromNode(n.inner[0]), ptypeFromNode(n.inner[1])}
		}
	}
	return t
}

// readProperty reads the value of a property. When the value cannot be decoded, it is kept as
// bytes, because the size says where the next property starts.
func (r *reader) readProperty(tg *tag, start int, parent *span) model.Slot {
	bodyStart := r.pos
	end := bodyStart + tg.size
	ps := &span{start: start, end: end, sizeAt: tg.sizeAt}
	savedLim := r.lim
	r.lim = end
	slot, err := r.readBody(tg, ps)
	if err == nil && r.pos != end {
		err = errors.New(tagSizeMismatch, "the value of a property does not fill its size",
			"property", tg.name, "size", tg.size, "read", r.pos-bodyStart)
	}
	r.lim = savedLim
	r.pos = end
	if err != nil {
		ps.kids = nil
		ps.leaf = nil
		raw := append([]byte(nil), r.d[bodyStart:end]...)
		slot = model.Slot{Value: &model.Value{Kind: model.KindBytes, Type: tg.typ.name, Bytes: raw}}
	}
	parent.kids = append(parent.kids, ps)
	return slot
}

func (r *reader) readBody(tg *tag, ps *span) (model.Slot, error) {
	t := tg.typ
	switch t.name {
	case "BoolProperty":
		sp := &span{start: tg.boolAt, end: tg.boolAt + 1, sizeAt: -1}
		op := opBool
		if tg.boolFlag {
			op = opBoolFlag
		}
		site := r.addLeaf(ps, sp, model.KindBool, op, 1, false)
		return model.Slot{Value: &model.Value{Kind: model.KindBool, Type: t.name, Bool: tg.boolVal}, Site: site}, nil
	case "ArrayProperty":
		return r.readArray(t, ps)
	case "SetProperty":
		return r.readSet(t, ps)
	case "MapProperty":
		return r.readMap(t, ps)
	}
	return r.readScalar(t, ps, elemCtx{byteLabel: t.enumName != "", structSize: tg.size, hint: -1, binary: tg.binary})
}

// attempt runs fn with a scratch span. When fn succeeds the spans it made are added to parent;
// when it fails the reader goes back to where it was.
func (r *reader) attempt(parent *span, fn func(tmp *span) (model.Slot, error)) (model.Slot, error) {
	pos, lim := r.pos, r.lim
	tmp := &span{sizeAt: -1}
	s, err := fn(tmp)
	if err != nil {
		r.pos, r.lim = pos, lim
		return model.Slot{}, err
	}
	parent.kids = append(parent.kids, tmp.kids...)
	return s, nil
}

// readScalar reads one value of type t: the value of a property, or an element of a container.
func (r *reader) readScalar(t *ptype, parent *span, c elemCtx) (model.Slot, error) {
	switch t.name {
	case "BoolProperty":
		start := r.pos
		b, err := r.u8()
		if err != nil {
			return model.Slot{}, err
		}
		site := r.addLeaf(parent, &span{start: start, end: start + 1, sizeAt: -1}, model.KindBool, opBool, 1, false)
		return model.Slot{Value: &model.Value{Kind: model.KindBool, Type: t.name, Bool: b != 0}, Site: site}, nil
	case "ByteProperty":
		if c.byteLabel {
			return r.readString(t, parent)
		}
		return r.readInt(t, parent, 1, false)
	case "Int8Property":
		return r.readInt(t, parent, 1, true)
	case "Int16Property":
		return r.readInt(t, parent, 2, true)
	case "IntProperty":
		return r.readInt(t, parent, 4, true)
	case "Int64Property":
		return r.readInt(t, parent, 8, true)
	case "UInt16Property":
		return r.readInt(t, parent, 2, false)
	case "UInt32Property":
		return r.readInt(t, parent, 4, false)
	case "UInt64Property":
		return r.readInt(t, parent, 8, false)
	case "FloatProperty":
		return r.readFloat(t, parent, 4)
	case "DoubleProperty":
		return r.readFloat(t, parent, 8)
	case "StrProperty", "NameProperty", "EnumProperty", "ObjectProperty":
		return r.readString(t, parent)
	case "TextProperty":
		return r.readText(t, parent)
	case "SoftObjectProperty":
		return r.readSoftObject(t, parent)
	case "StructProperty":
		return r.readStruct(t, parent, c)
	}
	return model.Slot{}, errors.New(tagUnsupported, "the type of a value is not supported", "type", t.name)
}

func (r *reader) readInt(t *ptype, parent *span, width int, signed bool) (model.Slot, error) {
	start := r.pos
	if err := r.need(width); err != nil {
		return model.Slot{}, err
	}
	var u uint64
	for i := width - 1; i >= 0; i-- {
		u = u<<8 | uint64(r.d[start+i])
	}
	r.pos += width
	n := new(big.Int)
	if signed {
		shift := uint(64 - 8*width)
		n.SetInt64(int64(u<<shift) >> shift)
	} else {
		n.SetUint64(u)
	}
	site := r.addLeaf(parent, &span{start: start, end: r.pos, sizeAt: -1}, model.KindInt, opInt, width, signed)
	return model.Slot{Value: &model.Value{Kind: model.KindInt, Type: t.name, Int: n}, Site: site}, nil
}

func (r *reader) readFloat(t *ptype, parent *span, width int) (model.Slot, error) {
	start := r.pos
	if err := r.need(width); err != nil {
		return model.Slot{}, err
	}
	var f float64
	if width == 4 {
		f = float64(math.Float32frombits(le32(r.d[start:])))
	} else {
		f = math.Float64frombits(le64(r.d[start:]))
	}
	r.pos += width
	site := r.addLeaf(parent, &span{start: start, end: r.pos, sizeAt: -1}, model.KindFloat, opFloat, width, true)
	return model.Slot{Value: &model.Value{Kind: model.KindFloat, Type: t.name, Float: f, Float32: width == 4}, Site: site}, nil
}

func (r *reader) readString(t *ptype, parent *span) (model.Slot, error) {
	start := r.pos
	s, err := r.fstring()
	if err != nil {
		return model.Slot{}, err
	}
	site := r.addLeaf(parent, &span{start: start, end: r.pos, sizeAt: -1}, model.KindString, opString, 0, false)
	return model.Slot{Value: &model.Value{Kind: model.KindString, Type: t.name, QualifiedType: qualified(t), Str: s}, Site: site}, nil
}

// qualified is the name of the enum of a byte or enum property, which is what the editor shows
// as the full type.
func qualified(t *ptype) string {
	if t.enumName != "" {
		return t.name + " of " + t.enumName
	}
	return ""
}

// readText reads an FText in the two forms that hold a plain string: the Base history (a
// namespace, a key and the source string) and the None history with a culture-invariant
// string. The editable string is the source string. Other histories (formatted, date, number,
// string table...) are left as bytes.
func (r *reader) readText(t *ptype, parent *span) (model.Slot, error) {
	if err := r.skip(4); err != nil { // flags
		return model.Slot{}, err
	}
	h, err := r.u8()
	if err != nil {
		return model.Slot{}, err
	}
	switch int8(h) {
	case 0:
		if _, err := r.fstring(); err != nil { // namespace
			return model.Slot{}, err
		}
		if _, err := r.fstring(); err != nil { // key
			return model.Slot{}, err
		}
	case -1:
		has, err := r.u32()
		if err != nil {
			return model.Slot{}, err
		}
		if has != 1 {
			return model.Slot{}, errors.New(tagUnsupported, "a text without a string is kept as bytes")
		}
	default:
		return model.Slot{}, errors.New(tagUnsupported, "this kind of text is kept as bytes", "history", int8(h))
	}
	return r.readString(t, parent)
}

// readSoftObject reads a soft object path: an asset path name and a sub path, or, in Unreal
// Engine 5 files with package version 1007 and later, a package name, an asset name and a sub
// path.
func (r *reader) readSoftObject(t *ptype, parent *span) (model.Slot, error) {
	labels := []string{"asset", "subpath"}
	if r.v.softObjectNew() {
		labels = []string{"package", "asset", "subpath"}
	}
	v := &model.Value{Kind: model.KindObject, Type: t.name}
	for _, l := range labels {
		s, err := r.readString(&ptype{name: "StrProperty"}, parent)
		if err != nil {
			return model.Slot{}, err
		}
		v.Children = append(v.Children, model.Child{Label: l, Slot: s})
	}
	return model.Slot{Value: v}, nil
}

// readStruct reads a struct: a known binary struct when the name says so, otherwise a list of
// properties.
func (r *reader) readStruct(t *ptype, parent *span, c elemCtx) (model.Slot, error) {
	if spec, ok := binaryStructs[t.structName]; ok {
		s, err := r.attempt(parent, func(tmp *span) (model.Slot, error) { return r.readBinary(t.structName, spec, tmp, c) })
		if err == nil || c.binary {
			return s, err
		}
	} else if c.binary {
		return model.Slot{}, errors.New(tagUnsupported, "a struct that is serialized natively and is not known is kept as bytes",
			"struct", t.structName)
	}
	return r.attempt(parent, func(tmp *span) (model.Slot, error) {
		kids, err := r.readPropList(tmp)
		if err != nil {
			return model.Slot{}, err
		}
		name := t.structName
		if name == "" {
			name = "Struct"
		}
		return model.Slot{Value: &model.Value{Kind: model.KindObject, Type: name, QualifiedType: "StructProperty", Children: kids}}, nil
	})
}

// readItemCount reads a count of elements and checks that the bytes left could hold that many.
func (r *reader) readItemCount() (int, error) {
	n, err := r.u32()
	if err != nil {
		return 0, err
	}
	if int64(n) > int64(r.lim-r.pos) {
		return 0, errors.New(tagBadProperty, "a container has more items than the file has bytes", "count", n)
	}
	return int(n), nil
}

// hintFor is the size of one element when count elements share the rest of the property.
func (r *reader) hintFor(count int) int {
	if rest := r.lim - r.pos; count > 0 && rest%count == 0 {
		return rest / count
	}
	return -1
}

func index(i int) string { return "[" + strconv.Itoa(i) + "]" }

func (r *reader) readArray(t *ptype, ps *span) (model.Slot, error) {
	if len(t.kids) == 0 {
		return model.Slot{}, errors.New(tagUnsupported, "an array does not say the type of its items")
	}
	count, err := r.readItemCount()
	if err != nil {
		return model.Slot{}, err
	}
	inner := t.kids[0]
	list := &model.Value{Kind: model.KindList, Type: t.name}
	parent := ps
	st := inner
	if inner.name == "StructProperty" && !r.v.propertyTag() && r.v.guidAndInner() {
		in, name, err := r.readInnerTag()
		if err != nil {
			return model.Slot{}, err
		}
		ps.kids = append(ps.kids, in)
		parent = in
		cp := *inner
		cp.structName = name
		st = &cp
	}
	if inner.name == "ByteProperty" && inner.enumName == "" && r.lim-r.pos == count {
		if count > maxByteItems {
			list.Kind, list.Bytes = model.KindBytes, append([]byte(nil), r.d[r.pos:r.lim]...)
			r.pos = r.lim
			return model.Slot{Value: list}, nil
		}
	}
	labels := byteModes(inner)
	for _, label := range labels {
		s, err := r.attempt(parent, func(tmp *span) (model.Slot, error) {
			items, err := r.readItems(st, count, tmp, elemCtx{byteLabel: label, structSize: -1, hint: r.hintFor(count)})
			if err != nil {
				return model.Slot{}, err
			}
			l := *list
			l.Children = items
			return model.Slot{Value: &l}, nil
		})
		if err == nil {
			return s, nil
		}
		if len(labels) == 1 || label {
			return model.Slot{}, err
		}
	}
	return model.Slot{}, errors.New(tagUnsupported, "an array could not be read")
}

// byteModes lists how the elements of type t may be stored: a byte property without a known
// enum is a plain byte or, for an enum, a name.
func byteModes(t *ptype) []bool {
	switch {
	case t.name != "ByteProperty":
		return []bool{false}
	case t.enumName != "":
		return []bool{true}
	}
	return []bool{false, true}
}

// readItems reads count elements of type t.
func (r *reader) readItems(t *ptype, count int, parent *span, c elemCtx) ([]model.Child, error) {
	items := make([]model.Child, 0, count)
	for i := range count {
		s, err := r.readScalar(t, parent, c)
		if err != nil {
			return nil, err
		}
		items = append(items, model.Child{Label: index(i), Slot: s})
	}
	if r.pos != r.lim {
		return nil, errors.New(tagSizeMismatch, "the items of a container do not fill its size", "left", r.lim-r.pos)
	}
	return items, nil
}

// readInnerTag reads the tag that precedes the elements of an array of structs (classic
// layout): name, type, size, index, struct name, struct GUID and a property GUID flag. The
// returned span covers the tag and the elements, and its size field is the size of the elements.
func (r *reader) readInnerTag() (*span, string, error) {
	start := r.pos
	if _, err := r.fstring(); err != nil {
		return nil, "", err
	}
	typ, err := r.fstring()
	if err != nil {
		return nil, "", err
	}
	if typ != "StructProperty" {
		return nil, "", errors.New(tagBadProperty, "the tag of the items of an array is not a struct", "type", typ)
	}
	sizeAt := r.pos
	size, err := r.u32()
	if err != nil {
		return nil, "", err
	}
	if err := r.skip(4); err != nil { // index
		return nil, "", err
	}
	name, err := r.fstring()
	if err != nil {
		return nil, "", err
	}
	if err := r.skip(16); err != nil {
		return nil, "", err
	}
	has, err := r.u8()
	if err != nil {
		return nil, "", err
	}
	if has != 0 {
		if err := r.skip(16); err != nil {
			return nil, "", err
		}
	}
	if int64(size) != int64(r.lim-r.pos) {
		return nil, "", errors.New(tagSizeMismatch, "the tag of the items of an array has the wrong size",
			"size", size, "left", r.lim-r.pos)
	}
	return &span{start: start, end: r.lim, sizeAt: sizeAt}, name, nil
}

func (r *reader) readSet(t *ptype, ps *span) (model.Slot, error) {
	if len(t.kids) == 0 {
		return model.Slot{}, errors.New(tagUnsupported, "a set does not say the type of its items")
	}
	removed, err := r.u32()
	if err != nil {
		return model.Slot{}, err
	}
	if removed != 0 {
		return model.Slot{}, errors.New(tagUnsupported, "a set with removed items is kept as bytes")
	}
	count, err := r.readItemCount()
	if err != nil {
		return model.Slot{}, err
	}
	var lastErr error
	for _, st := range structOptions(t.kids[0], "Guid", "") {
		for _, label := range byteModes(t.kids[0]) {
			kt := withStruct(t.kids[0], st)
			s, err := r.attempt(ps, func(tmp *span) (model.Slot, error) {
				items, err := r.readItems(kt, count, tmp, elemCtx{byteLabel: label, structSize: -1, hint: r.hintFor(count)})
				if err != nil {
					return model.Slot{}, err
				}
				return model.Slot{Value: &model.Value{Kind: model.KindList, Type: t.name, Children: items}}, nil
			})
			if err == nil {
				return s, nil
			}
			lastErr = err
		}
	}
	return model.Slot{}, lastErr
}

// structOptions lists the struct names to try for an element of type t. A map or set of
// structs does not name the struct in a classic file; the first option is what Unreal's
// serializer needs for the key (a Guid), and the other is a list of properties.
func structOptions(t *ptype, first, second string) []string {
	if t.name != "StructProperty" || t.structName != "" {
		return []string{t.structName}
	}
	if first == second {
		return []string{first}
	}
	return []string{first, second}
}

func withStruct(t *ptype, name string) *ptype {
	if t.name != "StructProperty" || t.structName == name {
		return t
	}
	cp := *t
	cp.structName = name
	return &cp
}

// variant is one way to read the keys and values of a map.
type variant struct {
	kStruct, vStruct string
	kLabel, vLabel   bool
}

func (r *reader) readMap(t *ptype, ps *span) (model.Slot, error) {
	if len(t.kids) < 2 {
		return model.Slot{}, errors.New(tagUnsupported, "a map does not say the types of its keys and values")
	}
	removed, err := r.u32()
	if err != nil {
		return model.Slot{}, err
	}
	if removed != 0 {
		return model.Slot{}, errors.New(tagUnsupported, "a map with removed items is kept as bytes")
	}
	count, err := r.readItemCount()
	if err != nil {
		return model.Slot{}, err
	}
	kt, vt := t.kids[0], t.kids[1]
	var lastErr error
	for _, ks := range structOptions(kt, "Guid", "") {
		for _, vs := range structOptions(vt, "", "Guid") {
			for _, kl := range byteModes(kt) {
				for _, vl := range byteModes(vt) {
					va := variant{kStruct: ks, vStruct: vs, kLabel: kl, vLabel: vl}
					s, err := r.attempt(ps, func(tmp *span) (model.Slot, error) { return r.readEntries(t, count, tmp, va) })
					if err == nil {
						return s, nil
					}
					lastErr = err
				}
			}
		}
	}
	return model.Slot{}, lastErr
}

func (r *reader) readEntries(t *ptype, count int, parent *span, va variant) (model.Slot, error) {
	kt, vt := withStruct(t.kids[0], va.kStruct), withStruct(t.kids[1], va.vStruct)
	m := &model.Value{Kind: model.KindMap, Type: t.name}
	seen := map[string]int{}
	for i := range count {
		k, err := r.readScalar(kt, parent, elemCtx{byteLabel: va.kLabel, structSize: -1, hint: -1})
		if err != nil {
			return model.Slot{}, err
		}
		v, err := r.readScalar(vt, parent, elemCtx{byteLabel: va.vLabel, structSize: -1, hint: -1})
		if err != nil {
			return model.Slot{}, err
		}
		if label, ok := keyLabel(k.Value); ok {
			m.Children = append(m.Children, model.Child{Label: uniqueLabel(seen, label), Slot: v})
			continue
		}
		entry := &model.Value{Kind: model.KindObject, Type: "MapEntry", Children: []model.Child{
			{Label: "key", Slot: k}, {Label: "value", Slot: v},
		}}
		m.Children = append(m.Children, model.Child{Label: index(i), Slot: model.Slot{Value: entry}})
	}
	if r.pos != r.lim {
		return model.Slot{}, errors.New(tagSizeMismatch, "the entries of a map do not fill its size", "left", r.lim-r.pos)
	}
	return model.Slot{Value: m}, nil
}

// keyLabel is the text under which a map shows the entry of key k: the key itself when it is a
// number, a string, or a struct of numbers.
func keyLabel(k *model.Value) (string, bool) {
	switch k.Kind {
	case model.KindString:
		return k.Str, true
	case model.KindInt:
		return k.Int.String(), true
	case model.KindBool:
		return strconv.FormatBool(k.Bool), true
	case model.KindFloat:
		return model.FormatFloat(k.Float, k.Float32), true
	case model.KindObject:
		if _, ok := binaryStructs[k.Type]; !ok {
			return "", false
		}
		parts := make([]string, 0, len(k.Children))
		for _, c := range k.Children {
			if c.Slot.Value.Kind == model.KindObject || c.Slot.Value.Kind == model.KindList ||
				c.Slot.Value.Kind == model.KindMap || c.Slot.Value.Kind == model.KindBytes {
				return "", false
			}
			parts = append(parts, c.Label+"="+c.Slot.Value.Repr())
		}
		if len(parts) == 0 {
			return "", false
		}
		return "{" + strings.Join(parts, ", ") + "}", true
	}
	return "", false
}

// The tags of the errors that this package makes.
const (
	tagBadHeader    = "GvasBadHeader"
	tagBadProperty  = "GvasBadProperty"
	tagBadString    = "GvasBadString"
	tagBadText      = "GvasBadText"
	tagBadValue     = "GvasBadValue"
	tagNotGVAS      = "GvasNotGVAS"
	tagOutOfRange   = "GvasOutOfRange"
	tagSizeMismatch = "GvasSizeMismatch"
	tagTruncated    = "GvasTruncated"
	tagUnknownSite  = "GvasUnknownSite"
	tagUnsupported  = "GvasUnsupported"
	tagWrongKind    = "GvasWrongKind"
)
