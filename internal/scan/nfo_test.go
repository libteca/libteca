package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

const movieNFO = `<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<movie>
  <title>The Matrix</title>
  <plot>A hacker learns the truth.</plot>
  <outline>Shorter blurb.</outline>
  <year>1999</year>
  <premiered>1999-03-31</premiered>
  <genre>Action</genre>
  <genre>Sci-Fi</genre>
  <studio>Warner Bros.</studio>
  <director>Lana Wachowski</director>
  <credits>Lilly Wachowski</credits>
</movie>`

const tvshowNFO = `<tvshow>
  <title>Breaking Bad</title>
  <plot>A chemistry teacher cooks.</plot>
  <premiered>2008-01-20</premiered>
  <genre>Crime</genre>
  <studio>AMC</studio>
</tvshow>`

const episodeNFO = `<episodedetails>
  <title>Pilot</title>
  <season>1</season>
  <episode>1</episode>
  <plot>Walter White starts cooking.</plot>
  <aired>2008-01-20</aired>
</episodedetails>`

const seasonNFO = `<season>
  <title>Season One</title>
  <plot>The first year.</plot>
  <year>2008</year>
</season>`

func TestParseNFOMovie(t *testing.T) {
	n, err := ParseNFO([]byte(movieNFO))
	if err != nil {
		t.Fatal(err)
	}
	if n.Root != "movie" || n.Title != "The Matrix" {
		t.Fatalf("root/title = %q %q", n.Root, n.Title)
	}
	if n.Description() != "A hacker learns the truth." {
		t.Fatalf("plot = %q", n.Description())
	}
	if n.YearValue() != "1999" {
		t.Fatalf("year = %q", n.YearValue())
	}
	if n.Studio != "Warner Bros." {
		t.Fatalf("studio = %q", n.Studio)
	}
	if n.CreditsLine() != "Lana Wachowski" {
		t.Fatalf("credits = %q", n.CreditsLine())
	}
	if len(n.Genres) != 2 || n.Genres[0] != "Action" || n.Genres[1] != "Sci-Fi" {
		t.Fatalf("genres = %v", n.Genres)
	}
}

func TestParseNFOTvShowSeasonEpisode(t *testing.T) {
	show, err := ParseNFO([]byte(tvshowNFO))
	if err != nil {
		t.Fatal(err)
	}
	if show.Root != "tvshow" || show.Title != "Breaking Bad" || show.YearValue() != "2008" {
		t.Fatalf("tvshow = %+v", show)
	}
	ep, err := ParseNFO([]byte(episodeNFO))
	if err != nil {
		t.Fatal(err)
	}
	if !ep.isEpisode() || ep.Title != "Pilot" {
		t.Fatalf("episode = %+v", ep)
	}
	sn, err := ParseNFO([]byte(seasonNFO))
	if err != nil {
		t.Fatal(err)
	}
	if sn.Root != "season" || sn.Title != "Season One" || sn.YearValue() != "2008" {
		t.Fatalf("season = %+v", sn)
	}
}

func TestParseNFOMalformed(t *testing.T) {
	if _, err := ParseNFO([]byte(`<movie><title>no close`)); err == nil {
		t.Fatal("expected error")
	}
	if _, err := ParseNFO([]byte("")); err == nil {
		t.Fatal("empty: expected error")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "movie.nfo")
	if err := os.WriteFile(p, []byte(`<movie><title>no close`), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := parseNFOPath(p); n != nil {
		t.Fatalf("malformed sidecar should skip, got %+v", n)
	}
}

func TestNoSidecar(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Movie.mkv")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if findWorkNFO(dir) != "" || findFileNFO(video) != "" {
		t.Fatal("found nfo without sidecar")
	}
	if readWorkNFO(dir, video) != nil {
		t.Fatal("readWorkNFO without sidecar")
	}
	if episodeTitleFromNFO(video, "S01E01") != "S01E01" {
		t.Fatal("episode nfo ran without sidecar")
	}
	if err := applyNFO(nil, 1, nil); err != nil {
		t.Fatalf("nil nfo: %v", err)
	}
}

