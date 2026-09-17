package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestDeleteLibraryNotFound(t *testing.T) {
	db := openTestDB(t)
	if err := db.DeleteLibrary(999); err != store.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteLibraryRemovesDependentsKeepsDisk(t *testing.T) {
	db := openTestDB(t)
	keep, err := db.AddLibrary("keep", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	drop, err := db.AddLibrary("drop", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(t.TempDir(), "book.mp3")
	if err := os.WriteFile(media, []byte("xx"), 0644); err != nil {
		t.Fatal(err)
	}

	uid, err := db.CreateUser("u", "h", false)
	if err != nil {
		t.Fatal(err)
	}
	dropWork, err := db.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?, 'W', 0, 0)`, drop)
	if err != nil {
		t.Fatal(err)
	}
	dropWorkID, _ := dropWork.LastInsertId()
	keepWork, err := db.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?, 'KeepW', 0, 0)`, keep)
	if err != nil {
		t.Fatal(err)
	}
	keepWorkID, _ := keepWork.LastInsertId()
	dropEd, err := db.Exec(`INSERT INTO editions (work_id, format, title, created_at) VALUES (?, 'mp3', 'E', 0)`, dropWorkID)
	if err != nil {
		t.Fatal(err)
	}
	dropEdID, _ := dropEd.LastInsertId()
	keepEd, err := db.Exec(`INSERT INTO editions (work_id, format, title, created_at) VALUES (?, 'mp3', 'KeepE', 0)`, keepWorkID)
	if err != nil {
		t.Fatal(err)
	}
	keepEdID, _ := keepEd.LastInsertId()
	if _, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, probed_at) VALUES (?, ?, 1, 2, 0, 1, 0)`, dropEdID, media); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO progress (user_id, edition_id, updated_at) VALUES (?, ?, 0)`, uid, dropEdID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO playback_sessions (id, user_id, edition_id, started_at, updated_at) VALUES ('s', ?, ?, 0, 0)`, uid, dropEdID); err != nil {
		t.Fatal(err)
	}
	plID, err := db.CreatePlaylist(uid, "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddPlaylistItem(plID, dropEdID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddPlaylistItem(plID, keepEdID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateScanJob(drop); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteLibrary(drop); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Library(drop); err != store.ErrNotFound {
		t.Fatalf("dropped library still present: %v", err)
	}
	if _, err := os.Stat(media); err != nil {
		t.Fatalf("media file removed from disk: %v", err)
	}
	if _, err := db.Library(keep); err != nil {
		t.Fatalf("kept library gone: %v", err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM works WHERE library_id = ?`, drop).Scan(&n)
	if n != 0 {
		t.Fatalf("dropped works = %d", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM editions WHERE id = ?`, dropEdID).Scan(&n)
	if n != 0 {
		t.Fatalf("dropped edition survived")
	}
	db.QueryRow(`SELECT COUNT(*) FROM files WHERE edition_id = ?`, dropEdID).Scan(&n)
	if n != 0 {
		t.Fatalf("dropped files survived")
	}
	db.QueryRow(`SELECT COUNT(*) FROM progress WHERE edition_id = ?`, dropEdID).Scan(&n)
	if n != 0 {
		t.Fatalf("dropped progress survived")
	}
	db.QueryRow(`SELECT COUNT(*) FROM playlist_items WHERE edition_id = ?`, dropEdID).Scan(&n)
	if n != 0 {
		t.Fatalf("dropped playlist_items survived")
	}
	db.QueryRow(`SELECT COUNT(*) FROM playlist_items WHERE edition_id = ?`, keepEdID).Scan(&n)
	if n != 1 {
		t.Fatalf("kept playlist_items = %d", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM scan_jobs WHERE library_id = ?`, drop).Scan(&n)
	if n != 0 {
		t.Fatalf("dropped scan_jobs survived")
	}
	db.QueryRow(`SELECT COUNT(*) FROM works WHERE id = ?`, keepWorkID).Scan(&n)
	if n != 1 {
		t.Fatalf("kept work gone")
	}
}

func TestDeleteLibraryPodcasts(t *testing.T) {
	db := openTestDB(t)
	libID, err := db.AddLibrary("pods", "podcasts", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uid, err := db.CreateUser("u", "h", false)
	if err != nil {
		t.Fatal(err)
	}
	podID, err := db.AddPodcast(&store.Podcast{LibraryID: libID, FeedURL: "http://x/feed", Title: "P"})
	if err != nil {
		t.Fatal(err)
	}
	title := "ep"
	if _, err := db.UpsertPodcastEpisode(&store.PodcastEpisode{PodcastID: podID, GUID: "g", Title: &title, EnclosureURL: "http://x/e.mp3"}); err != nil {
		t.Fatal(err)
	}
	eps, err := db.PodcastEpisodes(podID)
	if err != nil || len(eps) != 1 {
		t.Fatalf("episodes = %v %v", eps, err)
	}
	if err := db.SetEpisodeProgress(&store.EpisodeProgress{UserID: uid, EpisodeID: eps[0].ID}); err != nil {
		t.Fatal(err)
	}

	fileID, err := db.InsertPodcastFile("/data/podcasts/1/ep.mp3", 10, 0, "h", 0, "mp3")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.LinkEpisodeFile(eps[0].ID, fileID); err != nil {
		t.Fatal(err)
	}
	otherLib, err := db.AddLibrary("pods2", "podcasts", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	otherPod, err := db.AddPodcast(&store.Podcast{LibraryID: otherLib, FeedURL: "http://x/2", Title: "P2"})
	if err != nil {
		t.Fatal(err)
	}
	title2 := "ep2"
	if _, err := db.UpsertPodcastEpisode(&store.PodcastEpisode{PodcastID: otherPod, GUID: "g2", Title: &title2, EnclosureURL: "http://x/2.mp3"}); err != nil {
		t.Fatal(err)
	}
	otherEps, err := db.PodcastEpisodes(otherPod)
	if err != nil || len(otherEps) != 1 {
		t.Fatalf("other episodes = %v %v", otherEps, err)
	}
	otherFileID, err := db.InsertPodcastFile("/data/podcasts/2/ep.mp3", 10, 0, "h2", 0, "mp3")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.LinkEpisodeFile(otherEps[0].ID, otherFileID); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteLibrary(libID); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM podcasts`).Scan(&n)
	if n != 1 {
		t.Fatalf("podcasts = %d, want the other library's podcast", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM podcast_episodes WHERE podcast_id = ?`, podID).Scan(&n)
	if n != 0 {
		t.Fatalf("episodes = %d", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM podcast_episode_progress`).Scan(&n)
	if n != 0 {
		t.Fatalf("episode_progress = %d", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM files WHERE id = ?`, fileID).Scan(&n)
	if n != 0 {
		t.Fatal("deleted library's episode-linked file row orphaned in files")
	}
	db.QueryRow(`SELECT COUNT(*) FROM files WHERE id = ?`, otherFileID).Scan(&n)
	if n != 1 {
		t.Fatal("other library's podcast file must survive")
	}
}
