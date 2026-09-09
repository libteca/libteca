package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/libteca/libteca/internal/audio"
	"github.com/libteca/libteca/internal/store"
)

var videoExts = map[string]bool{".mp4": true, ".mkv": true, ".avi": true, ".webm": true, ".mov": true, ".m4v": true}
var musicExts = map[string]bool{".mp3": true, ".flac": true, ".m4a": true, ".ogg": true, ".opus": true, ".wav": true}

var (
	reSxxExx  = regexp.MustCompile(`[Ss](\d{1,2})[Ee](\d{1,3})`)
	reExx     = regexp.MustCompile(`^(\d{1,2})x(\d{1,3})`)
	reYear    = regexp.MustCompile(`[\(\[\s](19|20)(\d{2})[\)\]\s]?`)
	reSeasonD = regexp.MustCompile(`(?i)season[ _-]?(\d{1,2})`)
)

type vidFile struct {
	path      string
	name      string
	season    int
	episode   int
	epTitle   string
	size      int64
	mtime     int64
	codec     string
	vcodec    string
	container string
	width     int
	height    int
	bitrate   int64
	duration  float64
	hash      string
}

func (v *vidFile) probe() error {
	info, err := audio.Probe(v.path)
	if err != nil {
		return err
	}
	v.duration = info.Duration
	v.bitrate = info.Bitrate
	v.container = info.Container
	v.codec = info.Codec
	v.vcodec, v.width, v.height = probeVideo(v.path)
	v.hash = hashFile(v.path, v.size)
	return nil
}

func scanVideoLibrary(db *store.DB, lib *store.Library, coversDir string, tv bool) (int, error) {
	abs, _ := filepath.Abs(lib.Path)
	var files []vidFile
	var series = map[string][]vidFile{}
	filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != abs {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if !videoExts[ext] {
			return nil
		}
		rel, _ := filepath.Rel(abs, path)
		parts := strings.Split(rel, string(filepath.Separator))
		top := abs
		if len(parts) > 1 {
			top = filepath.Join(abs, parts[0])
		}
		fi, _ := d.Info()
		v := vidFile{path: path, name: d.Name(), size: fi.Size(), mtime: fi.ModTime().Unix()}
		if tv {
			parseEpisode(&v, path, rel)
		}
		files = append(files, v)
		series[top] = append(series[top], v)
		return nil
	})

	tops := make([]string, 0, len(series))
	for k := range series {
		tops = append(tops, k)
	}
	sort.Strings(tops)

	count := 0
	for _, top := range tops {
		group := series[top]
		sort.Slice(group, func(i, j int) bool {
			if group[i].season != group[j].season {
				return group[i].season < group[j].season
			}
			return group[i].episode < group[j].episode
		})
		title, year := titleYear(filepath.Base(top))
		authorPtr := nullable(year)
		w := &store.Work{LibraryID: lib.ID, Title: title, Author: &authorPtr}
		workID, err := db.UpsertWork(w)
		if err != nil {
			return count, err
		}
		for i := range group {
			f := &group[i]
			if err := f.probe(); err != nil {
				fmt.Fprintf(os.Stderr, "libteca: probe fail %s: %v\n", f.path, err)
				continue
			}
			edTitle := title
			if tv {
				if f.epTitle != "" {
					edTitle = f.epTitle
				} else {
					edTitle = fmt.Sprintf("S%02dE%02d", f.season, f.episode)
				}
			}
			dur := f.duration
			e := &store.Edition{WorkID: workID, Format: "video", Title: edTitle, DurationSecs: &dur}
			if tv {
				s, ep := f.season, f.episode
				e.SeasonNum, e.EpisodeNum = &s, &ep
				e.Position = int64(ep)
			}
			editionID, err := db.UpsertEdition(e)
			if err != nil {
				return count, err
			}
			c, ct, vc, w_, h, br := f.codec, f.container, f.vcodec, f.width, f.height, f.bitrate
			fr := &store.FileRec{
				EditionID: editionID, Path: f.path, Seq: 1,
				SizeBytes: f.size, MtimeSecs: f.mtime, Hash: &f.hash,
				Codec: &c, VideoCodec: &vc, Width: &w_, Height: &h, Container: &ct,
				Bitrate: &br, DurationSecs: f.duration, Chapters: "[]",
			}
			if err := db.UpsertFile(fr); err != nil {
				return count, err
			}
			count++
		}
		if err := ensureCoverVideo(db, workID, top, coversDir); err != nil {
			return count, err
		}
	}
	return count, nil
}

