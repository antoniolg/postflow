package secure

import (
	"encoding/base64"
	"testing"
)

func TestCipherEncryptDecryptJSON(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	cipher, err := NewCipher(key, 1)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	payload := map[string]any{
		"access_token":  "token-123",
		"refresh_token": "refresh-123",
	}
	sealed, nonce, err := cipher.EncryptJSON(payload)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(sealed) == 0 || len(nonce) == 0 {
		t.Fatalf("expected ciphertext and nonce")
	}
	var decoded map[string]any
	if err := cipher.DecryptJSON(sealed, nonce, &decoded); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got, _ := decoded["access_token"].(string); got != "token-123" {
		t.Fatalf("unexpected access token %q", got)
	}
}

func TestNewCipherFromBase64RejectsWrongLength(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte("short"))
	if _, err := NewCipherFromBase64(raw, 1); err == nil {
		t.Fatalf("expected error for invalid key length")
	}
}
