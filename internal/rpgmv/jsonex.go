package rpgmv

import (
	"strconv"

	"github.com/rm4n0s/save-editor/internal/model"
)

// JsonEx is the engines' JSON with class names. A save is JsonEx.stringify(contents).
//
// MV (rpg_core.js, JsonEx._encode and _decode), every object and array gets an id:
//   - an object has "@c": the id, and for an instance of a class "@": the name of the class
//     (Game_Actor); both are written after the object's own keys;
//   - an array is written as the wrapper {"@c": id, "@a": [...]}, because JSON has no place
//     for an id on an array;
//   - an object met for the second time (shared, or circular) is written as {"@r": id}.
//
// MZ (rmmz_core.js) writes only "@" on an instance of a class, and nothing else; it throws
// at a depth of 100 instead of writing a circular reference.
//
// The tree shows this as the game sees it: "@" is the Type of an object, an array wrapper is
// the list it wraps, a reference is the very same *model.Value as its target, and the
// bookkeeping keys are not shown. They stay in the file, which is only patched.

type jsonEx struct {
	// mv is set for MV, whose JsonEx has ids, wrappers and references.
	mv  bool
	ids map[int64]*model.Value
	// refs are the references to resolve when every id is known.
	refs []reference
}

type reference struct {
	children []model.Child
	index    int
	id       int64
}

// present turns the tree that jsondoc read into the tree of the game's values. The root is
// converted in place.
func present(root model.Slot, mv bool) {
	j := &jsonEx{mv: mv, ids: map[int64]*model.Value{}}
	j.convert(root.Value)
	for _, r := range j.refs {
		if target, ok := j.ids[r.id]; ok {
			r.children[r.index].Slot = model.Slot{Value: target}
			continue
		}
		r.children[r.index].Slot = model.Slot{Value: &model.Value{
			Kind: model.KindOpaque,
			Str:  "reference to missing #" + strconv.FormatInt(r.id, 10),
		}}
	}
}

// intChild is the value of the child with the label, when it is a whole number.
func intChild(v *model.Value, label string) (int64, bool) {
	for _, c := range v.Children {
		if n, ok := intOf(c.Slot.Value); ok && c.Label == label {
			return n, true
		}
	}
	return 0, false
}

// listChild is the list that is the child with the label.
func listChild(v *model.Value, label string) *model.Value {
	for i := len(v.Children) - 1; i >= 0; i-- {
		if c := v.Children[i]; c.Label == label && c.Slot.Value.Kind == model.KindList {
			return c.Slot.Value
		}
	}
	return nil
}

// convert presents the container v and everything under it.
func (j *jsonEx) convert(v *model.Value) {
	var id int64
	var hasID bool
	class := ""
	if v.Kind == model.KindMap {
		kept := make([]model.Child, 0, len(v.Children))
		for _, c := range v.Children {
			switch {
			case c.Label == "@" && c.Slot.Value.Kind == model.KindString && c.Slot.Value.Str != "" && class == "":
				class = c.Slot.Value.Str
				continue
			case j.mv && c.Label == "@c" && !hasID:
				if n, ok := intOf(c.Slot.Value); ok {
					id, hasID = n, true
					continue
				}
			}
			kept = append(kept, c)
		}
		v.Children = kept
		if class != "" {
			v.Kind = model.KindObject
			v.Type = class
		}
	}
	if hasID {
		j.ids[id] = v
	}
	for i, c := range v.Children {
		child := c.Slot.Value
		if child.Kind != model.KindMap && child.Kind != model.KindList {
			continue
		}
		if j.mv && child.Kind == model.KindMap {
			if body := listChild(child, "@a"); body != nil {
				// An array wrapper: the list takes the wrapper's place and its id.
				if n, ok := intChild(child, "@c"); ok {
					j.ids[n] = body
				}
				v.Children[i].Slot = model.Slot{Value: body}
				j.convert(body)
				continue
			}
			if n, ok := intChild(child, "@r"); ok {
				j.refs = append(j.refs, reference{children: v.Children, index: i, id: n})
				continue
			}
		}
		j.convert(child)
	}
}

// find returns the child of a value by its label.
func find(v *model.Value, label string) (model.Slot, bool) {
	if v == nil {
		return model.Slot{}, false
	}
	for _, c := range v.Children {
		if c.Label == label {
			return c.Slot, true
		}
	}
	return model.Slot{}, false
}

// intOf is a value that is a whole number, as an int64.
func intOf(v *model.Value) (int64, bool) {
	if v == nil || v.Kind != model.KindInt || v.Int == nil || !v.Int.IsInt64() {
		return 0, false
	}
	return v.Int.Int64(), true
}
