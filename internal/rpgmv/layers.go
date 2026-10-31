package rpgmv

import (
	"bytes"
	"compress/zlib"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/rm4n0s/errors"
)

// The layers between a save file and its JSON text.
//
// MV (rpg_managers.js, StorageManager.saveToLocalFile and loadFromLocalFile):
//
//	file = LZString.compressToBase64(JsonEx.stringify(contents))
//
// MZ (rmmz_managers.js, StorageManager.jsonToZip, zipToJson, fsWriteFile and fsReadFile):
//
//	zip  = pako.deflate(json, {to: "string", level: 1})  // a string of one character per byte
//	file = fs.writeFileSync(path, zip)                    // as UTF-8: a byte above 0x7F is two bytes
//
// and the file is read back with { encoding: "utf8" } and given to pako.inflate.

const (
	tagNotMV     = "RpgmvNotMV"
	tagNotMZ     = "RpgmvNotMZ"
	tagMZText    = "RpgmvMZText"
	tagMZInflate = "RpgmvMZInflate"
	tagMZDeflate = "RpgmvMZDeflate"
	tagMZLarge   = "RpgmvMZLarge"

	// maxInflated bounds the JSON that a zlib stream may expand to.
	maxInflated = 1 << 29
	// maxUnits bounds the text that an LZString decompression may produce.
	maxUnits = 1 << 29
)

// decodeMV is the JSON text of an MV save file, and the layout of its Base64.
//
// The two layouts of lz-string carry the same bit stream and differ only in how the last
// bits are padded, so either decodes the other's file. The layout is the one whose
// compressToBase64 of the text gives the file back; a file that neither gives back (another
// writer's) is of the layout that MV ships.
func decodeMV(data []byte) ([]byte, lzLayout, error) {
	text := string(data)
	var firstErr error
	for _, layout := range []lzLayout{lzMV, lzDigits} {
		units, err := lzDecompressFromBase64(text, layout, maxUnits)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		json, err := toUTF8(units)
		if err != nil {
			return nil, 0, err
		}
		if !looksLikeJSON(json) {
			continue
		}
		if layout == lzMV && lzCompressToBase64(units, lzMV) != strings.TrimSpace(text) &&
			lzCompressToBase64(units, lzDigits) == strings.TrimSpace(text) {
			layout = lzDigits
		}
		return json, layout, nil
	}
	if firstErr != nil {
		return nil, 0, firstErr
	}
	return nil, 0, errors.New(tagNotMV, "The file is Base64 text, but not the text of an RPG Maker MV save")
}

// encodeMV is the file of a JSON text.
func encodeMV(json []byte, layout lzLayout) []byte {
	return []byte(lzCompressToBase64(toUnits(json), layout))
}

// decodeMZ is the JSON text of an MZ save file.
func decodeMZ(data []byte, limit int) ([]byte, error) {
	zipped := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		r, n := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && n <= 1 {
			return nil, errors.New(tagMZText, "The file is not UTF-8 text, so it is not an RPG Maker MZ save", "offset", i)
		}
		if r > 0xFF {
			return nil, errors.New(tagMZText, "The file has a character that is not one byte of a compressed stream, so it is not an RPG Maker MZ save", "offset", i)
		}
		zipped = append(zipped, byte(r))
		i += n
	}
	zr, err := zlib.NewReader(bytes.NewReader(zipped))
	if err != nil {
		return nil, errors.NewErr(tagMZInflate, err)
	}
	json, err := io.ReadAll(io.LimitReader(zr, int64(limit)+1))
	if err != nil {
		return nil, errors.NewErr(tagMZInflate, err)
	}
	if len(json) > limit {
		return nil, errors.New(tagMZLarge, "The save is too large when decompressed", "limit", limit)
	}
	if !looksLikeJSON(json) {
		return nil, errors.New(tagNotMZ, "The file is compressed, but not the text of an RPG Maker MZ save")
	}
	return json, nil
}

// encodeMZ is the file of a JSON text: zlib at the level (the engine's is 1,
// zlib.BestSpeed), each byte written as the UTF-8 of the character with that number.
func encodeMZ(json []byte, level int) ([]byte, error) {
	var zipped bytes.Buffer
	zw, err := zlib.NewWriterLevel(&zipped, level)
	if err != nil {
		return nil, errors.NewErr(tagMZDeflate, err)
	}
	if _, err := zw.Write(json); err != nil {
		return nil, errors.NewErr(tagMZDeflate, err)
	}
	if err := zw.Close(); err != nil {
		return nil, errors.NewErr(tagMZDeflate, err)
	}
	out := make([]byte, 0, zipped.Len()+zipped.Len()/2)
	for _, b := range zipped.Bytes() {
		out = utf8.AppendRune(out, rune(b))
	}
	return out, nil
}

// looksLikeJSON reports whether the text is an object or an array, the contents of a save.
func looksLikeJSON(b []byte) bool {
	b = bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF"))
	b = bytes.TrimLeft(b, " \t\r\n")
	return len(b) > 0 && (b[0] == '{' || b[0] == '[')
}
