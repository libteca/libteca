package scan

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/libteca/libteca/internal/assets"
	"github.com/libteca/libteca/internal/mediafs"
	"github.com/libteca/libteca/internal/resourcebudget"
	"github.com/libteca/libteca/internal/store"
)

// Books/comics scanner (SPEC §3 style): every .epub/.pdf/.cbz file is its own
// edition; files that resolve to the same title+author share one work through
// the works unique-index upsert. .cbr needs an external extractor (unrar or
// unar) on PATH — without one it is detected and skipped with a log line.

var bookExts = map[string]bool{".epub": true, ".pdf": true, ".cbz": true}

var cbzImgExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true, ".avif": true,
}

type bookDoc struct {
	path        string
	name        string
	size        int64
	mtime       int64
	mtimeNs     int64
	format      string
	title       string
	author      string
	language    string
	description string
	pageCount   int
	cover       []byte
	toc         []EpubTOCEntry
}

func scanBooksLibrary(ctx context.Context, db *store.DB, lib *store.Library, coversDir string, tr *tracker) (int, error) {
	abs, err := filepath.Abs(lib.Path)
	if err != nil {
		return 0, err
	}
	walkRoot, toAlias, err := scanRoot(abs)
	if err != nil {
		return 0, err
	}
	cbrTool := cbrExtractor()
	var docs []bookDoc
	err = filepath.WalkDir(walkRoot, func(p string, d os.DirEntry, err error) error {
		if cerr := cancelErr(ctx); cerr != nil {
			return cerr
		}
		if err != nil {
			return fmt.Errorf("scan %s: %w", p, err)
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != walkRoot {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext == ".cbr" && cbrTool == "" {
			fmt.Fprintf(os.Stderr, "libteca: cbr skipped (no unrar/unar on PATH): %s\n", p)
			return nil
		}
		if ext != ".cbr" && !bookExts[ext] {
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("scan %s: %w", p, ierr)
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		stored := toAlias(p)
		docs = append(docs, bookDoc{path: stored, name: d.Name(), format: ext[1:], size: fi.Size(), mtime: fi.ModTime().Unix(), mtimeNs: fi.ModTime().UnixNano()})
		tr.seen(stored)
		return nil
	})
	if err != nil {
		return 0, err
	}
	if _, err := db.MarkMissingLibraryFiles(lib.ID); err != nil {
		return 0, err
	}

	sort.Slice(docs, func(i, j int) bool { return natLess(docs[i].path, docs[j].path) })

	count := 0
	var itemErrors []error
	for i := range docs {
		if cerr := cancelErr(ctx); cerr != nil {
			return count, cerr
		}
		d := &docs[i]
		if fileUnchanged(db, d.path, d.size, d.mtime, d.mtimeNs) {
			continue
		}
		budget, berr := resourcebudget.ArchiveBudget()
		if berr != nil {
			return count, berr
		}
		coverReservationBytes := int64(cbrMaxCoverBytes + 1)
		if d.format == "epub" {
			coverReservationBytes *= 2
		}
		reservation, berr := budget.Reserve(coverReservationBytes)
		if berr != nil {
			return count, berr
		}
		if perr := probeBook(ctx, d, cbrTool); perr != nil {
			d.cover = nil
			reservation.Release()
			if cerr := ctx.Err(); cerr != nil {
				return count, cerr
			}
			itemErrors = append(itemErrors, fmt.Errorf("probe %s: %w", d.path, perr))
			continue
		}
		tr.probed()
		if cerr := ctx.Err(); cerr != nil {
			d.cover = nil
			reservation.Release()
			return count, cerr
		}
		if serr := storeBook(db, lib, d, coversDir, tr); serr != nil {
			d.cover = nil
			reservation.Release()
			return count, serr
		}
		d.cover = nil
		reservation.Release()
		count++
	}
	return count, errors.Join(itemErrors...)
}

func probeBook(ctx context.Context, d *bookDoc, cbrTool string) error {
	switch d.format {
	case "epub":
		info, err := ParseEPUB(d.path)
		if err != nil {
			return err
		}
		d.title, d.author, d.language, d.description = info.Title, info.Author, info.Language, info.Description
		d.pageCount, d.cover, d.toc = info.PageCount, info.Cover, info.TOC
	case "cbz":
		n, cover, err := probeCBZ(d.path)
		if err != nil {
			return err
		}
		d.pageCount, d.cover = n, cover
	case "cbr":
		n, cover, err := probeCBR(ctx, d.path, cbrTool)
		if err != nil {
			return err
		}
		d.pageCount, d.cover = n, cover
	case "pdf":
		d.pageCount = pdfPageCount(d.path)
	}
	fbTitle, fbAuthor := fileTitleAuthor(d.name)
	if d.title == "" {
		d.title = fbTitle
	}
	if d.author == "" {
		d.author = fbAuthor
	}
	if d.title == "" {
		d.title = strings.TrimSuffix(d.name, filepath.Ext(d.name))
	}
	return nil
}

func storeBook(db *store.DB, lib *store.Library, d *bookDoc, coversDir string, tr *tracker) error {
	hash := hashFile(d.path, d.size)
	source, err := mediafs.Open(lib.Path, d.path)
	if err != nil {
		return err
	}
	sha := SHA256Content(source)
	closeErr := source.Close()
	if sha == "" {
		return fmt.Errorf("hash book content: %s", d.path)
	}
	if closeErr != nil {
		return closeErr
	}
	relative, err := filepath.Rel(lib.Path, d.path)
	if err != nil {
		return err
	}
	authorPtr := nullable(d.author)
	var descPtr *string
	if d.description != "" {
		descPtr = &d.description
	}
	var workID int64
	groupWorks, groupAdded, groupUpdated := 0, 0, 0
	err = db.Update(func(tx *store.Tx) error {
		// Files without their own metadata (cbz/pdf beside an epub) must not
		// clobber the work metadata a sibling edition already set, so they link
		// to the existing work without running the unconditional UPDATE.
		var sourceMatches int
		if err := tx.QueryRow(`SELECT count(*) FROM files WHERE source_library_id=? AND (path=? OR (missing=1 AND sha256=?))`, lib.ID, d.path, sha).Scan(&sourceMatches); err != nil {
			return err
		}
		if descPtr == nil && sourceMatches == 0 {
			if id, ok := tx.FindWorkID(lib.ID, d.title, &authorPtr); ok {
				workID = id
			}
		}
		if workID == 0 {
			w := &store.Work{LibraryID: lib.ID, Title: d.title, Author: &authorPtr, Description: descPtr}
			var err error
			workID, err = tx.UpsertSourceWorkDigests(w, []string{d.path}, lib.ID, []string{sha})
			if err != nil {
				return err
			}
			if w.Created {
				groupWorks++
			}
		}

		var langPtr *string
		if d.language != "" {
			langPtr = &d.language
		}
		pages := d.pageCount
		e := &store.EditionPages{Edition: store.Edition{WorkID: workID, Format: d.format, Title: d.title, Language: langPtr, SourceLibraryID: lib.ID, SourceKey: filepath.ToSlash(relative), SourcePaths: []string{d.path}, SourceDigests: []string{sha}}, PageCount: &pages}
		editionID, err := tx.UpsertEditionPages(e)
		if err != nil {
			return err
		}

		fr := &store.FileRec{
			EditionID: editionID, Path: d.path, Seq: 1, SourceLibraryID: lib.ID,
			SizeBytes: d.size, MtimeSecs: d.mtime, MtimeNS: d.mtimeNs, Hash: &hash, SHA256: &sha,
			DurationSecs: 0, Chapters: "[]",
		}
		if err := tx.UpsertFile(fr); err != nil {
			return err
		}
		if fr.Inserted {
			groupAdded++
		} else {
			groupUpdated++
		}
		if len(d.toc) > 0 {
			if meta, merr := json.Marshal(map[string]any{"toc": d.toc}); merr == nil {
				if serr := tx.SetFileMeta(fr.ID, string(meta)); serr != nil {
					return serr
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	tr.workN(groupWorks)
	tr.fileN(groupAdded, groupUpdated)
	return writeBookCover(db, workID, d, coversDir)
}

const sidecarCoverMax = 20 << 20

func readSidecar(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := readCoverBytes(f, sidecarCoverMax)
	if err != nil {
		return nil, err
	}
	if len(data) > sidecarCoverMax {
		return nil, fmt.Errorf("cover sidecar exceeds the %d MB cap", sidecarCoverMax>>20)
	}
	return data, nil
}

func writeCoverFile(dst string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".cover-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, dst)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

func writeBookCover(db *store.DB, workID int64, d *bookDoc, coversDir string) error {
	dst := filepath.Join(coversDir, fmt.Sprintf("%d.jpg", workID))
	if _, err := os.Stat(dst); err == nil {
		return db.SetWorkCover(workID, fmt.Sprintf("%d.jpg", workID))
	}
	if len(d.cover) == 0 {
		for _, name := range []string{"cover.jpg", "Cover.jpg", "folder.jpg", "Folder.jpg"} {
			data, rerr := readSidecar(filepath.Join(filepath.Dir(d.path), name))
			if rerr == nil {
				d.cover = data
				break
			}
		}
	}
	if len(d.cover) == 0 {
		return nil
	}
	return assets.WithCoversLock(coversDir, false, func() error {
		if _, err := os.Stat(dst); err == nil {
			return db.SetWorkCover(workID, fmt.Sprintf("%d.jpg", workID))
		}
		if err := writeCoverFile(dst, d.cover); err != nil {
			return err
		}
		return db.SetWorkCover(workID, fmt.Sprintf("%d.jpg", workID))
	})
}

func probeCBZ(p string) (int, []byte, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return 0, nil, err
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !cbzImgExts[strings.ToLower(filepath.Ext(f.Name))] {
			continue
		}
		if n := filepath.Base(f.Name); strings.HasPrefix(n, ".") || strings.Contains(strings.ToUpper(f.Name), "__MACOSX") {
			continue
		}
		names = append(names, f.Name)
	}
	sort.Slice(names, func(i, j int) bool { return natLess(names[i], names[j]) })
	if len(names) == 0 {
		return 0, nil, nil
	}
	rc, err := zr.Open(names[0])
	if err != nil {
		return 0, nil, err
	}
	defer rc.Close()
	cover, err := readCoverBytes(rc, 20<<20)
	if err != nil {
		return 0, nil, err
	}
	if len(cover) > 20<<20 {
		// Truncating at the cap produced a corrupt cover persisted as valid.
		return len(names), nil, nil
	}
	return len(names), cover, nil
}

// pdfPageCount is best-effort: counts "/Type /Page" object markers in the raw
// bytes. PDFs that only describe pages inside compressed object streams
// return 0.
func pdfPageCount(p string) int {
	// Streamed: materializing multi-GB PDFs (plus a full string copy) blew
	// the process up during routine scans. The marker count survives a
	// chunk-boundary split by overlapping chunks by the marker length.
	f, err := os.Open(p)
	if err != nil {
		return 0
	}
	defer f.Close()
	const markerPage = "/Type /Page"
	const markerPages = "/Type /Pages"
	markerLen := len(markerPages)
	n := 0
	buf := make([]byte, 1<<20)
	// Count only in the freshly-read region; the carried tail exists so a
	// MARKER STRADDLING the boundary is caught, and counting the whole
	// combined string double-counted markers fully contained in the tail.
	tail := ""
	for {
		nr, rerr := f.Read(buf)
		if nr > 0 {
			chunk := string(buf[:nr])
			combined := tail + chunk
			n += strings.Count(combined, markerPage) - strings.Count(combined, markerPages)
			n -= strings.Count(tail, markerPage) - strings.Count(tail, markerPages)
			if len(combined) >= markerLen {
				tail = combined[len(combined)-markerLen:]
			} else {
				tail = combined
			}
		}
		if rerr != nil {
			break
		}
	}
	if n < 0 {
		return 0
	}
	return n
}

const cbrMaxPages = 2000
const cbrMaxCoverBytes = 20 << 20

// cbrExtractor returns "unrar" or "unar" when one is on PATH, else "".
// Resolved once per scan (not cached) so environments and tests can change
// PATH between runs.
func cbrExtractor() string {
	if _, err := exec.LookPath("unrar"); err == nil {
		return "unrar"
	}
	if _, err := exec.LookPath("unar"); err == nil {
		return "unar"
	}
	return ""
}

// probeCBR lists the archive via the extractor (unrar lb / lsar), filters and
// naturally sorts image entries like probeCBZ does, and extracts the first
// page as the cover (unrar p / unar into a temp dir). Zero deps: the archive
// itself is never parsed in-process.
func probeCBR(ctx context.Context, p, tool string) (int, []byte, error) {
	names, releaseListing, err := cbrListReserved(ctx, p, tool)
	if releaseListing != nil {
		defer releaseListing()
	}
	if err != nil {
		return 0, nil, err
	}
	pages := cbrPageNames(names)
	if len(pages) == 0 {
		return 0, nil, nil
	}
	cover, err := cbrExtract(ctx, tool, p, pages[0])
	if err != nil {
		return 0, nil, err
	}
	return len(pages), cover, nil
}

func cbrList(ctx context.Context, p, tool string) ([]string, error) {
	names, release, err := cbrListReserved(ctx, p, tool)
	if release != nil {
		release()
	}
	return names, err
}
func cbrListReserved(ctx context.Context, p, tool string) ([]string, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if tool == "unrar" {
		cmd = cbrCommand(ctx, tool, "lb", p)
	} else {
		cmd = cbrCommand(ctx, "lsar", p)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stopClose := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	defer stopClose()
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	const limit = 4 << 20
	budget, budgetErr := resourcebudget.ArchiveBudget()
	if budgetErr != nil {
		_ = cmd.Cancel()
		_ = cmd.Wait()
		return nil, nil, budgetErr
	}
	listing, listingReservation, rerr := resourcebudget.ReadReserved(stdout, limit, budget)
	defer listingReservation.Release()
	oversized := len(listing) > limit || errors.Is(rerr, resourcebudget.ErrLimit)
	if oversized || rerr != nil {
		if cmd.Process != nil {
			_ = cmd.Cancel()
		}
	}
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if oversized {
		return nil, nil, fmt.Errorf("archive listing exceeds resource cap: %w", rerr)
	}
	if rerr != nil {
		return nil, nil, rerr
	}
	if werr != nil {
		return nil, nil, werr
	}
	entryCount := bytes.Count(listing, []byte{'\n'}) + 1
	namesReservation, err := budget.Reserve(int64(len(listing)) + int64(entryCount)*16)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, entryCount)
	remaining := string(listing)
	for len(remaining) > 0 {
		line, rest, found := strings.Cut(remaining, "\n")
		remaining = rest
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
		if !found {
			break
		}
	}
	// lsar prints the archive's own name as a header line before the entries
	if tool == "unar" && len(names) > 0 && names[0] == filepath.Base(p) {
		names = names[1:]
	}
	return names, namesReservation.Release, nil
}

func cbrPageNames(names []string) []string {
	var out []string
	for _, n := range names {
		if !cbzImgExts[strings.ToLower(filepath.Ext(n))] {
			continue
		}
		if base := filepath.Base(n); strings.HasPrefix(base, ".") || strings.Contains(strings.ToUpper(n), "__MACOSX") {
			continue
		}
		out = append(out, n)
		if len(out) >= cbrMaxPages {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return natLess(out[i], out[j]) })
	return out
}

func cbrExtract(ctx context.Context, tool, archive, name string) (result []byte, resultErr error) {
	// The cap is enforced WHILE READING and REJECTED when exceeded: the
	// previous shape buffered the full page (Output / ReadFile) first, and
	// truncating at exactly the cap silently produced corrupt covers.
	// max+1 is read so oversize is detectable; an unrar child blocked
	// writing past the limit is killed rather than deadlocking Wait.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var data []byte
	if tool == "unrar" {
		cmd := cbrCommand(ctx, tool, "p", "-inul", archive, name)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		stopClose := context.AfterFunc(ctx, func() { _ = stdout.Close() })
		defer stopClose()
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		buf, rerr := readCoverBytes(stdout, cbrMaxCoverBytes)
		oversized := len(buf) > cbrMaxCoverBytes
		if oversized || rerr != nil {
			if cmd.Process != nil {
				_ = cmd.Cancel()
			}
		}
		waitErr := cmd.Wait()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if oversized {
			return nil, &resourcebudget.LimitError{Resource: "CBR page " + name, Limit: cbrMaxCoverBytes, Requested: int64(len(buf))}
		}
		if rerr != nil {
			return nil, rerr
		}
		if waitErr != nil {
			return nil, waitErr
		}
		data = buf
	} else {
		tempBudget, budgetErr := resourcebudget.ConfiguredBudget("LIBTECA_CBR_TEMP_BYTES", "aggregate CBR temporary disk")
		if budgetErr != nil {
			return nil, budgetErr
		}
		tempReservation, budgetErr := tempBudget.Reserve(cbrMaxCoverBytes)
		if budgetErr != nil {
			return nil, budgetErr
		}
		dir, err := resourcebudget.OwnedTemporaryDirectory()
		if err != nil {
			tempReservation.Release()
			return nil, err
		}
		defer func() {
			if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
				result = nil
				resultErr = errors.Join(resultErr, cleanupErr)
			} else {
				tempReservation.Release()
			}
		}()
		runCtx, stop := context.WithCancel(ctx)
		defer stop()
		finish := resourcebudget.Monitor(runCtx, dir, cbrMaxCoverBytes, stop)
		err = cbrCommand(runCtx, tool, "-q", "-f", "-o", dir, archive, name).Run()
		if budgetErr := finish(); budgetErr != nil {
			return nil, budgetErr
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, err
		}
		var tooBig bool
		var extractedTotal int64
		err = filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err != nil {
				return err
			}
			if e.IsDir() {
				return nil
			}
			fi, serr := e.Info()
			if serr != nil {
				return serr
			}
			extractedTotal += fi.Size()
			// unar has no streaming mode; the on-disk stat is the earliest
			// signal, and the running total bounds multi-file spills.
			if extractedTotal > cbrMaxCoverBytes {
				tooBig = true
				return filepath.SkipAll
			}
			if data != nil {
				return nil
			}
			if fi, serr := os.Stat(p); serr == nil && fi.Size() > cbrMaxCoverBytes {
				tooBig = true
				return filepath.SkipAll
			}
			f, ferr := os.Open(p)
			if ferr != nil {
				return ferr
			}
			defer f.Close()
			data, ferr = readCoverBytes(f, cbrMaxCoverBytes)
			if ferr != nil {
				return ferr
			}
			if len(data) > cbrMaxCoverBytes {
				tooBig = true
				data = nil
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if tooBig {
			return nil, &resourcebudget.LimitError{Resource: "CBR temporary output " + name, Limit: cbrMaxCoverBytes, Requested: extractedTotal}
		}
		if data == nil {
			return nil, fmt.Errorf("unar extracted nothing for %s", name)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func fileTitleAuthor(name string) (string, string) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if i := strings.Index(base, " - "); i > 0 {
		return strings.TrimSpace(base[i+3:]), strings.TrimSpace(base[:i])
	}
	return base, ""
}

func readCoverBytes(r io.Reader, max int) ([]byte, error) {
	if configured := os.Getenv("LIBTECA_ARCHIVE_MEMORY_BYTES"); configured == "" || configured == "0" {
		return io.ReadAll(io.LimitReader(r, int64(max+1)))
	}
	data := make([]byte, max+1)
	n, err := io.ReadFull(r, data)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		err = nil
	}
	return data[:n], err
}

func cbrCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	return cmd
}
