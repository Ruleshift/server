package gamejampromo

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
)

type CodeManager struct {
	lookupKey []byte
	aead      cipher.AEAD
}

func NewCodeManager(encodedMasterKey string) (*CodeManager, error) {
	master, err := base64.StdEncoding.DecodeString(encodedMasterKey)
	if err != nil {
		return nil, fmt.Errorf("decode GAMEJAM_CODE_MASTER_KEY: %w", err)
	}
	if len(master) != 32 {
		return nil, fmt.Errorf("GAMEJAM_CODE_MASTER_KEY must decode to exactly 32 bytes")
	}
	lookupKey, err := hkdf.Key(sha256.New, master, nil, "ruleshift/gamejam/code-lookup/v1", 32)
	if err != nil {
		return nil, fmt.Errorf("derive lookup key: %w", err)
	}
	encryptionKey, err := hkdf.Key(sha256.New, master, nil, "ruleshift/gamejam/code-encryption/v1", 32)
	if err != nil {
		return nil, fmt.Errorf("derive encryption key: %w", err)
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("create code cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create code AEAD: %w", err)
	}
	return &CodeManager{lookupKey: lookupKey, aead: aead}, nil
}

func GenerateCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(10_000_000_000))
	if err != nil {
		return "", fmt.Errorf("generate promotion code: %w", err)
	}
	return fmt.Sprintf("%010d", value.Uint64()), nil
}

func (m *CodeManager) LookupHMAC(code string) []byte {
	mac := hmac.New(sha256.New, m.lookupKey)
	_, _ = mac.Write([]byte(code))
	return mac.Sum(nil)
}

func (m *CodeManager) Protect(code string) (ProtectedCode, error) {
	if !codePattern.MatchString(code) {
		return ProtectedCode{}, fmt.Errorf("promotion code must contain exactly 10 digits")
	}
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ProtectedCode{}, fmt.Errorf("generate code nonce: %w", err)
	}
	ciphertext := m.aead.Seal(nil, nonce, []byte(code), nil)
	return ProtectedCode{
		LookupHMAC: m.LookupHMAC(code),
		Ciphertext: ciphertext,
		Nonce:      nonce,
		LastFour:   code[len(code)-4:],
	}, nil
}

func (m *CodeManager) Reveal(value ProtectedCode) (string, error) {
	plaintext, err := m.aead.Open(nil, value.Nonce, value.Ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt promotion code: %w", err)
	}
	code := string(plaintext)
	if !codePattern.MatchString(code) {
		return "", fmt.Errorf("decrypted promotion code has an invalid format")
	}
	if !hmac.Equal(m.LookupHMAC(code), value.LookupHMAC) {
		return "", fmt.Errorf("promotion code integrity check failed")
	}
	return code, nil
}
