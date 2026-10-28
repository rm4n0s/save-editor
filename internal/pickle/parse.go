package pickle

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
)

// Document is a parsed pickle: the bytes it was read from, and the value they make.
type Document struct {
	// Data are the bytes of the pickle, never changed.
	Data []byte
	// Protocol is the protocol of the PROTO opcode, or 0 when there is none.
	Protocol int
	// Root is the value the pickle returns.
	Root Slot
	// End is the offset just after the STOP opcode. Bytes after it are kept as they are.
	End int

	frames [][2]int
}

// Parse reads the pickle in data. It understands every opcode of protocols 0 to 5 except
// the out-of-band buffers of protocol 5, which a pickle written to a file never has.
func Parse(data []byte) (*Document, error) {
	p := &parser{data: data, memo: map[int]*Value{}}
	if err := p.run(); err != nil {
		return nil, err
	}
	return &Document{Data: data, Protocol: p.protocol, Root: p.root, End: p.pos, frames: p.frames}, nil
}

type parser struct {
	data     []byte
	pos      int
	op       int // the offset of the opcode being read
	stack    []Slot
	marks    []int
	memo     map[int]*Value
	protocol int
	frames   [][2]int
	root     Slot
}

func (p *parser) run() error {
	for {
		if p.pos >= len(p.data) {
			return errors.New(tagPickleNoStop, "the pickle ends without a STOP opcode", "offset", p.pos)
		}
		p.op = p.pos
		code := p.data[p.pos]
		p.pos++
		if code == opStop {
			root, err := p.pop()
			if err != nil {
				return err
			}
			p.root = root
			return nil
		}
		if err := p.step(code); err != nil {
			return err
		}
	}
}

