package mediafs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenResolvesRegularFilesWithinRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Open(root, filepath.Join(root, "sub", "a.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestOpenRefusesEscapeAndIrregular(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.mkv")
	if err := os.WriteFile(outsideFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "linked.mkv")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linkeddir")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if _, err := Open(root, filepath.Join(root, "..", filepath.Base(outside), "secret.mkv")); err == nil {
		t.Fatal("traversal must be refused")
	}
	if _, err := Open(root, filepath.Join(root, "linked.mkv")); err == nil {
		t.Fatal("symlink escaping the root must be refused")
	}
	if _, err := Open(root, filepath.Join(root, "linkeddir")); err == nil {
		t.Fatal("directory must be refused")
	}
	if _, err := Open(root, root); err == nil {
		t.Fatal("root itself must be refused")
	}
}
