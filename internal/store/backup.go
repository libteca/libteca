package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
		if err := copyTree(coversDir, filepath.Join(stage, "covers")); err != nil {
			return err
		}
		if err := syncPath(stage); err != nil {
			return err
		}
		suffix := strings.TrimPrefix(filepath.Base(stage), ".libteca-stage-")
		dest := filepath.Join(backupsDir,
			"gen-"+time.Now().UTC().Format("20060102-150405.000000000")+"-"+suffix)
		if err := os.Rename(stage, dest); err != nil {
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

// pruneBackups deletes the oldest backups beyond keep, counting generation
// directories and legacy libteca-*.db files together. A generation is
// self-contained (snapshot.db + covers) and removed whole; a legacy file
// shares backups/covers, which stays until the last legacy backup is gone.
// keep < 1 prunes nothing — deleting every backup including the just-written
// one is never wanted. protect names the just-published backup and counts
// toward the budget.
func pruneBackups(dir string, keep int, protect string) error {
	if keep < 1 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type backupEntry struct {
		path  string
		key   string
		isGen bool
	}
	var list []backupEntry
	legacy := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if strings.HasPrefix(name, "gen-") {
				list = append(list, backupEntry{path: filepath.Join(dir, name), key: strings.TrimPrefix(name, "gen-"), isGen: true})
			}
			continue
		}
		if strings.HasPrefix(name, "libteca-") && strings.HasSuffix(name, ".db") {
			legacy++
			list = append(list, backupEntry{path: filepath.Join(dir, name), key: strings.TrimSuffix(strings.TrimPrefix(name, "libteca-"), ".db")})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].key < list[j].key })
	protectedPresent := false
	var candidates []backupEntry
	for _, b := range list {
		if filepath.Clean(b.path) == filepath.Clean(protect) {
			protectedPresent = true
			continue
		}
		candidates = append(candidates, b)
	}
	slots := keep
	if protectedPresent {
		slots--
	}
	removedLegacy := 0
	for i := 0; i < len(candidates)-slots; i++ {
		var err error
		if candidates[i].isGen {
			err = os.RemoveAll(candidates[i].path)
		} else {
			err = os.Remove(candidates[i].path)
			removedLegacy++
		}
		if err != nil {
			return err
		}
	}
	if legacy-removedLegacy == 0 {
		if err := os.RemoveAll(filepath.Join(dir, "covers")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
