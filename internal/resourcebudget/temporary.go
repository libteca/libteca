package resourcebudget

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const ownedTemporaryPrefix = "libteca-cbr-v1-"

func OwnedTemporaryDirectory() (string, error) {
	if err := SweepOwnedTemporaryDirectories(os.TempDir()); err != nil {
		return "", err
	}
	return os.MkdirTemp("", fmt.Sprintf("%s%d-", ownedTemporaryPrefix, os.Getpid()))
}

func SweepOwnedTemporaryDirectories(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ownedTemporaryPrefix) || !entry.IsDir() {
			continue
		}
		pieces := strings.SplitN(strings.TrimPrefix(entry.Name(), ownedTemporaryPrefix), "-", 2)
		if len(pieces) != 2 || pieces[1] == "" {
			continue
		}
		pid, err := strconv.Atoi(pieces[0])
		if err != nil || pid <= 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(st.Uid) != os.Getuid() || info.Mode().Perm() != 0700 {
			continue
		}
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
