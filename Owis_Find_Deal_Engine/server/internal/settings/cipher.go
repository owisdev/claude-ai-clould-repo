package settings

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// encPrefix marks the format of an encrypted value (for a later key
// rotation or algorithm change).
const encPrefix = "v1:"

// Cipher encrypts secret settings with AES-256-GCM. The setting's name is
// authenticated with the value, so a stored secret cannot be copied to
// another setting.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher takes a 32-byte key as 64 hex characters or base64
// (`openssl rand -hex 32` or `openssl rand -base64 32`).
func NewCipher(key string) (*Cipher, error) {
	raw, err := ParseKey(key)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// ParseKey decodes SETTINGS_ENCRYPTION_KEY: 32 bytes as 64 hex characters
// or base64.
func ParseKey(key string) ([]byte, error) {
	key = strings.TrimSpace(key)
	raw, err := hex.DecodeString(key)
	if err == nil && len(raw) == 32 {
		return raw, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(key); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errors.New("SETTINGS_ENCRYPTION_KEY must be 32 bytes as 64 hex characters or base64 (openssl rand -hex 32)")
}

// Encrypt seals the value of setting key.
func (c *Cipher) Encrypt(key, value string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(value), []byte("setting:"+key))
	return encPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// ErrDecrypt means a secret could not be opened: wrong
// SETTINGS_ENCRYPTION_KEY or a damaged value.
var ErrDecrypt = errors.New("secret setting cannot be decrypted (wrong SETTINGS_ENCRYPTION_KEY?)")

// Decrypt opens a value sealed by Encrypt for the same key.
func (c *Cipher) Decrypt(key, sealed string) (string, error) {
	rest, ok := strings.CutPrefix(sealed, encPrefix)
	if !ok {
		return "", ErrDecrypt
	}
	raw, err := base64.RawStdEncoding.DecodeString(rest)
	n := c.aead.NonceSize()
	if err != nil || len(raw) < n {
		return "", ErrDecrypt
	}
	plain, err := c.aead.Open(nil, raw[:n], raw[n:], []byte("setting:"+key))
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}
