package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestEditionDownloadRefusesOutsideLibrary(t *testing.T) {
	env := newReadingEnv(t)
	lib, _ := env.db.AddLibrary("Books", "books", t.TempDir())
	w := seedWork(t, env.db, lib, "The Trial", ptr("Franz Kafka"), nil, 1, 1)
	epub := seedBookEdition(t, env.db, w, "epub", ptr(3))
	outside := filepath.Join(t.TempDir(), "outside.epub")
	if err := os.WriteFile(outside, []byte("PK outside bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedFileAt(t, env.db, epub, outside)

	resp, _ := readingReq(t, env, "GET", env.base+fmt.Sprintf("/editions/%d/download", epub), env.token, "")
	if resp.StatusCode != 404 {
		t.Fatalf("out-of-library download status = %d, want 404", resp.StatusCode)
	}
}

func TestSetProgressFinishedOnlyPreservesPosition(t *testing.T) {
	env := newReadingEnv(t)
	lib, _ := env.db.AddLibrary("Books", "books", t.TempDir())
	w := seedWork(t, env.db, lib, "The Trial", nil, nil, 1, 1)
	epub := seedBookEdition(t, env.db, w, "epub", ptr(3))
	seedFileOnDisk(t, env.db, lib, epub, "trial.epub", []byte("PK"))

	if code, body := readingPost(t, env, epub, `{"position":642,"duration":3600,"device":"pixel"}`); code != 200 {
		t.Fatalf("position post = %d %s", code, body)
	}
	if code, body := readingPost(t, env, epub, `{"finished":true}`); code != 200 {
		t.Fatalf("finished-only post = %d %s", code, body)
	}

	_, body := readingReq(t, env, "GET", env.base+"/progress/"+strconv.FormatInt(epub, 10), env.token, "")
	if !strings.Contains(body, `"position":642`) {
		t.Fatalf("finished-only update reset position: %s", body)
	}
	if !strings.Contains(body, `"duration":3600`) {
		t.Fatalf("finished-only update reset duration: %s", body)
	}
}

func seedFileAt(t *testing.T, db *store.DB, editionID int64, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, probed_at) VALUES (?,?,1,?,1,0,0)`,
		editionID, path, fi.Size()); err != nil {
		t.Fatal(err)
	}
}

func readingPost(t *testing.T, env *readingEnv, editionID int64, body string) (int, string) {
	t.Helper()
	resp, out := readingReq(t, env, "POST", env.base+fmt.Sprintf("/progress/%d", editionID), env.token, body)
	return resp.StatusCode, out
}
