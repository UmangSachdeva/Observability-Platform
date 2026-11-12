package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// SecretCipher encrypts secrets that the server must be able to read back
// (API secrets, invite tokens) with AES-256-GCM. The stored form is
// base64(nonce || ciphertext).
type SecretCipher struct {
	aead cipher.AEAD
}

// NewSecretCipher builds a cipher from a base64-encoded 32-byte key.
func NewSecretCipher(encodedKey string) (*SecretCipher, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("must be base64 encoded: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("must decode to 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &SecretCipher{aead: aead}, nil
}

// Encrypt seals plaintext. associatedData is not stored, but the same value
// must be passed to Decrypt; use it to bind a ciphertext to its record.
func (c *SecretCipher) Encrypt(plaintext, associatedData []byte) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := c.aead.Seal(nonce, nonce, plaintext, associatedData)
	return base64.RawStdEncoding.EncodeToString(ciphertext), nil
}

func (c *SecretCipher) Decrypt(encoded string, associatedData []byte) ([]byte, error) {
	data, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	nonceSize := c.aead.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("encrypted secret is truncated")
	}
	return c.aead.Open(nil, data[:nonceSize], data[nonceSize:], associatedData)
}
