package nrbf

import (
	"encoding/binary"
	"math"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
)

// The reader follows [MS-NRBF] (https://learn.microsoft.com/openspecs/windows_protocols/ms-nrbf),
// revision 13.0.

// recordType is the RecordTypeEnumeration of the spec.
type recordType byte

const (
	recHeader               recordType = 0
	recClassWithID          recordType = 1
	recSystemClassMembers   recordType = 2
	recClassMembers         recordType = 3
	recSystemClassTypes     recordType = 4
	recClassTypes           recordType = 5
	recObjectString         recordType = 6
	recBinaryArray          recordType = 7
	recPrimitiveTyped       recordType = 8
	recMemberReference      recordType = 9
	recObjectNull           recordType = 10
	recMessageEnd           recordType = 11
	recLibrary              recordType = 12
	recObjectNullMultiple25 recordType = 13
	recObjectNullMultiple   recordType = 14
	recArraySinglePrimitive recordType = 15
	recArraySingleObject    recordType = 16
	recArraySingleString    recordType = 17
	recMethodCall           recordType = 21
	recMethodReturn         recordType = 22
)

// binType is the BinaryTypeEnumeration.
type binType byte

const (
	binPrimitive binType = iota
	binString
	binObject
	binSystemClass
	binClass
	binObjectArray
	binStringArray
	binPrimitiveArray
)

// primType is the PrimitiveTypeEnumeration.
type primType byte

const (
	primBoolean  primType = 1
	primByte     primType = 2
	primChar     primType = 3
	primDecimal  primType = 5
	primDouble   primType = 6
	primInt16    primType = 7
	primInt32    primType = 8
	primInt64    primType = 9
	primSByte    primType = 10
	primSingle   primType = 11
	primTimeSpan primType = 12
	primDateTime primType = 13
	primUInt16   primType = 14
	primUInt32   primType = 15
	primUInt64   primType = 16
)

// span is a range of bytes of the file.
type span struct{ start, end int }

// slotType is the type of a member of a class or of the items of an array.
type slotType struct {
	bin  binType
	prim primType
	// className is the name of the class of a SystemClass or Class slot.
	className string
}

// classMeta is the description of a class, which later ClassWithId records share.
type classMeta struct {
	name        string
	memberNames []string
	types       []slotType
	libraryID   int32
}

type objKind uint8

const (
	objClass objKind = iota
	objString
	objArray
)

// object is a record that other records can refer to by its id.
type object struct {
	id   int32
	kind objKind

	// a class
	meta    *classMeta
	members []memberVal

	// a string: the bytes of the text and the bytes of the length and the text
	text []byte
	str  span

	// an array
	elem  slotType
	rank  []int32 // the lengths of the dimensions
	lower []int32 // the lower bounds, when the array has them
	items []memberVal
	// raw holds the values of a primitive array, and rawOnly says that they are not read
	// into items (a byte array, or an array with very many items).
	raw     span
	rawOnly bool
	count   int
}

type valKind uint8

const (
	valNull valKind = iota
	valPrim
	valRef
	valObj
)

// memberVal is a value in a slot: a null, a primitive in the bytes of span, a reference to
// an object by id, or an object that is written there.
type memberVal struct {
	kind valKind
	prim primType
	span span
	ref  int32
	obj  *object
}

// Limits on what a file can ask the reader to hold; no real save comes near them.
const (
	maxItems     = 1 << 20
	maxPrimItems = 1 << 16
	maxMembers   = 1 << 16
	maxDepth     = 10000
	// maxTotalItems bounds the members and items of all the objects together: a run of nulls
	// is a few bytes of the file that stands for many of them.
	maxTotalItems = 1 << 22
)

// reader reads the records of a stream.
type reader struct {
	data    []byte
	pos     int
	depth   int
	objects map[int32]*object
	classes map[int32]*classMeta
	libs    map[int32]string
	// libOrder is the ids of the libraries in the order of the file.
	libOrder []int32
	// rootID is the id of the root object, from the header.
	rootID int32
	// items counts the members and items read so far.
	items int
}

