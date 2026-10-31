package main

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// row is one line of the list of variables: a variable, or a part of one that is expanded.
type row struct {
	// id is the path of labels from the variable, unique in the list.
	id    string
	depth int
	label string
	slot  model.Slot
	// expandable is a row with parts to show; expanded says they are shown.
	expandable bool
	expanded   bool
	// recursive is a container that is inside itself; its parts are not shown again.
	recursive bool
}

// buildRows lists the variables that match filter (case-insensitive, in the name or in the
// text of a primitive value), with the parts of the expanded rows under them.
func buildRows(vars []model.Variable, filter string, showInternal bool, expanded map[string]bool) []row {
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
func matches(v model.Variable, filter string) bool {
	if strings.Contains(strings.ToLower(v.Name), filter) {
		return true
	}
	return v.Slot.Value.IsPrimitive() && strings.Contains(strings.ToLower(v.Slot.Value.Repr()), filter)
}

func appendRow(rows []row, id string, depth int, label string, slot model.Slot, expanded map[string]bool,
	ancestors []*model.Value) []row {
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
	for _, c := range slot.Value.Children {
		rows = appendRow(rows, id+"\x00"+c.Label, depth+1, c.Label, c.Slot, expanded, ancestors)
	}
	return rows
}

// editable reports whether a slot can be changed in the editor: it has a site, and its value
// is a switch, a number or a text.
func editable(slot model.Slot) bool {
	if slot.Site == nil || slot.Value == nil {
		return false
	}
	switch slot.Value.Kind {
	case model.KindBool, model.KindInt, model.KindFloat, model.KindString:
		return true
	}
	return false
}

// editText is the text a field shows for a primitive value.
func editText(v *model.Value) string {
	switch v.Kind {
	case model.KindInt:
		return intOf(v).String()
	case model.KindFloat:
		return model.FormatFloat(v.Float, v.Float32)
	case model.KindString:
		return v.Str
	default:
		return v.Repr()
	}
}

// parseValue reads the text of a field as a new value for the slot, of the kind of the old
// one, within what the slot's site can hold.
func parseValue(slot model.Slot, text string) (*model.Value, error) {
	old, site := slot.Value, slot.Site
	out := &model.Value{Kind: old.Kind, Type: old.Type, QualifiedType: old.QualifiedType, Float32: old.Float32}
	switch old.Kind {
	case model.KindInt:
		n, ok := new(big.Int).SetString(strings.ReplaceAll(strings.TrimSpace(text), "_", ""), 10)
		if !ok {
			return nil, errors.New(tagValueNotInt, rangeMessage(site, "Enter a whole number"), "text", text)
		}
		if site != nil && ((site.IntMin != nil && n.Cmp(site.IntMin) < 0) || (site.IntMax != nil && n.Cmp(site.IntMax) > 0)) {
			return nil, errors.New(tagValueOutOfRange, rangeMessage(site, "Enter a whole number"), "text", text)
		}
		out.Int = n
	case model.KindFloat:
		bits := 64
		if old.Float32 {
			bits = 32
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(text), bits)
		if errors.Is(err, strconv.ErrRange) {
			return nil, errors.NewErr(tagValueOutOfRange, err).SetMessage("This number is too large or too small").SetMetadata("text", text)
		}
		if err != nil {
			return nil, errors.NewErr(tagValueNotFloat, err).SetMessage("Enter a number").SetMetadata("text", text)
		}
		out.Float = f
	case model.KindString:
		if site != nil && site.CheckText != nil {
			if err := site.CheckText(text); err != nil {
				if _, ok := errors.FromError(err); ok {
					return nil, err
				}
				return nil, errors.NewErr(tagValueTextRefused, err).SetMetadata("text", text)
			}
		}
		out.Str = text
	default:
		return nil, errors.New(tagValueNotEditable, "This value cannot be edited", "kind", old.Kind.String())
	}
	return out, nil
}

// rangeMessage is what to ask for in a whole number that the site bounds: "Enter a whole
// number from 0 to 255".
func rangeMessage(site *model.Site, base string) string {
	switch {
	case site == nil:
		return base
	case site.IntMin != nil && site.IntMax != nil:
		return base + " from " + site.IntMin.String() + " to " + site.IntMax.String()
	case site.IntMin != nil:
		return base + " of " + site.IntMin.String() + " or more"
	case site.IntMax != nil:
		return base + " of " + site.IntMax.String() + " or less"
	}
	return base
}

// intOf is the number of a KindInt value, zero when it has none.
func intOf(v *model.Value) *big.Int {
	if v.Int == nil {
		return new(big.Int)
	}
	return v.Int
}

// sameValue reports whether two primitive values are equal.
func sameValue(a, b *model.Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case model.KindInt:
		return intOf(a).Cmp(intOf(b)) == 0
	case model.KindFloat:
		return a.Float == b.Float
	case model.KindBool:
		return a.Bool == b.Bool
	case model.KindString:
		return a.Str == b.Str
	}
	return a.Kind == model.KindNull
}

// The tags of the errors that this file makes.
const (
	tagValueNotEditable = "ValueNotEditable"
	tagValueNotFloat    = "ValueNotFloat"
	tagValueNotInt      = "ValueNotInt"
	tagValueOutOfRange  = "ValueOutOfRange"
	tagValueTextRefused = "ValueTextRefused"
)
