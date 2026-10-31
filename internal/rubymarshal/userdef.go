package rubymarshal

import (
	"encoding/binary"
	"math"
	"math/big"

	"github.com/rm4n0s/save-editor/internal/model"
)

// userDef reads a 'u' value: a class name and the bytes its _dump made. RGSS's Table, Color,
// Tone and Rect are shown as objects; any other class is KindBytes with the class as Type.
func (r *reader) userDef(ivar bool) (*model.Slot, *pending, error) {
	class, err := r.symbol()
	if err != nil {
		return nil, nil, err
	}
	n, err := r.length()
	if err != nil {
		return nil, nil, err
	}
	blobStart := r.pos
	blob, err := r.bytes(n)
	if err != nil {
		return nil, nil, err
	}
	var pend *pending
	if ivar {
		// Ruby reads the instance variables of the data before it makes the object, so
		// what they hold is numbered before the object is.
		if err := r.skipIvars(); err != nil {
			return nil, nil, err
		}
		pend = &pending{consumed: true}
	}
	slot := &model.Slot{Value: r.decodeUserDef(class, blobStart, blob)}
	r.register(slot)
	return slot, pend, nil
}

// decodeUserDef shows the data of an RGSS class, or the bytes of any other.
func (r *reader) decodeUserDef(class string, start int, blob []byte) *model.Value {
	opaque := &model.Value{Kind: model.KindBytes, Type: class, Bytes: blob}
	switch class {
	case "Table":
		// Five int32 (dimensions, xsize, ysize, zsize, count), then count int16 cells.
		if len(blob) < 20 {
			return opaque
		}
		var h [5]int32
		for i := range h {
			h[i] = int32(binary.LittleEndian.Uint32(blob[4*i:]))
		}
		if h[4] < 0 || len(blob) != 20+2*int(h[4]) || h[1] < 0 || h[2] < 0 || h[3] < 0 {
			return opaque
		}
		v := &model.Value{Kind: model.KindObject, Type: class}
		for i, name := range []string{"xsize", "ysize", "zsize"} {
			v.Children = append(v.Children, model.Child{Label: name, Slot: model.Slot{Value: intValue(int64(h[i+1]))}})
		}
		cells := &model.Value{Kind: model.KindBytes, Type: "int16 cells", Bytes: blob[20:]}
		v.Children = append(v.Children, model.Child{Label: "data", Slot: model.Slot{Value: cells}})
		return v
	case "Color", "Tone":
		if len(blob) != 32 {
			return opaque
		}
		names := []string{"red", "green", "blue", "alpha"}
		if class == "Tone" {
			names[3] = "gray"
		}
		v := &model.Value{Kind: model.KindObject, Type: class}
		for i, name := range names {
			f := math.Float64frombits(binary.LittleEndian.Uint64(blob[8*i:]))
			slot := model.Slot{
				Value: &model.Value{Kind: model.KindFloat, Type: "Float", Float: f},
				Site:  r.newSite(&siteRec{kind: skF64, start: start + 8*i, end: start + 8*i + 8}),
			}
			v.Children = append(v.Children, model.Child{Label: name, Slot: slot})
		}
		return v
	case "Rect":
		if len(blob) != 16 {
			return opaque
		}
		v := &model.Value{Kind: model.KindObject, Type: class}
		for i, name := range []string{"x", "y", "width", "height"} {
			n := int32(binary.LittleEndian.Uint32(blob[4*i:]))
			slot := model.Slot{
				Value: intValue(int64(n)),
				Site:  r.newSite(&siteRec{kind: skI32, start: start + 4*i, end: start + 4*i + 4}),
			}
			v.Children = append(v.Children, model.Child{Label: name, Slot: slot})
		}
		return v
	}
	return opaque
}

func intValue(n int64) *model.Value {
	return &model.Value{Kind: model.KindInt, Type: "Integer", Int: big.NewInt(n)}
}