// step runs one opcode whose code byte has been read.
func (p *parser) step(code byte) error {
	switch code {
	case opProto:
		b, err := p.read(1)
		if err != nil {
			return err
		}
		p.protocol = int(b[0])
	case opFrame:
		if _, err := p.read(8); err != nil {
			return err
		}
		p.frames = append(p.frames, [2]int{p.op, p.pos})
	case opMark:
		p.marks = append(p.marks, len(p.stack))
	case opPop:
		if len(p.stack) == 0 {
			_, err := p.popMark()
			return err
		}
		_, err := p.pop()
		return err
	case opPopMark:
		_, err := p.popMark()
		return err
	case opDup:
		top, err := p.top()
		if err != nil {
			return err
		}
		p.stack = append(p.stack, Slot{Value: top.Value, Site: Site{Kind: SiteNone, Start: p.op, End: p.pos, MemoEnd: p.pos}})

	case opNone:
		p.pushLiteral(&Value{Kind: KindNone})
	case opTrue, opFalse:
		p.pushLiteral(&Value{Kind: KindBool, Bool: code == opTrue})
	case opInt:
		line, err := p.readLine()
		if err != nil {
			return err
		}
		switch line {
		case "00", "01":
			p.pushLiteral(&Value{Kind: KindBool, Bool: line == "01"})
			return nil
		}
		n, ok := new(big.Int).SetString(line, 10)
		if !ok {
			return errors.New(tagPickleNumberInvalid, "an INT opcode has no number", "offset", p.op, "text", line)
		}
		p.pushLiteral(&Value{Kind: KindInt, Int: n})
	case opLong:
		line, err := p.readLine()
		if err != nil {
			return err
		}
		n, ok := new(big.Int).SetString(strings.TrimSuffix(line, "L"), 10)
		if !ok {
			return errors.New(tagPickleNumberInvalid, "a LONG opcode has no number", "offset", p.op, "text", line)
		}
		p.pushLiteral(&Value{Kind: KindInt, Int: n})
	case opBinInt:
		b, err := p.read(4)
		if err != nil {
			return err
		}
		p.pushLiteral(&Value{Kind: KindInt, Int: big.NewInt(int64(int32(binary.LittleEndian.Uint32(b))))})
	case opBinInt1:
		b, err := p.read(1)
		if err != nil {
			return err
		}
		p.pushLiteral(&Value{Kind: KindInt, Int: big.NewInt(int64(b[0]))})
	case opBinInt2:
		b, err := p.read(2)
		if err != nil {
			return err
		}
		p.pushLiteral(&Value{Kind: KindInt, Int: big.NewInt(int64(binary.LittleEndian.Uint16(b)))})
	case opLong1, opLong4:
		var n int
		if code == opLong1 {
			b, err := p.read(1)
			if err != nil {
				return err
			}
			n = int(b[0])
		} else {
			b, err := p.read(4)
			if err != nil {
				return err
			}
			n = int(int32(binary.LittleEndian.Uint32(b)))
		}
		b, err := p.read(n)
		if err != nil {
			return err
		}
		p.pushLiteral(&Value{Kind: KindInt, Int: decodeLong(b)})
	case opFloat:
		line, err := p.readLine()
		if err != nil {
			return err
		}
		f, err := strconv.ParseFloat(line, 64)
		if err != nil {
			return errors.NewErr(tagPickleNumberInvalid, err).SetMetadata("offset", p.op, "text", line)
		}
		p.pushLiteral(&Value{Kind: KindFloat, Float: f})
	case opBinFloat:
		b, err := p.read(8)
		if err != nil {
			return err
		}
		p.pushLiteral(&Value{Kind: KindFloat, Float: math.Float64frombits(binary.BigEndian.Uint64(b))})

	case opString:
		line, err := p.readLine()
		if err != nil {
			return err
		}
		if len(line) < 2 || line[0] != line[len(line)-1] || (line[0] != '\'' && line[0] != '"') {
			return errors.New(tagPickleStringInvalid, "a STRING opcode is not quoted", "offset", p.op)
		}
		p.pushLiteral(&Value{Kind: KindByteString, Str: decodeEscapes(line[1 : len(line)-1])})
	case opBinString, opShortBinString:
		size := 4
		if code == opShortBinString {
			size = 1
		}
		b, err := p.readSized(size, true)
		if err != nil {
			return err
		}
		p.pushLiteral(&Value{Kind: KindByteString, Str: string(b)})
	case opUnicode:
		line, err := p.readLine()
		if err != nil {
			return err
		}
		p.pushLiteral(&Value{Kind: KindString, Str: decodeRawUnicodeEscape(line)})
	case opBinUnicode, opShortBinUnicode, opBinUnicode8:
		b, err := p.readSized(lengthSize(code), false)
		if err != nil {
			return err
		}
		p.pushLiteral(&Value{Kind: KindString, Str: string(b)})
	case opBinBytes, opShortBinBytes, opBinBytes8, opByteArray8:
		b, err := p.readSized(lengthSize(code), false)
		if err != nil {
			return err
		}
		p.pushLiteral(&Value{Kind: KindBytes, Bytes: append([]byte(nil), b...)})

	case opEmptyList:
		p.pushBuilt(&Value{Kind: KindList})
	case opEmptyDict:
		p.pushBuilt(&Value{Kind: KindDict})
	case opEmptyTuple:
		p.pushBuilt(&Value{Kind: KindTuple})
	case opEmptySet:
		p.pushBuilt(&Value{Kind: KindSet})
	case opList, opTuple, opFrozenSet:
		items, err := p.popMark()
		if err != nil {
			return err
		}
		kind := map[byte]Kind{opList: KindList, opTuple: KindTuple, opFrozenSet: KindFrozenSet}[code]
		p.pushBuilt(&Value{Kind: kind, Items: items})
	case opTuple1, opTuple2, opTuple3:
		n := int(code-opTuple1) + 1
		if len(p.stack)-n < p.markFloor() {
			return errors.New(tagPickleStackUnderflow, "a TUPLE opcode needs more values than the stack has", "offset", p.op)
		}
		items := append([]Slot(nil), p.stack[len(p.stack)-n:]...)
		p.stack = p.stack[:len(p.stack)-n]
		p.pushBuilt(&Value{Kind: KindTuple, Items: items})
	case opDict:
		items, err := p.popMark()
		if err != nil {
			return err
		}
		d := &Value{Kind: KindDict}
		if err := p.addEntries(d, items); err != nil {
			return err
		}
		p.pushBuilt(d)
	case opAppend, opAppends:
		var items []Slot
		if code == opAppend {
			v, err := p.pop()
			if err != nil {
				return err
			}
			items = []Slot{v}
		} else {
			var err error
			if items, err = p.popMark(); err != nil {
				return err
			}
		}
		target, err := p.top()
		if err != nil {
			return err
		}
		if target.Value.Kind != KindList && target.Value.Kind != KindObject {
			return errors.New(tagPickleTargetInvalid, "APPENDS on a value that is not a list", "offset", p.op,
				"kind", target.Value.Kind.String())
		}
		target.Value.Items = append(target.Value.Items, items...)
	case opSetItem, opSetItems:
		var items []Slot
		if code == opSetItem {
			if len(p.stack)-2 < p.markFloor() {
				return errors.New(tagPickleStackUnderflow, "SETITEM needs a key and a value", "offset", p.op)
			}
			items = append([]Slot(nil), p.stack[len(p.stack)-2:]...)
			p.stack = p.stack[:len(p.stack)-2]
		} else {
			var err error
			if items, err = p.popMark(); err != nil {
				return err
			}
		}
		target, err := p.top()
		if err != nil {
			return err
		}
		if err := p.addEntries(target.Value, items); err != nil {
			return err
		}
	case opAddItems:
		items, err := p.popMark()
		if err != nil {
			return err
		}
		target, err := p.top()
		if err != nil {
			return err
		}
		if target.Value.Kind != KindSet {
			return errors.New(tagPickleTargetInvalid, "ADDITEMS on a value that is not a set", "offset", p.op,
				"kind", target.Value.Kind.String())
		}
		target.Value.Items = append(target.Value.Items, items...)

	case opGlobal:
		module, err := p.readLine()
		if err != nil {
			return err
		}
		name, err := p.readLine()
		if err != nil {
			return err
		}
		p.pushBuilt(&Value{Kind: KindGlobal, Module: module, Name: name})
	case opStackGlobal:
		name, err := p.pop()
		if err != nil {
			return err
		}
		module, err := p.pop()
		if err != nil {
			return err
		}
		if !isText(module.Value) || !isText(name.Value) {
			return errors.New(tagPickleGlobalInvalid, "STACK_GLOBAL needs two strings", "offset", p.op)
		}
		p.pushBuilt(&Value{Kind: KindGlobal, Module: module.Value.Str, Name: name.Value.Str})
	case opReduce, opNewObj:
		args, err := p.pop()
		if err != nil {
			return err
		}
		callable, err := p.pop()
		if err != nil {
			return err
		}
		p.pushBuilt(&Value{Kind: KindObject, Callable: callable.Value, Args: args.Value})
	case opNewObjEx:
		kwargs, err := p.pop()
		if err != nil {
			return err
		}
		args, err := p.pop()
		if err != nil {
			return err
		}
		cls, err := p.pop()
		if err != nil {
			return err
		}
		p.pushBuilt(&Value{Kind: KindObject, Callable: cls.Value, Args: args.Value, Kwargs: kwargs.Value})
	case opInst:
		module, err := p.readLine()
		if err != nil {
			return err
		}
		name, err := p.readLine()
		if err != nil {
			return err
		}
		items, err := p.popMark()
		if err != nil {
			return err
		}
		p.pushBuilt(&Value{Kind: KindObject, Callable: &Value{Kind: KindGlobal, Module: module, Name: name},
			Args: &Value{Kind: KindTuple, Items: items}})
	case opObj:
		items, err := p.popMark()
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.New(tagPickleStackUnderflow, "OBJ needs a class", "offset", p.op)
		}
		p.pushBuilt(&Value{Kind: KindObject, Callable: items[0].Value, Args: &Value{Kind: KindTuple, Items: items[1:]}})
	case opBuild:
		state, err := p.pop()
		if err != nil {
			return err
		}
		target, err := p.top()
		if err != nil {
			return err
		}
		target.Value.State = state.Value

	case opPersID:
		line, err := p.readLine()
		if err != nil {
			return err
		}
		p.pushBuilt(&Value{Kind: KindOpaque, Str: "persistent id " + line})
	case opBinPersID:
		if _, err := p.pop(); err != nil {
			return err
		}
		p.pushBuilt(&Value{Kind: KindOpaque, Str: "persistent id"})
	case opExt1, opExt2, opExt4:
		if _, err := p.read(map[byte]int{opExt1: 1, opExt2: 2, opExt4: 4}[code]); err != nil {
			return err
		}
		p.pushBuilt(&Value{Kind: KindOpaque, Str: "extension"})

	case opPut, opBinPut, opLongBinPut, opMemoize:
		var index int
		switch code {
		case opPut:
			line, err := p.readLine()
			if err != nil {
				return err
			}
			if index, err = strconv.Atoi(line); err != nil {
				return errors.NewErr(tagPickleMemoInvalid, err).SetMetadata("offset", p.op)
			}
		case opBinPut:
			b, err := p.read(1)
			if err != nil {
				return err
			}
			index = int(b[0])
		case opLongBinPut:
			b, err := p.read(4)
			if err != nil {
				return err
			}
			index = int(binary.LittleEndian.Uint32(b))
		default:
			index = len(p.memo)
		}
		if len(p.stack) == 0 {
			return errors.New(tagPickleStackUnderflow, "a memo opcode on an empty stack", "offset", p.op)
		}
		top := &p.stack[len(p.stack)-1]
		p.memo[index] = top.Value
		// The memo opcode belongs to the literal right before it, or before the FRAME
		// that Python's framer writes after a large string.
		follows := top.Site.MemoEnd == p.op
		if n := len(p.frames); n > 0 && p.frames[n-1][0] == top.Site.MemoEnd && p.frames[n-1][1] == p.op {
			follows = true
		}
		if top.Site.Kind == SiteLiteral && top.Site.MemoEnd == top.Site.End && follows {
			top.Site.MemoEnd = p.pos
		}
	case opGet, opBinGet, opLongBinGet:
		var index int
		switch code {
		case opGet:
			line, err := p.readLine()
			if err != nil {
				return err
			}
			if index, err = strconv.Atoi(line); err != nil {
				return errors.NewErr(tagPickleMemoInvalid, err).SetMetadata("offset", p.op)
			}
		case opBinGet:
			b, err := p.read(1)
			if err != nil {
				return err
			}
			index = int(b[0])
		default:
			b, err := p.read(4)
			if err != nil {
				return err
			}
			index = int(binary.LittleEndian.Uint32(b))
		}
		v, ok := p.memo[index]
		if !ok {
			return errors.New(tagPickleMemoInvalid, "a GET of a memo entry that was never set", "offset", p.op, "index", index)
		}
		p.stack = append(p.stack, Slot{Value: v, Site: Site{Kind: SiteMemo, Start: p.op, End: p.pos, MemoEnd: p.pos}})
	case opReadonlyBuffer:
		if _, err := p.top(); err != nil {
			return err
		}
	default:
		return errors.New(tagPickleOpcodeUnknown, "an opcode this reader does not know", "offset", p.op, "opcode", int(code))
	}
	return nil
}

