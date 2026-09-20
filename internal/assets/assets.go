package assets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// WithCoversLock runs fn while holding the cross-process covers lock.
// Cover mutations take it shared; store.Snapshot takes it exclusive so the
// database snapshot and the cover tree describe one point in time: a cover
// referenced by the snapshot cannot be removed (or republished under a new
// identity) while the generation is being copied. The lock file lives
// beside the covers directory (<dataDir>/.covers.lock); acquisition order
// against .backup.lock is always backup -> covers, writers only ever take
// the covers lock, so there is no cycle.
func WithCoversLock(coversDir string, exclusive bool, fn func() error) error {
	lockPath := filepath.Join(filepath.Dir(coversDir), ".covers.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		err = syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("covers lock: %w", err)
		}
		<-tick.C
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
