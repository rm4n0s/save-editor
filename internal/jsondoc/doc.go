// Package jsondoc reads a JSON text into model values and writes edits back by replacing only
// the bytes of the edited tokens.
//
// It knows nothing of any game engine: a save format that is JSON inside (RPG Maker, Unity's
// JsonUtility, Easy Save 3) builds on it. The reader is strict RFC 8259 JSON, with a UTF-8
// byte order mark allowed at the start. Every object becomes a model.KindMap, every array a
// model.KindList, and every null, bool, number and string a primitive with a model.Site whose
// Ref is a *Span, the bytes of the token in the file. Patch replaces those bytes and nothing
// else: the white space, the order and spelling of the keys, and the spelling of every number
// and string that is not edited stay as they were.
package jsondoc

import (
	"math/big"
	"slices"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// DefaultMaxDepth is the deepest nesting of arrays and objects that Parse accepts when
// Options.MaxDepth is zero.
const DefaultMaxDepth = 512

// Options are the choices of a reader of JSON.
type Options struct {
	// IntMin and IntMax bound the whole numbers that the sites of the document can hold,
	// the range that the program which reads the file keeps exactly. Nil is the JavaScript
	// safe range, -(2^53-1) to 2^53-1. A whole number in the file outside the bounds is
	// still read exactly; only the numbers written by an edit are held to them.
	IntMin, IntMax *big.Int
	// MaxDepth is the deepest nesting accepted; zero is DefaultMaxDepth.
	MaxDepth int
}

// Span is the place of a token in the file: Site.Ref of every site of a Doc.
type Span struct {
	// Start and End are the byte offsets of the token, End exclusive. A string includes its
	// quotes.
	Start, End int
	// Kind is the kind of the value the token holds: KindNull, KindBool, KindInt, KindFloat
	// or KindString.
	Kind model.Kind
}

// Doc is a parsed JSON text.
type Doc struct {
	// Root is the top-level value of the text.
	Root model.Slot

	data  []byte
	sites []*model.Site // by Site.ID
}

// Parse reads a JSON text with the default Options.
func Parse(data []byte) (*Doc, error) {
	return ParseOptions(data, Options{})
}

// ParseOptions reads a JSON text. It does not keep or change data except that Doc.Data
// returns it; the caller must not change data while the Doc is in use.
func ParseOptions(data []byte, opts Options) (*Doc, error) {
	p := &parser{data: data, maxDepth: opts.MaxDepth, minInt: opts.IntMin, maxInt: opts.IntMax}
	if p.maxDepth <= 0 {
		p.maxDepth = DefaultMaxDepth
	}
	if p.minInt == nil || p.maxInt == nil {
		safe := big.NewInt(1<<53 - 1)
		if p.maxInt == nil {
			p.maxInt = safe
		}
		if p.minInt == nil {
			p.minInt = new(big.Int).Neg(safe)
		}
	}
	d := &Doc{data: data}
	p.doc = d
	if err := p.parseDocument(); err != nil {
		return nil, err
	}
	return d, nil
}

// Data is the text that was parsed.
func (d *Doc) Data() []byte { return d.data }

// SiteCount is the number of sites of the document; their IDs are 0 to SiteCount-1, in the
// order of the file.
func (d *Doc) SiteCount() int { return len(d.sites) }

// SiteByID returns the site with the ID, or nil.
func (d *Doc) SiteByID(id int) *model.Site {
	if id < 0 || id >= len(d.sites) {
		return nil
	}
	return d.sites[id]
}

type replacement struct {
	span *Span
	text []byte
}

// Patch returns the text with each edit's value written over the token of its site. The
// value has the kind of the site's token, a whole number is within the bounds of the site,
// and a float is finite; the text of a string must be valid UTF-8 and pass the site's
// CheckText. A site may be edited once. Patch does not change the document.
func (d *Doc) Patch(edits []model.Edit) ([]byte, error) {
	reps := make([]replacement, 0, len(edits))
	for _, e := range edits {
		if e.Site == nil || e.Site.ID < 0 || e.Site.ID >= len(d.sites) || d.sites[e.Site.ID] != e.Site {
			return nil, errors.New(tagEditSite, "The edit is not for a place of this file")
		}
		if e.Value == nil {
			return nil, errors.New(tagEditKind, "The edit has no value", "site", e.Site.ID)
		}
		span, ok := e.Site.Ref.(*Span)
		if !ok {
			return nil, errors.New(tagEditSite, "The edit is not for a place of this file", "site", e.Site.ID)
		}
		if e.Value.Kind != span.Kind {
			return nil, errors.New(tagEditKind, "The new value is not a "+span.Kind.String(),
				"site", e.Site.ID, "want", span.Kind.String(), "got", e.Value.Kind.String())
		}
		var text []byte
		switch span.Kind {
		case model.KindNull:
			text = []byte("null")
		case model.KindBool:
			text = strconv.AppendBool(nil, e.Value.Bool)
		case model.KindInt:
			n := e.Value.Int
			if n == nil {
				n = new(big.Int)
			}
			if (e.Site.IntMin != nil && n.Cmp(e.Site.IntMin) < 0) || (e.Site.IntMax != nil && n.Cmp(e.Site.IntMax) > 0) {
				return nil, errors.New(tagEditRange, "The number is out of range",
					"site", e.Site.ID, "value", n.String())
			}
			text = n.Append(nil, 10)
		case model.KindFloat:
			f := e.Value.Float
			if f != f || f > maxFloat || f < -maxFloat {
				return nil, errors.New(tagEditFloat, "A JSON number must be finite", "site", e.Site.ID)
			}
			text = AppendFloat(nil, f, e.Value.Float32)
		default:
			s := e.Value.Str
			if !utf8.ValidString(s) {
				return nil, errors.New(tagEditText, "The text is not valid UTF-8", "site", e.Site.ID)
			}
			if e.Site.CheckText != nil {
				if err := e.Site.CheckText(s); err != nil {
					return nil, errors.NewErr(tagEditText, err).SetMetadata("site", e.Site.ID)
				}
			}
			text = AppendString(nil, s)
		}
		reps = append(reps, replacement{span, text})
	}
	sort.Slice(reps, func(i, j int) bool { return reps[i].span.Start < reps[j].span.Start })
	for i := 1; i < len(reps); i++ {
		if reps[i].span == reps[i-1].span {
			return nil, errors.New(tagEditTwice, "A place is edited twice", "offset", reps[i].span.Start)
		}
	}
	size := len(d.data)
	for _, r := range reps {
		size += len(r.text) - (r.span.End - r.span.Start)
	}
	out := make([]byte, 0, size)
	at := 0
	for _, r := range reps {
		out = append(out, d.data[at:r.span.Start]...)
		out = append(out, r.text...)
		at = r.span.End
	}
	return append(out, d.data[at:]...), nil
}

// Clone returns a copy of the text, for an Encode with no edits.
func (d *Doc) Clone() []byte { return slices.Clone(d.data) }

const (
	tagEditSite  = "JsonEditSite"
	tagEditKind  = "JsonEditKind"
	tagEditRange = "JsonEditRange"
	tagEditFloat = "JsonEditFloat"
	tagEditText  = "JsonEditText"
	tagEditTwice = "JsonEditTwice"
)
