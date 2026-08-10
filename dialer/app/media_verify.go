package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type MediaSpec struct {
	StorageName string
	SHA256      []byte
	ByteSize    int64
	DurationMS  int64
}

func verifyMediaFile(
	dir string, spec MediaSpec, maxDuration time.Duration, maxBytes int64,
) (string, error) {
	if spec.StorageName == "" || filepath.Base(spec.StorageName) != spec.StorageName ||
		len(spec.SHA256) != sha256.Size {
		return "", errors.New("invalid media metadata")
	}
	expectedName := hex.EncodeToString(spec.SHA256) + ".wav"
	if spec.StorageName != expectedName {
		return "", errors.New("media path does not match its digest")
	}
	path := filepath.Join(dir, spec.StorageName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("media file is unavailable")
	}
	if info.Size() != spec.ByteSize || info.Size() <= 0 || info.Size() > maxBytes {
		return "", errors.New("media file size changed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read media: %w", err)
	}
	sum := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(sum[:], spec.SHA256) != 1 {
		return "", errors.New("media digest changed")
	}
	wav, err := validateWAV(data, maxDuration)
	if err != nil || wav.Duration.Milliseconds() != spec.DurationMS {
		return "", errors.New("media format or duration changed")
	}
	return strings.TrimSuffix(spec.StorageName, ".wav"), nil
}

func mediaURI(mediaSHA string) string {
	return "sound:campaigns/" + mediaSHA
}
