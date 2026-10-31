package gvas

import (
	"fmt"
	"strings"

	"github.com/rm4n0s/errors"
)

// magic is the first four bytes of every SaveGame file.
const magic = "GVAS"

// customVersion is one entry of the custom version table of the header.
type customVersion struct {
	guid    [16]byte
	version int32
}

// header is what comes before the properties. The layout is that of UE's FGameSaveHeader
// (GameplayStatics.cpp) as other open-source readers (uesave) document it.
type header struct {
	saveGameVersion int32
	ue4Version      int32
	ue5Version      int32 // -1 when the header has none
	major, minor    uint16
	patch           uint16
	changelist      uint32
	branch          string
	customFormat    uint32
	customs         []customVersion
	className       string
	// tagByte is the byte after the class name that files of Unreal Engine 5.4 and later
	// have; hasTagByte says it is there.
	tagByte    byte
	hasTagByte bool
}

// atLeast reports whether the engine version is at least major.minor.
func (h *header) atLeast(major, minor uint16) bool {
	return h.major > major || (h.major == major && h.minor >= minor)
}

// propertyTag reports whether properties use the layout of Unreal Engine 5.4 and later: a tree
// of type names (FPropertyTypeName) and a flags byte in place of the type, struct, enum and
// bool fields of the older tag.
func (h *header) propertyTag() bool { return h.atLeast(5, 4) }

// guidAndInner reports whether tags have the optional property GUID and arrays, sets and maps
// name the types of their elements (Unreal Engine 4.12 and later).
func (h *header) guidAndInner() bool { return h.atLeast(4, 12) }

// largeWorld reports whether the engine stores vectors as doubles (Unreal Engine 5).
func (h *header) largeWorld() bool { return h.major >= 5 }

// softObjectNew reports whether a soft object path is a top-level asset path with a package
// name and an asset name (package file version UE5 1007 and later) instead of an asset path
// name.
func (h *header) softObjectNew() bool { return h.ue5Version >= 1007 }

// readHeader reads the header and the class name, and leaves the reader at the first property.
func (r *reader) readHeader() error {
	if err := r.need(4); err != nil {
		return err
	}
	if string(r.d[:4]) != magic {
		return errors.New(tagNotGVAS, "the file does not start with GVAS, so it is not an Unreal Engine save")
	}
	r.pos = 4
	h := &r.v
	h.ue5Version = -1
	var err error
	if h.saveGameVersion, err = r.i32(); err != nil {
		return err
	}
	if h.ue4Version, err = r.i32(); err != nil {
		return err
	}
	if h.saveGameVersion >= 3 {
		// uesave notes that 34 is probably game-specific, and that it is not a UE5 version.
		v, err := r.i32()
		if err != nil {
			return err
		}
		if v != 34 {
			h.ue5Version = v
		} else {
			r.pos -= 4
		}
	}
	if h.major, err = r.u16(); err != nil {
		return err
	}
	if h.minor, err = r.u16(); err != nil {
		return err
	}
	if h.patch, err = r.u16(); err != nil {
		return err
	}
	if h.changelist, err = r.u32(); err != nil {
		return err
	}
	if h.branch, err = r.fstring(); err != nil {
		return err
	}
	if h.saveGameVersion >= 2 {
		if h.customFormat, err = r.u32(); err != nil {
			return err
		}
		n, err := r.u32()
		if err != nil {
			return err
		}
		if int64(n)*20 > int64(r.lim-r.pos) {
			return errors.New(tagBadHeader, "the custom version table is longer than the file", "count", n)
		}
		for range n {
			var c customVersion
			if err := r.need(16); err != nil {
				return err
			}
			copy(c.guid[:], r.d[r.pos:])
			r.pos += 16
			if c.version, err = r.i32(); err != nil {
				return err
			}
			h.customs = append(h.customs, c)
		}
	}
	if h.className, err = r.fstring(); err != nil {
		return err
	}
	if h.propertyTag() {
		if h.tagByte, err = r.u8(); err != nil {
			return err
		}
		h.hasTagByte = true
	}
	return nil
}

// engineText is the engine version as UE prints it.
func (h *header) engineText() string {
	s := fmt.Sprintf("%d.%d.%d-%d", h.major, h.minor, h.patch, h.changelist)
	if h.branch != "" {
		s += " " + h.branch
	}
	return s
}

// guidText writes 16 bytes as UE's FGuid::ToString does (four little-endian words in capital
// hexadecimal).
func guidText(b []byte) string {
	var sb strings.Builder
	for i := 0; i < 16; i += 4 {
		fmt.Fprintf(&sb, "%02X%02X%02X%02X", b[i+3], b[i+2], b[i+1], b[i])
	}
	return sb.String()
}
