package podcast

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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
func (s *Service) downloadPending(ctx context.Context, p *store.Podcast) error {
	pending, err := s.DB.PendingEpisodes(p.ID)
	if err != nil {
		return err
	}
	var failures []error
	for i := range pending {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		if i < p.MaxEpisodes {
			if err := s.downloadEpisode(ctx, p, &pending[i]); err != nil {
				failures = append(failures, fmt.Errorf("episode %d download: %w", pending[i].ID, err))
			}
		} else if err := s.DB.MarkEpisodeSeen(pending[i].ID); err != nil {
			failures = append(failures, fmt.Errorf("episode %d mark seen: %w", pending[i].ID, err))
		}
	}
	return errors.Join(failures...)
}

const maxEpisodeBytes int64 = 2 << 30
const maxEpisodeReadTime = 2 * time.Hour

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
	used := func(candidate string) (bool, error) {
		otherID, err := s.DB.EpisodeUsingFilePath(candidate)
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return true, err
		}
		return otherID != ep.ID, nil
	}
	taken, err := used(path)
	if err != nil {
		return err
	}
	if taken {
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.%s", name, ep.ID, ext))
		taken, err = used(path)
		if err != nil {
			return err
		}
		if taken {
			var nonce [6]byte
			if _, rerr := rand.Read(nonce[:]); rerr != nil {
				return rerr
			}
			path = filepath.Join(dir, fmt.Sprintf("%s-%d-%s.%s", name, ep.ID, hex.EncodeToString(nonce[:]), ext))
		}
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
	if resp.ContentLength > maxEpisodeBytes {
		return fmt.Errorf("enclosure exceeds the %d-byte episode limit", maxEpisodeBytes)
	}

	readCtx, cancel := context.WithTimeout(ctx, s.downloadTimeout(resp.ContentLength))
	defer cancel()
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-readCtx.Done():
			resp.Body.Close()
		case <-watchDone:
		}
	}()

	tmp, err := os.CreateTemp(dir, name+".*.part")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	h := xxhash.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxEpisodeBytes+1))
	if err == nil && size > maxEpisodeBytes {
		err = fmt.Errorf("enclosure exceeds the %d-byte episode limit", maxEpisodeBytes)
	}
	if err == nil && size == 0 {
		err = fmt.Errorf("empty enclosure body")
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Link(tmpPath, path); err != nil {
		otherID, lerr := s.DB.EpisodeUsingFilePath(path)
		if lerr != nil && !errors.Is(lerr, store.ErrNotFound) {
			os.Remove(tmpPath)
			return lerr
		}
		if errors.Is(lerr, store.ErrNotFound) || otherID == ep.ID {
			if rerr := os.Rename(tmpPath, path); rerr != nil {
				return rerr
			}
		} else {
			os.Remove(tmpPath)
			return fmt.Errorf("episode filename collision at publish: %s", path)
		}
	} else {
		os.Remove(tmpPath)
	}

	fileID, err := s.DB.InsertPodcastFile(path, size, time.Now().Unix(), fmt.Sprintf("%x-%d", h.Sum64(), size), derefFlt(ep.DurationSecs), ext)
	if err != nil {
		osRemove(path)
		return err
	}
	if err := s.DB.LinkEpisodeFile(ep.ID, fileID); err != nil {
		s.DB.DeleteFilesByIDs([]int64{fileID})
		osRemove(path)
		return err
	}
	return nil
}

func (s *Service) downloadTimeout(contentLength int64) time.Duration {
	if contentLength < 0 {
		return maxEpisodeReadTime
	}
	floor := s.dlReadFloor
	if floor <= 0 {
		floor = 90 * time.Second
	}
	secs := contentLength / (32 * 1024)
	if contentLength%(32*1024) != 0 {
		secs++
	}
	maxSeconds := int64(maxEpisodeReadTime / time.Second)
	if secs > maxSeconds {
		secs = maxSeconds
	}
	duration := time.Duration(secs) * time.Second
	if duration < floor {
		duration = floor
	}
	if duration > maxEpisodeReadTime {
		duration = maxEpisodeReadTime
	}
	return duration
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
		if ep.FileID == nil {
			return fmt.Errorf("purge episode %d: downloaded episode has no file", ep.ID)
		}
		path, ok := paths[*ep.FileID]
		if !ok {
			return fmt.Errorf("purge episode %d: file %d is not tracked", ep.ID, *ep.FileID)
		}
		if !s.purgeablePath(path) {
			return fmt.Errorf("purge episode %d: refusing to remove uncontained path %s", ep.ID, path)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("purge episode %d: %w", ep.ID, err)
		}
		if err := s.DB.PurgeEpisode(ep.ID, *ep.FileID); err != nil {
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
