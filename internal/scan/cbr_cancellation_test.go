package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/store"
)

const blockingCBRExtractor = `#!/bin/sh
case "$1" in
lb) printf 'p001.jpg\n'; exit 0 ;;
p) printf 'partial-cover' ;;
*) printf 'partial-cover' > "$4/p001.jpg"
   printf '%s' "$4" > "$CBR_DIR_FILE" ;;
esac
printf '%s' "$$" > "$CBR_PID_FILE"
exec sleep 30
`

func blockingCBRFixture(t *testing.T, tool string) (string, string, string) {
	t.Helper()
	dir := fakeExtractor(t, tool, blockingCBRExtractor)
	if tool == "unar" {
		if err := os.WriteFile(filepath.Join(dir, "lsar"), []byte(fakeLsar), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	pidFile := filepath.Join(root, "extractor.pid")
	dirFile := filepath.Join(root, "extractor.dir")
	t.Setenv("CBR_PID_FILE", pidFile)
	t.Setenv("CBR_DIR_FILE", dirFile)
	t.Setenv("TMPDIR", t.TempDir())
	archive := filepath.Join(root, "Comic.cbr")
	if err := os.WriteFile(archive, []byte("Rar!"), 0o644); err != nil {
		t.Fatal(err)
	}
	return archive, pidFile, dirFile
}

func waitCBRProcess(t *testing.T, pidFile string) *os.Process {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			pid, err := strconv.Atoi(string(data))
			if err == nil && pid > 0 {
				process, err := os.FindProcess(pid)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_ = process.Kill()
					_ = process.Release()
				})
				return process
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("extractor did not start")
	return nil
}

func assertCBRStopped(t *testing.T, process *os.Process, dirFile string) {
	t.Helper()
	if err := process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("extractor is still running or unreaped: %v", err)
	}
	data, err := os.ReadFile(dirFile)
	if err == nil {
		if _, err := os.Stat(string(data)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("temporary extraction directory was not removed: %v", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("temporary extraction files remain: %v", entries)
	}
}

func TestProbeCBRCancellation(t *testing.T) {
	for _, tool := range []string{"unrar", "unar"} {
		t.Run(tool, func(t *testing.T) {
			archive, pidFile, dirFile := blockingCBRFixture(t, tool)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				pages, cover, err := probeCBR(ctx, archive, tool)
				if pages != 0 || cover != nil {
					err = errors.Join(err, fmt.Errorf("cancelled probe returned %d pages and %q cover", pages, cover))
				}
				done <- err
			}()
			process := waitCBRProcess(t, pidFile)
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("probe error = %v, want context.Canceled", err)
				}
			case <-time.After(time.Second):
				_ = process.Kill()
				err := <-done
				t.Errorf("cancelled extraction did not stop; forced cleanup returned %v", err)
			}
			assertCBRStopped(t, process, dirFile)
		})
	}
}

func TestScanCBRCancellationDoesNotPublish(t *testing.T) {
	for _, tool := range []string{"unrar", "unar"} {
		t.Run(tool, func(t *testing.T) {
			archive, pidFile, dirFile := blockingCBRFixture(t, tool)
			db := openScanDB(t)
			root := filepath.Dir(archive)
			libID, err := db.AddLibrary("Comics", "comics", root)
			if err != nil {
				t.Fatal(err)
			}
			covers := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				n, err := Library(ctx, db, &store.Library{ID: libID, Type: "comics", Path: root}, covers, nil)
				if n != 0 {
					err = errors.Join(err, fmt.Errorf("cancelled scan published %d books", n))
				}
				done <- err
			}()
			process := waitCBRProcess(t, pidFile)
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("scan error = %v, want context.Canceled", err)
				}
			case <-time.After(time.Second):
				_ = process.Kill()
				err := <-done
				t.Errorf("cancelled scan did not stop; forced cleanup returned %v", err)
			}
			assertCBRStopped(t, process, dirFile)
			for _, table := range []string{"works", "editions", "files"} {
				var count int
				if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Errorf("%s has %d rows after cancellation", table, count)
				}
			}
			entries, err := os.ReadDir(covers)
			if err != nil || len(entries) != 0 {
				t.Errorf("cancelled scan published cover files: %v, %v", entries, err)
			}
		})
	}
}

func TestProbeCBRRejectsFailedPartialOutput(t *testing.T) {
	fakeExtractor(t, "unrar", "#!/bin/sh\ncase \"$1\" in\nlb) printf 'p001.jpg\\n' ;;\np) printf 'partial-cover'; exit 2 ;;\nesac\n")
	pages, cover, err := probeCBR(context.Background(), "Comic.cbr", "unrar")
	if err == nil || pages != 0 || cover != nil {
		t.Fatalf("failed extraction returned pages=%d, cover=%q, error=%v", pages, cover, err)
	}
	if !strings.Contains(err.Error(), "exit status 2") {
		t.Fatalf("error = %v, want extractor failure", err)
	}
}

