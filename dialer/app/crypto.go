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
	aead     cipher.AEAD
	phoneKey []byte
	auditKey []byte
}

const cryptoVersion byte = 1

func NewProtector(phoneKey, fieldKey, auditKey []byte) (*Protector, error) {
	if !highEntropy(phoneKey) || !highEntropy(fieldKey) || !highEntropy(auditKey) ||
		!distinctKeys(phoneKey, fieldKey, auditKey) {
		return nil, errors.New("protection keys must be high entropy and distinct")
	}
	derived := keyedDigest(fieldKey, "field-encryption:v1")
	block, err := aes.NewCipher(derived[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Protector{
		aead:     aead,
		phoneKey: append([]byte(nil), phoneKey...),
		auditKey: append([]byte(nil), auditKey...),
	}, nil
}

func (p *Protector) Encrypt(plain string) ([]byte, error) {
	return p.EncryptBytes([]byte(plain))
}

func (p *Protector) EncryptBytes(plain []byte) ([]byte, error) {
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	result := []byte{cryptoVersion}
	result = append(result, nonce...)
	return p.aead.Seal(result, nonce, plain, []byte{cryptoVersion}), nil
}

func (p *Protector) Decrypt(data []byte) (string, error) {
	plain, err := p.DecryptBytes(data)
	return string(plain), err
}

func (p *Protector) DecryptBytes(data []byte) ([]byte, error) {
	n := p.aead.NonceSize()
	if len(data) < n+1 || data[0] != cryptoVersion {
		return nil, errors.New("invalid ciphertext")
	}
	return p.aead.Open(nil, data[1:1+n], data[1+n:], data[:1])
}

func (p *Protector) LookupHash(phone string) []byte {
	mac := hmac.New(sha256.New, p.phoneKey)
	_, _ = mac.Write([]byte("phone-index:v1:" + phone))
	return append([]byte{cryptoVersion}, mac.Sum(nil)...)
}

func (p *Protector) ActorID(token string) string {
	mac := hmac.New(sha256.New, p.auditKey)
	_, _ = mac.Write([]byte("audit-actor:v1:" + token))
	return "v1:" + hex.EncodeToString(mac.Sum(nil)[:8])
}

func (p *Protector) VerifySignature(body []byte, signature string) bool {
	if len(signature) < 4 || signature[:3] != "v1=" {
		return false
	}
	got, err := hex.DecodeString(signature[3:])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, p.auditKey)
	_, _ = mac.Write([]byte("callback:v1:"))
	_, _ = mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

func keyedDigest(key []byte, label string) [32]byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(label))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}
