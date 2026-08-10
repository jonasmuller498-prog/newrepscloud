package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

type Protector struct {
	aead cipher.AEAD
	key  []byte
}

func NewProtector(key []byte) (*Protector, error) {
	if len(key) < 32 {
		return nil, errors.New("protection key is too short")
	}
	derived := sha256.Sum256(append([]byte("dialer-encryption:"), key...))
	block, err := aes.NewCipher(derived[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Protector{aead: aead, key: append([]byte(nil), key...)}, nil
}

func (p *Protector) Encrypt(plain string) ([]byte, error) {
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return p.aead.Seal(nonce, nonce, []byte(plain), nil), nil
}

func (p *Protector) Decrypt(data []byte) (string, error) {
	n := p.aead.NonceSize()
	if len(data) < n {
		return "", errors.New("invalid ciphertext")
	}
	plain, err := p.aead.Open(nil, data[:n], data[n:], nil)
	return string(plain), err
}

func (p *Protector) LookupHash(phone string) []byte {
	mac := hmac.New(sha256.New, p.key)
	_, _ = mac.Write([]byte("phone:" + phone))
	return mac.Sum(nil)
}

func (p *Protector) ActorID(token string) string {
	mac := hmac.New(sha256.New, p.key)
	_, _ = mac.Write([]byte("actor:" + token))
	return hex.EncodeToString(mac.Sum(nil)[:8])
}

func (p *Protector) VerifySignature(body []byte, signature string) bool {
	got, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, p.key)
	_, _ = mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
