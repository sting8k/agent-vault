package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
)

const valuePrefix = "v1:"

var errDecrypt = errors.New("cannot decrypt: wrong key or damaged vault.json")

// keyID is the first 8 bytes of sha256(key), hex. It tells a vault which key it belongs to.
func keyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal encrypts one field value as "v1:<base64(nonce|ciphertext)>". The additional data is
// NAME.field, so a ciphertext moved to another field does not decrypt.
func seal(key []byte, name, field string, plain []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, plain, []byte(name+"."+field))
	return valuePrefix + base64.StdEncoding.EncodeToString(out), nil
}

func open(key []byte, name, field, stored string) ([]byte, error) {
	raw, ok := strings.CutPrefix(stored, valuePrefix)
	if !ok {
		return nil, errDecrypt
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errDecrypt
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	n := gcm.NonceSize()
	if len(b) < n {
		return nil, errDecrypt
	}
	plain, err := gcm.Open(nil, b[:n], b[n:], []byte(name+"."+field))
	if err != nil {
		return nil, errDecrypt
	}
	return plain, nil
}
