// Package model is the tree of values that the editor shows and edits, the same for every
// kind of save file, and the Format that each kind of save file implements to be opened.
//
// A format reads a file into a Document: a list of variables, each a tree of values. A value
// that can be replaced where it is in the file has a Site; the editor gives Encode the new
// values by site, and Encode writes the file with only those values changed.
package model

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Kind is the kind of a value, the same for every format.
type Kind uint8

const (
	// KindNull is a missing value: Python's None, Ruby's nil, JSON's null.
	KindNull Kind = iota
	// KindBool is true or false.
	KindBool
	// KindInt is a whole number.
	KindInt
	// KindFloat is a floating-point number.
	KindFloat
	// KindString is text.
	KindString
	// KindBytes is binary data. It is shown, not edited.
	KindBytes
	// KindList is an ordered container; its children are labeled by index.
	KindList
	// KindMap is a container of values by key; its children are labeled by key.
	KindMap
	// KindObject is an instance of a class or a struct of the game; its children are its
	// fields.
	KindObject
	// KindOpaque is a value that is kept but not shown in detail; Str says what it is.
	KindOpaque
)

// Value is a value of a save. Containers and objects hold their parts in Children. A value
// reached from two places (a shared reference) is the same *Value in both, so a value that
// contains itself is found by comparing pointers.
type Value struct {
	Kind Kind
	// Type is the short name of the value's type as the game's engine spells it, such as
	// "int", "Game_Actor" or "IntProperty".
	Type string
	// QualifiedType is the full name of the type, such as "renpy.revertable.RevertableList";
	// empty when it is Type.
	QualifiedType string

	Bool bool
	Int  *big.Int
	// Float is the number of a KindFloat; Float32 says it is stored with single precision,
	// so it is shown with the digits a float32 needs.
	Float   float64
	Float32 bool
	// Str is the text of a KindString, and what a KindOpaque or a KindNull is called (such as
	// "None"); empty uses the default.
	Str   string
	Bytes []byte

	// Children are the parts of a KindList, KindMap or KindObject.
	Children []Child
}

// Child is one part of a value, with the label the tree shows for it ("[0]", a key, a field
// name).
type Child struct {
	Label string
	Slot  Slot
}

// Slot is one place where a value appears in the file.
type Slot struct {
	Value *Value
	// Site is where a primitive value can be replaced in the file; nil when it cannot be.
	Site *Site
}

// Site is a place in the file where a primitive value can be replaced by another value of
// the same kind.
type Site struct {
	// ID is unique among the sites of a document; the editor keeps its edits by ID.
	ID int
	// IntMin and IntMax bound the whole numbers the site can hold; nil is no bound.
	IntMin, IntMax *big.Int
	// CheckText, when set, returns an error whose message tells the user why a text cannot
	// be stored at the site (such as "Use only Latin letters"), or nil.
	CheckText func(string) error
	// Ref is the format's own data about the site, such as a byte offset.
	Ref any
}

// Edit is a new value for a site. The value has the kind of the site's old value.
type Edit struct {
	Site  *Site
	Value *Value
}

// Variable is one top-level row of the tree.
type Variable struct {
	// Name is the name the tree shows, unique in the document.
	Name string
	// Internal is a variable of the engine rather than of the game; the editor hides it
	// unless asked to show it.
	Internal bool
	Slot     Slot
}

// Detail is one fact about a save, shown beside the tree.
type Detail struct {
	Label   string
	Text    string
	Tooltip string
}

// Document is a parsed save file.
type Document interface {
	// Variables are the top-level rows of the tree, in the order to show them.
	Variables() []Variable
	// Details are facts about the save to show beside the tree, in order.
	Details() []Detail
	// Encode is the file with the edits made. Every value that no edit changes is kept as
	// it was; a format re-encodes only what it must (a length, a checksum, a compressed
	// layer). Encode does not change the document.
	Encode(edits []Edit) ([]byte, error)
}

// Screenshotter is a Document whose save has a picture of the game (PNG or JPEG).
type Screenshotter interface {
	Screenshot() []byte
}

// Format is a kind of save file that the editor opens.
type Format struct {
	// Name is the name of the engine and its versions, such as "RPG Maker MV".
	Name string
	// Patterns are file name patterns for the open dialog, such as "*.rpgsave".
	Patterns []string
	// Detect reports whether the file called name (a base name) with the contents data is
	// in this format. It looks at the contents first and at the name only when the
	// contents cannot tell.
	Detect func(name string, data []byte) bool
	// Parse reads a file of this format.
	Parse func(name string, data []byte) (Document, error)
}

// IsPrimitive reports whether the value is null, a bool, a number or a string.
func (v *Value) IsPrimitive() bool {
	switch v.Kind {
	case KindNull, KindBool, KindInt, KindFloat, KindString:
		return true
	default:
		return false
	}
}

// HasChildren reports whether the value has parts to show.
func (v *Value) HasChildren() bool { return len(v.Children) > 0 }

// TypeName is Type, or the name of the kind when Type is empty.
func (v *Value) TypeName() string {
	if v.Type != "" {
		return v.Type
	}
	return v.Kind.String()
}

// QualifiedTypeName is QualifiedType, or TypeName when it is empty.
func (v *Value) QualifiedTypeName() string {
	if v.QualifiedType != "" {
		return v.QualifiedType
	}
	return v.TypeName()
}

// Repr is a short text for the value: the value of a primitive, and a summary of a
// container.
func (v *Value) Repr() string {
	switch v.Kind {
	case KindNull:
		if v.Str != "" {
			return v.Str
		}
		return "null"
	case KindBool:
		return strconv.FormatBool(v.Bool)
	case KindInt:
		if v.Int == nil {
			return "0"
		}
		return v.Int.String()
	case KindFloat:
		return FormatFloat(v.Float, v.Float32)
	case KindString:
		return strconv.Quote(v.Str)
	case KindBytes:
		n := len(v.Bytes)
		if n == 1 {
			return "1 byte"
		}
		return strconv.Itoa(n) + " bytes"
	case KindOpaque:
		if v.Str != "" {
			return "<" + v.Str + ">"
		}
		return "<" + v.TypeName() + ">"
	}
	n := len(v.Children)
	unit := "items"
	if v.Kind == KindObject {
		unit = "parts"
		if n == 1 {
			unit = "part"
		}
	} else if n == 1 {
		unit = "item"
	}
	return v.TypeName() + " (" + strconv.Itoa(n) + " " + unit + ")"
}

// FormatFloat writes f as the shortest text that reads back as f (as a float32 when
// float32 is set), with a ".0" on whole numbers.
func FormatFloat(f float64, float32 bool) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	bits := 64
	if float32 {
		bits = 32
	}
	e := strconv.FormatFloat(f, 'e', -1, bits)
	exp, err := strconv.Atoi(e[strings.IndexByte(e, 'e')+1:])
	if err == nil && exp >= -4 && exp < 16 {
		s := strconv.FormatFloat(f, 'f', -1, bits)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	return e
}

// String is the name of the kind.
func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	case KindBytes:
		return "bytes"
	case KindList:
		return "list"
	case KindMap:
		return "map"
	case KindObject:
		return "object"
	default:
		return "opaque"
	}
}
