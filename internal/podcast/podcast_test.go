package podcast

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/store"
)

func newTestService(t *testing.T) (*Service, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db, t.TempDir()), db
}

type feedItem struct {
	guid, title, date string
}

type feedServer struct {
	*httptest.Server
	mu          sync.Mutex
	title       string
	etag        string
	items       []feedItem
	feedHits    int
	ifNoneMatch []string
}

func newFeedServer(t *testing.T, title string) *feedServer {
	fs := &feedServer{title: title, etag: "v1", items: []feedItem{
		{"guid-1", "Episode One", "Tue, 10 Dec 2024 06:00:00 +0000"},
		{"guid-2", "Episode Two", "Tue, 17 Dec 2024 06:00:00 +0000"},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("/feed", func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		fs.feedHits++
		if inm := r.Header.Get("If-None-Match"); inm != "" {
			fs.ifNoneMatch = append(fs.ifNoneMatch, inm)
			if inm == fs.etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		w.Header().Set("ETag", fs.etag)
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, fs.buildFeedLocked())
	})
	mux.HandleFunc("/enclosure/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, "fake-audio-"+filepath.Base(r.URL.Path))
	})
	mux.HandleFunc("/cover.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		io.WriteString(w, "fake-jpeg-bytes")
	})
	fs.Server = httptest.NewServer(mux)
	t.Cleanup(fs.Close)
	return fs
}

func (fs *feedServer) setItems(items []feedItem, etag string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.items = items
	fs.etag = etag
}

func (fs *feedServer) hits() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.feedHits
}

func (fs *feedServer) seenIfNoneMatch() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]string(nil), fs.ifNoneMatch...)
}

func (fs *feedServer) buildFeedLocked() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"><channel>`)
	fmt.Fprintf(&b, `<title>%s</title><description>Test show about tests</description>`, fs.title)
	fmt.Fprintf(&b, `<itunes:author>Author Name</itunes:author><image><url>%s/cover.jpg</url></image>`, fs.URL)
	for _, it := range fs.items {
		fmt.Fprintf(&b, `<item><title>%s</title><guid>%s</guid><description>desc %s</description>`, it.title, it.guid, it.guid)
		fmt.Fprintf(&b, `<pubDate>%s</pubDate><itunes:duration>1:02:03</itunes:duration>`, it.date)
		fmt.Fprintf(&b, `<enclosure url="%s/enclosure/%s.mp3" length="1024" type="audio/mpeg"/></item>`, fs.URL, it.guid)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func TestSubscribeParsesDownloadsAndCovers(t *testing.T) {
	svc, db := newTestService(t)
	fs := newFeedServer(t, "Test Cast")

	p, err := svc.Subscribe(context.Background(), fs.URL+"/feed", true, 3)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Test Cast" || p.Author == nil || *p.Author != "Author Name" || p.Description == nil {
		t.Fatalf("podcast meta = %+v", p)
	}
	if p.AutoDownload != true || p.MaxEpisodes != 3 || p.ETag == nil || *p.ETag != "v1" || p.LastFetchAt == nil {
		t.Fatalf("podcast state = %+v", p)
	}

	// podcasts library created lazily
	var libType string
	if err := db.QueryRow(`SELECT type FROM libraries WHERE id = ?`, p.LibraryID).Scan(&libType); err != nil || libType != "podcasts" {
		t.Fatalf("library type = %q err = %v, want podcasts", libType, err)
	}

	eps, err := db.PodcastEpisodes(p.ID)
	if err != nil || len(eps) != 2 {
		t.Fatalf("episodes = %v err = %v, want 2", eps, err)
	}
	// newest first
	if eps[0].GUID != "guid-2" || eps[1].GUID != "guid-1" {
		t.Fatalf("episode order = %s, %s", eps[0].GUID, eps[1].GUID)
	}
	if eps[0].DurationSecs == nil || *eps[0].DurationSecs != 3723 {
		t.Fatalf("duration = %v, want 3723 (1:02:03)", eps[0].DurationSecs)
	}
	if eps[0].PubDate == nil {
		t.Fatal("pubDate not parsed")
	}

	// both episodes downloaded: file rows linked, files on disk, hashed
	for _, ep := range eps {
		if ep.FileID == nil || ep.DownloadedAt == nil {
			t.Fatalf("episode %s not downloaded: %+v", ep.GUID, ep)
		}
		path, err := db.FilePath(*ep.FileID)
		if err != nil {
			t.Fatalf("file row for %s: %v", ep.GUID, err)
		}
		want := int64(len("fake-audio-" + ep.GUID + ".mp3"))
		var size int64
		var hash sql.NullString
		var editionID sql.NullInt64
		if err := db.QueryRow(`SELECT size_bytes, hash, edition_id FROM files WHERE id = ?`, *ep.FileID).Scan(&size, &hash, &editionID); err != nil {
			t.Fatal(err)
		}
		if size != want || !hash.Valid || !strings.HasSuffix(hash.String, fmt.Sprintf("-%d", want)) {
			t.Fatalf("file row size=%d hash=%v, want size %d with full-file hash", size, hash.String, want)
		}
		if editionID.Valid {
			t.Fatalf("podcast file has edition_id %d, want NULL", editionID.Int64)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("downloaded file missing: %v", err)
		}
		if !strings.HasPrefix(path, filepath.Join(svc.DataDir, "podcasts")) {
			t.Fatalf("file outside data/podcasts: %s", path)
		}
	}

	// cover stored under scan's covers convention
	cover := filepath.Join(svc.DataDir, "covers", fmt.Sprintf("podcast-%d.jpg", p.ID))
	if _, err := os.Stat(cover); err != nil {
		t.Fatalf("cover not downloaded: %v", err)
	}
	fresh, _ := db.Podcast(p.ID)
	if fresh.CoverPath == nil || *fresh.CoverPath != fmt.Sprintf("podcast-%d.jpg", p.ID) {
		t.Fatalf("cover_path = %v", fresh.CoverPath)
	}
}

