package podcast

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
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
	// lifecycleMu serializes subscription creation against library
	// deletion: a subscribe that inserts its row between a deletion's ID
	// snapshot and its transaction was deleted mid-download without ever
	// being locked.
	lifecycleMu sync.Mutex
	sem         chan struct{}
	dlReadFloor time.Duration
}

// publicHTTPClient builds a client whose dial refuses every non-public
// destination (loopback, RFC1918/ULA, link-local, multicast, unspecified) at
// resolve time, and whose redirects are re-validated. Feed, cover and
// enclosure URLs are attacker-supplied input to a server-side fetch; without
// this a subscription could probe local services and metadata endpoints
// reachable only from the host.
func publicHTTPClient(totalTimeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	// Never inherit HTTP(S)_PROXY: with a proxy selected, DialContext sees
	// the PROXY's address, so destination validation would vet the proxy
	// while the proxy itself could forward anywhere - including hosts this
	// guard exists to refuse.
	tr.Proxy = nil
	tr.ResponseHeaderTimeout = 30 * time.Second
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		// Try every public address: giving up after the first one made a
		// single unreachable A record fatal for a host with working mirrors.
		var lastErr error
		for _, ip := range ips {
			if !publicIP(ip) {
				continue
			}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("podcast URL resolves only to non-public addresses")
	}
	return &http.Client{
		Transport: tr,
		Timeout:   totalTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			if u := req.URL; u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("redirect to a non-http(s) URL")
			}
			return nil
		},
	}
}

var sharedV4 = netip.MustParsePrefix("100.64.0.0/10")

func publicIP(ip net.IP) bool {
	if ip == nil ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast() {
		return false
	}
	// IsPrivate does not cover RFC 6598 shared address space - which is
	// exactly the range Tailscale and carrier NAT live in, i.e. internal
	// endpoints in this server's own deployment model.
	if a, ok := netip.AddrFromSlice(ip); ok && sharedV4.Contains(a.Unmap()) {
		return false
	}
	return true
}

func New(db *store.DB, dataDir string) *Service {
	// Two clients: feeds are small and get a whole-request timeout; enclosure
	// downloads are large and rely on downloadEpisode's own size-derived read
	// deadline - one shared 30s client capped every big episode at 30 seconds.
	return NewWithClient(db, dataDir, publicHTTPClient(30*time.Second), publicHTTPClient(0))
}

// EgressGuardedClient exposes the guarded outbound transport for other
// packages fetching provider-supplied URLs (metadata covers): destination
// validation must not be podcast-only policy.
func EgressGuardedClient(timeout time.Duration) *http.Client {
	return publicHTTPClient(timeout)
}

