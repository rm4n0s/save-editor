package main

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/pickle"
	"github.com/rm4n0s/save-editor/internal/renpysave"
)

// row is one line of the list of variables: a variable, or a part of one that is expanded.
type row struct {
	// id is the path of labels from the variable, unique in the list.
	id    string
	depth int
	label string
	slot  pickle.Slot
	// expandable is a row with parts to show; expanded says they are shown.
	expandable bool
	expanded   bool
	// recursive is a container that is inside itself; its parts are not shown again.
	recursive bool
}

// buildRows lists the variables that match filter (case-insensitive, in the name or in the
// text of a primitive value), with the parts of the expanded rows under them.
func buildRows(vars []renpysave.Variable, filter string, showInternal bool, expanded map[string]bool) []row {
	filter = strings.ToLower(strings.TrimSpace(filter))
	var rows []row
	for _, v := range vars {
		if v.Internal && !showInternal {
			continue
		}
		if filter != "" && !matches(v, filter) {
			continue
		}
		rows = appendRow(rows, v.Name, 0, v.Name, v.Slot, expanded, nil)
	}
	return rows
}

// matches reports whether the name of the variable, or the text of its value when it is a
// primitive, contains filter (which is lower case).
func matches(v renpysave.Variable, filter string) bool {
	if strings.Contains(strings.ToLower(v.Name), filter) {
		return true
	}
	return v.Slot.Value.IsPrimitive() && strings.Contains(strings.ToLower(v.Slot.Value.Repr()), filter)
}

func appendRow(rows []row, id string, depth int, label string, slot pickle.Slot, expanded map[string]bool,
	ancestors []*pickle.Value) []row {
	r := row{id: id, depth: depth, label: label, slot: slot, expandable: slot.Value.HasChildren()}
	for _, a := range ancestors {
		if a == slot.Value {
			r.recursive, r.expandable = true, false
		}
	}
	r.expanded = r.expandable && expanded[id]
	rows = append(rows, r)
	if !r.expanded {
		return rows
	}
	ancestors = append(ancestors, slot.Value)
	for _, c := range slot.Value.Children() {
		rows = appendRow(rows, id+"\x00"+c.Label, depth+1, c.Label, c.Slot, expanded, ancestors)
	}
	return rows
}

// editText is the text a field shows for a primitive value.
func editText(v *pickle.Value) string {
	switch v.Kind {
	case pickle.KindInt:
		return v.Int.String()
	case pickle.KindFloat:
		return pickle.FormatFloat(v.Float)
	case pickle.KindString, pickle.KindByteString:
		return v.Str
	default:
		return v.Repr()
	}
}

// parseValue reads the text of a field as a new value of the kind of the old one.
func parseValue(kind pickle.Kind, text string) (*pickle.Value, error) {
	switch kind {
	case pickle.KindInt:
		n, ok := new(big.Int).SetString(strings.ReplaceAll(strings.TrimSpace(text), "_", ""), 10)
		if !ok {
			return nil, errors.New(tagValueNotInt, "Enter a whole number", "text", text)
		}
		return &pickle.Value{Kind: kind, Int: n}, nil
	case pickle.KindFloat:
		f, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			return nil, errors.NewErr(tagValueNotFloat, err).SetMessage("Enter a number").SetMetadata("text", text)
		}
		return &pickle.Value{Kind: kind, Float: f}, nil
	case pickle.KindString, pickle.KindByteString:
		return &pickle.Value{Kind: kind, Str: text}, nil
	}
	return nil, errors.New(tagValueNotEditable, "This value cannot be edited", "kind", kind.String())
}

// sameValue reports whether two primitive values are equal.
func sameValue(a, b *pickle.Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case pickle.KindInt:
		return a.Int.Cmp(b.Int) == 0
	case pickle.KindFloat:
		return a.Float == b.Float
	case pickle.KindBool:
		return a.Bool == b.Bool
	case pickle.KindString, pickle.KindByteString:
		return a.Str == b.Str
	}
	return a.Kind == pickle.KindNone
}

// The tags of the errors that this file makes.
const (
	tagValueNotEditable = "ValueNotEditable"
	tagValueNotFloat    = "ValueNotFloat"
	tagValueNotInt      = "ValueNotInt"
)
