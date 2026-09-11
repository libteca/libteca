package podcast

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/libteca/libteca/internal/store"
)

const (
	refreshInterval = 15 * time.Minute
	staleThreshold  = time.Hour
	workerPool      = 2
)

var (
	ErrDuplicateFeed = errors.New("feed already subscribed")
	ErrRefreshBusy   = errors.New("refresh already running")
)

// Service owns subscribe/refresh/download/retention. It is safe for
// concurrent use; per-podcast refreshes are single-flight and the scheduler
// caps concurrent refreshes at workerPool.
type Service struct {
	DB       *store.DB
	DataDir  string
	Client   *http.Client // feeds + covers, 30s whole-request timeout
	DLClient *http.Client // enclosures: no total timeout, ctx-cancellable

	fetcher  Fetcher
	mu       sync.Mutex
	inflight map[int64]bool
	sem      chan struct{}
}

func New(db *store.DB, dataDir string) *Service {
	client := &http.Client{Timeout: 30 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 30 * time.Second
	return &Service{
		DB:       db,
		DataDir:  dataDir,
		Client:   client,
		DLClient: &http.Client{Transport: tr},
		fetcher:  Fetcher{Client: client},
		inflight: map[int64]bool{},
		sem:      make(chan struct{}, workerPool),
	}
}

// Subscribe fetches and parses a feed now, creates the podcasts library on
// first use, downloads the cover and the first maxEpisodes episodes.
func (s *Service) Subscribe(ctx context.Context, feedURL string, autoDownload bool, maxEpisodes int) (*store.Podcast, error) {
	feedURL = normalizeFeedURL(feedURL)
	if existing, err := s.DB.PodcastByFeedURL(feedURL); err == nil {
		return existing, ErrDuplicateFeed
	}
	feed, _, etag, lastModified, err := s.fetcher.FetchFeed(ctx, feedURL, "", "")
	if err != nil {
		return nil, err
	}
	libID, err := s.DB.EnsurePodcastsLibrary(filepath.Join(s.DataDir, "podcasts"))
	if err != nil {
		return nil, err
	}
	p := &store.Podcast{
		LibraryID: libID, FeedURL: feedURL, Title: feed.Title,
		Author: strPtr(feed.Author), Description: strPtr(feed.Description),
		AutoDownload: autoDownload, MaxEpisodes: maxEpisodes,
	}
	p.ID, err = s.DB.AddPodcast(p)
	if err != nil {
		if existing, qerr := s.DB.PodcastByFeedURL(feedURL); qerr == nil {
			return existing, ErrDuplicateFeed
		}
		return nil, err
	}
	s.DB.UpdatePodcastFetch(p.ID, nilOrEmpty(etag), nilOrEmpty(lastModified), nowMs())
	if !s.acquire(p.ID) {
		return p, ErrRefreshBusy
	}
	defer s.release(p.ID)
	if feed.ImageURL != "" {
		s.fetchCover(ctx, p.ID, feed.ImageURL)
	}
	if err := s.applyFeed(ctx, p, feed); err != nil {
		return p, err
	}
	return s.DB.Podcast(p.ID)
}

// RefreshPodcast fetches one feed; a 304 leaves everything untouched except
// last_fetch_at.
func (s *Service) RefreshPodcast(ctx context.Context, id int64) (p *store.Podcast, changed bool, err error) {
	p, err = s.DB.Podcast(id)
	if err != nil {
		return nil, false, err
	}
	if !s.acquire(p.ID) {
		return p, false, ErrRefreshBusy
	}
	defer s.release(p.ID)
	return s.refresh(ctx, p)
}

func (s *Service) refresh(ctx context.Context, p *store.Podcast) (*store.Podcast, bool, error) {
	etag, lastMod := derefStr(p.ETag), derefStr(p.LastModified)
	feed, changed, newETag, newMod, err := s.fetcher.FetchFeed(ctx, p.FeedURL, etag, lastMod)
	if err != nil {
		s.DB.UpdatePodcastFetch(p.ID, p.ETag, p.LastModified, nowMs())
		return p, false, err
	}
	s.DB.UpdatePodcastFetch(p.ID, nilOrEmpty(newETag), nilOrEmpty(newMod), nowMs())
	if !changed {
		return p, false, nil
	}
	if feed.Title != "" {
		s.DB.UpdatePodcastMeta(p.ID, feed.Title, strPtr(feed.Author), strPtr(feed.Description))
		p.Title = feed.Title
	}
	if derefStr(p.CoverPath) == "" && feed.ImageURL != "" {
		s.fetchCover(ctx, p.ID, feed.ImageURL)
	}
	if err := s.applyFeed(ctx, p, feed); err != nil {
		return p, true, err
	}
	fresh, err := s.DB.Podcast(p.ID)
	return fresh, true, err
}

// applyFeed upserts all feed episodes, then (if auto-download is on)
// downloads the newest pending episodes up to maxEpisodes — the rest are
// marked seen so the back catalog is not re-chewed every refresh — and
// finally enforces retention.
func (s *Service) applyFeed(ctx context.Context, p *store.Podcast, feed *Feed) error {
	for i := range feed.Episodes {
		ep := &feed.Episodes[i]
		_, err := s.DB.UpsertPodcastEpisode(&store.PodcastEpisode{
			PodcastID: p.ID, GUID: ep.GUID,
			Title: strPtr(ep.Title), Description: strPtr(ep.Description),
			PubDate: msPtr(ep.PubDateMs), DurationSecs: fltPtr(ep.DurationSecs),
			EnclosureURL: ep.EnclosureURL, EnclosureBytes: int64Ptr(ep.EnclosureBytes),
		})
		if err != nil {
			return err
		}
	}
	if p.AutoDownload {
		s.downloadPending(ctx, p)
	}
	return s.enforceRetention(p)
}

// DeletePodcast removes the subscription, its episodes and their files
// (rows + on-disk; an explicit unsubscribe deletes content, unlike retention).
// DB rows are deleted first so a failure mid-delete never strands rows
// pointing at already-deleted files; on-disk removal afterwards is
// best-effort.
func (s *Service) DeletePodcast(id int64) error {
	p, err := s.DB.Podcast(id)
	if err != nil {
		return err
	}
	files, err := s.DB.FilesForPodcast(id)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	ids := make([]int64, 0, len(files))
	for _, f := range files {
		ids = append(ids, f.ID)
		if s.purgeablePath(f.Path) {
			paths = append(paths, f.Path)
		}
	}
	if err := s.DB.DeletePodcast(id); err != nil {
		return err
	}
	if err := s.DB.DeleteFilesByIDs(ids); err != nil {
		return err
	}
	for _, path := range paths {
		osRemove(path)
	}
	if rel := derefStr(p.CoverPath); rel != "" {
		osRemove(filepath.Join(s.DataDir, "covers", rel))
	}
	return nil
}

// RefreshAll refreshes every podcast (or only those not fetched within
// staleThreshold), bounded to workerPool concurrent refreshes.
func (s *Service) RefreshAll(ctx context.Context, staleOnly bool) {
	all, err := s.DB.Podcasts()
	if err != nil {
		return
	}
	cutoff := nowMs() - staleThreshold.Milliseconds()
	podcasts := all[:0]
	for _, p := range all {
		if staleOnly && p.LastFetchAt != nil && *p.LastFetchAt >= cutoff {
			continue
		}
		podcasts = append(podcasts, p)
	}
	var wg sync.WaitGroup
	for i := range podcasts {
		p := podcasts[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case s.sem <- struct{}{}:
				defer func() { <-s.sem }()
			case <-ctx.Done():
				return
			}
			if !s.acquire(p.ID) {
				return
			}
			defer s.release(p.ID)
			if _, _, err := s.refresh(ctx, &p); err != nil {
				fmt.Printf("libteca: podcast refresh %q: %v\n", p.Title, err)
			}
		}()
	}
	wg.Wait()
}

// Run is the scheduler: refresh stale feeds at startup, then every 15
// minutes. It returns when ctx is cancelled; keep it a thin loop — all real
// logic lives in RefreshAll so it is testable without time.
func (s *Service) Run(ctx context.Context) {
	s.RefreshAll(ctx, true)
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RefreshAll(ctx, true)
		}
	}
}

func (s *Service) fetchCover(ctx context.Context, podcastID int64, url string) {
	data, err := s.fetcher.FetchBytes(ctx, url, coverLimit)
	if err != nil || len(data) == 0 {
		return
	}
	covers := filepath.Join(s.DataDir, "covers")
	if err := mkdir(covers); err != nil {
		return
	}
	name := fmt.Sprintf("podcast-%d.jpg", podcastID)
	if err := writeFile(filepath.Join(covers, name), data); err != nil {
		return
	}
	s.DB.SetPodcastCover(podcastID, name)
}

func (s *Service) acquire(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[id] {
		return false
	}
	s.inflight[id] = true
	return true
}

func (s *Service) release(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inflight, id)
}

func nowMs() int64 { return time.Now().UnixMilli() }