func (p *parser) pushLiteral(v *Value) {
	p.stack = append(p.stack, Slot{Value: v, Site: Site{Kind: SiteLiteral, Start: p.op, End: p.pos, MemoEnd: p.pos}})
}

func (p *parser) pushBuilt(v *Value) {
	p.stack = append(p.stack, Slot{Value: v, Site: Site{Kind: SiteBuilt, Start: p.op, End: p.pos, MemoEnd: p.pos}})
}

// markFloor is the lowest index of the stack that the opcodes after the last MARK may use.
func (p *parser) markFloor() int {
	if len(p.marks) == 0 {
		return 0
	}
	return p.marks[len(p.marks)-1]
}

func (p *parser) pop() (Slot, error) {
	if len(p.stack) <= p.markFloor() {
		return Slot{}, errors.New(tagPickleStackUnderflow, "an opcode pops a value from an empty stack", "offset", p.op)
	}
	s := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	return s, nil
}

func (p *parser) top() (Slot, error) {
	if len(p.stack) == 0 {
		return Slot{}, errors.New(tagPickleStackUnderflow, "an opcode needs a value on an empty stack", "offset", p.op)
	}
	return p.stack[len(p.stack)-1], nil
}

// popMark removes the values above the last MARK, and the MARK.
func (p *parser) popMark() ([]Slot, error) {
	if len(p.marks) == 0 {
		return nil, errors.New(tagPickleMarkMissing, "an opcode needs a MARK and there is none", "offset", p.op)
	}
	at := p.marks[len(p.marks)-1]
	p.marks = p.marks[:len(p.marks)-1]
	items := append([]Slot(nil), p.stack[at:]...)
	p.stack = p.stack[:at]
	return items, nil
}

