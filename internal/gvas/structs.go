package gvas

import (
	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// binKind is how a struct that Unreal serializes natively (not as a list of properties) is laid
// out.
type binKind uint8

const (
	// bkFloatVar is numbers that are float32 in Unreal Engine 4 and float64 in Unreal Engine 5.
	bkFloatVar binKind = iota
	bkFloat32
	bkFloat64
	bkInt32
	bkColor // four bytes, in the order blue, green, red, alpha
	bkInt64
	bkGuid
)

// binSpec is a natively serialized struct: its layout and the names of its fields.
type binSpec struct {
	kind   binKind
	fields []string
}

// binaryStructs are the structs of the engine that a save writes as raw bytes. The layouts are
// those of the structs' Serialize functions in Unreal Engine.
var binaryStructs = map[string]binSpec{
	"Vector":      {bkFloatVar, []string{"x", "y", "z"}},
	"Vector2D":    {bkFloatVar, []string{"x", "y"}},
	"Vector4":     {bkFloatVar, []string{"x", "y", "z", "w"}},
	"Rotator":     {bkFloatVar, []string{"pitch", "yaw", "roll"}},
	"Quat":        {bkFloatVar, []string{"x", "y", "z", "w"}},
	"Vector3f":    {bkFloat32, []string{"x", "y", "z"}},
	"Vector3d":    {bkFloat64, []string{"x", "y", "z"}},
	"Vector2f":    {bkFloat32, []string{"x", "y"}},
	"Vector2d":    {bkFloat64, []string{"x", "y"}},
	"LinearColor": {bkFloat32, []string{"r", "g", "b", "a"}},
	"Color":       {bkColor, []string{"b", "g", "r", "a"}},
	"IntPoint":    {bkInt32, []string{"x", "y"}},
	"IntVector":   {bkInt32, []string{"x", "y", "z"}},
	"DateTime":    {bkInt64, []string{"ticks"}},
	"Timespan":    {bkInt64, []string{"ticks"}},
	"Guid":        {bkGuid, nil},
}

// floatWidth is the width of the floats of a struct of n numbers: from the size of the struct
// or of one element when it is known, else from the engine version.
func floatWidth(n int, c elemCtx, largeWorld bool) (int, error) {
	size := c.structSize
	if size < 0 {
		size = c.hint
	}
	switch {
	case size < 0 && largeWorld:
		return 8, nil
	case size < 0:
		return 4, nil
	case size == 4*n:
		return 4, nil
	case size == 8*n:
		return 8, nil
	}
	return 0, errors.New(tagSizeMismatch, "a struct of numbers has a size that fits neither float32 nor float64",
		"size", size, "numbers", n)
}

// readBinary reads a natively serialized struct.
func (r *reader) readBinary(name string, spec binSpec, parent *span, c elemCtx) (model.Slot, error) {
	start := r.pos
	v := &model.Value{Kind: model.KindObject, Type: name, QualifiedType: "StructProperty"}
	add := func(label string, s model.Slot, err error) error {
		if err != nil {
			return err
		}
		v.Children = append(v.Children, model.Child{Label: label, Slot: s})
		return nil
	}
	switch spec.kind {
	case bkFloatVar, bkFloat32, bkFloat64:
		w := 4
		switch spec.kind {
		case bkFloat64:
			w = 8
		case bkFloatVar:
			var err error
			if w, err = floatWidth(len(spec.fields), c, r.v.largeWorld()); err != nil {
				return model.Slot{}, err
			}
		}
		t := &ptype{name: "FloatProperty"}
		if w == 8 {
			t = &ptype{name: "DoubleProperty"}
		}
		for _, f := range spec.fields {
			s, err := r.readFloat(t, parent, w)
			if err := add(f, s, err); err != nil {
				return model.Slot{}, err
			}
		}
	case bkInt32:
		for _, f := range spec.fields {
			s, err := r.readInt(&ptype{name: "IntProperty"}, parent, 4, true)
			if err := add(f, s, err); err != nil {
				return model.Slot{}, err
			}
		}
	case bkColor:
		for _, f := range spec.fields {
			s, err := r.readInt(&ptype{name: "ByteProperty"}, parent, 1, false)
			if err := add(f, s, err); err != nil {
				return model.Slot{}, err
			}
		}
		// Shown as red, green, blue, alpha; the spans stay in file order.
		ch := v.Children
		v.Children = []model.Child{ch[2], ch[1], ch[0], ch[3]}
	case bkInt64:
		s, err := r.readInt(&ptype{name: "Int64Property"}, parent, 8, true)
		if err != nil {
			return model.Slot{}, err
		}
		s.Value.Type, s.Value.QualifiedType = name, "StructProperty"
		return s, r.checkStructSize(name, start, c)
	case bkGuid:
		at := r.pos
		if err := r.need(16); err != nil {
			return model.Slot{}, err
		}
		text := guidText(r.d[at : at+16])
		r.pos += 16
		site := r.addLeaf(parent, &span{start: at, end: r.pos, sizeAt: -1}, model.KindString, opGuid, 16, false)
		v = &model.Value{Kind: model.KindString, Type: name, QualifiedType: "StructProperty", Str: text}
		return model.Slot{Value: v, Site: site}, r.checkStructSize(name, at, c)
	}
	return model.Slot{Value: v}, r.checkStructSize(name, start, c)
}

// checkStructSize checks that a struct read from start fills the size the file gave it.
func (r *reader) checkStructSize(name string, start int, c elemCtx) error {
	if c.structSize >= 0 && r.pos-start != c.structSize {
		return errors.New(tagSizeMismatch, "a struct does not have the size of its layout",
			"struct", name, "size", c.structSize, "layout", r.pos-start)
	}
	return nil
}
