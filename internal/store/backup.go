package store

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BackupTo writes a consistent snapshot of the database to dest via
// VACUUM INTO. Stop-free: concurrent readers and writers keep going; the
// output is a fully checkpointed, standalone database.
func (d *DB) BackupTo(dest string) error {
	if _, err := os.Stat(dest); err == nil {
		if err := os.Remove(dest); err != nil {
			return err
		}
	}
	_, err := d.Exec(`VACUUM INTO ?`, dest)
	return err
}

// Snapshot writes data/backups/libteca-<date>.db (VACUUM INTO), mirrors
// coversDir into backupsDir/covers (skipping the thumb cache), prunes the
// oldest libteca-*.db backups beyond keep, and returns the new db path.
// The new backup and its directory are fsynced before any prune runs, so a
// power loss can never lose both the old and the new snapshot at once.
func (d *DB) Snapshot(coversDir, backupsDir string, keep int) (string, error) {
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		return "", err
	}
	dest := filepath.Join(backupsDir, "libteca-"+time.Now().Format("20060102-150405")+".db")
	if err := d.BackupTo(dest); err != nil {
		return "", err
	}
	if err := syncPath(dest); err != nil {
		return "", err
	}
	if err := syncPath(backupsDir); err != nil {
		return "", err
	}
	if err := copyTree(coversDir, filepath.Join(backupsDir, "covers")); err != nil {
		return "", err
	}
	if err := pruneBackups(backupsDir, keep, dest); err != nil {
		return "", err
	}
	return dest, nil
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
	return filepath.WalkDir(src, func(p string, e os.DirEntry, err error) error {
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
			return os.MkdirAll(filepath.Join(dst, rel), 0o700)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(filepath.Join(dst, rel), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// pruneBackups deletes the oldest libteca-*.db backups beyond keep; the file
// named by protect (the just-written snapshot) is never deleted, even when a
// clock-skewed name sorts it into the prune range.
func pruneBackups(dir string, keep int, protect string) error {
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
	for i := 0; i < len(dbs)-keep; i++ {
		path := filepath.Join(dir, dbs[i])
		if path == protect {
			continue
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
