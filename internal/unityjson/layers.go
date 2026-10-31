package unityjson

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"io"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/jsondoc"
)

// The layers of an Easy Save 3 file.
//
// Easy Save 3 (ES3) writes its JSON text through a stream that can compress and encrypt it
// (ES3Settings.compressionType Gzip, encryptionType AES). The layout of the encrypted file, as
// public descriptions of it agree (see the package comment):
//
//	file = IV || AES-128-CBC(key, IV, PKCS7(plaintext))
//	key  = PBKDF2-HMAC-SHA1(password, salt = IV, 100 iterations, 16 bytes)
//
// with the 16 bytes of the IV random for each file. A gzipped file is a plain gzip stream.
// When both are on, the gzip is inside the encryption. Layers are found by looking at the
// bytes, so either order is read, and each layer is written again in the order it was found.

const (
	blockSize = aes.BlockSize
	// pbkdf2Iterations and keySize are what ES3's AES stream uses.
	pbkdf2Iterations = 100
	keySize          = 16
	// maxInflated bounds the text that a gzip layer may expand to.
	maxInflated = 1 << 29
	// maxLayers is how many layers are peeled off a file.
	maxLayers = 4
)

// defaultPasswords are tried on an encrypted file: the password that Easy Save 3's default
// settings ship with.
var defaultPasswords = []string{"password"}

type layerKind uint8

const (
	layerGzip layerKind = iota
	layerAES
)

// layer is one wrapper of the JSON text.
type layer struct {
	kind layerKind
	// password is the password of an AES layer.
	password string
	// gzipHeader is the header of the stream that a gzip layer was read from.
	gzipHeader gzip.Header
	// xfl is the "extra flags" byte of that header, which gzip.Writer sets by its own rule.
	xfl byte
}

// isGzip reports whether the data begins as a gzip stream with the deflate method.
func isGzip(data []byte) bool {
	return len(data) >= 18 && data[0] == 0x1f && data[1] == 0x8b && data[2] == 8
}

// looksLikeJSON reports whether the text, after a byte order mark and white space, is the
// start of an object or an array.
func looksLikeJSON(b []byte) bool {
	b = bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF"))
	b = bytes.TrimLeft(b, " \t\r\n")
	return len(b) > 0 && (b[0] == '{' || b[0] == '[')
}

// outcome is what unwrap found.
type outcome uint8

const (
	// found: the JSON text, and the layers around it.
	found outcome = iota
	// encryptedOther: the file has the shape of an encrypted file that none of the passwords opens.
	encryptedOther
	// notSave: the file is none of these.
	notSave
)

// newCipher is the AES cipher of a password and an IV: the key is PBKDF2-HMAC-SHA1 of the
// password with the IV as the salt. keyLen is keySize; it is a parameter so that a test can
// make the derivation fail.
func newCipher(password string, iv []byte, keyLen int) (cipher.Block, error) {
	key, err := pbkdf2.Key(sha1.New, password, iv, pbkdf2Iterations, keyLen)
	if err != nil {
		return nil, errors.NewErr(tagCrypto, err)
	}
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.NewErr(tagCrypto, err)
	}
	return c, nil
}

// firstBlock is the first block of the plaintext, to see whether a password could be right
// before the whole file is decrypted.
func firstBlock(data []byte, password string) ([]byte, error) {
	c, err := newCipher(password, data[:blockSize], keySize)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, blockSize)
	cipher.NewCBCDecrypter(c, data[:blockSize]).CryptBlocks(plain, data[blockSize:2*blockSize])
	return plain, nil
}

// decrypt is the plaintext of an encrypted file, without its padding. ok is false when the
// padding is not valid, which is what a wrong password gives.
func decrypt(data []byte, password string) (plain []byte, ok bool, err error) {
	c, err := newCipher(password, data[:blockSize], keySize)
	if err != nil {
		return nil, false, err
	}
	body := data[blockSize:]
	plain = make([]byte, len(body))
	cipher.NewCBCDecrypter(c, data[:blockSize]).CryptBlocks(plain, body)
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > blockSize || pad > len(plain) {
		return nil, false, nil
	}
	for _, b := range plain[len(plain)-pad:] {
		if int(b) != pad {
			return nil, false, nil
		}
	}
	return plain[:len(plain)-pad], true, nil
}

// encrypt is the file of a plaintext: a new IV read from random, then the ciphertext. A new IV
// is made for each file written, because the same IV for two different texts leaks what they
// share at their start; Easy Save reads the IV from the file, so any IV is read.
func encrypt(plain []byte, password string, random io.Reader) ([]byte, error) {
	iv := make([]byte, blockSize)
	if _, err := io.ReadFull(random, iv); err != nil {
		return nil, errors.NewErr(tagCrypto, err)
	}
	c, err := newCipher(password, iv, keySize)
	if err != nil {
		return nil, err
	}
	pad := blockSize - len(plain)%blockSize
	padded := append(bytes.Clone(plain), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, blockSize+len(padded))
	copy(out, iv)
	cipher.NewCBCEncrypter(c, iv).CryptBlocks(out[blockSize:], padded)
	return out, nil
}

