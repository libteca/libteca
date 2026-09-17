package scan

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	if err := validateScanRoot(abs); err != nil {
		return 0, err
	}
	cbrTool := cbrExtractor()
	var docs []bookDoc
	err = filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
		if cerr := cancelErr(ctx); cerr != nil {
			return cerr
		}
		if err != nil {
			return fmt.Errorf("scan %s: %w", p, err)
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != abs {
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
		docs = append(docs, bookDoc{path: p, name: d.Name(), format: ext[1:], size: fi.Size(), mtime: fi.ModTime().Unix(), mtimeNs: fi.ModTime().UnixNano()})
		tr.seen(p)
		return nil
	})
	if err != nil {
		return 0, err
	}

	sort.Slice(docs, func(i, j int) bool { return natLess(docs[i].path, docs[j].path) })

	count := 0
	for i := range docs {
		if cerr := cancelErr(ctx); cerr != nil {
			return count, cerr
		}
		d := &docs[i]
		if size, mtime, mtimeNs, ok, serr := db.FileStatByPath(d.path); serr == nil && ok && size == d.size && mtime == d.mtime && mtimeNs == d.mtimeNs && mtimeNs != 0 {
			continue
		}
		if perr := probeBook(ctx, d, cbrTool); perr != nil {
			fmt.Fprintf(os.Stderr, "libteca: skip %s: %v\n", d.path, perr)
			continue
		}
		tr.probed()
		if serr := storeBook(db, lib, d, coversDir, tr); serr != nil {
			return count, serr
		}
		count++
	}
	return count, nil
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
	authorPtr := nullable(d.author)
	var descPtr *string
	if d.description != "" {
		descPtr = &d.description
	}
	var workID int64
	err := db.Update(func(tx *store.Tx) error {
		// Files without their own metadata (cbz/pdf beside an epub) must not
		// clobber the work metadata a sibling edition already set, so they link
		// to the existing work without running the unconditional UPDATE.
		if descPtr == nil {
			if id, ok := tx.FindWorkID(lib.ID, d.title, &authorPtr); ok {
				workID = id
			}
		}
		if workID == 0 {
			w := &store.Work{LibraryID: lib.ID, Title: d.title, Author: &authorPtr, Description: descPtr}
			var err error
			workID, err = tx.UpsertWork(w)
			if err != nil {
				return err
			}
			if w.Created {
				tr.work()
			}
		}

		var langPtr *string
		if d.language != "" {
			langPtr = &d.language
		}
		pages := d.pageCount
		e := &store.EditionPages{Edition: store.Edition{WorkID: workID, Format: d.format, Title: d.title, Language: langPtr}, PageCount: &pages}
		editionID, err := tx.UpsertEditionPages(e)
		if err != nil {
			return err
		}

		hash := hashFile(d.path, d.size)
		fr := &store.FileRec{
			EditionID: editionID, Path: d.path, Seq: 1,
			SizeBytes: d.size, MtimeSecs: d.mtime, MtimeNS: d.mtimeNs, Hash: &hash,
			DurationSecs: 0, Chapters: "[]",
		}
		if err := tx.UpsertFile(fr); err != nil {
			return err
		}
		tr.file(fr.Inserted)
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
	return writeBookCover(db, workID, d, coversDir)
}

const sidecarCoverMax = 20 << 20

// readSidecar reads a cover candidate fully bounded: the whole file was
// materialized before any size check, so an oversized "cover" beside the
// media blew the scan's memory budget.
func readSidecar(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, sidecarCoverMax+1))
	if err != nil {
		return nil, err
	}
	if len(data) > sidecarCoverMax {
		return nil, fmt.Errorf("cover sidecar exceeds the %d MB cap", sidecarCoverMax>>20)
	}
	return data, nil
}

// writeCoverFile publishes atomically: a direct WriteFile exposes partially
// written bytes to concurrent readers and never repairs a torn file from a
// previous crash.
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
	if err := writeCoverFile(dst, d.cover); err != nil {
		return err
	}
	return db.SetWorkCover(workID, fmt.Sprintf("%d.jpg", workID))
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
	cover, err := io.ReadAll(io.LimitReader(rc, 20<<20+1))
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
	names, err := cbrList(ctx, p, tool)
	if err != nil {
		return 0, nil, err
	}
	pages := cbrPageNames(names)
	if len(pages) == 0 {
		return 0, nil, nil
	}
	cover, err := cbrExtract(tool, p, pages[0])
	if err != nil {
		return 0, nil, err
	}
	return len(pages), cover, nil
}

// cbrList runs the extractor listing with a hard deadline and an output cap
// that KILLS the child: reading exactly the cap and then calling Wait left
// the child blocked on a full pipe forever for listings past the cap.
func cbrList(ctx context.Context, p, tool string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if tool == "unrar" {
		cmd = exec.CommandContext(ctx, tool, "lb", p)
	} else {
		cmd = exec.CommandContext(ctx, "lsar", p)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	const limit = 4 << 20
	listing, rerr := io.ReadAll(io.LimitReader(stdout, limit+1))
	oversized := len(listing) > limit
	if oversized || rerr != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	werr := cmd.Wait()
	if oversized {
		return nil, fmt.Errorf("archive listing exceeds %d bytes", limit)
	}
	if rerr != nil {
		return nil, rerr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if werr != nil {
		return nil, werr
	}
	out := listing
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	// lsar prints the archive's own name as a header line before the entries
	if tool == "unar" && len(names) > 0 && names[0] == filepath.Base(p) {
		names = names[1:]
	}
	return names, nil
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

func cbrExtract(tool, archive, name string) ([]byte, error) {
	// The cap is enforced WHILE READING and REJECTED when exceeded: the
	// previous shape buffered the full page (Output / ReadFile) first, and
	// truncating at exactly the cap silently produced corrupt covers.
	// max+1 is read so oversize is detectable; an unrar child blocked
	// writing past the limit is killed rather than deadlocking Wait.
	var data []byte
	if tool == "unrar" {
		cmd := exec.Command(tool, "p", "-inul", archive, name)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		buf, rerr := io.ReadAll(io.LimitReader(stdout, cbrMaxCoverBytes+1))
		if rerr == nil && len(buf) > cbrMaxCoverBytes {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
			return nil, fmt.Errorf("cbr page %s decompresses past the %d MB cap", name, cbrMaxCoverBytes>>20)
		}
		data = buf
		if waitErr := cmd.Wait(); rerr == nil && waitErr != nil && len(data) == 0 {
			return nil, waitErr
		}
	} else {
		dir, err := os.MkdirTemp("", "libteca-cbr-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := exec.CommandContext(ctx, tool, "-q", "-f", "-o", dir, archive, name).Run(); err != nil {
			return nil, err
		}
		var tooBig bool
		var extractedTotal int64
		filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			if fi, serr := e.Info(); serr == nil {
				extractedTotal += fi.Size()
			}
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
				return nil
			}
			defer f.Close()
			data, _ = io.ReadAll(io.LimitReader(f, cbrMaxCoverBytes+1))
			if len(data) > cbrMaxCoverBytes {
				tooBig = true
				data = nil
			}
			return nil
		})
		if tooBig {
			return nil, fmt.Errorf("cbr page %s exceeds the %d MB cap", name, cbrMaxCoverBytes>>20)
		}
		if data == nil {
			return nil, fmt.Errorf("unar extracted nothing for %s", name)
		}
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