func TestApplyNFOAndPoster(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	libID, err := db.AddLibrary("m", "movies", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	folder := t.TempDir()
	video := filepath.Join(folder, "The Matrix (1999).mkv")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "movie.nfo"), []byte(movieNFO), 0o644); err != nil {
		t.Fatal(err)
	}
	poster := []byte("\xff\xd8fakejpg")
	if err := os.WriteFile(filepath.Join(folder, "poster.jpg"), poster, 0o644); err != nil {
		t.Fatal(err)
	}

	nfo := readWorkNFO(folder, video)
	if nfo == nil || nfo.Title != "The Matrix" {
		t.Fatalf("readWorkNFO = %+v", nfo)
	}
	author := "1999"
	w := &store.Work{LibraryID: libID, Title: "The Matrix (1999)", Author: &author}
	workID, err := db.UpsertWork(w)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyNFO(db, workID, nfo); err != nil {
		t.Fatal(err)
	}
	got, err := db.WorkByID(workID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "The Matrix" {
		t.Fatalf("title = %q", got.Title)
	}
	if got.Description == nil || *got.Description != "A hacker learns the truth." {
		t.Fatalf("description = %v", got.Description)
	}
	g := db.WorkGenres(workID)
	if len(g) != 2 || g[0] != "Action" {
		t.Fatalf("genres = %v", g)
	}

	covers := t.TempDir()
	ok, err := importSidecarPoster(db, workID, folder, covers, true)
	if err != nil || !ok {
		t.Fatalf("poster import ok=%v err=%v", ok, err)
	}
	rel := fmt.Sprintf("%d.jpg", workID)
	got, err = db.WorkByID(workID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CoverPath == nil || *got.CoverPath != rel {
		t.Fatalf("cover_path = %v", got.CoverPath)
	}
	data, err := os.ReadFile(filepath.Join(covers, rel))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(poster) {
		t.Fatalf("cover bytes = %q", data)
	}
}

func TestEpisodeNFOTitleFill(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "S01E01.mkv")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "S01E01.nfo"), []byte(episodeNFO), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := episodeTitleFromNFO(video, "S01E01"); got != "Pilot" {
		t.Fatalf("S01E01 -> %q", got)
	}
	if got := episodeTitleFromNFO(video, "Show.S01E01"); got != "Pilot" {
		t.Fatalf("dotted -> %q", got)
	}
	if got := episodeTitleFromNFO(video, "The Pilot"); got != "The Pilot" {
		t.Fatalf("good title clobbered: %q", got)
	}
}

func TestTvShowNFOOnFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tvshow.nfo"), []byte(tvshowNFO), 0o644); err != nil {
		t.Fatal(err)
	}
	ep := filepath.Join(dir, "S01E01.mkv")
	if err := os.WriteFile(ep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "S01E01.nfo"), []byte(episodeNFO), 0o644); err != nil {
		t.Fatal(err)
	}
	n := readWorkNFO(dir, ep)
	if n == nil || n.Title != "Breaking Bad" || n.isEpisode() {
		t.Fatalf("work nfo = %+v", n)
	}
	if episodeTitleFromNFO(ep, "S01E01") != "Pilot" {
		t.Fatal("episode title")
	}
}

func TestApplyNFODoesNotClobberGenres(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	libID, err := db.AddLibrary("m", "movies", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := &store.Work{LibraryID: libID, Title: "X"}
	id, err := db.UpsertWork(w)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetWorkGenres(id, []string{"Drama"}); err != nil {
		t.Fatal(err)
	}
	n, err := ParseNFO([]byte(movieNFO))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyNFO(db, id, n); err != nil {
		t.Fatal(err)
	}
	g := db.WorkGenres(id)
	if len(g) != 1 || g[0] != "Drama" {
		t.Fatalf("genres clobbered: %v", g)
	}
	got, _ := db.WorkByID(id)
	if got.Title != "The Matrix" {
		t.Fatalf("title should still win: %q", got.Title)
	}
}

func TestParseNFOYearFromPremiered(t *testing.T) {
	n, err := ParseNFO([]byte(`<movie><title>X</title><premiered>2012-06-01</premiered></movie>`))
	if err != nil {
		t.Fatal(err)
	}
	if n.YearValue() != "2012" {
		t.Fatalf("year = %q", n.YearValue())
	}
}

func TestImportSuffixArtAndFanart(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	libID, _ := db.AddLibrary("m", "movies", t.TempDir())
	w := &store.Work{LibraryID: libID, Title: "Film"}
	wid, err := db.UpsertWork(w)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	covers := t.TempDir()
	media := filepath.Join(dir, "Film (2010).mkv")
	os.WriteFile(media, []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "Film (2010)-poster.jpg"), []byte("POSTER"), 0o644)
	os.WriteFile(filepath.Join(dir, "Film (2010)-fanart.jpg"), []byte("FANART"), 0o644)

	ok, err := importSuffixArt(db, wid, media, covers)
	if err != nil || !ok {
		t.Fatalf("suffix art = %v %v", ok, err)
	}
	b, _ := os.ReadFile(filepath.Join(covers, fmt.Sprintf("%d.jpg", wid)))
	if string(b) != "POSTER" {
		t.Fatalf("cover = %q", b)
	}
	cp := "1.jpg"
	if err := db.SetWorkCover(wid, cp); err != nil {
		t.Fatal(err)
	}
	importFanart(wid, dir, media, covers)
	b, _ = os.ReadFile(filepath.Join(covers, fmt.Sprintf("%d-fanart.jpg", wid)))
	if string(b) != "FANART" {
		t.Fatalf("fanart = %q", b)
	}

	os.WriteFile(filepath.Join(dir, "backdrop.jpg"), []byte("BD"), 0o644)
	importFanart(wid+1, dir, media, covers)
	b, _ = os.ReadFile(filepath.Join(covers, fmt.Sprintf("%d-fanart.jpg", wid+1)))
	if string(b) != "BD" {
		t.Fatalf("folder backdrop = %q", b)
	}
}
