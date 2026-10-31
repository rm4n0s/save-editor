// Package rubymarshal reads Ruby's Marshal format (version 4.8) into the values of package
// model, and writes edited values back into the original bytes.
//
// The format is the one of Ruby 1.8 to 3.x (the "dump" of RPG Maker XP, VX and VX Ace
// saves). A file can hold several dumps one after the other, as RGSS writes them; Parse
// reads them all. The reader follows marshal.c of Ruby: every type code is understood, and
// the numbering of objects (the targets of the '@' links) is the one Ruby gives, so a value
// that the file shares is the same *model.Value, with the same *model.Site, wherever the
// file refers to it.
//
// Nothing is re-serialized. The primitives that can be edited (true and false, Fixnum,
// Bignum, Float and String values, and the numbers inside the RGSS Color, Tone and Rect
// blobs) have a Site that knows the bytes of the value in the file; Encode splices the new
// bytes in place of those and keeps every other byte. A Fixnum stays an 'i', a Bignum stays an
// 'l' (Ruby makes it a Fixnum when it loads, and the object numbering is unchanged), a
// Float stays an 'f', and a String keeps its instance variables (its encoding flag) and
// changes only its length and bytes, so the numbering of the objects never moves.
//
// The reference is marshal.c of Ruby and doc/marshal.rdoc of ruby/ruby.
package rubymarshal

import (
	"math/big"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// Options say how to read a file.
type Options struct {
	// DefaultEncoding is the encoding of a String that does not say which it has. Ruby 1.8
	// strings never do, and RGSS 1 and 2 use UTF-8 for them; Ruby 1.9 and later give them
	// no flag only when they are binary. The empty name is "ASCII-8BIT", Ruby's own reading,
	// which still shows the strings that are valid UTF-8 as text.
	DefaultEncoding string
	// MaxDumps, when above zero, stops after that many dumps; Doc.Rest is then where the
	// next one would start. Zero reads dumps until the end of the data, and the data must
	// then hold nothing but dumps.
	MaxDumps int
}

// Dump is one Marshal.dump in a file.
type Dump struct {
	// Start and End are the byte range of the dump, from its 4.8 header to its last byte.
	Start, End int
	// Root is the value that was dumped.
	Root model.Slot
}

// Doc is a file of one or more Marshal dumps.
type Doc struct {
	// Data is the file.
	Data []byte
	// Dumps are the dumps, in order.
	Dumps []Dump
	// Rest is the offset of the first byte after the last dump read.
	Rest int

	nextID int
}

// The range of a Fixnum written as 'i' by the 32-bit Ruby of RGSS.
var (
	fixnumMin = big.NewInt(-1 << 30)
	fixnumMax = big.NewInt(1<<30 - 1)
	int32Min  = big.NewInt(-1 << 31)
	int32Max  = big.NewInt(1<<31 - 1)
)

// Parse reads the dumps of data. The result refers to data, which must not change.
func Parse(data []byte, opts Options) (*Doc, error) {
	doc := &Doc{Data: data}
	r := &reader{doc: doc, data: data, defEnc: encodingBinary}
	if opts.DefaultEncoding != "" {
		r.defEnc = lookupEncoding(opts.DefaultEncoding)
	}
	for r.pos < len(data) && (opts.MaxDumps == 0 || len(doc.Dumps) < opts.MaxDumps) {
		start := r.pos
		if len(data)-start < 2 || data[start] != 4 || data[start+1] > 8 {
			return nil, errors.New(TagBadHeader, "not a Ruby Marshal dump (major version 4, minor 8 expected)", "offset", start)
		}
		r.pos += 2
		r.syms = r.syms[:0]
		r.objs = r.objs[:0]
		slot, err := r.value()
		if err != nil {
			return nil, err
		}
		doc.Dumps = append(doc.Dumps, Dump{Start: start, End: r.pos, Root: *slot})
	}
	if len(doc.Dumps) == 0 {
		return nil, errors.New(TagNoDump, "the file holds no Marshal dump")
	}
	doc.Rest = r.pos
	return doc, nil
}

// Tags of the errors of Parse.
const (
	// TagBadHeader tags data that does not start with the Marshal 4.8 header.
	TagBadHeader = "BadHeader"
	// TagNoDump tags a file with no dump at all.
	TagNoDump = "NoDump"
)
