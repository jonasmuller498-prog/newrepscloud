package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
)

func keyValue(get func(string) (string, bool), name string) ([]byte, error) {
	raw := value(get, name, "")
	if strings.HasPrefix(raw, "base64:") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, "base64:"))
		if err != nil {
			return nil, fmt.Errorf("%s must contain valid base64", name)
		}
		return decoded, nil
	}
	return []byte(raw), nil
}

func highEntropy(secret []byte) bool {
	if len(secret) < 32 {
		return false
	}
	counts := make(map[byte]int, len(secret))
	maxCount := 0
	for _, value := range secret {
		counts[value]++
		maxCount = max(maxCount, counts[value])
	}
	return len(counts) >= 12 && maxCount*4 <= len(secret)
}

func distinctKeys(keys ...[]byte) bool {
	for i, left := range keys {
		for _, right := range keys[i+1:] {
			if bytes.Equal(left, right) {
				return false
			}
		}
	}
	return true
}

func validEndpointName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("._-", char)) {
			return false
		}
	}
	return true
}