func TestSubscribeDuplicateFeed(t *testing.T) {
	svc, _ := newTestService(t)
	fs := newFeedServer(t, "Test Cast")
	if _, err := svc.Subscribe(context.Background(), fs.URL+"/feed", true, 3); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Subscribe(context.Background(), fs.URL+"/feed", true, 3)
	if err != ErrDuplicateFeed {
		t.Fatalf("err = %v, want ErrDuplicateFeed", err)
	}
	if p == nil || p.ID == 0 {
		t.Fatal("duplicate subscribe should return existing podcast")
	}
}

func TestAutoDownloadOff(t *testing.T) {
	svc, db := newTestService(t)
	fs := newFeedServer(t, "Test Cast")
	p, err := svc.Subscribe(context.Background(), fs.URL+"/feed", false, 3)
	if err != nil {
		t.Fatal(err)
	}
	eps, _ := db.PodcastEpisodes(p.ID)
	if len(eps) != 2 {
		t.Fatalf("episodes = %d, want 2", len(eps))
	}
	for _, ep := range eps {
		if ep.FileID != nil || ep.DownloadedAt != nil {
			t.Fatalf("episode %s downloaded despite auto-download off: %+v", ep.GUID, ep)
		}
	}
}

func TestRetentionKeepsNewest(t *testing.T) {
	svc, db := newTestService(t)
	fs := newFeedServer(t, "Test Cast")
	p, err := svc.Subscribe(context.Background(), fs.URL+"/feed", true, 2)
	if err != nil {
		t.Fatal(err)
	}

	// tighten retention below the downloaded set, then let a new episode
	// arrive: download triggers the purge of the overflow
	if err := db.UpdatePodcastSettings(p.ID, nil, ptr(1)); err != nil {
		t.Fatal(err)
	}
	fs.setItems([]feedItem{
		{"guid-1", "Episode One", "Tue, 10 Dec 2024 06:00:00 +0000"},
		{"guid-2", "Episode Two", "Tue, 17 Dec 2024 06:00:00 +0000"},
		{"guid-3", "Episode Three", "Tue, 24 Dec 2024 06:00:00 +0000"},
	}, "v2")
	if _, _, err := svc.RefreshPodcast(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}

	eps, _ := db.PodcastEpisodes(p.ID)
	var kept, purged *store.PodcastEpisode
	for i := range eps {
		switch eps[i].GUID {
		case "guid-3":
			kept = &eps[i]
		case "guid-1":
			purged = &eps[i]
		}
	}
	if kept == nil || kept.FileID == nil {
		t.Fatal("newest episode not kept")
	}
	if purged == nil || purged.FileID != nil {
		t.Fatal("oldest episode still linked to a file after retention")
	}
	if purged.DownloadedAt == nil {
		t.Fatal("purged episode lost downloaded_at (would re-download forever)")
	}
	if _, err := db.FilePath(*kept.FileID); err != nil {
		t.Fatal(err)
	}
	// purged file row is marked missing and its file freed from disk
	var missing int
	var path string
	if err := db.QueryRow(`SELECT missing, path FROM files WHERE path LIKE ?`, "%/Episode_One.mp3").Scan(&missing, &path); err != nil {
		t.Fatal(err)
	}
	if missing != 1 {
		t.Fatalf("purged file missing flag = %d, want 1", missing)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("purged file still on disk: %s", path)
	}

	// purged episodes are not pending: a changed feed (new etag, same items)
	// must not re-download them
	fs.setItems([]feedItem{
		{"guid-1", "Episode One", "Tue, 10 Dec 2024 06:00:00 +0000"},
		{"guid-2", "Episode Two", "Tue, 17 Dec 2024 06:00:00 +0000"},
		{"guid-3", "Episode Three", "Tue, 24 Dec 2024 06:00:00 +0000"},
	}, "v3")
	if _, _, err := svc.RefreshPodcast(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := db.PodcastEpisodes(p.ID)
	for i := range after {
		if after[i].GUID == "guid-1" && after[i].FileID != nil {
			t.Fatal("purged episode re-downloaded")
		}
	}
}

func ptr(v int) *int { return &v }

func TestRefresh304SkipsParse(t *testing.T) {
	svc, db := newTestService(t)
	fs := newFeedServer(t, "Test Cast")
	p, err := svc.Subscribe(context.Background(), fs.URL+"/feed", true, 3)
	if err != nil {
		t.Fatal(err)
	}
	hits := fs.hits()
	before, _ := db.PodcastEpisodes(p.ID)

	fresh, changed, err := svc.RefreshPodcast(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("unchanged feed reported changed")
	}
	if fs.hits() != hits+1 {
		t.Fatalf("feed hits = %d, want %d", fs.hits(), hits+1)
	}
	seen := fs.seenIfNoneMatch()
	if len(seen) == 0 || seen[len(seen)-1] != "v1" {
		t.Fatalf("If-None-Match not sent: %v", seen)
	}
	after, _ := db.PodcastEpisodes(p.ID)
	if len(after) != len(before) {
		t.Fatalf("episodes after 304 = %d, want %d", len(after), len(before))
	}
	if fresh.LastFetchAt == nil || *fresh.LastFetchAt < time.Now().Add(-time.Minute).UnixMilli() {
		t.Fatal("last_fetch_at not updated on 304")
	}
}

func TestRefreshAddsEpisodesAndDedups(t *testing.T) {
	svc, db := newTestService(t)
	fs := newFeedServer(t, "Test Cast")
	p, err := svc.Subscribe(context.Background(), fs.URL+"/feed", true, 3)
	if err != nil {
		t.Fatal(err)
	}
	fs.setItems([]feedItem{
		{"guid-1", "Episode One", "Tue, 10 Dec 2024 06:00:00 +0000"},
		{"guid-2", "Episode Two", "Tue, 17 Dec 2024 06:00:00 +0000"},
		{"guid-3", "Episode Three", "Tue, 24 Dec 2024 06:00:00 +0000"},
	}, "v2")

	_, changed, err := svc.RefreshPodcast(context.Background(), p.ID)
	if err != nil || !changed {
		t.Fatalf("refresh = changed=%v err=%v", changed, err)
	}
	eps, _ := db.PodcastEpisodes(p.ID)
	if len(eps) != 3 {
		t.Fatalf("episodes = %d, want 3 (dedup by guid)", len(eps))
	}
	guids := map[string]bool{}
	for _, ep := range eps {
		guids[ep.GUID] = true
	}
	if !guids["guid-3"] || len(guids) != 3 {
		t.Fatalf("guids = %v", guids)
	}
}

func TestRefreshStaleGate(t *testing.T) {
	svc, db := newTestService(t)
	fs := newFeedServer(t, "Test Cast")
	p, err := svc.Subscribe(context.Background(), fs.URL+"/feed", true, 3)
	if err != nil {
		t.Fatal(err)
	}
	hits := fs.hits()

	// fresh: skipped entirely
	svc.RefreshAll(context.Background(), true)
	if fs.hits() != hits {
		t.Fatalf("fresh feed refreshed: hits %d -> %d", hits, fs.hits())
	}

	// backdate beyond the 1h threshold: refreshed
	if _, err := db.Exec(`UPDATE podcasts SET last_fetch_at = 1 WHERE id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}
	svc.RefreshAll(context.Background(), true)
	if fs.hits() != hits+1 {
		t.Fatalf("stale feed not refreshed: hits %d -> %d", hits, fs.hits())
	}
}

func TestRefreshBusyGuard(t *testing.T) {
	svc, _ := newTestService(t)
	fs := newFeedServer(t, "Test Cast")
	p, err := svc.Subscribe(context.Background(), fs.URL+"/feed", true, 3)
	if err != nil {
		t.Fatal(err)
	}
	svc.acquire(p.ID)
	_, _, err = svc.RefreshPodcast(context.Background(), p.ID)
	if err != ErrRefreshBusy {
		t.Fatalf("err = %v, want ErrRefreshBusy", err)
	}
	svc.release(p.ID)
	if _, _, err := svc.RefreshPodcast(context.Background(), p.ID); err != nil {
		t.Fatalf("refresh after release: %v", err)
	}
}

func TestDeletePodcastRemovesFiles(t *testing.T) {
	svc, db := newTestService(t)
	fs := newFeedServer(t, "Test Cast")
	p, err := svc.Subscribe(context.Background(), fs.URL+"/feed", true, 3)
	if err != nil {
		t.Fatal(err)
	}
	eps, _ := db.PodcastEpisodes(p.ID)
	files, err := db.FilesForPodcast(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	if len(eps) != 2 || len(paths) != 2 {
		t.Fatalf("expected 2 episodes / 2 downloaded files, got %d/%d", len(eps), len(paths))
	}
	if _, err := db.Exec(`UPDATE files SET missing = 1 WHERE id = ?`, files[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeletePodcast(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Podcast(p.ID); err == nil {
		t.Fatal("podcast row not deleted")
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM podcast_episodes`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d episode rows survived delete", n)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("file %s still on disk after unsubscribe", path)
		}
	}
	if _, err := os.Stat(filepath.Join(svc.DataDir, "covers", fmt.Sprintf("podcast-%d.jpg", p.ID))); !os.IsNotExist(err) {
		t.Fatal("cover survived unsubscribe")
	}
}

