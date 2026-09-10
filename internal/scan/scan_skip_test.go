package scan

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/store"
)

func genAudio(t *testing.T, path, freq string, dur float64) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not in PATH: %v", err)
	}
	if err := exec.Command("ffmpeg", "-y", "-v", "quiet",
		"-f", "lavfi", "-i", "sine=frequency="+freq+":duration="+durStr(dur),
		"-ar", "22050", "-ac", "1",
		"-c:a", "libmp3lame", "-b:a", "32k", "-f", "mp3", path).Run(); err != nil {
		t.Skipf("ffmpeg failed (%s): %v", path, err)
	}
}

func genVideo(t *testing.T, path string, dur float64) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not in PATH: %v", err)
	}
	if err := exec.Command("ffmpeg", "-y", "-v", "quiet",
		"-f", "lavfi", "-i", "testsrc2=size=128x72:rate=12:duration="+durStr(dur),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "32",
		"-pix_fmt", "yuv420p", "-f", "mp4", path).Run(); err != nil {
		t.Skipf("ffmpeg failed (%s): %v", path, err)
	}
}

func durStr(d float64) string { return time.Duration(d * float64(time.Second)).String() }

func scanOnce(t *testing.T, db *store.DB, lib *store.Library, covers string) Progress {
	t.Helper()
	var final Progress
	_, err := Library(db, lib, covers, func(p Progress) { final = p })
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return final
}

type fileRow struct {
	probedAt  int64
	hash      string
	editionID int64
}

func fileRows(t *testing.T, db *store.DB) map[string]fileRow {
	t.Helper()
	rows, err := db.Query(`SELECT path, probed_at, coalesce(hash,''), edition_id FROM files`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]fileRow{}
	for rows.Next() {
		var p string
		var r fileRow
		if err := rows.Scan(&p, &r.probedAt, &r.hash, &r.editionID); err != nil {
			t.Fatal(err)
		}
		out[p] = r
	}
	return out
}

