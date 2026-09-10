package main

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Synthetic library generator. Output is idempotent: existing non-empty
// files are skipped, so a seed dir is a cache until deleted.
// Audio/video variants are real ffmpeg outputs, hardlinked into place.

const (
	audioVariants = 16
	videoVariants = 4
	bookPages     = 3
)

func main() {
	kind := flag.String("kind", "audio", "audio | video | books")
	count := flag.Int("count", 100, "file count (books: works, each epub+cbz)")
	out := flag.String("out", "", "output root (required)")
	tracks := flag.Int("tracks-per-book", 5, "audio: mp3 files per book folder")
	dur := flag.Float64("duration", 2, "video: seconds per clip")
	flag.Parse()

	if *out == "" || *count <= 0 {
		flag.Usage()
		os.Exit(2)
	}
	start := time.Now()
	var files int
	var err error
	switch *kind {
	case "audio":
		files, err = seedAudio(*out, *count, *tracks)
	case "video":
		files, err = seedVideo(*out, *count, *dur)
	case "books":
		files, err = seedBooks(*out, *count)
	default:
		err = fmt.Errorf("unknown kind %q", *kind)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
	fmt.Printf("seed: %d %s files under %s (%.1fs)\n", files, *kind, *out, time.Since(start).Seconds())
}

func fileOK(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

func placeVariant(dst, variant string) error {
	if fileOK(dst) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Link(variant, dst); err == nil {
		return nil
	}
	src, err := os.Open(variant)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := dst + ".tmp"
	dstF, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dstF, src); err != nil {
		dstF.Close()
		return err
	}
	if err := dstF.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// genVariants builds n distinct real ffmpeg outputs in dir, in parallel,
// skipping any that already exist. Returns variant paths in index order.
func genVariants(dir string, n int, mk func(i int, tmp string) *exec.Cmd) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	paths := make([]string, n)
	workers := runtime.GOMAXPROCS(0) * 2
	if workers > 8 {
		workers = 8
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("var%02d", i))
		paths[i] = p
		if fileOK(p) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, p string) {
			defer wg.Done()
			defer func() { <-sem }()
			tmp := p + ".tmp"
			os.Remove(tmp)
			cmd := mk(i, tmp)
			if err := cmd.Run(); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("ffmpeg variant %d: %w", i, err)
				}
				mu.Unlock()
				return
			}
			if err := os.Rename(tmp, p); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(i, p)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return paths, nil
}

func seedAudio(out string, count, tracksPerBook int) (int, error) {
	if tracksPerBook < 2 {
		return 0, fmt.Errorf("--tracks-per-book must be >= 2 (scanner groups folders with >=2 audio files)")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return 0, fmt.Errorf("ffmpeg not in PATH (audio seeding needs it): %w", err)
	}
	variants, err := genVariants(filepath.Join(out, ".variants", "audio"), audioVariants,
		func(i int, tmp string) *exec.Cmd {
			freq := 110 + i*61
			dur := 1.0 + float64(i%3)*0.5
			return exec.Command("ffmpeg", "-y", "-v", "quiet",
				"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=%d:duration=%.1f", freq, dur),
				"-ar", "22050", "-ac", "1",
				"-c:a", "libmp3lame", "-b:a", "32k", "-f", "mp3", tmp)
		})
	if err != nil {
		return 0, err
	}
	files := 0
	book := 0
	for files < count {
		author := book%137 + 1
		title := book + 1
		dir := filepath.Join(out, fmt.Sprintf("Author %03d - Title %05d", author, title))
		n := tracksPerBook
		if remaining := count - files; remaining < n {
			n = remaining
		}
		for t := 1; t <= n; t++ {
			dst := filepath.Join(dir, fmt.Sprintf("Track %03d.mp3", t))
			if err := placeVariant(dst, variants[(book+t)%len(variants)]); err != nil {
				return files, err
			}
			files++
		}
		book++
	}
	return files, nil
}