func TestDeletePodcastKeepsFilesWhenDBFails(t *testing.T) {
	svc, db := newTestService(t)
	fs := newFeedServer(t, "Test Cast")
	p, err := svc.Subscribe(context.Background(), fs.URL+"/feed", true, 3)
	if err != nil {
		t.Fatal(err)
	}
	files, err := db.FilesForPodcast(p.ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("files = %v err = %v, want 2 downloaded", files, err)
	}
	if _, err := db.Exec(`CREATE TRIGGER block_ep_delete BEFORE DELETE ON podcast_episodes
		BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeletePodcast(p.ID); err == nil {
		t.Fatal("DeletePodcast should fail when the DB delete fails")
	}
	if _, err := db.Podcast(p.ID); err != nil {
		t.Fatal("podcast row deleted despite failed unsubscribe")
	}
	for _, f := range files {
		if _, err := os.Stat(f.Path); err != nil {
			t.Fatalf("file %s removed from disk before DB rows deleted: %v", f.Path, err)
		}
	}
}

func TestSubscribeHoldsInflightLockDuringDownloads(t *testing.T) {
	svc, db := newTestService(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/feed", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Lock Cast</title>
		<item><title>Ep</title><guid>lock-1</guid>
		<pubDate>Tue, 10 Dec 2024 06:00:00 +0000</pubDate>
		<enclosure url="%s/enclosure/lock-1.mp3" length="1024" type="audio/mpeg"/></item>
		</channel></rss>`, "http://"+r.Host)
	})
	mux.HandleFunc("/enclosure/lock-1.mp3", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, "fake-audio-lock-1.mp3")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	subDone := make(chan error, 1)
	var p *store.Podcast
	go func() {
		var err error
		p, err = svc.Subscribe(context.Background(), srv.URL+"/feed", true, 3)
		subDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("enclosure never fetched")
	}
	existing, err := db.PodcastByFeedURL(srv.URL + "/feed")
	if err != nil {
		t.Fatalf("podcast row missing while subscribe runs: %v", err)
	}
	if _, _, err := svc.RefreshPodcast(context.Background(), existing.ID); err != ErrRefreshBusy {
		t.Fatalf("refresh during subscribe download = %v, want ErrRefreshBusy", err)
	}
	close(release)
	if err := <-subDone; err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, _, err := svc.RefreshPodcast(context.Background(), p.ID); err == ErrRefreshBusy {
		t.Fatal("lock not released after subscribe")
	}
}

