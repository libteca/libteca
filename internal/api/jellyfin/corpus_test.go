package jellyfin_test

import (
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/api/corpusutil"
	"github.com/libteca/libteca/internal/api/jellyfin"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/libteca/libteca/internal/transcode"
	"github.com/neutron-build/neutron/go/neutron"
)

const (
	corpusUser     = "admin"
	corpusPassword = "corpus-password"
)

func corpusStack(t *testing.T) (*httptest.Server, string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "corpus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	now := time.Now().UnixMilli()
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?,?,1,?,?)`,
		corpusUser, auth.Hash(corpusPassword), now, now)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.IssueToken(db, uid, "corpus")
	if err != nil {
		t.Fatal(err)
	}

	libRoot := filepath.Join(dir, "audiobooks")
	libDir := filepath.Join(libRoot, "Corpus Book")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bookPath := filepath.Join(libDir, "book.m4b")
	if err := os.WriteFile(bookPath, []byte("fake-audio-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	libID, err := db.AddLibrary("Audiobooks", "audiobooks", libRoot)
	if err != nil {
		t.Fatal(err)
	}
	author := "Corpus Author"
	workID, err := db.UpsertWork(&store.Work{LibraryID: libID, Title: "Corpus Book", Author: &author})
	if err != nil {
		t.Fatal(err)
	}
	dur := 3600.0
	edID, err := db.UpsertEdition(&store.Edition{WorkID: workID, Format: "m4b", Title: "Corpus Book", DurationSecs: &dur})
	if err != nil {
		t.Fatal(err)
	}
	aac, mp4 := "aac", "mp4"
	file := &store.FileRec{
		EditionID: edID, Path: bookPath, Seq: 1, SizeBytes: 16,
		Codec: &aac, Container: &mp4, DurationSecs: dur, Chapters: "[]",
	}
	if err := db.UpsertFile(file); err != nil {
		t.Fatal(err)
	}

	// mirrors the Jellyfin mounting in internal/server/server.go
	app := neutron.New(
		neutron.WithOpenAPIInfo("libteca", "0.1.0"),
		neutron.WithLogger(slog.Default()),
		neutron.WithMiddleware(neutron.Recover()),
	)
	r := app.Router()
	jf := jellyfin.New(db, dir, transcode.New(dir))
	jf.Mount(r)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	vars := map[string]string{
		"{userId}":   strconv.FormatInt(uid, 10),
		"{libId}":    "lib" + strconv.FormatInt(libID, 10),
		"{itemId}":   "e" + strconv.FormatInt(edID, 10),
		"{id}":       "e" + strconv.FormatInt(edID, 10),
		"{fileId}":   "f" + strconv.FormatInt(file.ID, 10),
		"{seriesId}": "w" + strconv.FormatInt(workID, 10),
		"{password}": corpusPassword,
	}
	return srv, token, vars
}

func TestCorpus(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "testcorpus", "jellyfin")
	fixtures, err := corpusutil.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Skip("no corpus fixtures")
	}
	srv, token, vars := corpusStack(t)
	client := srv.Client()
	for _, fx := range fixtures {
		fx := fx
		t.Run(fx.File, func(t *testing.T) {
			corpusutil.Replay(t, client, srv.URL, fx, vars, corpusutil.ReplayOpts{
				AuthHeader:    `MediaBrowser Token="` + token + `"`,
				TokenHeader:   token,
				Username:      corpusUser,
				Password:      corpusPassword,
				HarvestSuffix: "/playbackinfo",
				HarvestKey:    "PlaySessionId",
			})
		})
	}
}