// NewWithClient is the test seam: httptest servers bind loopback, which the
// egress guard refuses by design, so fixtures inject unrestricted clients
// through here. Production callers use New.
func NewWithClient(db *store.DB, dataDir string, client, dlClient *http.Client) *Service {
	return &Service{
		DB:          db,
		DataDir:     dataDir,
		Client:      client,
		DLClient:    dlClient,
		fetcher:     Fetcher{Client: client},
		inflight:    map[int64]bool{},
		sem:         make(chan struct{}, workerPool),
		dlReadFloor: 90 * time.Second,
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
	// Row publication and the single-flight acquisition are atomic against
	// library deletion (which holds the same gate): outside it, a subscribe
	// landing between a deletion's ID snapshot and its transaction was
	// deleted mid-download without ever being locked.
	s.lifecycleMu.Lock()
	libID, err := s.DB.EnsurePodcastsLibrary(filepath.Join(s.DataDir, "podcasts"))
	if err != nil {
		s.lifecycleMu.Unlock()
		return nil, err
	}
	p := &store.Podcast{
		LibraryID: libID, FeedURL: feedURL, Title: feed.Title,
		Author: strPtr(feed.Author), Description: strPtr(feed.Description),
		AutoDownload: autoDownload, MaxEpisodes: maxEpisodes,
	}
	p.ID, err = s.DB.AddPodcast(p)
	if err != nil {
		s.lifecycleMu.Unlock()
		if existing, qerr := s.DB.PodcastByFeedURL(feedURL); qerr == nil {
			return existing, ErrDuplicateFeed
		}
		return nil, err
	}
	acquired := s.acquire(p.ID)
	s.lifecycleMu.Unlock()
	if !acquired {
		return p, ErrRefreshBusy
	}
	defer s.release(p.ID)
	if feed.ImageURL != "" {
		s.fetchCover(ctx, p.ID, feed.ImageURL)
	}
	if err := s.applyFeed(ctx, p, feed); err != nil {
		return p, err
	}
	if err := s.DB.UpdatePodcastFetch(p.ID, nilOrEmpty(etag), nilOrEmpty(lastModified), nowMs()); err != nil {
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
		if ferr := s.DB.UpdatePodcastFetch(p.ID, p.ETag, p.LastModified, nowMs()); ferr != nil {
			return p, false, ferr
		}
		return p, false, err
	}
	if !changed {
		var errs []error
		if p.AutoDownload {
			if err := s.downloadPending(ctx, p); err != nil {
				errs = append(errs, err)
			}
		}
		// Retention runs even when a download failed: one permanently bad
		// enclosure used to suppress purging forever while other episodes
		// kept arriving, defeating the retained-episode count.
		if err := s.enforceRetention(p); err != nil {
			errs = append(errs, err)
		}
		if err := errors.Join(errs...); err != nil {
			return p, false, err
		}
		if err := s.DB.UpdatePodcastFetch(p.ID, p.ETag, p.LastModified, nowMs()); err != nil {
			return p, false, err
		}
		return p, false, nil
	}
	if feed.Title != "" {
		if err := s.DB.UpdatePodcastMeta(p.ID, feed.Title, strPtr(feed.Author), strPtr(feed.Description)); err != nil {
			return p, true, err
		}
		p.Title = feed.Title
	}
	if derefStr(p.CoverPath) == "" && feed.ImageURL != "" {
		s.fetchCover(ctx, p.ID, feed.ImageURL)
	}
	if err := s.applyFeed(ctx, p, feed); err != nil {
		return p, true, err
	}
	if err := s.DB.UpdatePodcastFetch(p.ID, nilOrEmpty(newETag), nilOrEmpty(newMod), nowMs()); err != nil {
		return p, true, err
	}
	fresh, err := s.DB.Podcast(p.ID)
	return fresh, true, err
}

// applyFeed upserts all feed episodes, then (if auto-download is on)
// downloads the newest pending episodes up to maxEpisodes — the rest are
// marked seen so the back catalog is not re-chewed every refresh — and
// finally enforces retention. A download failure does not skip retention:
// both errors are reported.
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
	var errs []error
	if p.AutoDownload {
		if err := s.downloadPending(ctx, p); err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.enforceRetention(p); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// DeletePodcast removes the subscription, its episodes and their files
// (rows + on-disk; an explicit unsubscribe deletes content, unlike retention).
// DB rows are deleted first so a failure mid-delete never strands rows
// pointing at already-deleted files; on-disk removal afterwards is
// best-effort.
func (s *Service) DeletePodcast(id int64) error {
	if !s.acquire(id) {
		return ErrRefreshBusy
	}
	defer s.release(id)
	// Cover state is read UNDER the slot: a refresh holding it could write a
	// new cover after an earlier read, and the post-commit cleanup would
	// then leave the new bytes orphaned.
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
	// One transaction for the subscription, its episodes and its file rows:
	// the old two-step committed the subscription first, so a failure on the
	// files delete orphaned rows while reporting an error.
	if _, err := s.DB.DeletePodcastWithFiles(id); err != nil {
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

// DeleteLibraryPodcasts removes every subscription of a podcasts library
// atomically: all per-podcast single-flight slots are acquired BEFORE
// anything is deleted, so a busy subscription aborts the whole operation
// instead of leaving half a library permanently deleted behind a 409.
func (s *Service) DeleteLibraryPodcasts(libID int64) error {
	// The whole operation runs under the lifecycle gate: Subscribe holds it
	// while inserting a new subscription, so no row can appear between this
	// ID snapshot and the transaction.
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	// Only stable IDs are snapshotted here; covers are re-read under the
	// slots below, after exclusion is guaranteed.
	all, err := s.DB.Podcasts()
	if err != nil {
		return err
	}
	var ids []int64
	for i := range all {
		if all[i].LibraryID == libID {
			ids = append(ids, all[i].ID)
		}
	}
	if len(ids) == 0 {
		// An empty podcasts library still has its row deleted: returning
		// success here left the library undeletable once its last
		// subscription was removed.
		_, err := s.DB.DeletePodcastsLibrary(libID)
		return err
	}

	// The single-flight slots are taken BEFORE any path/cover snapshot: a
	// refresh that was still running at snapshot time could link new
	// enclosures after it, and the post-commit cleanup would then remove
	// nothing while the transaction deleted the rows - orphaning bytes.
	s.mu.Lock()
	for _, id := range ids {
		if s.inflight[id] {
			s.mu.Unlock()
			return ErrRefreshBusy
		}
	}
	for _, id := range ids {
		s.inflight[id] = true
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		for _, id := range ids {
			delete(s.inflight, id)
		}
		s.mu.Unlock()
	}()

	var covers []string
	fresh, err := s.DB.Podcasts()
	if err != nil {
		return err
	}
	for i := range fresh {
		if fresh[i].LibraryID == libID {
			if rel := derefStr(fresh[i].CoverPath); rel != "" {
				covers = append(covers, filepath.Join(s.DataDir, "covers", rel))
			}
		}
	}
	var paths []string
	for _, id := range ids {
		files, err := s.DB.FilesForPodcast(id)
		if err != nil {
			return err
		}
		for _, f := range files {
			if s.purgeablePath(f.Path) {
				paths = append(paths, f.Path)
			}
		}
	}

	if _, err := s.DB.DeletePodcastsLibrary(libID); err != nil {
		return err
	}
	for _, path := range paths {
		osRemove(path)
	}
	for _, cover := range covers {
		osRemove(cover)
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
