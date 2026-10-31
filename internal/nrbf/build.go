package nrbf

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// siteRef is the data of the site of a primitive value or a string.
type siteRef struct {
	// kind is the kind of the value the site holds.
	kind model.Kind
	// prim is the primitive type; 0 is the text of a BinaryObjectString.
	prim primType
	// span is the bytes the new value replaces. A string includes its length.
	span span
}

// builder makes the model of the objects of a stream.
type builder struct {
	d     *Document
	r     *reader
	slots map[*object]*model.Slot
	// depth is how many objects are being made, one inside the other.
	depth int
}

func (d *Document) newSite(ref *siteRef, lo, hi *big.Int, check func(string) error) *model.Site {
	s := &model.Site{ID: len(d.sites), IntMin: lo, IntMax: hi, CheckText: check, Ref: ref}
	d.sites = append(d.sites, s)
	return s
}

// slot is the slot of a value found in a member or an item.
func (b *builder) slot(mv memberVal) (*model.Slot, error) {
	switch mv.kind {
	case valPrim:
		return b.primSlot(mv.prim, mv.span), nil
	case valObj:
		return b.object(mv.obj)
	case valRef:
		o, ok := b.r.objects[mv.ref]
		if !ok {
			return nil, errors.New(tagReference, "a reference to an object that is not in the stream", "id", mv.ref)
		}
		return b.object(o)
	}
	return &model.Slot{Value: &model.Value{Kind: model.KindNull}}, nil
}

// object is the slot of an object. An object that is reached from several places has one
// slot, so its value and its site are shared.
func (b *builder) object(o *object) (*model.Slot, error) {
	if s, ok := b.slots[o]; ok {
		return s, nil
	}
	b.depth++
	defer func() { b.depth-- }()
	if b.depth > maxDepth {
		return nil, errors.New(tagTooLarge, "objects are nested too deeply", "id", o.id)
	}
	v := &model.Value{}
	s := &model.Slot{Value: v}
	b.slots[o] = s
	switch o.kind {
	case objString:
		v.Type = "string"
		if !utf8.Valid(o.text) {
			v.Kind, v.Bytes = model.KindBytes, append([]byte(nil), o.text...)
			return s, nil
		}
		v.Kind, v.Str = model.KindString, string(o.text)
		s.Site = b.d.newSite(&siteRef{kind: model.KindString, span: o.str}, nil, nil, checkUTF8)
		return s, nil
	case objClass:
		return s, b.fillClass(o, v)
	}
	return s, b.fillArray(o, v)
}

// fillClass makes the value of an instance of a class.
func (b *builder) fillClass(o *object, v *model.Value) error {
	meta := o.meta
	v.Type, v.QualifiedType = shortTypeName(meta.name), meta.name
	if ok, err := b.collection(o, v); err != nil || ok {
		return err
	}
	v.Kind = model.KindObject
	for i, name := range meta.memberNames {
		slot, err := b.slot(o.members[i])
		if err != nil {
			return err
		}
		v.Children = append(v.Children, model.Child{Label: memberLabel(name), Slot: *slot})
	}
	return nil
}

// memberLabel is the name of a field as the game's code spells it: the field behind an
// automatic property "<Name>k__BackingField" is shown as Name.
func memberLabel(name string) string {
	if strings.HasPrefix(name, "<") && strings.HasSuffix(name, ">k__BackingField") {
		return name[1 : len(name)-len(">k__BackingField")]
	}
	return name
}

// typeBase is the name of a class without its generic arguments.
func typeBase(name string) string {
	if i := strings.IndexAny(name, "`["); i >= 0 {
		return name[:i]
	}
	return name
}

