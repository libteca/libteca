package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/resourcebudget"
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
			ctx := &startedCBRDeadline{Context: context.Background(), done: make(chan struct{})}
			defer ctx.stop()
			done := make(chan error, 1)
			go func() {
				_, _, err := probeCBR(ctx, archive, tool)
				done <- err
			}()
			process := waitCBRProcess(t, pidFile)
			cancel := ctx.arm(500 * time.Millisecond)
			defer cancel()
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

func TestUnarTemporaryLimitStopsBeforeExtractorCompletes(t *testing.T) {
	script := "#!/bin/sh\nhead -c 20971521 /dev/zero > \"$4/p001.jpg\"\nprintf '%s' \"$4\" > \"$CBR_DIR_FILE\"\nprintf '%s' \"$$\" > \"$CBR_PID_FILE\"\nexec sleep 30\n"
	fakeExtractor(t, "unar", script)
	pidFile := filepath.Join(t.TempDir(), "pid")
	dirFile := filepath.Join(t.TempDir(), "dir")
	t.Setenv("CBR_PID_FILE", pidFile)
	t.Setenv("CBR_DIR_FILE", dirFile)
	t.Setenv("TMPDIR", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := cbrExtract(ctx, "unar", "Comic.cbr", "p001.jpg")
	if err == nil || !strings.Contains(err.Error(), "cap") || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ongoing output limit: %v", err)
	}
	data, err := os.ReadFile(dirFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(data)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("extractor remains alive: %v", err)
	}
}

func TestCBRExactCoverBoundary(t *testing.T) {
	for _, size := range []int{cbrMaxCoverBytes - 1, cbrMaxCoverBytes, cbrMaxCoverBytes + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			fakeExtractor(t, "unrar", fmt.Sprintf("#!/bin/sh\nexec head -c %d /dev/zero\n", size))
			data, err := cbrExtract(context.Background(), "unrar", "Comic.cbr", "page.jpg")
			if size > cbrMaxCoverBytes {
				if err == nil || data != nil {
					t.Fatalf("size=%d err=%v", len(data), err)
				}
				return
			}
			if err != nil || len(data) != size {
				t.Fatalf("size=%d err=%v", len(data), err)
			}
		})
	}
}

