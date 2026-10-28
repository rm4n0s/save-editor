package pickle

import (
	"strconv"
	"strings"
	"unicode"
)

// Child is one part of a value, with the label a tree shows for it.
type Child struct {
	Label string
	Slot  Slot
}

// Children are the parts of a value that a tree shows under it: the elements of a container,
// the values of a dict by key, and the attributes and items of an object. A primitive has
// none.
func (v *Value) Children() []Child {
	var out []Child
	switch v.Kind {
	case KindTuple, KindList, KindSet, KindFrozenSet:
		out = appendItems(out, v.Items)
	case KindDict:
		out = appendEntries(out, v.Entries, false)
	case KindObject:
		out = appendItems(out, v.Items)
		out = appendEntries(out, v.Entries, false)
		out = append(out, objectState(v)...)
	}
	return out
}

// HasChildren reports whether Children would return anything, without building them.
func (v *Value) HasChildren() bool {
	switch v.Kind {
	case KindTuple, KindList, KindSet, KindFrozenSet, KindDict:
		return len(v.Items)+len(v.Entries) > 0
	case KindObject:
		return len(v.Children()) > 0
	}
	return false
}

// Lookup finds the value of a dict, or of an object's dict items, by its string key. A key
// given twice has its last value, as in Python.
func (v *Value) Lookup(key string) (Slot, bool) {
	for i := len(v.Entries) - 1; i >= 0; i-- {
		if k := v.Entries[i].Key.Value; isText(k) && k.Str == key {
			return v.Entries[i].Value, true
		}
	}
	return Slot{}, false
}

func appendItems(out []Child, items []Slot) []Child {
	for i, s := range items {
		out = append(out, Child{Label: "[" + strconv.Itoa(i) + "]", Slot: s})
	}
	return out
}

// appendEntries adds the values of a dict, labelled by key; attrs labels string keys that are
// identifiers as attributes (".name").
func appendEntries(out []Child, entries []Entry, attrs bool) []Child {
	for _, e := range entries {
		out = append(out, Child{Label: keyLabel(e.Key.Value, attrs), Slot: e.Value})
	}
	return out
}

func keyLabel(k *Value, attrs bool) string {
	if attrs && isText(k) && isIdentifier(k.Str) {
		return "." + k.Str
	}
	return "[" + k.Repr() + "]"
}

// objectState is what an object's state and arguments show: the attributes of a __dict__ (and
// of __slots__), the members of a set kept as a dict (Ren'Py's RevertableSet), the base value
// of a protocol 0 or 1 subclass of list or dict, or the arguments of a call.
func objectState(v *Value) []Child {
	var out []Child
	if v.Callable != nil && v.Callable.Kind == KindGlobal && (v.Callable.Module == "copyreg" || v.Callable.Module == "copy_reg") &&
		v.Callable.Name == "_reconstructor" && v.Args != nil && len(v.Args.Items) == 3 {
		out = append(out, baseChildren(v.Args.Items[2].Value)...)
	} else if v.State == nil && v.Args != nil && len(v.Items)+len(v.Entries) == 0 {
		for i, s := range v.Args.Items {
			out = append(out, Child{Label: "args[" + strconv.Itoa(i) + "]", Slot: s})
		}
	}
	state := v.State
	if state == nil {
		return out
	}
	switch state.Kind {
	case KindDict:
		return appendEntries(out, state.Entries, true)
	case KindTuple:
		if len(state.Items) == 1 && state.Items[0].Value.Kind == KindDict && strings.HasSuffix(v.TypeName(), "Set") {
			for i, e := range state.Items[0].Value.Entries {
				out = append(out, Child{Label: "[" + strconv.Itoa(i) + "]", Slot: e.Key})
			}
			return out
		}
		if len(state.Items) == 2 {
			for _, part := range state.Items {
				if part.Value.Kind == KindDict {
					out = appendEntries(out, part.Value.Entries, true)
				}
			}
			return out
		}
	case KindNone:
		return out
	}
	return append(out, Child{Label: "state", Slot: Slot{Value: state, Site: Site{Kind: SiteBuilt}}})
}

func baseChildren(base *Value) []Child {
	switch base.Kind {
	case KindList, KindTuple, KindSet:
		return appendItems(nil, base.Items)
	case KindDict:
		return appendEntries(nil, base.Entries, false)
	case KindNone:
		return nil
	}
	return []Child{{Label: "base", Slot: Slot{Value: base, Site: Site{Kind: SiteBuilt}}}}
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r != '_' && !unicode.IsLetter(r) && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}