// count adds n members or items to the count of the stream, and fails when there are too
// many.
func (r *reader) count(n int) error {
	r.items += n
	if r.items > maxTotalItems {
		return errors.New(tagTooLarge, "the stream has more members and items than can be read", "offset", r.pos)
	}
	return nil
}

func (r *reader) need(n int) error {
	if n < 0 || n > len(r.data)-r.pos {
		return errors.New(tagTruncated, "the stream ends in the middle of a record", "offset", r.pos, "need", n)
	}
	return nil
}

func (r *reader) u8() (byte, error) {
	if err := r.need(1); err != nil {
		return 0, err
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func (r *reader) i32() (int32, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	v := int32(binary.LittleEndian.Uint32(r.data[r.pos:]))
	r.pos += 4
	return v, nil
}

// lengthPrefixed reads a LengthPrefixedString: a length of 7 bits per byte, low bits
// first, and as many UTF-8 bytes. It returns the bytes of the text and the bytes of the
// length and the text together.
func (r *reader) lengthPrefixed() ([]byte, span, error) {
	start := r.pos
	var n uint32
	for i := range 5 {
		b, err := r.u8()
		if err != nil {
			return nil, span{}, err
		}
		if i == 4 && b > 0x07 {
			return nil, span{}, errors.New(tagValueInvalid, "the length of a string does not fit 31 bits", "offset", start)
		}
		n |= uint32(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			break
		}
		if i == 4 {
			return nil, span{}, errors.New(tagValueInvalid, "the length of a string has more than 5 bytes", "offset", start)
		}
	}
	if err := r.need(int(n)); err != nil {
		return nil, span{}, err
	}
	text := r.data[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return text, span{start, r.pos}, nil
}

// str reads a LengthPrefixedString that is a name.
func (r *reader) str() (string, error) {
	text, _, err := r.lengthPrefixed()
	return string(text), err
}

// primSize is the size in bytes of a primitive of fixed size, and 0 for the others.
func primSize(p primType) int {
	switch p {
	case primBoolean, primByte, primSByte:
		return 1
	case primInt16, primUInt16:
		return 2
	case primInt32, primUInt32, primSingle:
		return 4
	case primInt64, primUInt64, primDouble, primTimeSpan, primDateTime:
		return 8
	}
	return 0
}

// readPrim reads the bytes of a primitive value, which has no record type before it.
func (r *reader) readPrim(p primType) (span, error) {
	start := r.pos
	if n := primSize(p); n > 0 {
		if err := r.need(n); err != nil {
			return span{}, err
		}
		r.pos += n
		return span{start, r.pos}, nil
	}
	switch p {
	case primChar:
		b, err := r.u8()
		if err != nil {
			return span{}, err
		}
		n := 1
		switch {
		case b >= 0xF0:
			n = 4
		case b >= 0xE0:
			n = 3
		case b >= 0xC0:
			n = 2
		}
		if err := r.need(n - 1); err != nil {
			return span{}, err
		}
		r.pos += n - 1
		return span{start, r.pos}, nil
	case primDecimal:
		_, sp, err := r.lengthPrefixed()
		return sp, err
	}
	return span{}, errors.New(tagValueInvalid, "unknown primitive type", "offset", start, "type", byte(p))
}

// readPrimType reads and checks a PrimitiveTypeEnumeration.
func (r *reader) readPrimType() (primType, error) {
	b, err := r.u8()
	if err != nil {
		return 0, err
	}
	p := primType(b)
	if p < primBoolean || p > primUInt64 || p == 4 {
		return 0, errors.New(tagValueInvalid, "unknown primitive type", "offset", r.pos-1, "type", b)
	}
	return p, nil
}

// readSlotInfo reads the AdditionalInfo that goes with a BinaryTypeEnumeration.
func (r *reader) readSlotInfo(bin binType) (slotType, error) {
	st := slotType{bin: bin}
	switch bin {
	case binPrimitive, binPrimitiveArray:
		p, err := r.readPrimType()
		if err != nil {
			return st, err
		}
		st.prim = p
	case binSystemClass:
		name, err := r.str()
		if err != nil {
			return st, err
		}
		st.className = name
	case binClass:
		name, err := r.str()
		if err != nil {
			return st, err
		}
		if _, err := r.i32(); err != nil {
			return st, err
		}
		st.className = name
	case binString, binObject, binObjectArray, binStringArray:
	default:
		return st, errors.New(tagValueInvalid, "unknown binary type", "offset", r.pos, "type", byte(bin))
	}
	return st, nil
}

func (r *reader) register(o *object) error {
	if _, dup := r.objects[o.id]; dup {
		return errors.New(tagReference, "two records have the same object id", "id", o.id, "offset", r.pos)
	}
	r.objects[o.id] = o
	return nil
}

// readRecordValue reads the record of a member or an item. It handles the BinaryLibrary
// records that may come before it. A null record returns the number of nulls it stands for.
func (r *reader) readRecordValue() (mv memberVal, nulls int, err error) {
	for {
		at := r.pos
		b, err := r.u8()
		if err != nil {
			return mv, 0, err
		}
		switch rt := recordType(b); rt {
		case recLibrary:
			if err := r.readLibrary(); err != nil {
				return mv, 0, err
			}
		case recMemberReference:
			id, err := r.i32()
			if err != nil {
				return mv, 0, err
			}
			return memberVal{kind: valRef, ref: id}, 0, nil
		case recObjectNull:
			return memberVal{kind: valNull}, 1, nil
		case recObjectNullMultiple25:
			n, err := r.u8()
			if err != nil {
				return mv, 0, err
			}
			return memberVal{kind: valNull}, int(n), nil
		case recObjectNullMultiple:
			n, err := r.i32()
			if err != nil {
				return mv, 0, err
			}
			if n < 0 {
				return mv, 0, errors.New(tagValueInvalid, "a negative count of nulls", "offset", at)
			}
			return memberVal{kind: valNull}, int(n), nil
		case recPrimitiveTyped:
			p, err := r.readPrimType()
			if err != nil {
				return mv, 0, err
			}
			sp, err := r.readPrim(p)
			if err != nil {
				return mv, 0, err
			}
			return memberVal{kind: valPrim, prim: p, span: sp}, 0, nil
		case recObjectString, recClassWithID, recSystemClassTypes, recClassTypes, recBinaryArray,
			recArraySinglePrimitive, recArraySingleObject, recArraySingleString:
			o, err := r.readObject(rt)
			if err != nil {
				return mv, 0, err
			}
			return memberVal{kind: valObj, obj: o}, 0, nil
		case recSystemClassMembers, recClassMembers:
			return mv, 0, errors.New(tagUnsupported, "a class record without the types of its members cannot be read", "offset", at, "record", b)
		case recMethodCall, recMethodReturn:
			return mv, 0, errors.New(tagUnsupported, "method call and return records are not saves", "offset", at, "record", b)
		default:
			return mv, 0, errors.New(tagRecord, "a record that cannot be here", "offset", at, "record", b)
		}
	}
}

func (r *reader) readLibrary() error {
	id, err := r.i32()
	if err != nil {
		return err
	}
	name, err := r.str()
	if err != nil {
		return err
	}
	if _, dup := r.libs[id]; dup {
		return errors.New(tagReference, "two libraries have the same id", "id", id, "offset", r.pos)
	}
	r.libs[id] = name
	r.libOrder = append(r.libOrder, id)
	return nil
}

// readObject reads a record that defines an object, after its record type.
func (r *reader) readObject(rt recordType) (*object, error) {
	r.depth++
	defer func() { r.depth-- }()
	if r.depth > maxDepth {
		return nil, errors.New(tagTooLarge, "records are nested too deeply", "offset", r.pos)
	}
	switch rt {
	case recObjectString:
		id, err := r.i32()
		if err != nil {
			return nil, err
		}
		text, sp, err := r.lengthPrefixed()
		if err != nil {
			return nil, err
		}
		o := &object{id: id, kind: objString, text: text, str: sp}
		return o, r.register(o)
	case recClassWithID, recSystemClassTypes, recClassTypes:
		return r.readClass(rt)
	case recArraySinglePrimitive:
		return r.readArraySinglePrimitive()
	case recArraySingleObject, recArraySingleString:
		return r.readArraySingle(rt)
	default:
		return r.readBinaryArray()
	}
}

func (r *reader) readClass(rt recordType) (*object, error) {
	o := &object{kind: objClass}
	var err error
	if rt == recClassWithID {
		if o.id, err = r.i32(); err != nil {
			return nil, err
		}
		metaID, err := r.i32()
		if err != nil {
			return nil, err
		}
		meta, ok := r.classes[metaID]
		if !ok {
			return nil, errors.New(tagReference, "ClassWithId refers to a class that was not defined before", "id", o.id, "metadata", metaID)
		}
		o.meta = meta
	} else {
		meta := &classMeta{}
		if o.id, err = r.i32(); err != nil {
			return nil, err
		}
		if meta.name, err = r.str(); err != nil {
			return nil, err
		}
		count, err := r.i32()
		if err != nil {
			return nil, err
		}
		if count < 0 || count > maxMembers || int(count) > len(r.data)-r.pos {
			return nil, errors.New(tagValueInvalid, "the count of members is not valid", "class", meta.name, "count", count)
		}
		meta.memberNames = make([]string, count)
		for i := range meta.memberNames {
			if meta.memberNames[i], err = r.str(); err != nil {
				return nil, err
			}
		}
		meta.types = make([]slotType, count)
		bins := make([]binType, count)
		for i := range bins {
			b, err := r.u8()
			if err != nil {
				return nil, err
			}
			bins[i] = binType(b)
		}
		for i, bin := range bins {
			if meta.types[i], err = r.readSlotInfo(bin); err != nil {
				return nil, err
			}
		}
		if rt == recClassTypes {
			if meta.libraryID, err = r.i32(); err != nil {
				return nil, err
			}
		}
		o.meta = meta
		if _, dup := r.classes[o.id]; dup {
			return nil, errors.New(tagReference, "two classes have the same object id", "id", o.id)
		}
		r.classes[o.id] = meta
	}
	if err := r.register(o); err != nil {
		return nil, err
	}
	o.members, err = r.readSlots(len(o.meta.types), func(i int) slotType { return o.meta.types[i] })
	return o, err
}

func (r *reader) readArraySinglePrimitive() (*object, error) {
	o := &object{kind: objArray}
	var err error
	if o.id, err = r.i32(); err != nil {
		return nil, err
	}
	n, err := r.i32()
	if err != nil {
		return nil, err
	}
	p, err := r.readPrimType()
	if err != nil {
		return nil, err
	}
	if n < 0 {
		return nil, errors.New(tagValueInvalid, "an array with a negative length", "id", o.id)
	}
	o.elem = slotType{bin: binPrimitive, prim: p}
	o.rank = []int32{n}
	if err := r.register(o); err != nil {
		return nil, err
	}
	var kept bool
	o.items, o.raw, kept, err = r.readPrimItems(p, int(n))
	o.rawOnly, o.count = !kept, int(n)
	return o, err
}

// readPrimItems reads n primitives of one type. Many of them, or bytes, are not kept one by
// one: their bytes are returned as a span.
func (r *reader) readPrimItems(p primType, n int) (items []memberVal, sp span, kept bool, err error) {
	start := r.pos
	keep := n <= maxPrimItems && p != primByte
	if keep {
		if err := r.count(n); err != nil {
			return nil, span{}, false, err
		}
		items = make([]memberVal, 0, min(n, 1024))
	}
	if size := primSize(p); size > 0 {
		if n > (len(r.data)-r.pos)/size {
			return nil, span{}, false, errors.New(tagTruncated, "the stream ends in the middle of an array", "offset", r.pos, "items", n)
		}
		if keep {
			for range n {
				items = append(items, memberVal{kind: valPrim, prim: p, span: span{r.pos, r.pos + size}})
				r.pos += size
			}
		} else {
			r.pos += n * size
		}
		return items, span{start, r.pos}, keep, nil
	}
	if n > len(r.data)-r.pos {
		return nil, span{}, false, errors.New(tagTruncated, "the stream ends in the middle of an array", "offset", r.pos, "items", n)
	}
	for range n {
		sp, err := r.readPrim(p)
		if err != nil {
			return nil, span{}, false, err
		}
		if keep {
			items = append(items, memberVal{kind: valPrim, prim: p, span: sp})
		}
	}
	return items, span{start, r.pos}, keep, nil
}

func (r *reader) readArraySingle(rt recordType) (*object, error) {
	o := &object{kind: objArray, elem: slotType{bin: binObject}}
	if rt == recArraySingleString {
		o.elem = slotType{bin: binString}
	}
	var err error
	if o.id, err = r.i32(); err != nil {
		return nil, err
	}
	n, err := r.i32()
	if err != nil {
		return nil, err
	}
	if n < 0 || n > maxItems {
		return nil, errors.New(tagTooLarge, "an array of this length is not supported", "id", o.id, "length", n)
	}
	o.rank = []int32{n}
	if err := r.register(o); err != nil {
		return nil, err
	}
	o.items, err = r.readSlots(int(n), func(int) slotType { return o.elem })
	o.count = int(n)
	return o, err
}

func (r *reader) readBinaryArray() (*object, error) {
	o := &object{kind: objArray}
	var err error
	if o.id, err = r.i32(); err != nil {
		return nil, err
	}
	at, err := r.u8()
	if err != nil {
		return nil, err
	}
	if at > 5 {
		return nil, errors.New(tagValueInvalid, "unknown kind of array", "id", o.id, "kind", at)
	}
	rank, err := r.i32()
	if err != nil {
		return nil, err
	}
	if rank < 0 || int(rank) > len(r.data)-r.pos {
		return nil, errors.New(tagValueInvalid, "the rank of an array is not valid", "id", o.id, "rank", rank)
	}
	o.rank = make([]int32, rank)
	total := 1
	for i := range o.rank {
		if o.rank[i], err = r.i32(); err != nil {
			return nil, err
		}
		if o.rank[i] < 0 {
			return nil, errors.New(tagValueInvalid, "an array with a negative length", "id", o.id)
		}
		total *= int(o.rank[i])
		if total > maxItems {
			return nil, errors.New(tagTooLarge, "an array of this size is not supported", "id", o.id)
		}
	}
	if at >= 3 {
		o.lower = make([]int32, rank)
		for i := range o.lower {
			if o.lower[i], err = r.i32(); err != nil {
				return nil, err
			}
		}
	}
	b, err := r.u8()
	if err != nil {
		return nil, err
	}
	if o.elem, err = r.readSlotInfo(binType(b)); err != nil {
		return nil, err
	}
	if rank == 0 {
		total = 0
	}
	if err := r.register(o); err != nil {
		return nil, err
	}
	o.count = total
	if o.elem.bin == binPrimitive {
		var kept bool
		o.items, o.raw, kept, err = r.readPrimItems(o.elem.prim, total)
		o.rawOnly = !kept
		return o, err
	}
	o.items, err = r.readSlots(total, func(int) slotType { return o.elem })
	return o, err
}

// readSlots reads the values of n members or items, each of the type that typeAt gives.
func (r *reader) readSlots(n int, typeAt func(int) slotType) ([]memberVal, error) {
	if n > maxItems {
		return nil, errors.New(tagTooLarge, "too many members or items", "count", n)
	}
	if err := r.count(n); err != nil {
		return nil, err
	}
	out := make([]memberVal, 0, min(n, 1024))
	for len(out) < n {
		st := typeAt(len(out))
		if st.bin == binPrimitive {
			sp, err := r.readPrim(st.prim)
			if err != nil {
				return nil, err
			}
			out = append(out, memberVal{kind: valPrim, prim: st.prim, span: sp})
			continue
		}
		mv, nulls, err := r.readRecordValue()
		if err != nil {
			return nil, err
		}
		if mv.kind != valNull {
			out = append(out, mv)
			continue
		}
		if nulls > n-len(out) {
			return nil, errors.New(tagValueInvalid, "a run of nulls is longer than the members or items it fills", "offset", r.pos, "nulls", nulls)
		}
		for range nulls {
			out = append(out, mv)
		}
	}
	return out, nil
}

// parseStream reads the header and all the records.
func parseStream(data []byte) (*reader, int, error) {
	r := &reader{
		data: data, objects: map[int32]*object{}, classes: map[int32]*classMeta{}, libs: map[int32]string{},
	}
	rt, err := r.u8()
	if err != nil {
		return nil, 0, err
	}
	if recordType(rt) != recHeader {
		return nil, 0, errors.New(tagHeader, "the stream does not begin with a SerializedStreamHeader", "record", rt)
	}
	if r.rootID, err = r.i32(); err != nil {
		return nil, 0, err
	}
	if _, err = r.i32(); err != nil {
		return nil, 0, err
	}
	major, err := r.i32()
	if err != nil {
		return nil, 0, err
	}
	minor, err := r.i32()
	if err != nil {
		return nil, 0, err
	}
	if major != 1 || minor != 0 {
		return nil, 0, errors.New(tagHeader, "the version of the format is not 1.0", "major", major, "minor", minor)
	}
	for {
		at := r.pos
		b, err := r.u8()
		if err != nil {
			return nil, 0, err
		}
		switch recordType(b) {
		case recMessageEnd:
			return r, r.pos, nil
		case recLibrary:
			if err := r.readLibrary(); err != nil {
				return nil, 0, err
			}
		case recObjectString, recClassWithID, recSystemClassTypes, recClassTypes, recBinaryArray,
			recArraySinglePrimitive, recArraySingleObject, recArraySingleString:
			if _, err := r.readObject(recordType(b)); err != nil {
				return nil, 0, err
			}
		case recSystemClassMembers, recClassMembers:
			return nil, 0, errors.New(tagUnsupported, "a class record without the types of its members cannot be read", "offset", at, "record", b)
		case recMethodCall, recMethodReturn:
			return nil, 0, errors.New(tagUnsupported, "method call and return records are not saves", "offset", at, "record", b)
		default:
			return nil, 0, errors.New(tagRecord, "a record that cannot be at the top of the stream", "offset", at, "record", b)
		}
	}
}

// decodeChar reads the character of a Char value: one UTF-8 encoded character.
func decodeChar(b []byte) (rune, bool) {
	r, n := utf8.DecodeRune(b)
	if r == utf8.RuneError && n <= 1 || n != len(b) {
		return 0, false
	}
	return r, true
}

func f32(b []byte) float64 {
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
}

const (
	tagTruncated    = "NRBFTruncated"
	tagHeader       = "NRBFHeader"
	tagRecord       = "NRBFRecordInvalid"
	tagValueInvalid = "NRBFValueInvalid"
	tagUnsupported  = "NRBFUnsupported"
	tagReference    = "NRBFReference"
	tagTooLarge     = "NRBFTooLarge"
)
