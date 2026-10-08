package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/mediafs"
	"github.com/libteca/libteca/internal/procfd"
	"github.com/libteca/libteca/internal/store"
)

var srtStamp = regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}),(\d{1,3})`)

var (
	ffmpegLookPath = exec.LookPath
	ffmpegRun      = func(ctx context.Context, name string, files []*os.File, args ...string) ([]byte, error) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		out := &boundedWriter{limit: subtitleCacheLimit, cancel: cancel}
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.ExtraFiles = files
		cmd.Stdout = out
		err := cmd.Run()
		if out.over {
			return nil, fmt.Errorf("ffmpeg %s: output exceeds %d bytes", name, subtitleCacheLimit)
		}
		if err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
	errSubtitleBusy = errors.New("subtitle extraction capacity exhausted")
)

// boundedWriter caps collected child output and kills the child the moment
// the cap is exceeded - the pass-7 ffprobe pattern (audit F08) applied to
// subtitle extraction: a crafted stream must not buffer unbounded stdout
// for the whole 30s deadline. The buffer is a field, not an embedded
// value: embedding would promote bytes.Buffer's ReadFrom, and os.exec's
// internal io.Copy prefers ReaderFrom over Write, silently bypassing the
// cap (the same flaw the ffprobe boundedBuffer carried until audit C).
type boundedWriter struct {
	buf    bytes.Buffer
	limit  int
	cancel context.CancelFunc
	over   bool
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	room := b.limit - b.buf.Len()
	if room < 0 {
		room = 0
	}
	if len(p) > room {
		b.over = true
		if room > 0 {
			b.buf.Write(p[:room])
		}
		b.cancel()
	}
	if !b.over {
		b.buf.Write(p)
	}
	return len(p), nil
}

func (b *boundedWriter) Bytes() []byte { return b.buf.Bytes() }

var subtitleSlots = make(chan struct{}, 2)

const subtitleCacheLimit = 16 << 20

func (a *API) subtitles(w http.ResponseWriter, r *http.Request) {
	fid := auth.Atoi64(r.PathValue("fileId"))
	f, err := a.DB.FileByID(fid)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "file not found"})
		return
	}
	if expected := r.URL.Query().Get("generation"); expected != "" {
		current, err := a.DB.TimelineGeneration(f.EditionID)
		if err != nil || expected != current {
			writeJSON(w, 409, map[string]string{"error": "generation_mismatch"})
			return
		}
	}
	root, rerr := a.confinedFileRoot(f)
	if rerr != nil {
		writeJSON(w, 404, map[string]string{"error": "file not found"})
		return
	}
	side, ok := sidecarSRT(f.Path)
	if ok {
		sf, _, oerr := mediafs.OpenWithin(root, side)
		if oerr != nil {
			writeJSON(w, 404, map[string]string{"error": "no subtitles"})
			return
		}
		defer sf.Close()
		data, err := io.ReadAll(io.LimitReader(sf, subtitleCacheLimit))
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "no subtitles"})
			return
		}
		writeVTT(w, []byte(srtToVTT(string(data))))
		return
	}
	data, err := a.embeddedVTT(r.Context(), f)
	if err != nil {
		if errors.Is(err, errSubtitleBusy) {
			w.Header().Set("Retry-After", "5")
			writeJSON(w, 503, map[string]string{"error": "subtitle extraction busy"})
			return
		}
		writeJSON(w, 404, map[string]string{"error": "no subtitles"})
		return
	}
	writeVTT(w, data)
}

func writeVTT(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "text/vtt")
	w.Header().Set("Cache-Control", "max-age=86400")
	w.WriteHeader(200)
	w.Write(data)
}

// sidecarSRT looks for <base>.srt and <base>.<lang>[.flag].srt next to the
// media file; the plain sidecar wins, else the first lang match. The media
// path comes only from the files table (PLAN §11: never client-supplied paths).
func sidecarSRT(media string) (string, bool) {
	dir := filepath.Dir(media)
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(media), filepath.Ext(media)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	lang := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if !strings.HasSuffix(name, ".srt") || !strings.HasPrefix(name, base+".") {
			continue
		}
		if name == base+".srt" {
			return filepath.Join(dir, e.Name()), true
		}
		mid := name[len(base)+1 : len(name)-len(".srt")]
		ok := len(mid) > 0
		for _, r := range mid {
			if !(r >= 'a' && r <= 'z' || r == '.') {
				ok = false
				break
			}
		}
		if lang == "" && ok {
			lang = filepath.Join(dir, e.Name())
		}
	}
	if lang != "" {
		return lang, true
	}
	return "", false
}

func subtitleCacheKey(f *store.FileRec) string {
	digest := ""
	if f.SHA256 != nil {
		digest = *f.SHA256
	}
	sample := ""
	if f.Hash != nil {
		sample = *f.Hash
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("v2\x00%d\x00%s\x00%d\x00%d\x00%s\x00%s\x00%d", f.ID, f.Path, f.MtimeSecs, f.MtimeNS, digest, sample, f.SourceLibraryID)))
	return hex.EncodeToString(sum[:])
}

func readBoundedCache(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular cache file")
	}
	if fi.Size() > subtitleCacheLimit {
		return nil, fmt.Errorf("subtitle cache exceeds budget")
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	data, err := io.ReadAll(io.LimitReader(fh, subtitleCacheLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > subtitleCacheLimit {
		return nil, fmt.Errorf("subtitle cache exceeds budget")
	}
	return data, nil
}

func writeCacheAtomic(dst string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".subtitle-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, dst); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func extractEmbedded(ctx context.Context, src *os.File) ([]byte, error) {
	if _, err := ffmpegLookPath("ffmpeg"); err != nil {
		return nil, err
	}
	inArgs, err := procfd.Args("ffmpeg", "pipe")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := append([]string{"-v", "error"}, inArgs...)
	args = append(args, "-map", "0:s:0", "-f", "webvtt", "-")
	return ffmpegRun(ctx, "ffmpeg", []*os.File{src}, args...)
}

func (a *API) embeddedVTT(ctx context.Context, f *store.FileRec) ([]byte, error) {
	dir := filepath.Join(a.DataDir, "subtitles")
	dst := filepath.Join(dir, subtitleCacheKey(f)+".vtt")
	if data, err := readBoundedCache(dst); err == nil {
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("libteca: subtitle cache unreadable; re-extracting", "err", err)
	}
	select {
	case subtitleSlots <- struct{}{}:
		defer func() { <-subtitleSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, errSubtitleBusy
	}
	data, err := func() ([]byte, error) {
		root, rerr := a.confinedFileRoot(f)
		if rerr != nil {
			return nil, rerr
		}
		src, oerr := mediafs.Open(root, f.Path)
		if oerr != nil {
			return nil, oerr
		}
		defer src.Close()
		return extractEmbedded(ctx, src)
	}()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("libteca: subtitle cache directory unavailable", "err", err)
		return data, nil
	}
	if err := writeCacheAtomic(dst, data); err != nil {
		slog.Warn("libteca: subtitle cache write failed", "err", err)
	}
	return data, nil
}

func srtToVTT(s string) string {
	s = strings.TrimPrefix(s, "\uFEFF")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var out []string
	out = append(out, "WEBVTT", "")
	var cue []string
	flush := func() {
		if len(cue) == 0 {
			return
		}
		start := 0
		if len(cue) > 1 && isDigits(cue[0]) && strings.Contains(cue[1], "-->") {
			start = 1
		}
		for _, line := range cue[start:] {
			if strings.Contains(line, "-->") {
				line = srtStamp.ReplaceAllString(line, "$1.$2")
			}
			out = append(out, line)
		}
		out = append(out, "")
		cue = cue[:0]
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cue = append(cue, line)
	}
	flush()
	return strings.Join(out, "\n")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
