package scan

import (
	"archive/zip"
	"context"
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

const epubCoverBytes = "\xff\xd8fakejpg"

// buildEPUB writes a minimal valid EPUB. nav controls whether an EPUB3 nav
// document is included (TOC from nav) or only an NCX (TOC fallback path).
func buildEPUB(t *testing.T, path string, nav bool) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)

	mw, _ := zw.Create("mimetype")
	mw.Write([]byte("application/epub+zip"))

	w, _ := zw.Create("META-INF/container.xml")
	w.Write([]byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`))

	w, _ = zw.Create("OEBPS/content.opf")
	manifest := `    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="cover-image" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>`
	if nav {
		manifest += "\n    " + `<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`
	}
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>The Trial</dc:title>
    <dc:creator>Franz Kafka</dc:creator>
    <dc:language>en</dc:language>
    <dc:description>Arrested one morning.</dc:description>
  </metadata>
  <manifest>
` + manifest + `
    <item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
    <item id="c3" href="ch3.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine toc="ncx">
    <itemref idref="c1"/>
    <itemref idref="c2"/>
    <itemref idref="c3"/>
  </spine>
</package>`
	w.Write([]byte(opf))

	if nav {
		w, _ = zw.Create("OEBPS/nav.xhtml")
		w.Write([]byte(`<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body>
<nav epub:type="toc"><ol>
<li><a href="ch1.xhtml">Arrest</a></li>
<li><a href="ch2.xhtml">The Court</a><ol><li><a href="ch3.xhtml">End</a></li></ol></li>
</ol></nav>
</body></html>`))
	}

	w, _ = zw.Create("OEBPS/toc.ncx")
	w.Write([]byte(`<?xml version="1.0"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <navMap>
    <navPoint id="n1"><navLabel><text>Arrest</text></navLabel><content src="ch1.xhtml"/></navPoint>
    <navPoint id="n2"><navLabel><text>The Court</text></navLabel><content src="ch2.xhtml"/></navPoint>
  </navMap>
</ncx>`))

	w, _ = zw.Create("OEBPS/cover.jpg")
	w.Write([]byte(epubCoverBytes))

	for _, ch := range []string{"ch1.xhtml", "ch2.xhtml", "ch3.xhtml"} {
		w, _ = zw.Create("OEBPS/" + ch)
		w.Write([]byte("<html><body>chapter</body></html>"))
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func buildCBZ(t *testing.T, path string, names ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for i, name := range names {
		w, _ := zw.Create(name)
		w.Write([]byte{byte('p'), byte('0' + i)})
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestParseEPUB(t *testing.T) {
	p := filepath.Join(t.TempDir(), "book.epub")
	buildEPUB(t, p, true)
	info, err := ParseEPUB(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "The Trial" || info.Author != "Franz Kafka" || info.Language != "en" || info.Description != "Arrested one morning." {
		t.Fatalf("metadata = %+v", info)
	}
	if info.PageCount != 3 {
		t.Fatalf("page count = %d, want 3 (spine items)", info.PageCount)
	}
	if !bytes.Equal(info.Cover, []byte(epubCoverBytes)) {
		t.Fatalf("cover = %q", info.Cover)
	}
	if len(info.TOC) != 3 || info.TOC[0].Title != "Arrest" || info.TOC[0].Href != "ch1.xhtml" || info.TOC[2].Title != "End" {
		t.Fatalf("nav toc = %+v", info.TOC)
	}
}

func TestParseEPUBNCXFallback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "book.epub")
	buildEPUB(t, p, false)
	info, err := ParseEPUB(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.TOC) != 2 || info.TOC[0].Title != "Arrest" || info.TOC[1].Href != "ch2.xhtml" {
		t.Fatalf("ncx toc = %+v", info.TOC)
	}
}

func TestCBZPageCountNaturalSort(t *testing.T) {
	p := filepath.Join(t.TempDir(), "comic.cbz")
	// written deliberately out of order: p10, p2, p1
	buildCBZ(t, p, "p10.jpg", "p2.jpg", "p1.jpg", "notes.txt")
	n, cover, err := probeCBZ(p)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("page count = %d, want 3 images (notes.txt excluded)", n)
	}
	if !bytes.Equal(cover, []byte("p2")) {
		t.Fatalf("cover = %q, want naturally-first image p1.jpg", cover)
	}
}

func TestPDFPageCount(t *testing.T) {
	p := filepath.Join(t.TempDir(), "doc.pdf")
	body := "%PDF-1.4\n1 0 obj<</Type /Page>>endobj\n2 0 obj<</Type /Page>>endobj\n3 0 obj<</Type /Pages/Kids[1 0 R 2 0 R]>>endobj\n%%EOF"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := pdfPageCount(p); n != 2 {
		t.Fatalf("page count = %d, want 2", n)
	}
	// Compressed object streams yield no markers: fallback 0.
	os.WriteFile(p, []byte("%PDF-1.7 binary-stream"), 0o644)
	if n := pdfPageCount(p); n != 0 {
		t.Fatalf("compressed fallback = %d, want 0", n)
	}
}

func TestScanBooksLibrary(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := t.TempDir()
	mixed := filepath.Join(root, "Kafka - The Trial")
	solo := filepath.Join(root, "Solo Comic")
	os.MkdirAll(mixed, 0o755)
	os.MkdirAll(solo, 0o755)

	buildEPUB(t, filepath.Join(mixed, "Franz Kafka - The Trial.epub"), true)
	buildCBZ(t, filepath.Join(mixed, "Franz Kafka - The Trial.cbz"), "p02.jpg", "p10.jpg", "p01.jpg")
	pdfBody := "%PDF-1.4\n<</Type /Page>><</Type /Page>>><</Type /Pages>>"
	os.WriteFile(filepath.Join(mixed, "Franz Kafka - The Trial.pdf"), []byte(pdfBody), 0o644)
	buildCBZ(t, filepath.Join(solo, "Solo Comic.cbz"), "a.jpg", "b.jpg")
	os.WriteFile(filepath.Join(solo, "skipped.cbr"), []byte("rar"), 0o644)

	covers := filepath.Join(t.TempDir(), "covers")
	os.MkdirAll(covers, 0o755)
	lib := &store.Library{ID: 1, Type: "books", Path: root}
	// library row not required by scan.Library, but inserted for realism
	db.AddLibrary("Books", "books", root)

	n, err := Library(context.Background(), db, lib, covers, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("scanned = %d, want 4 docs", n)
	}

	works, err := db.WorksInLibrary(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 2 {
		t.Fatalf("works = %d, want 2 (title+author match collapses the folder)", len(works))
	}

	var trial *store.WorkView
	for i := range works {
		if works[i].Title == "The Trial" {
			trial = &works[i]
		}
	}
	if trial == nil {
		t.Fatal("The Trial work missing")
	}
	if trial.Author == nil || *trial.Author != "Franz Kafka" {
		t.Fatalf("author = %v", trial.Author)
	}
	if trial.Description == nil || *trial.Description != "Arrested one morning." {
		t.Fatalf("description = %v", trial.Description)
	}
	if len(trial.Editions) != 3 {
		t.Fatalf("editions = %d, want 3", len(trial.Editions))
	}
	pages := map[string]int{}
	langs := map[string]bool{}
	var epubFileID int64
	for _, ev := range trial.Editions {
		var pc sql.NullInt64
		var lang sql.NullString
		db.QueryRow(`SELECT page_count, language FROM editions WHERE id = ?`, ev.ID).Scan(&pc, &lang)
		if !pc.Valid {
			t.Fatalf("edition %d has NULL page_count", ev.ID)
		}
		pages[ev.Format] = int(pc.Int64)
		if lang.Valid && lang.String == "en" {
			langs[ev.Format] = true
		}
		if len(ev.Files) != 1 || ev.Files[0].Hash == nil || *ev.Files[0].Hash == "" {
			t.Fatalf("edition %d files = %+v", ev.ID, ev.Files)
		}
		if ev.Format == "epub" {
			epubFileID = ev.Files[0].ID
		}
	}
	if pages["epub"] != 3 || pages["cbz"] != 3 || pages["pdf"] != 2 {
		t.Fatalf("page counts = %v, want epub 3 cbz 3 pdf 2", pages)
	}
	if !langs["epub"] {
		t.Fatal("epub edition lost OPF language")
	}

	// TOC stored in embedded_meta for the epub file.
	var meta string
	db.QueryRow(`SELECT embedded_meta FROM files WHERE id = ?`, epubFileID).Scan(&meta)
	if !strings.Contains(meta, `"toc"`) || !strings.Contains(meta, "Arrest") {
		t.Fatalf("embedded_meta = %s, want toc", meta)
	}

	// Covers written per work from the archive (cbz first page / epub OPF).
	if trial.CoverPath == nil || *trial.CoverPath == "" {
		t.Fatal("The Trial has no cover")
	}
	for _, wv := range works {
		if fi, err := os.Stat(filepath.Join(covers, *wv.CoverPath)); err != nil || fi.Size() == 0 {
			t.Fatalf("cover file for work %d missing: %v", wv.ID, err)
		}
	}

	// cbr detected and skipped: no file rows outside the four docs.
	var fileCount int
	db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&fileCount)
	if fileCount != 4 {
		t.Fatalf("file rows = %d, want 4 (cbr skipped)", fileCount)
	}

	// Re-scan: mtime+size unchanged -> no re-probe (probed_at untouched).
	var before []int64
	rows, _ := db.Query(`SELECT probed_at FROM files ORDER BY id`)
	for rows.Next() {
		var v int64
		rows.Scan(&v)
		before = append(before, v)
	}
	rows.Close()
	if n, err := Library(context.Background(), db, lib, covers, nil); err != nil || n != 0 {
		t.Fatalf("rescan = (%d, %v), want 0 new docs", n, err)
	}
	rows, _ = db.Query(`SELECT probed_at FROM files ORDER BY id`)
	i := 0
	for rows.Next() {
		var v int64
		rows.Scan(&v)
		if v != before[i] {
			t.Fatal("rescan re-probed unchanged files")
		}
		i++
	}
	rows.Close()
}

// fakeExtractor writes an executable shell script mimicking the subset of
// unrar/lsar/unar behavior libteca uses: `unrar lb` lists names, `unrar p`
// prints one entry to stdout, `lsar` lists with the archive name as header,
// `unar -o dir` extracts into dir.
func fakeExtractor(t *testing.T, name, script string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
	return dir
}

const fakeUnrar = `#!/bin/sh
case "$1" in
lb) printf 'p2.jpg\np10.jpg\np001.jpg\nnote.txt\n__MACOSX/p.jpg\n.hidden.jpg\n' ;;
p)  if [ "$4" = "p001.jpg" ]; then printf 'cbr-cover-bytes'; fi ;;
esac
`

const fakeLsar = `#!/bin/sh
printf '%s\np2.jpg\np10.jpg\np001.jpg\nnote.txt\n' "$1"
`

const fakeUnar = `#!/bin/sh
# called as: unar -q -f -o DIR ARCHIVE NAME
if [ "$6" = "p001.jpg" ]; then printf 'unar-cover-bytes' > "$4/p001.jpg"; fi
`

func TestCBRExtractorDetection(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	if got := cbrExtractor(); got != "" {
		t.Fatalf("cbrExtractor = %q with empty PATH", got)
	}
	fakeExtractor(t, "unrar", fakeUnrar)
	if got := cbrExtractor(); got != "unrar" {
		t.Fatalf("cbrExtractor = %q, want unrar", got)
	}
}

func TestProbeCBRUnrar(t *testing.T) {
	fakeExtractor(t, "unrar", fakeUnrar)
	p := filepath.Join(t.TempDir(), "comic.cbr")
	os.WriteFile(p, []byte("Rar!"), 0o644)
	n, cover, err := probeCBR(p, "unrar")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("pages = %d, want 3 (txt, __MACOSX, hidden excluded)", n)
	}
	if string(cover) != "cbr-cover-bytes" {
		t.Fatalf("cover = %q", cover)
	}
}

func TestProbeCBRUnar(t *testing.T) {
	dir := fakeExtractor(t, "lsar", fakeLsar)
	os.WriteFile(filepath.Join(dir, "unar"), []byte(fakeUnar), 0o755)
	p := filepath.Join(t.TempDir(), "Comic.cbr")
	os.WriteFile(p, []byte("Rar!"), 0o644)
	n, cover, err := probeCBR(p, "unar")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("pages = %d, want 3", n)
	}
	if string(cover) != "unar-cover-bytes" {
		t.Fatalf("cover = %q", cover)
	}
}

