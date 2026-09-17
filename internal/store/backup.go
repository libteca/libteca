package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BackupTo writes a consistent snapshot of the database to dest via
// VACUUM INTO. Stop-free: concurrent readers and writers keep going; the
// output is a fully checkpointed, standalone database. An existing dest is
// REFUSED, never removed: deleting the previous snapshot before the
// replacement ran could destroy the only good backup when the second
// snapshot fails.
func (d *DB) BackupTo(dest string) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("backup destination already exists: %s", dest)
	} else if !os.IsNotExist(err) {
		return err
	}
	_, err := d.Exec(`VACUUM INTO ?`, dest)
	return err
}

// Snapshot writes data/backups/libteca-<date>.db (VACUUM INTO), mirrors
// coversDir into backupsDir/covers (skipping the thumb cache), prunes the
// oldest libteca-*.db backups beyond keep, and returns the new db path.
// The snapshot is staged in a private directory and published with a
// no-replace link, so two snapshots in the same second - or a repeated
// local clock timestamp - can never overwrite a completed backup. The new
// backup and its directory are fsynced before any prune runs, so a power
// loss can never lose both the old and the new snapshot at once.
func (d *DB) Snapshot(coversDir, backupsDir string, keep int) (string, error) {
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(backupsDir, ".libteca-stage-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	tmp := filepath.Join(stage, "snapshot.db")
	if err := d.BackupTo(tmp); err != nil {
		return "", err
	}
	if err := syncPath(tmp); err != nil {
		return "", err
	}
	suffix := strings.TrimPrefix(filepath.Base(stage), ".libteca-stage-")
	dest := filepath.Join(backupsDir,
		"libteca-"+time.Now().UTC().Format("20060102-150405.000000000")+"-"+suffix+".db")
	// Link publishes without replacing: unlike rename it cannot clobber an
	// existing path, so a colliding name surfaces as an error instead of a
	// destroyed snapshot.
	if err := os.Link(tmp, dest); err != nil {
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
		defer in.Close()
		out, err := os.OpenFile(filepath.Join(dst, rel), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		if err == nil {
			err = out.Sync()
		}
		if closeErr := out.Close(); err == nil {
			err = closeErr
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
// never wanted. The protected file counts toward the budget: the old loop
// skipped it without deleting a replacement candidate, so a clock-skewed
// protected name left keep+1 backups behind.
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