// collection makes the value of a class from the .NET collections that are shown by their
// items. It reports false, leaving the value alone, when the class is another one or does
// not have the members the collection has.
func (b *builder) collection(o *object, v *model.Value) (bool, error) {
	meta := o.meta
	member := func(name string) (memberVal, bool) {
		for i, n := range meta.memberNames {
			if n == name {
				return o.members[i], true
			}
		}
		return memberVal{}, false
	}
	var kind model.Kind
	var children []model.Child
	var err error
	switch typeBase(meta.name) {
	case "System.Collections.Generic.List", "System.Collections.ArrayList":
		items, ok1 := member("_items")
		size, ok2 := member("_size")
		if !ok1 || !ok2 || size.kind != valPrim || size.prim != primInt32 {
			return false, nil
		}
		kind = model.KindList
		n := int(int32(binary.LittleEndian.Uint32(b.d.data[size.span.start:])))
		if n < 0 {
			return false, nil
		}
		children, ok1, err = b.listItems(items, n)
		if err != nil || !ok1 {
			return false, err
		}
	case "System.Collections.Generic.HashSet":
		items, ok := member("Elements")
		if !ok {
			return false, nil
		}
		kind = model.KindList
		if children, ok, err = b.listItems(items, -1); err != nil || !ok {
			return false, err
		}
	case "System.Collections.Generic.Dictionary":
		pairs, ok := member("KeyValuePairs")
		if !ok {
			return false, nil
		}
		kind = model.KindMap
		if children, ok, err = b.dictionaryItems(pairs); err != nil || !ok {
			return false, err
		}
	case "System.Collections.Hashtable":
		keys, ok1 := member("Keys")
		values, ok2 := member("Values")
		if !ok1 || !ok2 {
			return false, nil
		}
		kind = model.KindMap
		var ok bool
		if children, ok, err = b.hashtableItems(keys, values); err != nil || !ok {
			return false, err
		}
	default:
		return false, nil
	}
	v.Kind, v.Children = kind, children
	return true, nil
}

// arrayOf is the array object that a member holds; nil for a null.
func (b *builder) arrayOf(mv memberVal) (*object, bool, error) {
	var o *object
	switch mv.kind {
	case valNull:
		return nil, true, nil
	case valObj:
		o = mv.obj
	case valRef:
		var ok bool
		if o, ok = b.r.objects[mv.ref]; !ok {
			return nil, false, errors.New(tagReference, "a reference to an object that is not in the stream", "id", mv.ref)
		}
	default:
		return nil, false, nil
	}
	return o, o.kind == objArray && !o.rawOnly, nil
}

// listItems are the first n items of an array that a collection holds, all of them when n is
// negative.
func (b *builder) listItems(mv memberVal, n int) ([]model.Child, bool, error) {
	arr, ok, err := b.arrayOf(mv)
	if err != nil || !ok {
		return nil, false, err
	}
	if arr == nil {
		return nil, true, nil
	}
	if n < 0 {
		n = len(arr.items)
	}
	if n > len(arr.items) {
		return nil, false, nil
	}
	kids, err := b.arrayChildren(arr, n)
	return kids, true, err
}

// arrayChildren are the first n items of the array, labeled by their index.
func (b *builder) arrayChildren(arr *object, n int) ([]model.Child, error) {
	n = min(n, len(arr.items))
	out := make([]model.Child, 0, n)
	for i := range n {
		slot, err := b.slot(arr.items[i])
		if err != nil {
			return nil, err
		}
		out = append(out, model.Child{Label: indexLabel(arr, i), Slot: *slot})
	}
	return out, nil
}

// indexLabel is the label of the i-th item of an array: "[3]", or "[1,2]" for a
// multi-dimensional array.
func indexLabel(arr *object, i int) string {
	lower := func(d int) int {
		if d < len(arr.lower) {
			return int(arr.lower[d])
		}
		return 0
	}
	if len(arr.rank) <= 1 {
		return "[" + strconv.Itoa(i+lower(0)) + "]"
	}
	idx := make([]string, len(arr.rank))
	for d := len(arr.rank) - 1; d >= 0; d-- {
		n := int(arr.rank[d])
		if n == 0 {
			n = 1
		}
		idx[d] = strconv.Itoa(i%n + lower(d))
		i /= n
	}
	return "[" + strings.Join(idx, ",") + "]"
}

// keyLabel is the label of an item of a dictionary: its key.
func keyLabel(v *model.Value) string {
	switch v.Kind {
	case model.KindString:
		return v.Str
	case model.KindInt:
		return v.Int.String()
	}
	return v.Repr()
}