func seedVideo(out string, count int, dur float64) (int, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return 0, fmt.Errorf("ffmpeg not in PATH (video seeding needs it): %w", err)
	}
	variants, err := genVariants(filepath.Join(out, ".variants", "video"), videoVariants,
		func(i int, tmp string) *exec.Cmd {
			d := dur + float64(i)*0.25
			return exec.Command("ffmpeg", "-y", "-v", "quiet",
				"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=1280x720:rate=24:duration=%.2f", d),
				"-c:v", "libx264", "-preset", "veryfast", "-crf", "28",
				"-pix_fmt", "yuv420p", "-movflags", "+faststart", "-f", "mp4", tmp)
		})
	if err != nil {
		return 0, err
	}
	files := 0
	for i := 0; i < count; i++ {
		year := 2016 + i%10
		name := fmt.Sprintf("Title %05d (%d)", i+1, year)
		dst := filepath.Join(out, name, name+".mp4")
		if err := placeVariant(dst, variants[i%len(variants)]); err != nil {
			return files, err
		}
		files++
	}
	return files, nil
}

func seedBooks(out string, count int) (int, error) {
	files := 0
	for i := 0; i < count; i++ {
		author := i%113 + 1
		title := i + 1
		base := fmt.Sprintf("Title %05d", title)
		dir := filepath.Join(out, fmt.Sprintf("Author %03d - Title %05d", author, title))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return files, err
		}
		epub := filepath.Join(dir, base+".epub")
		cbz := filepath.Join(dir, base+".cbz")
		if !fileOK(epub) {
			if err := writeEPUB(epub, base, fmt.Sprintf("Author %03d", author)); err != nil {
				return files, err
			}
		}
		files++
		if !fileOK(cbz) {
			if err := writeCBZ(cbz, bookPages); err != nil {
				return files, err
			}
		}
		files++
	}
	return files, nil
}

func writeEPUB(path, title, author string) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mh := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	w, err := zw.CreateHeader(mh)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte("application/epub+zip")); err != nil {
		return err
	}
	files := [][2]string{
		{"META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`},
		{"OEBPS/content.opf", `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:libteca:seed:` + title + `</dc:identifier>
    <dc:title>` + title + `</dc:title>
    <dc:creator>` + author + `</dc:creator>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ch1" href="index.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="ch1"/></spine>
</package>`},
		{"OEBPS/nav.xhtml", `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
  <body><nav epub:type="toc"><ol><li><a href="index.xhtml">` + title + `</a></li></ol></nav></body>
</html>`},
		{"OEBPS/index.xhtml", `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
  <head><title>` + title + `</title></head>
  <body><h1>` + title + `</h1><p>` + author + `</p></body>
</html>`},
	}
	for _, f := range files {
		w, err := zw.Create(f[0])
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(f[1])); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return atomicWrite(path, buf.Bytes())
}

func writeCBZ(path string, pages int) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for p := 1; p <= pages; p++ {
		w, err := zw.Create(fmt.Sprintf("page%02d.png", p))
		if err != nil {
			return err
		}
		if _, err := w.Write(pngBytes(8, 12, byte(40*p))); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return atomicWrite(path, buf.Bytes())
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// pngBytes emits a minimal valid grayscale-8 PNG (stdlib only).
func pngBytes(w, h int, gray byte) []byte {
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(h))
	ihdr[8] = 8  // bit depth
	ihdr[9] = 0  // color type grayscale
	ihdr[10] = 0 // compression
	ihdr[11] = 0 // filter
	ihdr[12] = 0 // interlace

	row := make([]byte, 1+w)
	for x := 0; x < w; x++ {
		row[1+x] = gray
	}
	var raw bytes.Buffer
	for y := 0; y < h; y++ {
		raw.Write(row)
	}
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	zw.Write(raw.Bytes())
	zw.Close()

	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	chunk(&out, "IHDR", ihdr[:])
	chunk(&out, "IDAT", zbuf.Bytes())
	chunk(&out, "IEND", nil)
	return out.Bytes()
}

func chunk(out *bytes.Buffer, typ string, data []byte) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	out.Write(lenBuf[:])
	out.WriteString(typ)
	out.Write(data)
	var crcBuf [4]byte
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	binary.BigEndian.PutUint32(crcBuf[:], crc.Sum32())
	out.Write(crcBuf[:])
}
