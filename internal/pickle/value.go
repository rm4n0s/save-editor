// Package pickle reads Python pickles (protocols 0 to 5) into a tree of values and writes
// them back with some primitive values replaced. Everything that is not replaced keeps its
// original bytes, so classes, memo references and data this package does not understand
// survive a round trip unchanged.
package pickle

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Kind is the Python type of a value.
type Kind uint8

const (
	// KindNone is None.
	KindNone Kind = iota
	// KindBool is a bool.
	KindBool
	// KindInt is an int (a Python 2 int or long as well).
	KindInt
	// KindFloat is a float.
	KindFloat
	// KindString is a text string: a Python 3 str or a Python 2 unicode.
	KindString
	// KindByteString is a Python 2 str, which Python 3 reads as a str.
	KindByteString
	// KindBytes is bytes or a bytearray.
	KindBytes
	// KindTuple is a tuple.
	KindTuple
	// KindList is a list.
	KindList
	// KindDict is a dict.
	KindDict
	// KindSet is a set.
	KindSet
	// KindFrozenSet is a frozenset.
	KindFrozenSet
	// KindGlobal is a reference to a class or a function by module and name.
	KindGlobal
	// KindObject is an object made by calling a class or a function (REDUCE, NEWOBJ, OBJ,
	// INST), with the state BUILD gave it and the items APPENDS and SETITEMS added.
	KindObject
	// KindOpaque is a value this package keeps but does not show: a persistent id or an
	// extension code.
	KindOpaque
)

// String is the name of the kind as Python spells the type.
func (k Kind) String() string {
	switch k {
	case KindNone:
		return "None"
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString, KindByteString:
		return "str"
	case KindBytes:
		return "bytes"
	case KindTuple:
		return "tuple"
	case KindList:
		return "list"
	case KindDict:
		return "dict"
	case KindSet:
		return "set"
	case KindFrozenSet:
		return "frozenset"
	case KindGlobal:
		return "class"
	case KindObject:
		return "object"
	default:
		return "opaque"
	}
}

// Value is a value of the pickle. Containers hold their elements as slots, and objects hold
// the parts that made them. A value reached through the memo is the same *Value everywhere.
type Value struct {
	Kind  Kind
	Bool  bool
	Int   *big.Int
	Float float64
	// Str is the text of KindString and KindByteString, and the text of a KindOpaque
	// persistent id.
	Str   string
	Bytes []byte

	// Module and Name name a KindGlobal.
	Module, Name string

	// Items are the elements of a tuple, list or set, and the items APPENDS added to an
	// object.
	Items []Slot
	// Entries are the items of a dict, and the items SETITEMS added to an object.
	Entries []Entry

	// Callable is the class or function that made a KindObject; Args and Kwargs are its
	// arguments (nil when there were none) and State what BUILD gave it (nil without BUILD).
	Callable *Value
	Args     *Value
	Kwargs   *Value
	State    *Value
}

// Entry is one key and value of a dict.
type Entry struct {
	Key   Slot
	Value Slot
}

// SiteKind says how a slot's value was put on the stack.
type SiteKind uint8

const (
	// SiteNone is a value that cannot be replaced where it is (it was duplicated with DUP).
	SiteNone SiteKind = iota
	// SiteLiteral is a value written out in place by one opcode.
	SiteLiteral
	// SiteMemo is a value taken from the memo (GET, BINGET, LONG_BINGET).
	SiteMemo
	// SiteBuilt is a value that several opcodes made (a container or an object).
	SiteBuilt
)

// Site is where a slot's value was put on the stack: the bytes [Start, End) of the opcode
// that pushed it, and through MemoEnd the memo opcode right after it, if any.
type Site struct {
	Kind    SiteKind
	Start   int
	End     int
	MemoEnd int
}

// Slot is one place where a value appears: an element of a container, a key or a value of a
// dict, or the value the pickle returns.
type Slot struct {
	Value *Value
	Site  Site
}

// IsPrimitive reports whether the value is None, a bool, a number or a string.
func (v *Value) IsPrimitive() bool {
	switch v.Kind {
	case KindNone, KindBool, KindInt, KindFloat, KindString, KindByteString:
		return true
	default:
		return false
	}
}

// Editable reports whether the slot can be given a new value with Document.Patch: its value
// is a primitive that one opcode put where it is.
func (s Slot) Editable() bool {
	return s.Value != nil && s.Value.IsPrimitive() && (s.Site.Kind == SiteLiteral || s.Site.Kind == SiteMemo)
}

// TypeName is the Python name of the value's type: the class name for an object.
func (v *Value) TypeName() string {
	if v.Kind == KindObject && v.Callable != nil && v.Callable.Kind == KindGlobal {
		if v.Callable.Module == "copyreg" || v.Callable.Module == "copy_reg" || v.Callable.Module == "builtins" ||
			v.Callable.Module == "__builtin__" {
			if v.Args != nil && v.Args.Kind == KindTuple && len(v.Args.Items) > 0 {
				if cls := v.Args.Items[0].Value; cls.Kind == KindGlobal {
					return cls.Name
				}
			}
		}
		return v.Callable.Name
	}
	return v.Kind.String()
}

// QualifiedTypeName is TypeName with the module of an object's class, such as
// "renpy.revertable.RevertableList".
func (v *Value) QualifiedTypeName() string {
	if v.Kind == KindObject && v.Callable != nil && v.Callable.Kind == KindGlobal {
		module := v.Callable.Module
		if module == "copyreg" || module == "copy_reg" {
			if v.Args != nil && v.Args.Kind == KindTuple && len(v.Args.Items) > 0 {
				if cls := v.Args.Items[0].Value; cls.Kind == KindGlobal {
					return cls.Module + "." + cls.Name
				}
			}
		}
		return module + "." + v.Callable.Name
	}
	return v.TypeName()
}

// Repr is a short text for the value, as Python's repr would write a primitive and as a
// summary for a container.
func (v *Value) Repr() string {
	switch v.Kind {
	case KindNone:
		return "None"
	case KindBool:
		if v.Bool {
			return "True"
		}
		return "False"
	case KindInt:
		return v.Int.String()
	case KindFloat:
		return FormatFloat(v.Float)
	case KindString, KindByteString:
		return strconv.Quote(v.Str)
	case KindBytes:
		return "b" + strconv.Quote(string(v.Bytes))
	case KindGlobal:
		return v.Module + "." + v.Name
	case KindOpaque:
		return "<" + v.Str + ">"
	}
	n := len(v.Items) + len(v.Entries)
	unit := "items"
	if n == 1 {
		unit = "item"
	}
	return v.TypeName() + " (" + strconv.Itoa(n) + " " + unit + ")"
}

// FormatFloat writes f as Python's repr does: the shortest text that reads back as f, with a
// ".0" on whole numbers.
func FormatFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	e := strconv.FormatFloat(f, 'e', -1, 64)
	exp, err := strconv.Atoi(e[strings.IndexByte(e, 'e')+1:])
	if err == nil && exp >= -4 && exp < 16 {
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	return e
}