// dictionaryItems are the pairs of a Dictionary: an array of KeyValuePair objects.
func (b *builder) dictionaryItems(mv memberVal) ([]model.Child, bool, error) {
	arr, ok, err := b.arrayOf(mv)
	if err != nil || !ok {
		return nil, false, err
	}
	if arr == nil {
		return nil, true, nil
	}
	var out []model.Child
	for _, item := range arr.items {
		if item.kind == valNull {
			continue
		}
		pair, ok, err := b.classOf(item)
		if err != nil || !ok {
			return nil, false, err
		}
		ki, vi := memberIndex(pair.meta, "key"), memberIndex(pair.meta, "value")
		if ki < 0 || vi < 0 {
			return nil, false, nil
		}
		key, err := b.slot(pair.members[ki])
		if err != nil {
			return nil, false, err
		}
		val, err := b.slot(pair.members[vi])
		if err != nil {
			return nil, false, err
		}
		out = append(out, model.Child{Label: keyLabel(key.Value), Slot: *val})
	}
	return out, true, nil
}

// hashtableItems are the entries of a Hashtable: an array of keys and one of values.
func (b *builder) hashtableItems(keys, values memberVal) ([]model.Child, bool, error) {
	ka, ok1, err := b.arrayOf(keys)
	if err != nil {
		return nil, false, err
	}
	va, ok2, err := b.arrayOf(values)
	if err != nil {
		return nil, false, err
	}
	if !ok1 || !ok2 {
		return nil, false, nil
	}
	if ka == nil || va == nil {
		return nil, ka == va, nil
	}
	if len(ka.items) != len(va.items) {
		return nil, false, nil
	}
	out := make([]model.Child, 0, len(ka.items))
	for i := range ka.items {
		key, err := b.slot(ka.items[i])
		if err != nil {
			return nil, false, err
		}
		val, err := b.slot(va.items[i])
		if err != nil {
			return nil, false, err
		}
		out = append(out, model.Child{Label: keyLabel(key.Value), Slot: *val})
	}
	return out, true, nil
}

// classOf is the class object that a member holds.
func (b *builder) classOf(mv memberVal) (*object, bool, error) {
	var o *object
	switch mv.kind {
	case valObj:
		o = mv.obj
	case valRef:
		var ok bool
		if o, ok = b.r.objects[mv.ref]; !ok {
			return nil, false, errors.New(tagReference, "a reference to an object that is not in the stream", "id", mv.ref)
		}
	default:
		return nil, false, nil
	}
	return o, o.kind == objClass, nil
}

func memberIndex(meta *classMeta, name string) int {
	for i, n := range meta.memberNames {
		if n == name {
			return i
		}
	}
	return -1
}

// fillArray makes the value of an array.
func (b *builder) fillArray(o *object, v *model.Value) error {
	v.Type = b.slotType(o)
	switch {
	case o.rawOnly && o.elem.prim == primByte:
		v.Kind, v.Bytes = model.KindBytes, append([]byte(nil), b.d.data[o.raw.start:o.raw.end]...)
	case o.rawOnly:
		v.Kind, v.Str = model.KindOpaque, fmt.Sprintf("%s with %d items", v.Type, o.count)
	default:
		v.Kind = model.KindList
		kids, err := b.arrayChildren(o, len(o.items))
		if err != nil {
			return err
		}
		v.Children = kids
	}
	return nil
}

// slotType is the name of the type of an object: "int[]", "string", "Address".
func (b *builder) slotType(o *object) string {
	switch o.kind {
	case objString:
		return "string"
	case objClass:
		return shortTypeName(o.meta.name)
	}
	name := elemTypeName(o.elem)
	if len(o.rank) > 1 {
		return name + "[" + strings.Repeat(",", len(o.rank)-1) + "]"
	}
	return name + "[]"
}

// elemTypeName is the name of the type of the items of an array.
func elemTypeName(st slotType) string {
	switch st.bin {
	case binPrimitive:
		return primName(st.prim)
	case binString:
		return "string"
	case binObject:
		return "object"
	case binSystemClass, binClass:
		return shortTypeName(st.className)
	case binObjectArray:
		return "object[]"
	case binStringArray:
		return "string[]"
	}
	return primName(st.prim) + "[]"
}

var primNames = map[primType]string{
	primBoolean: "bool", primByte: "byte", primChar: "char", primDecimal: "decimal", primDouble: "double",
	primInt16: "short", primInt32: "int", primInt64: "long", primSByte: "sbyte", primSingle: "float",
	primTimeSpan: "TimeSpan", primDateTime: "DateTime", primUInt16: "ushort", primUInt32: "uint", primUInt64: "ulong",
}

