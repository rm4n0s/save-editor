package lcf

// A save file is a header (a BER number, 11, and "LcfSaveData") and then the chunks of the Save
// struct, up to the end of the file. A chunk is a BER id, a BER size and size bytes of data. The
// data of a struct is chunks again, ended by a chunk id 0 that has no size. The data of an array
// of structs is a BER count and then, for each element, a BER number if the struct is numbered
// and its chunks. The layout is the one that liblcf's reader (src/reader_lcf.cpp,
// src/reader_struct_impl.h) reads.

// chunk is a chunk of the file, with the chunks it holds.
type chunk struct {
	id int
	// f is the field the id means in its struct; nil for an unknown chunk.
	f *field
	// typ is how the data was read: ftBytes when the id is unknown or the data does not have
	// the shape the field needs.
	typ fieldType
	// The chunk is data[start:end]: the id, the size at sizeStart, and the data at dataStart.
	start, sizeStart, dataStart, end int
	parent                           *chunk

	def   *structDef // the struct of an ftStruct, and of the elements of an ftArray
	kids  []*chunk   // the chunks of an ftStruct
	elems []*element // the elements of an ftArray
}

// element is an element of an array of structs.
type element struct {
	id   int // the number the file gives it; 0 when the struct has none
	kids []*chunk
}

// inner is the chunks inside the data of c, in file order.
func (c *chunk) inner() []*chunk {
	if c.typ != ftArray {
		return c.kids
	}
	var all []*chunk
	for _, e := range c.elems {
		all = append(all, e.kids...)
	}
	return all
}

// parser reads the chunks of a file.
type parser struct {
	data []byte
}

type status uint8

const (
	// stEnd: the chunks ended with an id 0.
	stEnd status = iota
	// stLimit: the chunks ended where the data did.
	stLimit
	// stBad: a chunk does not fit in the data.
	stBad
)

// readBER reads a BER number of at most 5 bytes in data[pos:limit]: groups of 7 bits, the first
// group first, every byte but the last with its high bit set. As in liblcf, the number is
// 32 bits wide and a negative number is written as its 32-bit unsigned value.
func readBER(data []byte, pos, limit int) (v uint32, n int, ok bool) {
	for n < 5 {
		if pos+n >= limit {
			return 0, 0, false
		}
		b := data[pos+n]
		n++
		v = v<<7 | uint32(b&0x7F)
		if b&0x80 == 0 {
			return v, n, true
		}
	}
	return 0, 0, false
}

// appendBER appends the shortest BER form of v.
func appendBER(dst []byte, v uint32) []byte {
	for shift := 28; shift > 0; shift -= 7 {
		if v >= 1<<uint(shift) {
			dst = append(dst, byte(v>>uint(shift))&0x7F|0x80)
		}
	}
	return append(dst, byte(v)&0x7F)
}

// chunks reads the chunks of def in data[pos:limit]. It returns what it read, where it stopped
// and why. On stBad the chunks before the bad one are returned.
func (p *parser) chunks(def *structDef, pos, limit int, parent *chunk) (kids []*chunk, next int, st status) {
	for {
		if pos >= limit {
			return kids, pos, stLimit
		}
		id, n, ok := readBER(p.data, pos, limit)
		if !ok {
			return kids, pos, stBad
		}
		if id == 0 {
			return kids, pos + n, stEnd
		}
		sizeStart := pos + n
		size, m, ok := readBER(p.data, sizeStart, limit)
		if !ok {
			return kids, pos, stBad
		}
		dataStart := sizeStart + m
		if uint64(size) > uint64(limit-dataStart) {
			return kids, pos, stBad
		}
		c := &chunk{
			id: int(id), typ: ftBytes, start: pos, sizeStart: sizeStart,
			dataStart: dataStart, end: dataStart + int(size), parent: parent,
		}
		c.f = def.byID[c.id]
		if c.f != nil {
			p.interpret(c)
		}
		kids = append(kids, c)
		pos = c.end
	}
}

// interpret decides how the data of a chunk with a known field is read. A chunk whose data does
// not have the shape its field needs stays ftBytes.
func (p *parser) interpret(c *chunk) {
	f := c.f
	size := c.end - c.dataStart
	data := p.data[c.dataStart:c.end]
	set := func(ok bool) {
		if ok {
			c.typ = f.typ
		}
	}
	switch f.typ {
	case ftInt32:
		if size == 0 {
			set(true)
			break
		}
		_, n, ok := readBER(p.data, c.dataStart, c.end)
		set(ok && n == size)
	case ftBool:
		set(size == 1 && data[0] < 0x80)
	case ftString:
		set(true)
	case ftDouble:
		set(size == 8)
	case ftInt8, ftUInt8, ftFlags:
		set(size == 1)
	case ftUInt32:
		set(size == 4)
	case ftVecBool, ftVecUInt8:
		set(true)
	case ftVecInt16:
		set(size%2 == 0)
	case ftVecInt32:
		set(size%4 == 0)
	case ftCount:
		set(true)
	case ftStruct:
		def := schema[f.sub]
		kids, _, st := p.chunks(def, c.dataStart, c.end, c)
		if st != stBad {
			c.typ, c.def, c.kids = ftStruct, def, kids
		}
	case ftArray:
		p.array(c, schema[f.sub])
	case ftBytes:
	}
}

// array reads the elements of an array chunk.
func (p *parser) array(c *chunk, def *structDef) {
	count, n, ok := readBER(p.data, c.dataStart, c.end)
	if !ok {
		return
	}
	pos := c.dataStart + n
	var elems []*element
	for i := uint32(0); i < count; i++ {
		e := &element{}
		if def.hasID {
			id, m, ok := readBER(p.data, pos, c.end)
			if !ok {
				return
			}
			e.id, pos = int(id), pos+m
		}
		kids, next, st := p.chunks(def, pos, c.end, c)
		if st != stEnd {
			return
		}
		e.kids, pos = kids, next
		elems = append(elems, e)
	}
	c.typ, c.def, c.elems = ftArray, def, elems
}

// walk calls fn for c and every chunk inside it.
func (c *chunk) walk(fn func(*chunk)) {
	fn(c)
	for _, k := range c.inner() {
		k.walk(fn)
	}
}
