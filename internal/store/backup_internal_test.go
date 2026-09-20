package store

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/assets"
)

func writeBackupNames(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func libtecaBackups(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && len(n) >= len("libteca-") && n[:8] == "libteca-" && n[len(n)-3:] == ".db" {
			out = append(out, n)
		}
	}
	return out
}

func TestPruneProtectCountsTowardBudget(t *testing.T) {
	dir := t.TempDir()
	writeBackupNames(t, dir,
		"libteca-20200101-000000.db",
		"libteca-20200201-000000.db",
		"libteca-20200301-000000.db",
	)
	protect := filepath.Join(dir, "libteca-20200101-000000.db")
	if err := pruneBackups(dir, 1, protect); err != nil {
		t.Fatal(err)
	}
	left := libtecaBackups(t, dir)
	if len(left) != 1 || left[0] != "libteca-20200101-000000.db" {
		t.Fatalf("protected-oldest keep=1 left %v, want exactly the protected file", left)
	}
}

func TestPruneProtectMiddleBudget(t *testing.T) {
	dir := t.TempDir()
	writeBackupNames(t, dir,
		"libteca-20200101-000000.db",
		"libteca-20200201-000000.db",
		"libteca-20200301-000000.db",
		"libteca-20200401-000000.db",
	)
	protect := filepath.Join(dir, "libteca-20200201-000000.db")
	if err := pruneBackups(dir, 2, protect); err != nil {
		t.Fatal(err)
	}
	left := libtecaBackups(t, dir)
	if len(left) != 2 {
		t.Fatalf("protected-middle keep=2 left %v, want exactly 2", left)
	}
	for _, n := range left {
		if n == "libteca-20200101-000000.db" {
			t.Fatalf("oldest candidate not removed: %v", left)
		}
	}
}

func TestPruneCountsGenerationsAndLegacyTogether(t *testing.T) {
	dir := t.TempDir()
	writeBackupNames(t, dir, "libteca-20200101-000000.db")
	if err := os.MkdirAll(filepath.Join(dir, "covers"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "covers", "7.jpg"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gen-20260101-000000.000000001-a", "gen-20260201-000000.000000001-b"} {
		if err := os.MkdirAll(filepath.Join(dir, name, "covers"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "snapshot.db"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	protect := filepath.Join(dir, "gen-20260201-000000.000000001-b")
	if err := pruneBackups(dir, 2, protect); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "libteca-20200101-000000.db")); !os.IsNotExist(err) {
		t.Fatal("oldest legacy backup not counted against the generation budget")
	}
	if _, err := os.Stat(filepath.Join(dir, "covers")); !os.IsNotExist(err) {
		t.Fatal("shared covers outlived the last legacy backup")
	}
	if _, err := os.Stat(filepath.Join(dir, "gen-20260101-000000.000000001-a", "snapshot.db")); err != nil {
		t.Fatalf("generation inside the keep budget damaged: %v", err)
	}
}

func TestPruneRemovesGenerationWhole(t *testing.T) {
	dir := t.TempDir()
	writeBackupNames(t, dir, "libteca-20200101-000000.db")
	gen := "gen-20260101-000000.000000001-a"
	if err := os.MkdirAll(filepath.Join(dir, gen, "covers"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, gen, "covers", "7.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	protect := filepath.Join(dir, "libteca-20200101-000000.db")
	if err := pruneBackups(dir, 1, protect); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, gen)); !os.IsNotExist(err) {
		t.Fatal("pruned generation directory left behind")
	}
}

func TestCopyTreeRejectsMissingRoot(t *testing.T) {
	dst := t.TempDir()
	if err := copyTree(filepath.Join(t.TempDir(), "absent"), dst); err == nil {
		t.Fatal("missing cover root must fail the copy, not look like an empty success")
	}
}

func TestCopyTreeRejectsNonRegularEntries(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "7.jpg"), []byte("cover"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o600); err != nil {
		t.Skipf("fifo unavailable: %v", err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "escape")); err != nil {
		t.Fatal(err)
	}
	err := copyTree(src, t.TempDir())
	if err == nil {
		t.Fatal("non-regular cover entries must be rejected")
	}
	if !strings.Contains(err.Error(), "pipe") && !strings.Contains(err.Error(), "escape") {
		t.Fatalf("rejection must name the offending entry: %v", err)
	}
}

func internalBackupDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func internalEmptyCovers(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "covers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSnapshotCleansAbandonedStages(t *testing.T) {
	db := internalBackupDB(t)
	backups := t.TempDir()
	abandoned := filepath.Join(backups, ".libteca-stage-abandoned")
	if err := os.MkdirAll(abandoned, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(abandoned, "snapshot.db"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := db.Snapshot(internalEmptyCovers(t), backups, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Fatal("abandoned stage survived a later backup")
	}
	if _, err := os.Stat(filepath.Join(path, "snapshot.db")); err != nil {
		t.Fatalf("new generation missing: %v", err)
	}
}

func TestSnapshotExcludesCoverMutationsDuringCopy(t *testing.T) {
	db := internalBackupDB(t)
	data := t.TempDir()
	covers := filepath.Join(data, "covers")
	if err := os.MkdirAll(covers, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(covers, "7.jpg"), []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	acquired := make(chan struct{})
	release := make(chan struct{})
	released := make(chan struct{})
	go func() {
		_ = assets.WithCoversLock(covers, true, func() error {
			close(acquired)
			<-release
			close(released)
			return nil
		})
	}()
	<-acquired
	done := make(chan struct{})
	var snapErr error
	go func() {
		_, snapErr = db.Snapshot(covers, filepath.Join(data, "backups"), 5)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("snapshot copied covers while a cover mutation held the lock")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	<-released
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("snapshot never completed after the lock was released")
	}
	if snapErr != nil {
		t.Fatalf("snapshot after release: %v", snapErr)
	}
}