func primName(p primType) string { return primNames[p] }

// The system types that C# has a keyword for.
var keywordTypes = map[string]string{
	"System.Boolean": "bool", "System.Byte": "byte", "System.SByte": "sbyte", "System.Char": "char",
	"System.Decimal": "decimal", "System.Double": "double", "System.Single": "float", "System.Int16": "short",
	"System.Int32": "int", "System.Int64": "long", "System.UInt16": "ushort", "System.UInt32": "uint",
	"System.UInt64": "ulong", "System.String": "string", "System.Object": "object",
}

// shortTypeName is a class name as C# writes it without namespaces: "List<int>" for
// "System.Collections.Generic.List`1[[System.Int32, mscorlib, ...]]", "Address" for
// "Game.Models.Address", "Inner" for "Outer+Inner".
func shortTypeName(full string) string {
	name, suffix := full, ""
	for strings.HasSuffix(name, "]") {
		i := strings.LastIndexByte(name, '[')
		if i < 0 || strings.Trim(name[i+1:len(name)-1], ",") != "" {
			break
		}
		suffix = name[i:] + suffix
		name = name[:i]
	}
	if i := strings.IndexByte(name, '`'); i >= 0 {
		rest := name[i+1:]
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if args := splitTypeArgs(rest[j:]); len(args) > 0 {
			short := make([]string, len(args))
			for k, a := range args {
				short[k] = shortTypeName(a)
			}
			return simpleTypeName(name[:i]) + "<" + strings.Join(short, ", ") + ">" + suffix
		}
		name = name[:i]
	}
	return simpleTypeName(name) + suffix
}

