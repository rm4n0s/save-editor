package renpysave

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
	"github.com/rm4n0s/save-editor/internal/pickle"
)

// Format is the Ren'Py save file, for the editor to open.
var Format = model.Format{
	Name:     "Ren'Py",
	Patterns: []string{"*.save"},
	Detect:   detect,
	Parse:    parseFormat,
}

// detect reports whether data is a zip with the "log" of a Ren'Py save and one of the files
// that Ren'Py writes next to it. It reads the zip's directory, not the files.
func detect(_ string, data []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}
	var hasLog, hasOther bool
	for _, f := range zr.File {
		switch f.Name {
		case entryLog:
			hasLog = true
		case entryRenpyVersion, entryExtraInfo, entryJSON:
			hasOther = true
		}
	}
	return hasLog && hasOther
}

func parseFormat(_ string, data []byte) (model.Document, error) {
	d, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// siteInfo is what Encode needs to know of a model.Site.
type siteInfo struct {
	site pickle.Site
	kind pickle.Kind
}

// convert makes the model values of the variables, with one model.Site for each place of the
// pickle that can be replaced.
func (s *Document) convert() {
	c := &converter{memo: map[*pickle.Value]*model.Value{}, byStart: map[int]*model.Site{}, sites: map[*model.Site]siteInfo{}}
	seen := map[string]int{}
	for _, e := range s.Roots.Entries {
		k := e.Key.Value
		if k.Kind != pickle.KindString && k.Kind != pickle.KindByteString {
			continue
		}
		name := strings.TrimPrefix(k.Str, "store.")
		internal := false
		for part := range strings.SplitSeq(name, ".") {
			if strings.HasPrefix(part, "_") {
				internal = true
			}
		}
		v := model.Variable{Name: name, Internal: internal, Slot: c.slot(e.Value)}
		if i, ok := seen[k.Str]; ok {
			s.vars[i] = v // the last value of a key is the one Python keeps
			continue
		}
		seen[k.Str] = len(s.vars)
		s.vars = append(s.vars, v)
	}
	slices.SortStableFunc(s.vars, func(a, b model.Variable) int { return strings.Compare(a.Name, b.Name) })
	s.sites = c.sites
}

type converter struct {
	// memo makes a value that is reached twice, or inside itself, one model.Value.
	memo    map[*pickle.Value]*model.Value
	byStart map[int]*model.Site
	sites   map[*model.Site]siteInfo
}

// slot converts a place of the pickle. A bool, a number or a text that one opcode wrote
// gets a site; None gets none, as there is no other value of its kind.
func (c *converter) slot(p pickle.Slot) model.Slot {
	out := model.Slot{Value: c.value(p.Value)}
	if !p.Editable() || p.Value.Kind == pickle.KindNone {
		return out
	}
	site, ok := c.byStart[p.Site.Start]
	if !ok {
		site = &model.Site{ID: len(c.byStart), Ref: p.Site}
		c.byStart[p.Site.Start] = site
		c.sites[site] = siteInfo{site: p.Site, kind: p.Value.Kind}
	}
	out.Site = site
	return out
}

func (c *converter) value(p *pickle.Value) *model.Value {
	if v, ok := c.memo[p]; ok {
		return v
	}
	v := &model.Value{Type: p.TypeName()}
	if q := p.QualifiedTypeName(); q != v.Type {
		v.QualifiedType = q
	}
	c.memo[p] = v
	switch p.Kind {
	case pickle.KindNone:
		v.Kind, v.Str = model.KindNull, "None"
	case pickle.KindBool:
		v.Kind, v.Bool = model.KindBool, p.Bool
	case pickle.KindInt:
		v.Kind, v.Int = model.KindInt, p.Int
	case pickle.KindFloat:
		v.Kind, v.Float = model.KindFloat, p.Float
	case pickle.KindString, pickle.KindByteString:
		v.Kind, v.Str = model.KindString, p.Str
	case pickle.KindBytes:
		v.Kind, v.Bytes = model.KindBytes, p.Bytes
	case pickle.KindTuple, pickle.KindList, pickle.KindSet, pickle.KindFrozenSet:
		v.Kind = model.KindList
	case pickle.KindDict:
		v.Kind = model.KindMap
	case pickle.KindObject:
		v.Kind = model.KindObject
	case pickle.KindGlobal:
		v.Kind, v.Str = model.KindOpaque, p.Module+"."+p.Name
	default:
		v.Kind, v.Str = model.KindOpaque, p.Str
	}
	for _, ch := range p.Children() {
		v.Children = append(v.Children, model.Child{Label: ch.Label, Slot: c.slot(ch.Slot)})
	}
	return v
}

// Variables are the variables of the roots whose keys are strings, sorted by name. Their
// names have no "store." in front, and those that start with an underscore (or have a part
// that does) are Internal.
func (s *Document) Variables() []model.Variable { return slices.Clone(s.vars) }

// Screenshot is the PNG that the save and load screens show, or nil.
func (s *Document) Screenshot() []byte { return s.screenshot }

// Details are when the save was made, how long the game had been played, the version of the
// game and of Ren'Py, and the number of variables; those that the save does not tell are left
// out.
func (s *Document) Details() []model.Detail {
	var out []model.Detail
	add := func(label, text string) {
		if text != "" {
			out = append(out, model.Detail{Label: label, Text: text})
		}
	}
	add("Saved", metadataTime(s.Metadata["_ctime"]))
	add("Play time", metadataDuration(s.Metadata["_game_runtime"]))
	add("Game version", metadataString(s.Metadata["_version"]))
	add("Ren'Py version", s.RenpyVersion)
	add("Variables", strconv.Itoa(len(s.vars)))
	return out
}

// patches turns the edits of the model into edits of the pickle. An edit has the kind of the
// value of its site, which must be a site of this document.
func (s *Document) patches(edits []model.Edit) ([]pickle.Edit, error) {
	out := make([]pickle.Edit, 0, len(edits))
	for _, e := range edits {
		info, ok := s.sites[e.Site]
		if !ok {
			return nil, errors.New(tagEditSiteUnknown, "the edit is for a place that is not in this save")
		}
		if e.Value == nil || !sameKind(e.Value.Kind, info.kind) {
			return nil, errors.New(tagEditKindMismatch, "the new value is not of the kind of the old one",
				"offset", info.site.Start, "kind", info.kind.String())
		}
		v := &pickle.Value{Kind: info.kind}
		switch info.kind {
		case pickle.KindBool:
			v.Bool = e.Value.Bool
		case pickle.KindInt:
			if e.Value.Int == nil {
				return nil, errors.New(tagEditKindMismatch, "the new number is missing", "offset", info.site.Start)
			}
			v.Int = e.Value.Int
		case pickle.KindFloat:
			v.Float = e.Value.Float
		default:
			v.Str = e.Value.Str
		}
		out = append(out, pickle.Edit{Site: info.site, Value: v})
	}
	return out, nil
}

// sameKind reports whether a model kind is the kind of a pickle value.
func sameKind(k model.Kind, p pickle.Kind) bool {
	switch p {
	case pickle.KindBool:
		return k == model.KindBool
	case pickle.KindInt:
		return k == model.KindInt
	case pickle.KindFloat:
		return k == model.KindFloat
	case pickle.KindString, pickle.KindByteString:
		return k == model.KindString
	}
	return false
}

func metadataNumber(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

// metadataTime is the local time of a Unix time in the metadata, or "".
func metadataTime(v any) string {
	f, ok := metadataNumber(v)
	if !ok || f <= 0 {
		return ""
	}
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9)).Local().Format("2006-01-02 15:04")
}

// metadataDuration is a number of seconds in the metadata as h:mm:ss, or "".
func metadataDuration(v any) string {
	f, ok := metadataNumber(v)
	if !ok || f < 0 {
		return ""
	}
	total := int64(f)
	return fmt.Sprintf("%d:%02d:%02d", total/3600, total/60%60, total%60)
}

func metadataString(v any) string {
	s, _ := v.(string)
	return s
}

// The tags of the errors that this file makes.
const (
	tagEditKindMismatch = "EditKindMismatch"
	tagEditSiteUnknown  = "EditSiteUnknown"
)