func TestProbeCBRDeadline(t *testing.T) {
	for _, tool := range []string{"unrar", "unar"} {
		t.Run(tool, func(t *testing.T) {
			archive, pidFile, dirFile := blockingCBRFixture(t, tool)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, _, err := probeCBR(ctx, archive, tool)
				done <- err
			}()
			process := waitCBRProcess(t, pidFile)
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("probe error = %v, want context.DeadlineExceeded", err)
				}
			case <-time.After(2 * time.Second):
				_ = process.Kill()
				err := <-done
				t.Errorf("extraction exceeded caller deadline; forced cleanup returned %v", err)
			}
			assertCBRStopped(t, process, dirFile)
		})
	}
}

func TestCBRListCancellation(t *testing.T) {
	for _, tool := range []string{"unrar", "unar"} {
		t.Run(tool, func(t *testing.T) {
			command := tool
			if tool == "unar" {
				command = "lsar"
			}
			fakeExtractor(t, command, "#!/bin/sh\nprintf 'p001.jpg\\n'\nprintf '%s' \"$$\" > \"$CBR_PID_FILE\"\nexec sleep 30\n")
			pidFile := filepath.Join(t.TempDir(), "extractor.pid")
			t.Setenv("CBR_PID_FILE", pidFile)
			t.Setenv("TMPDIR", t.TempDir())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				names, err := cbrList(ctx, "Comic.cbr", tool)
				if names != nil {
					err = errors.Join(err, fmt.Errorf("cancelled listing returned %v", names))
				}
				done <- err
			}()
			process := waitCBRProcess(t, pidFile)
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("listing error = %v, want context.Canceled", err)
				}
			case <-time.After(time.Second):
				_ = process.Kill()
				err := <-done
				t.Errorf("cancelled listing did not stop; forced cleanup returned %v", err)
			}
			assertCBRStopped(t, process, filepath.Join(t.TempDir(), "unused"))
		})
	}
}

func TestProbeCBRUnarFailureCleansPartialOutput(t *testing.T) {
	dir := fakeExtractor(t, "lsar", fakeLsar)
	if err := os.WriteFile(filepath.Join(dir, "unar"), []byte("#!/bin/sh\nprintf 'partial-cover' > \"$4/p001.jpg\"\nprintf '%s' \"$4\" > \"$CBR_DIR_FILE\"\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dirFile := filepath.Join(t.TempDir(), "extractor.dir")
	t.Setenv("CBR_DIR_FILE", dirFile)
	t.Setenv("TMPDIR", t.TempDir())
	pages, cover, err := probeCBR(context.Background(), "Comic.cbr", "unar")
	if err == nil || pages != 0 || cover != nil {
		t.Fatalf("failed extraction returned pages=%d, cover=%q, error=%v", pages, cover, err)
	}
	data, err := os.ReadFile(dirFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(data)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary extraction directory was not removed: %v", err)
	}
}

func TestProbeCBRCoverLimit(t *testing.T) {
	for _, tool := range []string{"unrar", "unar"} {
		t.Run(tool, func(t *testing.T) {
			script := "#!/bin/sh\ncase \"$1\" in\nlb) printf 'p001.jpg\\n' ;;\np) exec head -c 20971521 /dev/zero ;;\n*) head -c 20971521 /dev/zero > \"$4/p001.jpg\" ;;\nesac\n"
			dir := fakeExtractor(t, tool, script)
			if tool == "unar" {
				if err := os.WriteFile(filepath.Join(dir, "lsar"), []byte(fakeLsar), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("TMPDIR", t.TempDir())
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			pages, cover, err := probeCBR(ctx, "Comic.cbr", tool)
			if err == nil || !strings.Contains(err.Error(), "cap") || pages != 0 || cover != nil {
				t.Fatalf("oversized extraction returned pages=%d, cover bytes=%d, error=%v", pages, len(cover), err)
			}
			entries, err := os.ReadDir(os.TempDir())
			if err != nil || len(entries) != 0 {
				t.Fatalf("oversized extraction left temporary files: %v, %v", entries, err)
			}
		})
	}
}

func TestScanCBRCancelledBeforeStoreDoesNotPublish(t *testing.T) {
	fakeExtractor(t, "unrar", fakeUnrar)
	db := openScanDB(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Comic.cbr"), []byte("Rar!"), 0o644); err != nil {
		t.Fatal(err)
	}
	libID, err := db.AddLibrary("Comics", "comics", root)
	if err != nil {
		t.Fatal(err)
	}
	covers := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var tr *tracker
	tr = newTracker(func(p Progress) {
		if p.FilesProbed == 1 {
			cancel()
		} else {
			tr.mu.Lock()
			tr.last = time.Time{}
			tr.mu.Unlock()
		}
	})
	n, err := scanBooksLibrary(ctx, db, &store.Library{ID: libID, Type: "comics", Path: root}, covers, tr)
	if n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan returned count=%d, error=%v", n, err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cancelled scan stored %d files", count)
	}
}
