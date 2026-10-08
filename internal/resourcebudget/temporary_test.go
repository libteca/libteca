package resourcebudget

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTemporaryRecoveryRemovesOnlyOwnedDeadProcesses(t *testing.T) {
	root := t.TempDir()
	child := exec.Command("sh", "-c", "exit 0")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	dead := child.Process.Pid
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, fmt.Sprintf("%s%d-test", ownedTemporaryPrefix, dead))
	live := filepath.Join(root, fmt.Sprintf("%s%d-live", ownedTemporaryPrefix, os.Getpid()))
	legacy := filepath.Join(root, "libteca-cbr-legacy")
	public := filepath.Join(root, fmt.Sprintf("%s%d-public", ownedTemporaryPrefix, dead))
	for _, dir := range []string{stale, live, legacy, public} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	os.Chmod(public, 0755)
	outside := t.TempDir()
	link := filepath.Join(root, fmt.Sprintf("%s%d-link", ownedTemporaryPrefix, dead))
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := SweepOwnedTemporaryDirectories(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale output retained", err)
	}
	for _, path := range []string{live, legacy, public, link, outside} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("unowned or active output removed", path, err)
		}
	}
}
