package podcast

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func seedRetentionPolicy(t *testing.T, s *Service, path string) *store.Podcast {
	t.Helper()
	if _, err := s.DB.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('casts','podcasts',?,0)`, filepath.Join(s.DataDir, "root")); err != nil {
		t.Fatal(err)
	}
	pres, err := s.DB.Exec(`INSERT INTO podcasts (library_id, feed_url, title, auto_download, max_episodes, created_at) VALUES (1,'https://x/feed','X',0,1,0)`)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := pres.LastInsertId()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	fres, err := s.DB.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at) VALUES (NULL,?,1,5,0,0,'[]','{}',0,0)`, path)
	if err != nil {
		t.Fatal(err)
	}
	fid, _ := fres.LastInsertId()
	if _, err := s.DB.Exec(`INSERT INTO podcast_episodes (podcast_id, guid, enclosure_url, file_id, downloaded_at, pub_date, created_at) VALUES (?,'g','https://x/e.mp3',?,1,1000,0)`, pid, fid); err != nil {
		t.Fatal(err)
	}
	keepPath := filepath.Join(s.DataDir, "podcasts", "1", "keep.mp3")
	if err := os.MkdirAll(filepath.Dir(keepPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keepPath, []byte("audio2"), 0o644); err != nil {
		t.Fatal(err)
	}
	kres, err := s.DB.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at) VALUES (NULL,?,1,6,0,0,'[]','{}',0,0)`, keepPath)
	if err != nil {
		t.Fatal(err)
	}
	kfid, _ := kres.LastInsertId()
	if _, err := s.DB.Exec(`INSERT INTO podcast_episodes (podcast_id, guid, enclosure_url, file_id, downloaded_at, pub_date, created_at) VALUES (?,'g2','https://x/e2.mp3',?,1,2000,0)`, pid, kfid); err != nil {
		t.Fatal(err)
	}
	p, err := s.DB.Podcast(pid)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnforceRetentionRefusesUncontainedPath(t *testing.T) {
	s, _ := newTestService(t)
	outside := filepath.Join(t.TempDir(), "episode.mp3")
	p := seedRetentionPolicy(t, s, outside)
	err := s.enforceRetention(p)
	if err == nil || !strings.Contains(err.Error(), "refusing to remove uncontained path") {
		t.Fatalf("err = %v, want uncontained-path refusal", err)
	}
	var link any
	if qerr := s.DB.QueryRow(`SELECT file_id FROM podcast_episodes WHERE guid = 'g'`).Scan(&link); qerr != nil {
		t.Fatal(qerr)
	}
	if link == nil {
		t.Fatal("refused purge must keep the episode linked")
	}
	if _, rerr := os.Stat(outside); rerr != nil {
		t.Fatalf("outside file must be untouched: %v", rerr)
	}
}

func TestEnforceRetentionRemovesVanishedFile(t *testing.T) {
	s, _ := newTestService(t)
	gone := filepath.Join(s.DataDir, "podcasts", "1", "gone.mp3")
	p := seedRetentionPolicy(t, s, gone)
	os.Remove(gone)
	if err := s.enforceRetention(p); err != nil {
		t.Fatalf("vanished file must purge cleanly: %v", err)
	}
	var link any
	var missing int
	if err := s.DB.QueryRow(`SELECT file_id FROM podcast_episodes WHERE guid = 'g'`).Scan(&link); err != nil {
		t.Fatal(err)
	}
	if link != nil {
		t.Fatalf("episode still linked after purge: %v", link)
	}
	if err := s.DB.QueryRow(`SELECT missing FROM files WHERE path = ?`, gone).Scan(&missing); err != nil || missing != 1 {
		t.Fatalf("file missing = %d err = %v", missing, err)
	}
}