func TestBookPhysicalFileSourceKeys(t *testing.T) {
	db := openScanDB(t)
	root := t.TempDir()
	libID, err := db.AddLibrary("Books", "books", root)
	if err != nil {
		t.Fatal(err)
	}
	lib := &store.Library{ID: libID, Type: "books", Path: root}
	for _, name := range []string{"first.pdf", "second.pdf"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		doc := &bookDoc{path: path, name: name, format: "pdf", title: "Same title", size: int64(len(name)), mtime: 1, mtimeNs: 1}
		if err := storeBook(db, lib, doc, t.TempDir(), newTracker(nil)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Query(`SELECT e.id,e.source_library_id,e.source_key,f.source_library_id FROM editions e JOIN files f ON f.edition_id=e.id ORDER BY e.source_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id, editionSource, fileSource int64
		var key string
		if err := rows.Scan(&id, &editionSource, &key, &fileSource); err != nil {
			t.Fatal(err)
		}
		if editionSource != libID || fileSource != libID || (key != "first.pdf" && key != "second.pdf") {
			t.Fatalf("sources: %d %d %q", editionSource, fileSource, key)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("physical sources collapsed: %v", ids)
	}
	rows.Close()
	doc := &bookDoc{path: filepath.Join(root, "first.pdf"), name: "first.pdf", format: "pdf", title: "Retitled", size: 9, mtime: 2, mtimeNs: 2}
	if err := storeBook(db, lib, doc, t.TempDir(), newTracker(nil)); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.QueryRow(`SELECT id FROM editions WHERE source_library_id=? AND source_key='first.pdf'`, libID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != ids[0] {
		t.Fatalf("retitle replaced source ID %d with %d", ids[0], id)
	}
	var workCount int
	if err := db.QueryRow("SELECT count(*) FROM works").Scan(&workCount); err != nil {
		t.Fatal(err)
	}
	if workCount != 1 {
		t.Fatalf("retitle created %d works", workCount)
	}
}

func TestCBRAggregateTemporaryReservationAndCancellation(t *testing.T) {
	archive, pidFile, dirFile := blockingCBRFixture(t, "unar")
	t.Setenv("LIBTECA_CBR_TEMP_BYTES", strconv.Itoa(cbrMaxCoverBytes))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := cbrExtract(ctx, "unar", archive, "p001.jpg"); done <- err }()
	process := waitCBRProcess(t, pidFile)
	if _, err := cbrExtract(context.Background(), "unar", archive, "p001.jpg"); !errors.Is(err, resourcebudget.ErrLimit) {
		t.Fatalf("aggregate admission: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("extractor cancellation stalled")
	}
	assertCBRStopped(t, process, dirFile)
	budget, err := resourcebudget.ConfiguredBudget("LIBTECA_CBR_TEMP_BYTES", "aggregate CBR temporary disk")
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Reserve(cbrMaxCoverBytes)
	if err != nil {
		t.Fatal(err)
	}
	reservation.Release()
}

func TestCBRMalformedListingReservesBeforeNamesAllocation(t *testing.T) {
	fakeExtractor(t, "unrar", "#!/bin/sh\nhead -c 1000000 /dev/zero | tr '\\000' '\\n'\n")
	t.Setenv("LIBTECA_ARCHIVE_MEMORY_BYTES", strconv.Itoa(5<<20))
	names, err := cbrList(context.Background(), "Comic.cbr", "unrar")
	if !errors.Is(err, resourcebudget.ErrLimit) || names != nil {
		t.Fatalf("names=%d error=%v", len(names), err)
	}
	budget, err := resourcebudget.ArchiveBudget()
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Reserve(5 << 20)
	if err != nil {
		t.Fatal(err)
	}
	reservation.Release()
}

func TestBookSameStampReplacementReprobesAndPublishesDigest(t *testing.T) {
	db := openScanDB(t)
	root := t.TempDir()
	path := filepath.Join(root, "Book.pdf")
	if err := os.WriteFile(path, []byte("/Type /Page original"), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	libID, err := db.AddLibrary("Books", "books", root)
	if err != nil {
		t.Fatal(err)
	}
	lib := &store.Library{ID: libID, Type: "books", Path: root}
	covers := t.TempDir()
	if _, err := Library(context.Background(), db, lib, covers, nil); err != nil {
		t.Fatal(err)
	}
	var fileID, editionID int64
	var before string
	if err := db.QueryRow("SELECT id,edition_id,sha256 FROM files WHERE path=?", path).Scan(&fileID, &editionID, &before); err != nil {
		t.Fatal(err)
	}
	generation, err := db.TimelineGeneration(editionID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := Library(context.Background(), db, lib, covers, nil); err != nil || n != 0 {
		t.Fatalf("warm scan: %d %v", n, err)
	}
	if err := os.WriteFile(path, []byte("/Type /Page replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	if n, err := Library(context.Background(), db, lib, covers, nil); err != nil || n != 1 {
		t.Fatalf("replacement scan: %d %v", n, err)
	}
	var after string
	var afterFile, afterEdition int64
	if err := db.QueryRow("SELECT id,edition_id,sha256 FROM files WHERE path=?", path).Scan(&afterFile, &afterEdition, &after); err != nil {
		t.Fatal(err)
	}
	if after == before || afterFile != fileID || afterEdition != editionID {
		t.Fatalf("digest/identity: %s %s %d %d", before, after, afterFile, afterEdition)
	}
	afterGeneration, err := db.TimelineGeneration(editionID)
	if err != nil {
		t.Fatal(err)
	}
	if afterGeneration == generation {
		t.Fatal("replacement did not invalidate content generation")
	}
}

func TestBookRenameKeepsManuallyGroupedSourceAndMetadata(t *testing.T) {
	db := openScanDB(t)
	root := t.TempDir()
	path := filepath.Join(root, "Book.pdf")
	if err := os.WriteFile(path, []byte("/Type /Page"), 0600); err != nil {
		t.Fatal(err)
	}
	libID, err := db.AddLibrary("Books", "books", root)
	if err != nil {
		t.Fatal(err)
	}
	lib := &store.Library{ID: libID, Type: "books", Path: root}
	covers := t.TempDir()
	if _, err := Library(context.Background(), db, lib, covers, nil); err != nil {
		t.Fatal(err)
	}
	var fileID, editionID int64
	if err := db.QueryRow("SELECT id,edition_id FROM files WHERE path=?", path).Scan(&fileID, &editionID); err != nil {
		t.Fatal(err)
	}
	otherLib, err := db.AddLibrary("Grouped", "books", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	description := "curated sibling description"
	grouped, err := db.UpsertWork(&store.Work{LibraryID: otherLib, Title: "Manual grouping", Description: &description})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE editions SET work_id=? WHERE id=?", grouped, editionID); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(root, "Renamed.pdf")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	if _, err := Library(context.Background(), db, lib, covers, nil); err != nil {
		t.Fatal(err)
	}
	var afterFile, afterEdition, afterWork, source int64
	var key, desc string
	if err := db.QueryRow(`SELECT f.id,e.id,e.work_id,f.source_library_id,e.source_key,w.description FROM files f JOIN editions e ON e.id=f.edition_id JOIN works w ON w.id=e.work_id WHERE f.path=?`, renamed).Scan(&afterFile, &afterEdition, &afterWork, &source, &key, &desc); err != nil {
		t.Fatal(err)
	}
	if afterFile != fileID || afterEdition != editionID || afterWork != grouped || source != libID || key != "Renamed.pdf" || desc != description {
		t.Fatalf("renamed identity: %d %d %d %d %s %s", afterFile, afterEdition, afterWork, source, key, desc)
	}
}

type startedCBRDeadline struct {
	context.Context
	mu        sync.Mutex
	timed     context.Context
	done      chan struct{}
	cancel    context.CancelFunc
	closeOnce sync.Once
}

func (c *startedCBRDeadline) Done() <-chan struct{} { return c.done }
func (c *startedCBRDeadline) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timed == nil {
		return nil
	}
	return c.timed.Err()
}
func (c *startedCBRDeadline) Deadline() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timed == nil {
		return time.Time{}, false
	}
	return c.timed.Deadline()
}
func (c *startedCBRDeadline) arm(duration time.Duration) context.CancelFunc {
	timed, cancel := context.WithTimeout(context.Background(), duration)
	c.mu.Lock()
	c.timed = timed
	c.cancel = cancel
	c.mu.Unlock()
	go func() { <-timed.Done(); c.closeOnce.Do(func() { close(c.done) }) }()
	return cancel
}

func (c *startedCBRDeadline) stop() {
	c.mu.Lock()
	cancel := c.cancel
	if cancel == nil {
		timed, stop := context.WithCancel(context.Background())
		stop()
		c.timed = timed
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.closeOnce.Do(func() { close(c.done) })
}
