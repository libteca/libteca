package podcast

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

type cancellationTransport func(*http.Request) (*http.Response, error)

func (f cancellationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type cancelAtEOF struct {
	cancel context.CancelFunc
	done   bool
}

func (r *cancelAtEOF) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.cancel()
	return copy(p, "partial audio"), io.EOF
}

func (r *cancelAtEOF) Close() error { return nil }

func cancellationPodcast(t *testing.T, svc *Service) *store.Podcast {
	t.Helper()
	libID, err := svc.DB.EnsurePodcastsLibrary(filepath.Join(svc.DataDir, "podcasts"))
	if err != nil {
		t.Fatal(err)
	}
	p := &store.Podcast{LibraryID: libID, FeedURL: "https://example.com/feed", Title: "Cast", MaxEpisodes: 3}
	p.ID, err = svc.DB.AddPodcast(p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCancelledDownloadAtEOFStaysPending(t *testing.T) {
	svc, db := newTestService(t)
	p := cancellationPodcast(t, svc)
	ep := &store.PodcastEpisode{PodcastID: p.ID, GUID: "cancelled", Title: strPtr("Episode"), EnclosureURL: "https://example.com/audio.mp3"}
	if _, err := db.UpsertPodcastEpisode(ep); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.DLClient = &http.Client{Transport: cancellationTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: &cancelAtEOF{cancel: cancel}, ContentLength: -1, Header: make(http.Header)}, nil
	})}
	if err := svc.downloadEpisode(ctx, p, ep); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled download = %v, want context.Canceled", err)
	}
	fresh, err := db.EpisodeByID(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.FileID != nil || fresh.DownloadedAt != nil {
		t.Fatalf("cancelled episode marked downloaded: %+v", fresh)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM files`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cancelled file rows = %d, err %v", count, err)
	}
	dir := filepath.Join(svc.DataDir, "podcasts", strconv.FormatInt(p.ID, 10))
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("cancelled bytes left behind: %v, err %v", entries, err)
	}
	svc.DLClient = &http.Client{Transport: cancellationTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("complete audio")), ContentLength: 14, Header: make(http.Header)}, nil
	})}
	if err := svc.downloadPending(context.Background(), p); err != nil {
		t.Fatalf("retry: %v", err)
	}
	fresh, err = db.EpisodeByID(ep.ID)
	if err != nil || fresh.FileID == nil || fresh.DownloadedAt == nil {
		t.Fatalf("retry did not finish: %+v, err %v", fresh, err)
	}
}

func TestCancelledFeedDoesNotIngestEpisodes(t *testing.T) {
	svc, db := newTestService(t)
	p := cancellationPodcast(t, svc)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	feed := &Feed{Title: "Cast", Episodes: []Episode{{GUID: "cancelled", Title: "Episode", EnclosureURL: "https://example.com/audio.mp3"}}}
	if err := svc.applyFeed(ctx, p, feed); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled apply = %v, want context.Canceled", err)
	}
	episodes, err := db.PodcastEpisodes(p.ID)
	if err != nil || len(episodes) != 0 {
		t.Fatalf("cancelled feed ingested episodes: %v, err %v", episodes, err)
	}
}

func TestCancelledRefreshDoesNotDelayRetry(t *testing.T) {
	svc, db := newTestService(t)
	p := cancellationPodcast(t, svc)
	ctx, cancel := context.WithCancel(context.Background())
	svc.fetcher.Client = &http.Client{Transport: cancellationTransport(func(r *http.Request) (*http.Response, error) {
		cancel()
		return nil, context.Canceled
	})}
	defer cancel()
	if _, _, err := svc.refresh(ctx, p); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled refresh = %v", err)
	}
	fresh, err := db.Podcast(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.LastFetchAt != nil {
		t.Fatalf("cancelled refresh advanced last fetch to %d", *fresh.LastFetchAt)
	}
}
