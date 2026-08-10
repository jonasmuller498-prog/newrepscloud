package main

import (
	"os"
	"path/filepath"
	"sync/atomic"
)

type DependencyGate struct {
	ariConnected atomic.Bool
	mediaReady   atomic.Bool
}

func (g *DependencyGate) ReadyForDial() bool {
	return g.ariConnected.Load() && g.mediaReady.Load()
}

func checkMediaDirectory(dir string) bool {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return false
	}
	file, err := os.CreateTemp(dir, ".health-*")
	if err != nil {
		return false
	}
	name := file.Name()
	defer os.Remove(filepath.Clean(name))
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write([]byte("ok"))
	}
	closeErr := file.Close()
	return err == nil && closeErr == nil
}