func simpleTypeName(name string) string {
	if k, ok := keywordTypes[name]; ok {
		return k
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndexByte(name, '+'); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// splitTypeArgs splits "[[A, asm],[B, asm]]" into the type names "A" and "B"; nil when s is
// not that.
func splitTypeArgs(s string) []string {
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil
	}
	var args []string
	depth, start := 0, 0
	for i := 1; i < len(s)-1; i++ {
		switch s[i] {
		case '[':
			if depth == 0 {
				start = i + 1
			}
			depth++
		case ']':
			depth--
			if depth == 0 {
				arg := s[start:i]
				// The type name ends at the first comma that is outside brackets.
				d := 0
				for k := 0; k < len(arg); k++ {
					if arg[k] == '[' {
						d++
					} else if arg[k] == ']' {
						d--
					} else if arg[k] == ',' && d == 0 {
						arg = arg[:k]
						break
					}
				}
				args = append(args, arg)
			}
			if depth < 0 {
				return nil
			}
		}
	}
	if depth != 0 {
		return nil
	}
	return args
}

var (
	bigZero = big.NewInt(0)
	maxU8   = big.NewInt(math.MaxUint8)
	maxU16  = big.NewInt(math.MaxUint16)
	maxU32  = big.NewInt(math.MaxUint32)
	maxU64  = new(big.Int).SetUint64(math.MaxUint64)
	minI8   = big.NewInt(math.MinInt8)
	maxI8   = big.NewInt(math.MaxInt8)
	minI16  = big.NewInt(math.MinInt16)
	maxI16  = big.NewInt(math.MaxInt16)
	minI32  = big.NewInt(math.MinInt32)
	maxI32  = big.NewInt(math.MaxInt32)
	minI64  = big.NewInt(math.MinInt64)
	maxI64  = big.NewInt(math.MaxInt64)
)

// intTypes are the bounds of the whole-number primitives.
var intTypes = map[primType][2]*big.Int{
	primByte: {bigZero, maxU8}, primSByte: {minI8, maxI8}, primInt16: {minI16, maxI16}, primUInt16: {bigZero, maxU16},
	primInt32: {minI32, maxI32}, primUInt32: {bigZero, maxU32}, primInt64: {minI64, maxI64}, primUInt64: {bigZero, maxU64},
}

// intFrom reads a whole number of the type from its little-endian bytes.
func intFrom(p primType, raw []byte) *big.Int {
	switch p {
	case primByte:
		return big.NewInt(int64(raw[0]))
	case primSByte:
		return big.NewInt(int64(int8(raw[0])))
	case primInt16:
		return big.NewInt(int64(int16(binary.LittleEndian.Uint16(raw))))
	case primUInt16:
		return big.NewInt(int64(binary.LittleEndian.Uint16(raw)))
	case primInt32:
		return big.NewInt(int64(int32(binary.LittleEndian.Uint32(raw))))
	case primUInt32:
		return big.NewInt(int64(binary.LittleEndian.Uint32(raw)))
	case primInt64:
		return big.NewInt(int64(binary.LittleEndian.Uint64(raw)))
	}
	return new(big.Int).SetUint64(binary.LittleEndian.Uint64(raw))
}

// primSlot is the slot of a primitive value in the bytes of sp.
func (b *builder) primSlot(p primType, sp span) *model.Slot {
	raw := b.d.data[sp.start:sp.end]
	v := &model.Value{Type: primName(p)}
	s := &model.Slot{Value: v}
	site := func(kind model.Kind, lo, hi *big.Int, check func(string) error) {
		s.Site = b.d.newSite(&siteRef{kind: kind, prim: p, span: sp}, lo, hi, check)
	}
	switch p {
	case primBoolean:
		v.Kind, v.Bool = model.KindBool, raw[0] != 0
		site(model.KindBool, nil, nil, nil)
	case primByte, primSByte, primInt16, primUInt16, primInt32, primUInt32, primInt64, primUInt64:
		v.Kind, v.Int = model.KindInt, intFrom(p, raw)
		site(model.KindInt, intTypes[p][0], intTypes[p][1], nil)
	case primSingle:
		v.Kind, v.Float, v.Float32 = model.KindFloat, f32(raw), true
		site(model.KindFloat, nil, nil, nil)
	case primDouble:
		v.Kind, v.Float = model.KindFloat, math.Float64frombits(binary.LittleEndian.Uint64(raw))
		site(model.KindFloat, nil, nil, nil)
	case primChar:
		r, ok := decodeChar(raw)
		if !ok {
			v.Kind, v.Str = model.KindOpaque, "invalid char"
			break
		}
		v.Kind, v.Str = model.KindString, string(r)
		site(model.KindString, nil, nil, checkChar)
	case primDecimal:
		text, _, err := (&reader{data: raw}).lengthPrefixed()
		if err != nil || !utf8.Valid(text) {
			v.Kind, v.Str = model.KindOpaque, "invalid decimal"
			break
		}
		v.Kind, v.Str = model.KindString, string(text)
		site(model.KindString, nil, nil, checkDecimal)
	case primDateTime:
		v.Kind, v.Str = model.KindOpaque, dateTimeText(binary.LittleEndian.Uint64(raw))
	case primTimeSpan:
		v.Kind, v.Str = model.KindOpaque, timeSpanText(int64(binary.LittleEndian.Uint64(raw)))
	}
	return s
}

const ticksPerDay = 864_000_000_000

// dateTimeText is a System.DateTime: 62 bits of ticks (100 ns) since 0001-01-01, and 2 bits
// that say whether it is UTC (1) or local time (2).
func dateTimeText(v uint64) string {
	ticks := v & (1<<62 - 1)
	t := time.Date(1, 1, 1+int(ticks/ticksPerDay), 0, 0, 0, 0, time.UTC).Add(time.Duration(ticks%ticksPerDay) * 100)
	s := t.Format("2006-01-02T15:04:05.0000000")
	switch v >> 62 {
	case 1:
		return s + "Z"
	case 2:
		return s + " (local)"
	}
	return s
}

// timeSpanText is a System.TimeSpan, a count of ticks: [-][d.]hh:mm:ss[.fffffff].
func timeSpanText(ticks int64) string {
	neg := ticks < 0
	u := uint64(ticks)
	if neg {
		u = -u
	}
	days := u / ticksPerDay
	u %= ticksPerDay
	s := fmt.Sprintf("%02d:%02d:%02d", u/36_000_000_000, u/600_000_000%60, u/10_000_000%60)
	if days > 0 {
		s = strconv.FormatUint(days, 10) + "." + s
	}
	if f := u % 10_000_000; f != 0 {
		s += fmt.Sprintf(".%07d", f)
	}
	if neg {
		s = "-" + s
	}
	return s
}

// checkChar is the CheckText of a Char: one character of the Basic Multilingual Plane, as a
// .NET char is one UTF-16 unit.
func checkChar(s string) error {
	if utf8.RuneCountInString(s) != 1 || !utf8.ValidString(s) {
		return errors.New(tagEditText, "Enter exactly one character")
	}
	if r, _ := utf8.DecodeRuneInString(s); r > 0xFFFF {
		return errors.New(tagEditText, "Enter a character of the Basic Multilingual Plane (not an emoji)")
	}
	return nil
}

// checkUTF8 is the CheckText of a string.
func checkUTF8(s string) error {
	if !utf8.ValidString(s) {
		return errors.New(tagEditText, "The text is not valid UTF-8")
	}
	return nil
}

var maxDecimal, _ = new(big.Int).SetString("79228162514264337593543950335", 10)

// checkDecimal is the CheckText of a Decimal, which is a number written as text, such as
// -12.50: digits, then a point and digits, within the range of System.Decimal (28 digits
// after the point at most, and 79228162514264337593543950335 as the largest absolute value
// with the point taken away).
func checkDecimal(s string) error {
	t := strings.TrimPrefix(s, "-")
	whole, frac, hasFrac := strings.Cut(t, ".")
	digits := func(x string) bool {
		for i := 0; i < len(x); i++ {
			if x[i] < '0' || x[i] > '9' {
				return false
			}
		}
		return x != ""
	}
	if !digits(whole) || (hasFrac && !digits(frac)) {
		return errors.New(tagEditText, "Enter a decimal number such as -12.50")
	}
	if len(frac) > 28 {
		return errors.New(tagEditText, "A decimal has at most 28 digits after the point")
	}
	n, _ := new(big.Int).SetString(whole+frac, 10)
	if n.Cmp(maxDecimal) > 0 {
		return errors.New(tagEditText, "The number is too large for a decimal (79228162514264337593543950335)")
	}
	return nil
}

// lengthPrefixedBytes is a string as the stream writes it: its length in bytes, 7 bits at a
// time, then the bytes.
func lengthPrefixedBytes(s string) []byte {
	out := make([]byte, 0, len(s)+5)
	n := uint32(len(s))
	for n >= 0x80 {
		out = append(out, byte(n)|0x80)
		n >>= 7
	}
	out = append(out, byte(n))
	return append(out, s...)
}

// encode is the bytes that replace the old value at the site.
func (ref *siteRef) encode(e model.Edit) ([]byte, error) {
	v := e.Value
	switch ref.prim {
	case 0:
		if !utf8.ValidString(v.Str) {
			return nil, errors.New(tagEditText, "The text is not valid UTF-8", "site", e.Site.ID)
		}
		return lengthPrefixedBytes(v.Str), nil
	case primBoolean:
		if v.Bool {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	case primChar:
		if err := checkChar(v.Str); err != nil {
			return nil, err
		}
		return []byte(v.Str), nil
	case primDecimal:
		if err := checkDecimal(v.Str); err != nil {
			return nil, err
		}
		return lengthPrefixedBytes(v.Str), nil
	case primSingle:
		if math.Abs(v.Float) > math.MaxFloat32 && !math.IsInf(v.Float, 0) {
			return nil, errors.New(tagEditRange, "The number does not fit a 32-bit float", "site", e.Site.ID)
		}
		return binary.LittleEndian.AppendUint32(nil, math.Float32bits(float32(v.Float))), nil
	case primDouble:
		return binary.LittleEndian.AppendUint64(nil, math.Float64bits(v.Float)), nil
	}
	lo, hi := e.Site.IntMin, e.Site.IntMax
	if v.Int == nil || v.Int.Cmp(lo) < 0 || v.Int.Cmp(hi) > 0 {
		return nil, errors.New(tagEditRange, "The number is out of range", "site", e.Site.ID, "min", lo.String(), "max", hi.String())
	}
	var u uint64
	if v.Int.Sign() < 0 {
		u = uint64(v.Int.Int64())
	} else {
		u = v.Int.Uint64()
	}
	return binary.LittleEndian.AppendUint64(nil, u)[:primSize(ref.prim)], nil
}