func TestScanCBRSkippedWithoutExtractor(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "comic.cbr"), []byte("Rar!"), 0o644)
	db.AddLibrary("Comics", "comics", root)
	n, err := Library(context.Background(), db, &store.Library{ID: 1, Type: "comics", Path: root}, filepath.Join(t.TempDir(), "covers"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("scanned = %d, want 0 without extractor", n)
	}
	var files int
	db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&files)
	if files != 0 {
		t.Fatalf("file rows = %d, want 0", files)
	}
}

func TestScanBooksLibraryCBR(t *testing.T) {
	fakeExtractor(t, "unrar", fakeUnrar)
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Comic.cbr"), []byte("Rar!"), 0o644)
	covers := filepath.Join(t.TempDir(), "covers")
	os.MkdirAll(covers, 0o755)
	db.AddLibrary("Comics", "comics", root)

	n, err := Library(context.Background(), db, &store.Library{ID: 1, Type: "comics", Path: root}, covers, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("scanned = %d, want 1", n)
	}
	var format string
	var pages sql.NullInt64
	var coverPath sql.NullString
	err = db.QueryRow(`SELECT e.format, e.page_count, w.cover_path
		FROM editions e JOIN works w ON w.id = e.work_id`).Scan(&format, &pages, &coverPath)
	if err != nil {
		t.Fatal(err)
	}
	if format != "cbr" || !pages.Valid || pages.Int64 != 3 {
		t.Fatalf("edition = %s pages = %v", format, pages)
	}
	if !coverPath.Valid {
		t.Fatal("cbr cover not written")
	}
	cover, err := os.ReadFile(filepath.Join(covers, coverPath.String))
	if err != nil || string(cover) != "cbr-cover-bytes" {
		t.Fatalf("cover file = %q, %v", cover, err)
	}
}
