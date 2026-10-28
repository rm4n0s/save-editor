package renpysave

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // Ren'Py signs saves over SHA-1; the format is not ours to choose.
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"math/big"
	"os"
	"strings"

	"github.com/rm4n0s/errors"
)

// KeysFileName is the name of the file in Ren'Py's tokens folder that holds the keys of the
// user: "signing-key" lines with a private key, and "verifying-key" lines with the public keys
// of saves the user chose to trust.
const KeysFileName = "security_keys.txt"

// KeyRing holds the keys of a security_keys.txt file.
type KeyRing struct {
	signing   []*ecdsa.PrivateKey
	verifying [][]byte // SubjectPublicKeyInfo DER, as Ren'Py compares them
}

// LoadKeyRing reads a security_keys.txt file.
func LoadKeyRing(ctx context.Context, path string) (*KeyRing, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.NewErr(tagKeysUnreadable, err).SetMetadata("path", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.NewErr(tagKeysUnreadable, err).SetMetadata("path", path)
	}
	return ParseKeyRing(string(data))
}

// ParseKeyRing reads the text of a security_keys.txt file. Lines it does not know are skipped,
// as Ren'Py skips them; a file without a key is an error.
func ParseKeyRing(text string) (*KeyRing, error) {
	k := &KeyRing{}
	for _, line := range strings.Split(text, "\n") {
		kind, first, _, ok := decodeLine(line)
		if !ok {
			continue
		}
		switch kind {
		case "signing-key":
			priv, err := parsePrivateKey(first)
			if err != nil {
				return nil, err
			}
			pub, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
			if err != nil {
				return nil, errors.NewErr(tagKeyInvalid, err)
			}
			k.signing = append(k.signing, priv)
			k.verifying = append(k.verifying, pub)
		case "verifying-key":
			k.verifying = append(k.verifying, first)
		}
	}
	if len(k.verifying) == 0 {
		return nil, errors.New(tagKeysMissing, "the file has no signing-key or verifying-key line")
	}
	return k, nil
}

// CanSign reports whether the ring has a signing key.
func (k *KeyRing) CanSign() bool { return len(k.signing) > 0 }

// Sign returns the text of a save's "signatures" file for data: one line for each signing key,
// with its public key and an ECDSA P-256 signature of the SHA-1 of data, as Ren'Py's
// savetoken.sign_data writes it.
func (k *KeyRing) Sign(data []byte) (string, error) {
	digest := sha1.Sum(data) //nolint:gosec // see the import
	var out strings.Builder
	for _, priv := range k.signing {
		der, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
		if err != nil {
			return "", errors.NewErr(tagSignFailed, err)
		}
		var sig struct{ R, S *big.Int }
		if _, err := asn1.Unmarshal(der, &sig); err != nil {
			return "", errors.NewErr(tagSignFailed, err)
		}
		raw := make([]byte, 64)
		sig.R.FillBytes(raw[:32])
		sig.S.FillBytes(raw[32:])
		pub, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
		if err != nil {
			return "", errors.NewErr(tagSignFailed, err)
		}
		out.WriteString("signature " + base64.StdEncoding.EncodeToString(pub) + " " + base64.StdEncoding.EncodeToString(raw) + "\n")
	}
	return out.String(), nil
}

// trusts reports whether der is one of the ring's public keys.
func (k *KeyRing) trusts(der []byte) bool {
	if k == nil {
		return false
	}
	for _, v := range k.verifying {
		if bytes.Equal(v, der) {
			return true
		}
	}
	return false
}

// SignatureStatus says what the signatures of a save prove about its log.
type SignatureStatus int

const (
	// Unsigned is a save without signatures: Ren'Py asks the user once before loading it.
	Unsigned SignatureStatus = iota
	// SignedTrusted is a save signed by a key of the key ring: Ren'Py loads it without asking.
	SignedTrusted
	// SignedUntrusted is a save signed correctly by a key the ring does not have: Ren'Py asks
	// whether to trust that key.
	SignedUntrusted
	// SignedInvalid is a save whose signatures do not match its log: Ren'Py refuses to load
	// it after asking.
	SignedInvalid
)

// String describes the status for the user.
func (s SignatureStatus) String() string {
	switch s {
	case SignedTrusted:
		return "Signed with your key"
	case SignedUntrusted:
		return "Signed on another computer"
	case SignedInvalid:
		return "Signature does not match"
	default:
		return "Not signed"
	}
}

// VerifySignatures checks the "signatures" text of a save against its log. ring may be nil.
func VerifySignatures(log []byte, signatures string, ring *KeyRing) SignatureStatus {
	digest := sha1.Sum(log) //nolint:gosec // see the import
	status := Unsigned
	for _, line := range strings.Split(signatures, "\n") {
		kind, keyDER, sig, ok := decodeLine(line)
		if !ok || kind != "signature" || sig == nil {
			continue
		}
		status = SignedInvalid
		pub, err := parsePublicKey(keyDER)
		if err != nil || len(sig) != 64 {
			continue
		}
		r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(pub, digest[:], r, s) {
			continue
		}
		if ring.trusts(keyDER) {
			return SignedTrusted
		}
		status = SignedUntrusted
	}
	return status
}

// decodeLine reads "kind base64 [base64]", as savetoken.decode_line does. A blank line, a
// comment or bad base64 is not ok.
func decodeLine(line string) (kind string, first, second []byte, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
		return "", nil, nil, false
	}
	first, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", nil, nil, false
	}
	if len(fields) > 2 {
		if second, err = base64.StdEncoding.DecodeString(fields[2]); err != nil {
			return "", nil, nil, false
		}
	}
	return fields[0], first, second, true
}

// parsePrivateKey reads a P-256 private key as SEC1 DER (what Ren'Py writes), PKCS #8 DER, or
// the 32 bytes of the secret, the forms Ren'Py's ecsign reads.
func parsePrivateKey(der []byte) (*ecdsa.PrivateKey, error) {
	if len(der) == 32 {
		priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), der)
		if err != nil {
			return nil, errors.NewErr(tagKeyInvalid, err)
		}
		return priv, nil
	}
	priv, err := x509.ParseECPrivateKey(der)
	if err != nil {
		key, err8 := x509.ParsePKCS8PrivateKey(der)
		if err8 != nil {
			return nil, errors.NewErr(tagKeyInvalid, err)
		}
		ec, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New(tagKeyInvalid, "the signing key is not an elliptic curve key")
		}
		priv = ec
	}
	if priv.Curve != elliptic.P256() {
		return nil, errors.New(tagKeyInvalid, "the signing key is not on the P-256 curve", "curve", priv.Curve.Params().Name)
	}
	return priv, nil
}

// parsePublicKey reads a P-256 public key as SubjectPublicKeyInfo DER, or as the raw point of
// 64 or 65 bytes.
func parsePublicKey(der []byte) (*ecdsa.PublicKey, error) {
	if len(der) == 64 {
		der = append([]byte{4}, der...)
	}
	if len(der) == 65 {
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), der)
		if err != nil {
			return nil, errors.NewErr(tagKeyInvalid, err)
		}
		return pub, nil
	}
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, errors.NewErr(tagKeyInvalid, err)
	}
	pub, ok := key.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, errors.New(tagKeyInvalid, "the verifying key is not a P-256 key")
	}
	return pub, nil
}

// The tags of the errors that this file makes.
const (
	tagKeysUnreadable = "KeysUnreadable"
	tagKeysMissing    = "KeysMissing"
	tagKeyInvalid     = "KeyInvalid"
	tagSignFailed     = "SignFailed"
)
