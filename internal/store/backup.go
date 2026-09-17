package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// BackupTo writes a consistent snapshot of the database to dest via
// VACUUM INTO. Stop-free: concurrent readers and writers keep going; the
func (d *DB) BackupTo(dest string) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("backup destination already exists: %s", dest)
	} else if !os.IsNotExist(err) {
		return err
	}
	_, err := d.Exec(`VACUUM INTO ?`, dest)
	return err
}

// withBackupLock serializes snapshot/publication/retention across processes:
// two concurrent keep=1 runs each protected only their own file, so each
// pruned the other's backup and zero generations survived. A DB mutex cannot
// help separate CLI processes; the flock on a kept lock file does. The lock
// file is never deleted - unlinking while another holder has it open mints
// two independent locks.
func withBackupLock(dir string, fn func() error) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".backup.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another backup is active: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// Snapshot writes data/backups/libteca-<date>.db (VACUUM INTO), mirrors
// coversDir into backupsDir/covers (skipping the thumb cache), prunes the
// oldest libteca-*.db backups beyond keep, and returns the new db path.
// The database snapshot is published only AFTER the covers copy completed,
// so an interrupted backup never advertises a database generation whose
// assets were never written.
func (d *DB) Snapshot(coversDir, backupsDir string, keep int) (string, error) {
	var published string
	err := withBackupLock(backupsDir, func() error {
		if err := os.MkdirAll(backupsDir, 0o700); err != nil {
			return err
		}
		stage, err := os.MkdirTemp(backupsDir, ".libteca-stage-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		tmp := filepath.Join(stage, "snapshot.db")
		if err := d.BackupTo(tmp); err != nil {
			return err
		}
		if err := syncPath(tmp); err != nil {
			return err
		}
		if err := copyTree(coversDir, filepath.Join(backupsDir, "covers")); err != nil {
			return err
		}
		suffix := strings.TrimPrefix(filepath.Base(stage), ".libteca-stage-")
		dest := filepath.Join(backupsDir,
			"libteca-"+time.Now().UTC().Format("20060102-150405.000000000")+"-"+suffix+".db")
		if err := os.Link(tmp, dest); err != nil {
			return err
		}
		if err := syncPath(backupsDir); err != nil {
			return err
		}
		published = dest
		return pruneBackups(backupsDir, keep, dest)
	})
	return published, err
}

func syncPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func copyTree(src, dst string) error {
	var dirs []string
	err := filepath.WalkDir(src, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "thumb" && e.IsDir() {
			return filepath.SkipDir
		}
		if e.IsDir() {
			out := filepath.Join(dst, rel)
			if err := os.MkdirAll(out, 0o700); err != nil {
				return err
			}
			dirs = append(dirs, out)
			return nil
		}
		if !e.Type().IsRegular() {
			return fmt.Errorf("unsupported cover entry: %s", rel)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Join(dst, filepath.Dir(rel)), ".cover-*")
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(tmp, in)
		if err == nil {
			err = tmp.Sync()
		}
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if inErr := in.Close(); err == nil {
			err = inErr
		}
		if err == nil {
			// Replace, never truncate in place: generations share this
			// covers directory, and an interrupted copy must not tear a
			// file an older backup still references.
			err = os.Rename(tmp.Name(), filepath.Join(dst, rel))
		}
		if err != nil {
			os.Remove(tmp.Name())
		}
		return err
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := syncPath(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

// pruneBackups deletes the oldest libteca-*.db backups beyond keep. keep < 1
// prunes nothing — deleting every backup including the just-written one is
func pruneBackups(dir string, keep int, protect string) error {
	if keep < 1 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var dbs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "libteca-") && strings.HasSuffix(e.Name(), ".db") {
			dbs = append(dbs, e.Name())
		}
	}
	protectedPresent := false
	var candidates []string
	for _, name := range dbs {
		p := filepath.Join(dir, name)
		if filepath.Clean(p) == filepath.Clean(protect) {
			protectedPresent = true
			continue
		}
		candidates = append(candidates, p)
	}
	slots := keep
	if protectedPresent {
		slots--
	}
	for i := 0; i < len(candidates)-slots; i++ {
		if err := os.Remove(candidates[i]); err != nil {
			return err
		}
	}
	return nil
}
