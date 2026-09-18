package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockDataDirExclusive(t *testing.T) {
	dir := t.TempDir()
	release, err := lockDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockDataDir(dir); err == nil {
		t.Fatal("second lock on the same data directory must fail")
	}
	if _, err := os.Stat(filepath.Join(dir, ".server.lock")); err != nil {
		t.Fatalf("lock file must persist while held: %v", err)
	}
	release()
	release()
	release2, err := lockDataDir(dir)
	if err != nil {
		t.Fatalf("lock must be re-acquirable after release: %v", err)
	}
	release2()
}