func ensureCoverVideo(db *store.DB, workID int64, top, coversDir string) error {
	dst := filepath.Join(coversDir, fmt.Sprintf("%d.jpg", workID))
	if _, err := os.Stat(dst); err == nil {
		return db.SetWorkCover(workID, fmt.Sprintf("%d.jpg", workID))
	}
	for _, name := range []string{"poster.jpg", "Poster.jpg", "cover.jpg", "Cover.jpg", "folder.jpg"} {
		src := filepath.Join(top, name)
		if data, err := os.ReadFile(src); err == nil {
			if werr := os.WriteFile(dst, data, 0o644); werr != nil {
				return werr
			}
			return db.SetWorkCover(workID, fmt.Sprintf("%d.jpg", workID))
		}
	}
	return nil
}

func parseEpisode(v *vidFile, path, rel string) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if m := reSxxExx.FindStringSubmatch(base); m != nil {
		v.season, _ = strconv.Atoi(m[1])
		v.episode, _ = strconv.Atoi(m[2])
		v.epTitle = cleanTitle(strings.TrimPrefix(base, m[0]))
		return
	}
	if m := reExx.FindStringSubmatch(base); m != nil {
		v.season, _ = strconv.Atoi(m[1])
		v.episode, _ = strconv.Atoi(m[2])
		v.epTitle = cleanTitle(strings.TrimPrefix(base, m[0]))
		return
	}
	dir := filepath.Base(filepath.Dir(path))
	if m := reSeasonD.FindStringSubmatch(dir); m != nil {
		v.season, _ = strconv.Atoi(m[1])
	}
	if n, err := strconv.Atoi(base); err == nil {
		v.episode = n
	}
}

func cleanTitle(s string) string {
	s = strings.NewReplacer(".", " ", "_", " ", "-", " ", "  ", " ").Replace(s)
	return strings.TrimSpace(strings.Trim(s, " -–—"))
}

func titleYear(base string) (string, string) {
	year := ""
	if m := reYear.FindStringSubmatch(base); m != nil {
		year = m[1] + m[2]
		base = strings.TrimSpace(reYear.ReplaceAllString(base, ""))
	}
	title := cleanTitle(base)
	if i := strings.Index(title, " - "); i > 0 && year == "" {
		return strings.TrimSpace(title[i+3:]), strings.TrimSpace(title[:i])
	}
	return title, year
}

func scanMusicLibrary(db *store.DB, lib *store.Library, coversDir string) (int, error) {
	abs, _ := filepath.Abs(lib.Path)
	type track struct {
		path  string
		num   int
		name  string
		size  int64
		mtime int64
	}
	albums := map[string][]track{}
	filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != abs {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if !musicExts[ext] {
			return nil
		}
		rel, _ := filepath.Rel(abs, path)
		parts := strings.Split(rel, string(filepath.Separator))
		top := abs
		if len(parts) > 1 {
			top = filepath.Join(abs, parts[0])
		}
		fi, _ := d.Info()
		base := strings.TrimSuffix(d.Name(), ext)
		num := 0
		if n, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(base, " ", 2)[0])); err == nil {
			num = n
		}
		albums[top] = append(albums[top], track{path: path, num: num, name: base, size: fi.Size(), mtime: fi.ModTime().Unix()})
		return nil
	})

	tops := make([]string, 0, len(albums))
	for k := range albums {
		tops = append(tops, k)
	}
	sort.Strings(tops)

	count := 0
	for _, top := range tops {
		group := albums[top]
		sort.Slice(group, func(i, j int) bool { return group[i].num < group[j].num })
		base := filepath.Base(top)
		title, artist := base, ""
		if i := strings.Index(base, " - "); i > 0 {
			artist, title = strings.TrimSpace(base[:i]), strings.TrimSpace(base[i+3:])
		}
		artistPtr := nullable(artist)
		w := &store.Work{LibraryID: lib.ID, Title: title, Author: &artistPtr}
		workID, err := db.UpsertWork(w)
		if err != nil {
			return count, err
		}
		for i, t := range group {
			info, err := audio.Probe(t.path)
			if err != nil {
				continue
			}
			trackTitle := t.name
			if v := info.Meta["title"]; v != "" {
				trackTitle = v
			}
			dur := info.Duration
			e := &store.Edition{WorkID: workID, Format: "audio", Title: trackTitle, DurationSecs: &dur, Position: int64(i + 1)}
			editionID, err := db.UpsertEdition(e)
			if err != nil {
				return count, err
			}
			c, ct, br := info.Codec, info.Container, info.Bitrate
			hash := hashFile(t.path, t.size)
			fr := &store.FileRec{
				EditionID: editionID, Path: t.path, Seq: 1, SizeBytes: t.size, MtimeSecs: t.mtime,
				Hash: &hash, Codec: &c, Container: &ct, Bitrate: &br,
				DurationSecs: info.Duration, Chapters: "[]",
			}
			if err := db.UpsertFile(fr); err != nil {
				return count, err
			}
			count++
		}
		if err := ensureCoverVideo(db, workID, top, coversDir); err != nil {
			return count, err
		}
	}
	return count, nil
}