func TestConcurrentDownloadEpisodesUseDistinctTempFiles(t *testing.T) {
	svc, db := newTestService(t)
	body := "fake-audio-concurrent"
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	libID, err := db.EnsurePodcastsLibrary(filepath.Join(svc.DataDir, "podcasts"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := db.AddPodcast(&store.Podcast{LibraryID: libID, FeedURL: srv.URL + "/feed", Title: "Cast", AutoDownload: true, MaxEpisodes: 5})
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.Podcast(pid)
	if err != nil {
		t.Fatal(err)
	}
	ep := &store.PodcastEpisode{PodcastID: pid, GUID: "c-1", Title: strPtr("Ep"), EnclosureURL: srv.URL + "/e.mp3"}
	if _, err := db.UpsertPodcastEpisode(ep); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	close(block)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.downloadEpisode(context.Background(), p, ep); err != nil {
				t.Errorf("downloadEpisode: %v", err)
			}
		}()
	}
	wg.Wait()

	dir := filepath.Join(svc.DataDir, "podcasts", strconv.FormatInt(pid, 10))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var parts, finals int
	var finalName string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			parts++
		} else {
			finals++
			finalName = e.Name()
		}
	}
	if parts != 0 || finals != 1 {
		t.Fatalf("dir = %d finals, %d leftover .part files, want 1/0", finals, parts)
	}
	got, err := os.ReadFile(filepath.Join(dir, finalName))
	if err != nil || string(got) != body {
		t.Fatalf("final file = %q err = %v, want complete body", got, err)
	}
}