// addEntries adds key and value pairs to a dict or an object.
func (p *parser) addEntries(target *Value, items []Slot) error {
	if target.Kind != KindDict && target.Kind != KindObject {
		return errors.New(tagPickleTargetInvalid, "SETITEMS on a value that is not a dict", "offset", p.op,
			"kind", target.Kind.String())
	}
	if len(items)%2 != 0 {
		return errors.New(tagPickleTargetInvalid, "SETITEMS with a key without a value", "offset", p.op)
	}
	for i := 0; i < len(items); i += 2 {
		target.Entries = append(target.Entries, Entry{Key: items[i], Value: items[i+1]})
	}
	return nil
}

func (p *parser) read(n int) ([]byte, error) {
	if n < 0 || n > len(p.data)-p.pos {
		return nil, errors.New(tagPickleTruncated, "the pickle ends in the middle of an opcode", "offset", p.op, "want", n)
	}
	b := p.data[p.pos : p.pos+n]
	p.pos += n
	return b, nil
}

// readSized reads a little-endian length of size bytes (signed when signed is set), then
// that many bytes.
func (p *parser) readSized(size int, signed bool) ([]byte, error) {
	b, err := p.read(size)
	if err != nil {
		return nil, err
	}
	var n uint64
	switch size {
	case 1:
		n = uint64(b[0])
	case 4:
		n = uint64(binary.LittleEndian.Uint32(b))
		if signed && int32(n) < 0 {
			return nil, errors.New(tagPickleTruncated, "a string with a negative length", "offset", p.op)
		}
	default:
		n = binary.LittleEndian.Uint64(b)
	}
	if n > uint64(len(p.data)-p.pos) {
		return nil, errors.New(tagPickleTruncated, "a string longer than the rest of the pickle", "offset", p.op, "length", n)
	}
	return p.read(int(n))
}