func mustEq(t *testing.T, name string, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

// mtime_secs has second granularity, so a rewrite must land in a later second
// than the row's recorded mtime to count as changed.
func nextSecond(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for time.Now().Unix() <= fi.ModTime().Unix() {
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAudioRescanSkipsUnchanged(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := t.TempDir()
	bookA := filepath.Join(root, "Author 001 - Book A")
	bookB := filepath.Join(root, "Author 002 - Book B")
	os.MkdirAll(bookA, 0o755)
	os.MkdirAll(bookB, 0o755)
	a1 := filepath.Join(bookA, "Track 001.mp3")
	a2 := filepath.Join(bookA, "Track 002.mp3")
	b1 := filepath.Join(bookB, "Track 001.mp3")
	genAudio(t, a1, "110", 1.0)
	genAudio(t, a2, "220", 1.0)
	genAudio(t, b1, "330", 1.0)

	libID, err := db.AddLibrary("t", "audiobooks", root)
	if err != nil {
		t.Fatal(err)
	}
	lib := &store.Library{ID: libID, Type: "audiobooks", Path: root}
	covers := filepath.Join(t.TempDir(), "covers")

	p := scanOnce(t, db, lib, covers)
	mustEq(t, "cold probes", p.FilesProbed, 3)
	mustEq(t, "cold added", p.FilesAdded, 3)
	first := fileRows(t, db)
	if len(first) != 3 {
		t.Fatalf("file rows = %d, want 3", len(first))
	}

	// Warm re-scan: nothing changed -> no probes, no upserts, probed_at kept.
	p = scanOnce(t, db, lib, covers)
	mustEq(t, "warm seen", p.FilesSeen, 3)
	mustEq(t, "warm probes", p.FilesProbed, 0)
	mustEq(t, "warm added", p.FilesAdded, 0)
	mustEq(t, "warm updated", p.FilesUpdated, 0)
	second := fileRows(t, db)
	for path, r := range first {
		if second[path].probedAt != r.probedAt {
			t.Fatalf("warm rescan touched probed_at for %s", path)
		}
		if second[path].hash != r.hash {
			t.Fatalf("warm rescan changed hash for %s", path)
		}
	}

	// Rewrite one file of book A (size+mtime change) -> that group re-probes,
	// book B untouched.
	nextSecond(t, a1)
	genAudio(t, a1, "110", 1.5)
	p = scanOnce(t, db, lib, covers)
	mustEq(t, "changed probes", p.FilesProbed, 2)
	third := fileRows(t, db)
	if third[a1].hash == first[a1].hash {
		t.Fatal("rewritten file hash not updated")
	}
	mustEq(t, "edition linkage", third[a1].editionID, first[a1].editionID)
	if third[a2].probedAt == first[a2].probedAt {
		t.Fatal("sibling in changed group not re-probed")
	}
	if third[b1].probedAt != first[b1].probedAt {
		t.Fatal("unchanged group re-probed")
	}

	// Same-size replacement, different content (mtime changes) -> re-probed.
	oldSize := statSize(t, a2)
	nextSecond(t, a2)
	genAudio(t, a2, "440", 1.0)
	if sz := statSize(t, a2); sz == oldSize {
		t.Logf("replacement is same-size (%d bytes), different content", sz)
	} else {
		t.Logf("replacement size %d != old %d (mtime change still forces re-probe)", sz, oldSize)
	}
	p = scanOnce(t, db, lib, covers)
	if p.FilesProbed != 2 {
		t.Fatalf("same-size replacement probes = %d, want 2", p.FilesProbed)
	}
	fourth := fileRows(t, db)
	if fourth[a2].hash == third[a2].hash {
		t.Fatal("replaced file hash not updated")
	}
}

func TestMusicRescanSkipsUnchanged(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := t.TempDir()
	album := filepath.Join(root, "Artist 001 - Album 001")
	os.MkdirAll(album, 0o755)
	t1 := filepath.Join(album, "01 First.mp3")
	t2 := filepath.Join(album, "02 Second.mp3")
	t3 := filepath.Join(album, "03 Third.mp3")
	genAudio(t, t1, "110", 1.0)
	genAudio(t, t2, "220", 1.0)
	genAudio(t, t3, "330", 1.0)

	libID, err := db.AddLibrary("t", "music", root)
	if err != nil {
		t.Fatal(err)
	}
	lib := &store.Library{ID: libID, Type: "music", Path: root}
	covers := filepath.Join(t.TempDir(), "covers")

	p := scanOnce(t, db, lib, covers)
	mustEq(t, "cold probes", p.FilesProbed, 3)
	first := fileRows(t, db)

	p = scanOnce(t, db, lib, covers)
	mustEq(t, "warm seen", p.FilesSeen, 3)
	mustEq(t, "warm probes", p.FilesProbed, 0)
	mustEq(t, "warm updated", p.FilesUpdated, 0)
	second := fileRows(t, db)
	for path, r := range first {
		if second[path].probedAt != r.probedAt {
			t.Fatalf("warm rescan touched probed_at for %s", path)
		}
	}

	nextSecond(t, t2)
	genAudio(t, t2, "220", 1.5)
	p = scanOnce(t, db, lib, covers)
	mustEq(t, "single-file change probes", p.FilesProbed, 1)
	third := fileRows(t, db)
	if third[t2].hash == first[t2].hash {
		t.Fatal("rewritten track hash not updated")
	}
	mustEq(t, "edition linkage", third[t2].editionID, first[t2].editionID)
	if third[t1].probedAt != first[t1].probedAt {
		t.Fatal("unchanged sibling re-probed")
	}
}

func TestVideoRescanSkipsUnchanged(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := t.TempDir()
	movie := filepath.Join(root, "Movie 001 (2020)")
	os.MkdirAll(movie, 0o755)
	v1 := filepath.Join(movie, "Movie 001 (2020).mp4")
	genVideo(t, v1, 0.5)

	libID, err := db.AddLibrary("t", "movies", root)
	if err != nil {
		t.Fatal(err)
	}
	lib := &store.Library{ID: libID, Type: "movies", Path: root}
	covers := filepath.Join(t.TempDir(), "covers")

	p := scanOnce(t, db, lib, covers)
	mustEq(t, "cold probes", p.FilesProbed, 1)
	first := fileRows(t, db)

	p = scanOnce(t, db, lib, covers)
	mustEq(t, "warm seen", p.FilesSeen, 1)
	mustEq(t, "warm probes", p.FilesProbed, 0)
	mustEq(t, "warm updated", p.FilesUpdated, 0)
	second := fileRows(t, db)
	if second[v1].probedAt != first[v1].probedAt {
		t.Fatal("warm rescan touched probed_at")
	}

	nextSecond(t, v1)
	genVideo(t, v1, 0.8)
	p = scanOnce(t, db, lib, covers)
	mustEq(t, "changed probes", p.FilesProbed, 1)
	third := fileRows(t, db)
	if third[v1].hash == first[v1].hash {
		t.Fatal("rewritten movie hash not updated")
	}
	mustEq(t, "edition linkage", third[v1].editionID, first[v1].editionID)
}

func statSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}
