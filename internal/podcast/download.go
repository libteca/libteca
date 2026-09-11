package podcast

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"

	"github.com/libteca/libteca/internal/store"
)

// downloadPending downloads pending episodes newest-first, up to
// maxEpisodes this round; the remainder are marked seen (handled, not kept)
// so a deep back catalog is not re-chewed on every refresh.
func (s *Service) downloadPending(ctx context.Context, p *store.Podcast) {
	pending, err := s.DB.PendingEpisodes(p.ID)
	if err != nil {
		fmt.Printf("libteca: podcast %q pending query: %v\n", p.Title, err)
		return
	}
	for i := range pending {
		if i < p.MaxEpisodes {
			if err := s.downloadEpisode(ctx, p, &pending[i]); err != nil {
				fmt.Printf("libteca: podcast %q episode download: %v\n", p.Title, err)
			}
		} else {
			s.DB.MarkEpisodeSeen(pending[i].ID)
		}
	}
}

// downloadEpisode fetches the enclosure to
// data/podcasts/<podcastId>/<sanitized-title>.<ext>, hashes it (house rule:
// full-file xxhash for audio), links a files row (edition_id NULL — podcast
// episodes are not editions) and marks the episode downloaded.
func (s *Service) downloadEpisode(ctx context.Context, p *store.Podcast, ep *store.PodcastEpisode) error {
	dir := filepath.Join(s.DataDir, "podcasts", strconv.FormatInt(p.ID, 10))
	if err := mkdir(dir); err != nil {
		return err
	}
	name := sanitizeName(derefStr(ep.Title))
	if name == "" {
		name = sanitizeName(ep.GUID)
	}
	if name == "" {
		name = "episode"
	}
	ext := enclosureExt(ep.EnclosureURL)
	path := filepath.Join(dir, name+"."+ext)
	if otherID, err := s.DB.EpisodeUsingFilePath(path); err == nil && otherID != ep.ID {
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.%s", name, ep.ID, ext))
	}

	req, err := newGetRequest(ctx, ep.EnclosureURL)
	if err != nil {
		return err
	}
	resp, err := s.DLClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("enclosure fetch: %s", resp.Status)
	}

	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := xxhash.New()
	size, err := io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}

	fileID, err := s.DB.InsertPodcastFile(path, size, time.Now().Unix(), fmt.Sprintf("%x-%d", h.Sum64(), size), derefFlt(ep.DurationSecs), ext)
	if err != nil {
		os.Remove(path)
		return err
	}
	return s.DB.LinkEpisodeFile(ep.ID, fileID)
}

// enforceRetention keeps only the newest maxEpisodes downloaded episodes.
// Purged episode files are deleted from disk (guarded to the podcasts data
// dir) and their rows marked missing=1. The episode loses its file link but
// keeps downloaded_at, which also prevents re-downloading purged episodes.
func (s *Service) enforceRetention(p *store.Podcast) error {
	if p.MaxEpisodes <= 0 {
		return nil
	}
	downloaded, err := s.DB.DownloadedEpisodes(p.ID)
	if err != nil {
		return err
	}
	overflow := len(downloaded) - p.MaxEpisodes
	if overflow <= 0 {
		return nil
	}
	files, err := s.DB.FilesForPodcast(p.ID)
	if err != nil {
		return err
	}
	paths := make(map[int64]string, len(files))
	for _, f := range files {
		paths[f.ID] = f.Path
	}
	for i := 0; i < overflow; i++ {
		ep := downloaded[i]
		if path, ok := paths[*ep.FileID]; ok && s.purgeablePath(path) {
			osRemove(path)
		}
		if err := s.DB.MarkFileMissing(*ep.FileID); err != nil {
			return err
		}
		if err := s.DB.MarkEpisodePurged(ep.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) purgeablePath(path string) bool {
	dir := filepath.Clean(filepath.Dir(path))
	root := filepath.Clean(filepath.Join(s.DataDir, "podcasts"))
	return dir == root || strings.HasPrefix(dir, root+string(os.PathSeparator))
}

var audioExtWhitelist = map[string]bool{
	".mp3": true, ".m4a": true, ".m4b": true, ".mp4": true, ".aac": true,
	".ogg": true, ".oga": true, ".opus": true, ".wav": true, ".flac": true,
}

func enclosureExt(enclosureURL string) string {
	if u, err := url.Parse(enclosureURL); err == nil {
		if ext := strings.ToLower(filepath.Ext(u.Path)); audioExtWhitelist[ext] {
			return ext[1:]
		}
	}
	return "mp3"
}

// sanitizeName reduces a title to a safe filename component: [A-Za-z0-9._-],
// other bytes collapse to single underscores, trimmed, length-capped.
func sanitizeName(s string) string {
	var b strings.Builder
	wroteSep := false
	for i := 0; i < len(s) && b.Len() < 100; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '-' || c == '_':
			b.WriteByte(c)
			wroteSep = false
		default:
			if !wroteSep && b.Len() > 0 {
				b.WriteByte('_')
				wroteSep = true
			}
		}
	}
	return strings.Trim(b.String(), "._-")
}
