package unityprefs

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"io"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// xmlRef is where the text of a <pref> element is in the file.
type xmlRef struct {
	kind model.Kind
	// start and end are the bytes of the text between the tags. For an element written as
	// <pref ... /> they are the two bytes "/>" instead, and the edit writes the tags.
	start, end  int
	selfClosing bool
}

func parseXML(data []byte) (model.Document, error) {
	d := &Document{layout: layoutXML, data: data}
	dec := xml.NewDecoder(bytes.NewReader(data))
	used := map[string]bool{}
	depth := 0
	version := ""
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.NewErr(tagXMLInvalid, err).SetMetadata("offset", dec.InputOffset())
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			if _, isEnd := tok.(xml.EndElement); isEnd {
				depth--
			}
			continue
		}
		depth++
		switch {
		case depth == 1:
			if start.Name.Local != "unity_prefs" {
				return nil, errors.New(tagXMLRoot, "the root element is not unity_prefs", "name", start.Name.Local)
			}
			version = attr(start, "version_major") + "." + attr(start, "version_minor")
		case depth == 2 && start.Name.Local == "pref":
			p, err := readPref(dec, data, start)
			if err != nil {
				return nil, err
			}
			d.vars = append(d.vars, d.prefVariable(p, used))
			depth--
		default:
			if err := dec.Skip(); err != nil {
				return nil, errors.NewErr(tagXMLInvalid, err).SetMetadata("offset", dec.InputOffset())
			}
			depth--
		}
	}
	if depth != 0 || version == "" {
		return nil, errors.New(tagXMLRoot, "there is no unity_prefs element", "size", len(data))
	}
	d.details = []model.Detail{
		{Label: "Layout", Text: "Linux prefs file (XML), version " + version},
		{Label: "Preferences", Text: strconv.Itoa(len(d.vars))},
	}
	return d, nil
}

// attr is the value of the attribute of an element, empty when there is none.
func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// xmlPref is a <pref> element as read.
type xmlPref struct {
	name, typ string
	text      string
	// clean is false when the element has parts other than text.
	clean       bool
	start, end  int
	selfClosing bool
}

// readPref reads a <pref> element whose start tag was just read, up to its end tag.
func readPref(dec *xml.Decoder, data []byte, start xml.StartElement) (xmlPref, error) {
	startEnd := int(dec.InputOffset())
	p := xmlPref{
		name: attr(start, "name"), typ: attr(start, "type"), clean: true,
		start: startEnd, end: startEnd,
		selfClosing: startEnd >= 2 && data[startEnd-2] == '/' && data[startEnd-1] == '>',
	}
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return p, errors.NewErr(tagXMLInvalid, err).SetMetadata("name", p.name)
		}
		switch t := tok.(type) {
		case xml.CharData:
			text.Write(t)
			p.end = int(dec.InputOffset())
		case xml.EndElement:
			p.text = text.String()
			if p.selfClosing {
				p.start, p.end = startEnd-2, startEnd
				for p.start > 0 && (data[p.start-1] == ' ' || data[p.start-1] == '\t') {
					p.start--
				}
			}
			return p, nil
		case xml.StartElement:
			p.clean = false
			if err := dec.Skip(); err != nil {
				return p, errors.NewErr(tagXMLInvalid, err)
			}
		default:
			p.clean = false
		}
	}
}