func (p *parser) readLine() (string, error) {
	i := bytes.IndexByte(p.data[p.pos:], '\n')
	if i < 0 {
		return "", errors.New(tagPickleTruncated, "the pickle ends in the middle of a line", "offset", p.op)
	}
	line := string(p.data[p.pos : p.pos+i])
	p.pos += i + 1
	return line, nil
}

// lengthSize is the size of the length that a string or bytes opcode starts with.
func lengthSize(code byte) int {
	switch code {
	case opShortBinUnicode, opShortBinBytes, opShortBinString:
		return 1
	case opBinUnicode8, opBinBytes8, opByteArray8:
		return 8
	default:
		return 4
	}
}

func isText(v *Value) bool { return v.Kind == KindString || v.Kind == KindByteString }

// decodeLong reads a little-endian two's complement integer, as LONG1 and LONG4 store it.
func decodeLong(b []byte) *big.Int {
	if len(b) == 0 {
		return new(big.Int)
	}
	be := make([]byte, len(b))
	for i, c := range b {
		be[len(b)-1-i] = c
	}
	n := new(big.Int).SetBytes(be)
	if b[len(b)-1]&0x80 != 0 {
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(8*len(b))))
	}
	return n
}

// decodeEscapes reads the backslash escapes of a Python 2 str literal (codecs.escape_decode).
func decodeEscapes(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 == len(s) {
			out = append(out, c)
			continue
		}
		i++
		switch e := s[i]; e {
		case '\n':
		case '\\', '\'', '"':
			out = append(out, e)
		case 'a':
			out = append(out, '\a')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'v':
			out = append(out, '\v')
		case 'x':
			if i+2 < len(s) {
				if n, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
					out = append(out, byte(n))
					i += 2
					continue
				}
			}
			out = append(out, '\\', e)
		case '0', '1', '2', '3', '4', '5', '6', '7':
			j := i
			for j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7' {
				j++
			}
			n, _ := strconv.ParseUint(s[i:j], 8, 16)
			out = append(out, byte(n))
			i = j - 1
		default:
			out = append(out, '\\', e)
		}
	}
	return string(out)
}

// decodeRawUnicodeEscape reads Python's raw-unicode-escape: \uXXXX and \UXXXXXXXX are code
// points, and every other byte is a Latin-1 character.
func decodeRawUnicodeEscape(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && (s[i+1] == 'u' || s[i+1] == 'U') {
			width := 4
			if s[i+1] == 'U' {
				width = 8
			}
			if i+2+width <= len(s) {
				if n, err := strconv.ParseUint(s[i+2:i+2+width], 16, 32); err == nil && utf8.ValidRune(rune(n)) {
					out.WriteRune(rune(n))
					i += 1 + width
					continue
				}
			}
		}
		out.WriteRune(rune(s[i]))
	}
	return out.String()
}

// The tags of the errors that this file makes.
const (
	tagPickleGlobalInvalid  = "PickleGlobalInvalid"
	tagPickleMarkMissing    = "PickleMarkMissing"
	tagPickleMemoInvalid    = "PickleMemoInvalid"
	tagPickleNoStop         = "PickleNoStop"
	tagPickleNumberInvalid  = "PickleNumberInvalid"
	tagPickleOpcodeUnknown  = "PickleOpcodeUnknown"
	tagPickleStackUnderflow = "PickleStackUnderflow"
	tagPickleStringInvalid  = "PickleStringInvalid"
	tagPickleTargetInvalid  = "PickleTargetInvalid"
	tagPickleTruncated      = "PickleTruncated"
)
