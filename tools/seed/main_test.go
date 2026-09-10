package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedBooks(t *testing.T) {
	dir := t.TempDir()
	n, err := seedBooks(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if n != 10 {
		t.Fatalf("want 10 files (5 works x epub+cbz), got %d", n)
	}
	epub := filepath.Join(dir, "Author 001 - Title 00001", "Title 00001.epub")
	zr, err := zip.OpenReader(epub)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if zr.File[0].Name != "mimetype" {
		t.Fatalf("first zip entry must be mimetype, got %q", zr.File[0].Name)
	}
	if zr.File[0].Method != zip.Store {
		t.Fatal("mimetype must be stored uncompressed")
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	mt, _ := io.ReadAll(rc)
	rc.Close()
	if string(mt) != "application/epub+zip" {
		t.Fatalf("bad mimetype %q", mt)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"META-INF/container.xml", "OEBPS/content.opf", "OEBPS/index.xhtml"} {
		if !names[want] {
			t.Fatalf("epub missing %s", want)
		}
	}

	cbz := filepath.Join(dir, "Author 001 - Title 00001", "Title 00001.cbz")
	cr, err := zip.OpenReader(cbz)
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()
	if len(cr.File) != bookPages {
		t.Fatalf("want %d pages, got %d", bookPages, len(cr.File))
	}
	for _, f := range cr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		head := make([]byte, 8)
		_, err = io.ReadFull(rc, head)
		rc.Close()
		if err != nil || !bytes.Equal(head, []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatalf("%s not a PNG", f.Name)
		}
	}
}

func TestSeedBooksIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := seedBooks(dir, 3); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(filepath.Join(dir, "Author 001 - Title 00001", "Title 00001.epub"))
	if _, err := seedBooks(dir, 3); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(dir, "Author 001 - Title 00001", "Title 00001.epub"))
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("re-seed rewrote existing files")
	}
}

func TestSeedNamingVariety(t *testing.T) {
	dir := t.TempDir()
	if _, err := seedBooks(dir, 10); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 10 {
		t.Fatalf("want 10 book folders, got %d", len(entries))
	}
	authors := map[string]bool{}
	for _, e := range entries {
		parts := strings.SplitN(e.Name(), " - ", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "Author ") || !strings.HasPrefix(parts[1], "Title ") {
			t.Fatalf("bad folder name %q", e.Name())
		}
		authors[parts[0]] = true
	}
	if len(authors) < 2 {
		t.Fatal("expected author variety")
	}
}

func TestPngBytes(t *testing.T) {
	p := pngBytes(8, 12, 0x7f)
	if len(p) < 50 {
		t.Fatalf("png too small: %d", len(p))
	}
	if !bytes.Equal(p[:8], []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("bad signature")
	}
}

func TestSeedAudioVideoNeedFfmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	n, err := seedAudio(dir, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	if n != 7 {
		t.Fatalf("want 7 mp3s, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "Author 001 - Title 00001", "Track 001.mp3")); err != nil {
		t.Fatal(err)
	}
	n, err = seedVideo(dir, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("want 2 mp4s, got %d", n)
	}
}