// prefVariable makes the variable of a preference.
func (d *Document) prefVariable(p xmlPref, used map[string]bool) model.Variable {
	v := model.Variable{
		Name:     uniqueName(p.name, used),
		Internal: isInternalKey(p.name),
	}
	opaque := func(why string) {
		v.Slot = model.Slot{Value: &model.Value{Kind: model.KindOpaque, Type: p.typ, Str: why}}
	}
	if !p.clean {
		opaque("unreadable " + p.typ)
		return v
	}
	text := strings.TrimSpace(p.text)
	switch p.typ {
	case "int":
		n, err := strconv.ParseInt(text, 10, 32)
		if err != nil {
			opaque("unreadable int")
			return v
		}
		v.Slot = model.Slot{
			Value: &model.Value{Kind: model.KindInt, Type: "int", Int: big.NewInt(n)},
			Site:  d.newSite(&xmlRef{kind: model.KindInt, start: p.start, end: p.end, selfClosing: p.selfClosing}, int32Min, int32Max),
		}
	case "float":
		f, err := strconv.ParseFloat(text, 32)
		if err != nil {
			opaque("unreadable float")
			return v
		}
		v.Slot = model.Slot{
			Value: &model.Value{Kind: model.KindFloat, Type: "float", Float: f, Float32: true},
			Site:  d.newSite(&xmlRef{kind: model.KindFloat, start: p.start, end: p.end, selfClosing: p.selfClosing}, nil, nil),
		}
	case "string":
		raw, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			opaque("not base64")
			return v
		}
		if !utf8.Valid(raw) {
			v.Slot = model.Slot{Value: &model.Value{Kind: model.KindBytes, Type: "string", Bytes: raw}}
			return v
		}
		v.Slot = model.Slot{
			Value: &model.Value{Kind: model.KindString, Type: "string", Str: string(raw)},
			Site:  d.textSite(&xmlRef{kind: model.KindString, start: p.start, end: p.end, selfClosing: p.selfClosing}),
		}
	default:
		opaque("type " + p.typ)
	}
	return v
}

func (d *Document) encodeXML(edits []model.Edit) ([]byte, error) {
	type change struct {
		ref  *xmlRef
		text string
	}
	changes := make([]change, 0, len(edits))
	seen := map[int]bool{}
	for _, e := range edits {
		ref, err := d.siteOf(e, seen)
		if err != nil {
			return nil, err
		}
		r := ref.(*xmlRef)
		if e.Value.Kind != r.kind {
			return nil, errors.New(tagEditKind, "the new value has another kind than the old one", "site", e.Site.ID,
				"want", r.kind.String(), "got", e.Value.Kind.String())
		}
		var text string
		switch r.kind {
		case model.KindInt:
			if e.Value.Int == nil || e.Value.Int.Cmp(e.Site.IntMin) < 0 || e.Value.Int.Cmp(e.Site.IntMax) > 0 {
				return nil, errors.New(tagEditRange, "The number must be between -2147483648 and 2147483647", "site", e.Site.ID)
			}
			text = e.Value.Int.String()
		case model.KindFloat:
			f := e.Value.Float
			if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > math.MaxFloat32 {
				return nil, errors.New(tagEditRange, "The number must be finite and fit a 32-bit float", "site", e.Site.ID)
			}
			text = formatFloat32(f)
		default:
			if !utf8.ValidString(e.Value.Str) {
				return nil, errors.New(tagEditText, "The text is not valid UTF-8", "site", e.Site.ID)
			}
			text = base64.StdEncoding.EncodeToString([]byte(e.Value.Str))
		}
		if r.selfClosing {
			text = ">" + text + "</pref>"
		}
		changes = append(changes, change{r, text})
	}
	slices.SortFunc(changes, func(a, b change) int { return a.ref.start - b.ref.start })
	out := make([]byte, 0, len(d.data)+64)
	pos := 0
	for _, c := range changes {
		out = append(out, d.data[pos:c.ref.start]...)
		out = append(out, c.text...)
		pos = c.ref.end
	}
	return append(out, d.data[pos:]...), nil
}

// formatFloat32 is the shortest text that reads back as the float32 nearest to f. Whole
// numbers have no decimal point, as in the files Unity writes; very large and very small
// numbers use an exponent.
func formatFloat32(f float64) string {
	f32 := float64(float32(f))
	e := strconv.FormatFloat(f32, 'e', -1, 32)
	exp, err := strconv.Atoi(e[strings.IndexByte(e, 'e')+1:])
	if err != nil || (f32 != 0 && (exp < -5 || exp >= 15)) {
		return strconv.FormatFloat(f32, 'G', -1, 32)
	}
	return strconv.FormatFloat(f32, 'f', -1, 32)
}

const (
	tagXMLInvalid = "UnityPrefsXMLInvalid"
	tagXMLRoot    = "UnityPrefsXMLRoot"
)