// inflate is the text of a gzip stream, and the header it began with.
func inflate(data []byte, limit int) ([]byte, gzip.Header, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, gzip.Header{}, errors.NewErr(tagGzip, err)
	}
	text, err := io.ReadAll(io.LimitReader(zr, int64(limit)+1))
	if err != nil {
		return nil, gzip.Header{}, errors.NewErr(tagGzip, err)
	}
	if len(text) > limit {
		return nil, gzip.Header{}, errors.New(tagGzipLarge, "The save is too large when decompressed", "limit", limit)
	}
	return text, zr.Header, nil
}

// deflate is a gzip stream of the text with the header of the stream the text came from.
func deflate(text []byte, l layer) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Header = l.gzipHeader
	if _, err := zw.Write(text); err != nil {
		return nil, errors.NewErr(tagGzip, err)
	}
	if err := zw.Close(); err != nil {
		return nil, errors.NewErr(tagGzip, err)
	}
	out := buf.Bytes()
	out[8] = l.xfl
	return out, nil
}

// unwrap peels the layers off the data until a JSON text is left. A file with the shape of an
// encrypted one that no password opens is reported as encryptedOther.
//
// The first bytes of an encrypted file are its random IV, which may look like the start of
// JSON (a brace, a bracket, white space) or of gzip; so a file of whole blocks is taken for
// JSON or gzip only when it really parses or inflates, and is tried as ciphertext otherwise.
func unwrap(data []byte) ([]byte, []layer, outcome, error) {
	var layers []layer
	for range maxLayers {
		blocks := len(data) >= 2*blockSize && len(data)%blockSize == 0
		if looksLikeJSON(data) && (!blocks || validJSON(data)) {
			return data, layers, found, nil
		}
		if isGzip(data) {
			text, header, err := inflate(data, maxInflated)
			if err == nil {
				layers = append(layers, layer{kind: layerGzip, gzipHeader: header, xfl: data[8]})
				data = text
				continue
			}
			if !blocks {
				return nil, nil, notSave, err
			}
		}
		if !blocks {
			return nil, layers, notSave, nil
		}
		next, pw, ok, err := tryPasswords(data)
		if err != nil {
			return nil, nil, notSave, err
		}
		if ok {
			layers = append(layers, layer{kind: layerAES, password: pw})
			data = next
			continue
		}
		if looksLikeJSON(data) {
			// Whole blocks of text that begins as JSON: a JSON text with a mistake in it.
			return data, layers, found, nil
		}
		return nil, layers, encryptedOther, nil
	}
	return nil, layers, notSave, nil
}

// validJSON reports whether the text is a JSON document.
func validJSON(text []byte) bool {
	_, err := jsondoc.Parse(text)
	return err == nil
}

// tryPasswords decrypts the data with the first of the default passwords that gives a text
// that is JSON or gzip. The first block is checked before the whole file is decrypted.
func tryPasswords(data []byte) (plain []byte, password string, ok bool, err error) {
	for _, pw := range defaultPasswords {
		block, err := firstBlock(data, pw)
		if err != nil {
			return nil, "", false, err
		}
		if !looksLikeJSON(block) && !isGzipStart(block) {
			continue
		}
		plain, valid, err := decrypt(data, pw)
		if err != nil {
			return nil, "", false, err
		}
		if valid && (looksLikeJSON(plain) || isGzip(plain)) {
			return plain, pw, true, nil
		}
	}
	return nil, "", false, nil
}

// isGzipStart reports whether the bytes begin a gzip stream.
func isGzipStart(b []byte) bool { return len(b) >= 3 && b[0] == 0x1f && b[1] == 0x8b && b[2] == 8 }

// looksEncrypted reports whether the data has the shape of a file that Easy Save 3 encrypted
// and nothing more can be told: at least two blocks, a whole number of blocks, and bytes that
// are not text. A file of random bytes has this shape too, so the caller also asks for the
// name of an Easy Save file.
func looksEncrypted(data []byte) bool {
	if len(data) < 2*blockSize || len(data)%blockSize != 0 {
		return false
	}
	var count [256]int
	text := true
	for _, b := range data {
		count[b]++
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' || b >= 0x7f {
			text = false
		}
	}
	if text {
		return false
	}
	// Ciphertext is close to uniform, ordinary binary data (zeros, small numbers) is not: no
	// byte is more than a quarter of it.
	most := 0
	for _, c := range count {
		most = max(most, c)
	}
	return most*8 <= len(data)
}

// wrap puts the layers back around the text, the innermost first.
func wrap(text []byte, layers []layer) ([]byte, error) {
	for i := len(layers) - 1; i >= 0; i-- {
		var err error
		switch layers[i].kind {
		case layerGzip:
			text, err = deflate(text, layers[i])
		default:
			text, err = encrypt(text, layers[i].password, rand.Reader)
		}
		if err != nil {
			return nil, err
		}
	}
	return text, nil
}

const (
	tagCrypto    = "UnityJSONCrypto"
	tagGzip      = "UnityJSONGzip"
	tagGzipLarge = "UnityJSONGzipLarge"
)
