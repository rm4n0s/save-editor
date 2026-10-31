// Package unityprefs reads and edits Unity PlayerPrefs files, as a model.Format.
//
// Unity keeps PlayerPrefs (the game's int, float and string settings) where the platform
// keeps preferences:
//
//   - Linux: ~/.config/unity3d/<Company>/<Product>/prefs, an XML file. The root is
//     <unity_prefs version_major="1" version_minor="1"> and each value is
//     <pref name="..." type="int|float|string">text</pref>; a string is the base64 of its
//     UTF-8 bytes. The layout was checked against a real file posted in a public gist
//     (https://gist.github.com/MrSchism/87ade5ee34a07da7ea27) and Unity's documentation.
//   - macOS: ~/Library/Preferences/<bundle id>.plist (unity.<Company>.<Product>.plist in
//     older versions, com.<Company>.<Product>.plist in newer ones), a binary property
//     list (bplist00) that maps each key to an integer, a real or a string. That ints are
//     integers, floats are reals and strings are strings is how the PlayerPrefsEditor
//     asset (github.com/sabresaurus/PlayerPrefsEditor) reads the file.
//   - Windows keeps PlayerPrefs in the registry, which is not a file: it is not supported.
//
// A file is edited in place. The XML file changes only the text of the edited elements. The
// binary plist is rebuilt around the same objects: every object that is not edited keeps
// its bytes, and only the offset table, which depends on the sizes of the objects, is
// written again. A writer keeps one object for equal numbers and strings, so an edit of a
// place whose object other places use adds a new object and points that place at it; the
// others keep the old value.
package unityprefs

import (
	"bytes"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// Format is the Unity PlayerPrefs file: the XML file of Linux and the binary plist of macOS.
var Format = model.Format{
	Name:     "Unity PlayerPrefs",
	Patterns: []string{"prefs", "*.plist"},
	Detect:   detect,
	Parse:    parse,
}

// Document is a parsed PlayerPrefs file.
type Document struct {
	layout  layout
	data    []byte
	vars    []model.Variable
	details []model.Detail
	// sites are the sites of the document by ID.
	sites []*model.Site
	plist *plist
}

type layout uint8

const (
	layoutXML layout = iota
	layoutPlist
)

// Variables are the preferences in the order of the file.
func (d *Document) Variables() []model.Variable { return d.vars }

// Details are facts about the file.
func (d *Document) Details() []model.Detail { return d.details }

// Encode is the file with the edits made. An XML file keeps every byte but the text of the
// edited elements; a binary plist keeps the bytes of every object that is not edited.
func (d *Document) Encode(edits []model.Edit) ([]byte, error) {
	if len(edits) == 0 {
		return bytes.Clone(d.data), nil
	}
	if d.layout == layoutPlist {
		return d.encodePlist(edits)
	}
	return d.encodeXML(edits)
}

// detect reports whether the file is a PlayerPrefs XML file or a binary property list.
func detect(_ string, data []byte) bool {
	if isXML(data) {
		return true
	}
	if bytes.HasPrefix(data, []byte(plistMagic)) {
		p, err := parsePlist(data)
		return err == nil && p.objects[p.top].kind == plistDict
	}
	return false
}

// isXML reports whether the root element of the data is unity_prefs.
func isXML(data []byte) bool {
	head := data[:min(len(data), 1024)]
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	i := bytes.Index(head, []byte("<unity_prefs"))
	if i < 0 {
		return false
	}
	// Only an XML declaration, comments and white space may come before the root.
	before := bytes.TrimSpace(head[:i])
	if len(before) == 0 {
		return true
	}
	return bytes.HasPrefix(before, []byte("<?xml")) && !bytes.Contains(before[2:], []byte("<unity_prefs"))
}

func parse(_ string, data []byte) (model.Document, error) {
	if isXML(data) {
		return parseXML(data)
	}
	if bytes.HasPrefix(data, []byte(plistMagic)) {
		return parsePlistDocument(data)
	}
	return nil, errors.New(tagUnknownLayout, "this is neither a Unity prefs XML file nor a binary property list",
		"size", len(data))
}

// newSite adds a site to the document.
func (d *Document) newSite(ref any, lo, hi *big.Int) *model.Site {
	s := &model.Site{ID: len(d.sites), IntMin: lo, IntMax: hi, Ref: ref}
	d.sites = append(d.sites, s)
	return s
}

// textSite is a site of a text that is valid UTF-8.
func (d *Document) textSite(ref any) *model.Site {
	s := d.newSite(ref, nil, nil)
	s.CheckText = checkUTF8
	return s
}

// checkUTF8 is the CheckText of a string site.
func checkUTF8(s string) error {
	if !utf8.ValidString(s) {
		return errors.New(tagEditText, "The text is not valid UTF-8")
	}
	return nil
}

var (
	int32Min = big.NewInt(math.MinInt32)
	int32Max = big.NewInt(math.MaxInt32)
	int64Min = big.NewInt(math.MinInt64)
	int64Max = big.NewInt(math.MaxInt64)
)

// siteOf finds the document's own site of an edit, and checks the value against the kind.
func (d *Document) siteOf(e model.Edit, seen map[int]bool) (any, error) {
	s := e.Site
	if s == nil || s.ID < 0 || s.ID >= len(d.sites) || d.sites[s.ID] != s {
		return nil, errors.New(tagEditSite, "the site is not from this document")
	}
	if e.Value == nil {
		return nil, errors.New(tagEditKind, "the edit has no value", "site", s.ID)
	}
	if seen[s.ID] {
		return nil, errors.New(tagEditSite, "two edits of the same site", "site", s.ID)
	}
	seen[s.ID] = true
	return s.Ref, nil
}

// isInternalKey reports whether a key is one that Unity itself writes: the screen settings
// of the player and the keys that begin with "unity." (and the keys macOS adds to every preferences file).
func isInternalKey(name string) bool {
	switch name {
	case "UnityGraphicsQuality", "UnitySelectMonitor":
		return true
	}
	return strings.HasPrefix(name, "Screenmanager ") || strings.HasPrefix(name, "unity.") ||
		strings.HasPrefix(name, "Apple") || strings.HasPrefix(name, "com.apple.") || hasAnyPrefix(name,
		"NSWindow ", "NSNav", "NSToolbar ", "NSSplitView ", "NSTableView ", "NSOSP")
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// uniqueName makes name different from the names in used.
func uniqueName(name string, used map[string]bool) string {
	if !used[name] {
		used[name] = true
		return name
	}
	for n := 2; ; n++ {
		candidate := name + " (" + strconv.Itoa(n) + ")"
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

const (
	tagUnknownLayout = "UnityPrefsUnknownLayout"
	tagEditSite      = "UnityPrefsEditSite"
	tagEditKind      = "UnityPrefsEditKind"
	tagEditRange     = "UnityPrefsEditRange"
	tagEditText      = "UnityPrefsEditText"
)