func TestNormalizeFeedURL(t *testing.T) {
	cases := map[string]string{
		"http://example.com/feed":        "https://example.com/feed",
		"http://example.com/feed/":       "https://example.com/feed",
		"https://example.com/feed/":      "https://example.com/feed",
		"https://example.com/feed#frag":  "https://example.com/feed",
		"http://example.com/feed/?q=1#f": "https://example.com/feed?q=1",
		"http://example.com/":            "https://example.com",
		"https://example.com":            "https://example.com",
		"http://localhost/feed":          "http://localhost/feed",
		"http://127.0.0.1:8096/feed/":    "http://127.0.0.1:8096/feed",
		"http://[::1]/feed/":             "http://[::1]/feed",
		"not a url":                      "not a url",
	}
	for in, want := range cases {
		if got := normalizeFeedURL(in); got != want {
			t.Errorf("normalizeFeedURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubscribeNormalizesFeedURL(t *testing.T) {
	svc, db := newTestService(t)
	fs := newFeedServer(t, "Norm Cast")
	feed := fs.URL + "/feed"
	p, err := svc.Subscribe(context.Background(), feed+"/#frag", true, 3)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := db.PodcastByFeedURL(feed)
	if err != nil || stored.ID != p.ID {
		t.Fatalf("lookup by normalized url = %v err = %v", stored, err)
	}
	if _, err := svc.Subscribe(context.Background(), feed+"/", true, 3); err != ErrDuplicateFeed {
		t.Fatalf("err = %v, want ErrDuplicateFeed", err)
	}
}

func TestParseDurationSecs(t *testing.T) {
	cases := map[string]float64{
		"1:02:03":   3723,
		"02:03":     123,
		"83":        83,
		"0":         0,
		"":          0,
		"1:00:00.5": 3600.5,
		"bogus":     0,
	}
	for in, want := range cases {
		if got := parseDurationSecs(in); got != want {
			t.Errorf("parseDurationSecs(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseDateMs(t *testing.T) {
	if got := parseDateMs("Tue, 10 Dec 2024 06:00:00 +0000"); got == 0 {
		t.Error("RFC1123Z date not parsed")
	}
	if got := parseDateMs("Wed, 2 Oct 2024 05:00:00 -0700"); got == 0 {
		t.Error("single-digit day not parsed")
	}
	if got := parseDateMs("2024-12-10"); got == 0 {
		t.Error("ISO date not parsed")
	}
	if parseDateMs("not a date") != 0 || parseDateMs("") != 0 {
		t.Error("invalid dates must parse to 0")
	}
}

func TestOPMLParseAndRoundtrip(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0"><head><title>subs</title></head><body>
<outline text="Folder">
  <outline type="rss" text="A" xmlUrl="http://example.com/a"/>
</outline>
<outline type="rss" text="B" xmlUrl="http://example.com/b"/>
<outline type="rss" text="A dup" xmlUrl="http://example.com/a"/>
</body></opml>`
	urls, err := ParseOPML([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"http://example.com/a", "http://example.com/b"}
	if len(urls) != len(want) {
		t.Fatalf("urls = %v, want %v", urls, want)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Fatalf("urls = %v, want %v", urls, want)
		}
	}

	podcasts := []store.Podcast{{Title: "A", FeedURL: "http://example.com/a"}, {Title: "B", FeedURL: "http://example.com/b"}}
	out, err := BuildOPML("libteca podcasts", podcasts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Fatal("missing xml declaration")
	}
	parsed, err := ParseOPML(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 2 || parsed[0] != want[0] || parsed[1] != want[1] {
		t.Fatalf("roundtrip = %v, want %v", parsed, want)
	}
}

func TestParseRSSItunesNamespace(t *testing.T) {
	xml := `<?xml version="1.0"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
<channel>
<title>NS Cast</title>
<author>wrong@example.com</author>
<itunes:image href="http://example.com/it.jpg"/>
<item><guid>g1</guid><pubDate>Tue, 10 Dec 2024 06:00:00 +0000</pubDate>
<itunes:duration>90</itunes:duration><itunes:summary>sum</itunes:summary>
<enclosure url="http://example.com/e1.mp3" length="5" type="audio/mpeg"/></item>
</channel></rss>`
	feed, err := ParseRSS(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	if feed.Title != "NS Cast" || feed.Author != "wrong@example.com" || feed.ImageURL != "http://example.com/it.jpg" {
		t.Fatalf("feed = %+v", feed)
	}
	if len(feed.Episodes) != 1 {
		t.Fatalf("episodes = %d", len(feed.Episodes))
	}
	ep := feed.Episodes[0]
	if ep.DurationSecs != 90 || ep.EnclosureBytes != 5 || ep.Description != "sum" {
		t.Fatalf("episode = %+v", ep)
	}
	if ep.GUID != "g1" {
		t.Fatalf("guid = %q", ep.GUID)
	}
}

func TestParseRSSMissingGuidFallsBackToEnclosure(t *testing.T) {
	xml := `<rss version="2.0"><channel><title>G</title>
<item><enclosure url="http://example.com/x.mp3" length="1" type="audio/mpeg"/></item>
</channel></rss>`
	feed, err := ParseRSS(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Episodes) != 1 || feed.Episodes[0].GUID != "http://example.com/x.mp3" {
		t.Fatalf("feed = %+v", feed)
	}
}
