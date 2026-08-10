package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMediaIntegrityAndSharedMode(t *testing.T) {
	data := testWAV(16000, 1, 8000, 16)
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:]) + ".wav"
	dir := t.TempDir()
	if err := writeMediaFile(dir, name, data); err != nil {
		t.Fatal(err)
	}
	spec := MediaSpec{name, sum[:], int64(len(data)), 1000}
	if _, err := verifyMediaFile(dir, spec, 2*time.Second, 1<<20); err != nil {
		t.Fatalf("valid media failed integrity check: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("media mode=%v err=%v", info.Mode().Perm(), err)
	}
	tampered := append([]byte(nil), data...)
	tampered[len(tampered)-1] = 1
	if err = os.WriteFile(filepath.Join(dir, name), tampered, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err = verifyMediaFile(dir, spec, 2*time.Second, 1<<20); err == nil {
		t.Fatal("tampered media passed integrity check")
	}
}

func TestWAVRejectsMultipleDataChunks(t *testing.T) {
	data := testWAV(16000, 1, 8000, 16)
	data = append(data, []byte{'d', 'a', 't', 'a', 2, 0, 0, 0, 0, 0}...)
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	if _, err := validateWAV(data, 2*time.Second); err == nil {
		t.Fatal("WAV with multiple data chunks was accepted")
	}
}
