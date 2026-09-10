package scan

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/libteca/libteca/internal/store"
)

// Books/comics scanner (SPEC §3 style): every .epub/.pdf/.cbz file is its own
// edition; files that resolve to the same title+author share one work through
// the works unique-index upsert. .cbr is detected and skipped with a log line.

var bookExts = map[string]bool{".epub": true, ".pdf": true, ".cbz": true}

var cbzImgExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true, ".avif": true,
}

type bookDoc struct {
	path        string
	name        string
	size        int64
	mtime       int64
	format      string
	title       string
	author      string
	language    string
	description string
	pageCount   int
	cover       []byte
	toc         []EpubTOCEntry
}

func scanBooksLibrary(db *store.DB, lib *store.Library, coversDir string, tr *tracker) (int, error) {
	abs, err := filepath.Abs(lib.Path)
	if err != nil {
		return 0, err
	}
	var docs []bookDoc
	err = filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != abs {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext == ".cbr" {
			fmt.Fprintf(os.Stderr, "libteca: cbr not supported yet, skipped: %s\n", p)
			return nil
		}
		if !bookExts[ext] {
			return nil
		}
		fi, _ := d.Info()
		docs = append(docs, bookDoc{path: p, name: d.Name(), format: ext[1:], size: fi.Size(), mtime: fi.ModTime().Unix()})
		tr.seen(p)
		return nil
	})
	if err != nil {
		return 0, err
	}

	sort.Slice(docs, func(i, j int) bool { return natLess(docs[i].path, docs[j].path) })

	count := 0
	for i := range docs {
		d := &docs[i]
		if size, mtime, ok, serr := db.FileStatByPath(d.path); serr == nil && ok && size == d.size && mtime == d.mtime {
			continue
		}
		if perr := probeBook(d); perr != nil {
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

func probeBook(d *bookDoc) error {
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
	// Files without their own metadata (cbz/pdf beside an epub) must not
	// clobber the work metadata a sibling edition already set, so they link
	// to the existing work without running the unconditional UPDATE.
	var workID int64
	if descPtr == nil {
		if id, ok := db.FindWorkID(lib.ID, d.title, &authorPtr); ok {
			workID = id
		}
	}
	if workID == 0 {
		w := &store.Work{LibraryID: lib.ID, Title: d.title, Author: &authorPtr, Description: descPtr}
		var err error
		workID, err = db.UpsertWork(w)
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
	editionID, err := db.UpsertEditionPages(e)
	if err != nil {
		return err
	}

	hash := hashFile(d.path, d.size)
	fr := &store.FileRec{
		EditionID: editionID, Path: d.path, Seq: 1,
		SizeBytes: d.size, MtimeSecs: d.mtime, Hash: &hash,
		DurationSecs: 0, Chapters: "[]",
	}
	if err := db.UpsertFile(fr); err != nil {
		return err
	}
	tr.file(fr.Inserted)
	if len(d.toc) > 0 {
		if meta, merr := json.Marshal(map[string]any{"toc": d.toc}); merr == nil {
			if serr := db.SetFileMeta(fr.ID, string(meta)); serr != nil {
				return serr
			}
		}
	}
	return writeBookCover(db, workID, d, coversDir)
}

func writeBookCover(db *store.DB, workID int64, d *bookDoc, coversDir string) error {
	dst := filepath.Join(coversDir, fmt.Sprintf("%d.jpg", workID))
	if _, err := os.Stat(dst); err == nil {
		return db.SetWorkCover(workID, fmt.Sprintf("%d.jpg", workID))
	}
	if len(d.cover) == 0 {
		for _, name := range []string{"cover.jpg", "Cover.jpg", "folder.jpg", "Folder.jpg"} {
			if data, rerr := os.ReadFile(filepath.Join(filepath.Dir(d.path), name)); rerr == nil {
				d.cover = data
				break
			}
		}
	}
	if len(d.cover) == 0 {
		return nil
	}
	if err := os.WriteFile(dst, d.cover, 0o644); err != nil {
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
	cover, err := io.ReadAll(io.LimitReader(rc, 20<<20))
	if err != nil {
		return 0, nil, err
	}
	return len(names), cover, nil
}

// pdfPageCount is best-effort: counts "/Type /Page" object markers in the raw
// bytes. PDFs that only describe pages inside compressed object streams
// return 0.
func pdfPageCount(p string) int {
	data, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	n := strings.Count(string(data), "/Type /Page") - strings.Count(string(data), "/Type /Pages")
	if n < 0 {
		return 0
	}
	return n
}

func fileTitleAuthor(name string) (string, string) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if i := strings.Index(base, " - "); i > 0 {
		return strings.TrimSpace(base[i+3:]), strings.TrimSpace(base[:i])
	}
	return base, ""
}
