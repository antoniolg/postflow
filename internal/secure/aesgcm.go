package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Cipher struct {
	aead       cipher.AEAD
	keyVersion int
}

func NewCipherFromBase64(raw string, keyVersion int) (*Cipher, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("decode master key: %w", err)
	}
	if len(decoded) != 32 {
		return nil, fmt.Errorf("master key must decode to 32 bytes, got %d", len(decoded))
	}
	return NewCipher(decoded, keyVersion)
}

func NewCipher(key []byte, keyVersion int) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if keyVersion <= 0 {
		keyVersion = 1
	}
	return &Cipher{aead: aead, keyVersion: keyVersion}, nil
}

func (c *Cipher) KeyVersion() int {
	if c == nil {
		return 0
	}
	return c.keyVersion
}

func (c *Cipher) EncryptJSON(v any) (ciphertext []byte, nonce []byte, err error) {
	if c == nil {
		return nil, nil, fmt.Errorf("cipher is nil")
	}
	plain, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = c.aead.Seal(nil, nonce, plain, nil)
	return ciphertext, nonce, nil
}

func (c *Cipher) DecryptJSON(ciphertext, nonce []byte, out any) error {
	if c == nil {
		return fmt.Errorf("cipher is nil")
	}
	plain, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, out)
}
