package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/libteca/libteca/internal/audio"
	"github.com/libteca/libteca/internal/mediafs"
	"github.com/libteca/libteca/internal/store"
)

var videoExts = map[string]bool{".mp4": true, ".mkv": true, ".avi": true, ".webm": true, ".mov": true, ".m4v": true}
var musicExts = map[string]bool{".mp3": true, ".flac": true, ".m4a": true, ".ogg": true, ".opus": true, ".wav": true}

var (
	reSxxExx  = regexp.MustCompile(`[Ss](\d{1,2})[Ee](\d{1,3})`)
	reExx     = regexp.MustCompile(`(\d{1,2})x(\d{1,3})`)
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
	mtimeNs   int64
	codec     string
	vcodec    string
	container string
	width     int
	height    int
	bitrate   int64
	duration  float64
	hash      string
}

func (v *vidFile) probe(ctx context.Context, root string) error {
	f, err := mediafs.Open(root, v.path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := audio.ProbeFile(ctx, f)
	if err != nil {
		return err
	}
	f.Seek(0, 0)
	v.duration = info.Duration
	v.bitrate = info.Bitrate
	v.container = info.Container
	v.codec = info.Codec
	v.vcodec, v.width, v.height = probeVideoFile(f)
	v.hash = hashFile(v.path, v.size)
	return nil
}

func scanVideoLibrary(ctx context.Context, db *store.DB, lib *store.Library, coversDir string, tv bool, tr *tracker) (int, error) {
	abs, err := filepath.Abs(lib.Path)
	if err != nil {
		return 0, err
	}
	walkRoot, toAlias, err := scanRoot(abs)
	if err != nil {
		return 0, err
	}
	var files []vidFile
	var series = map[string][]vidFile{}
	err = filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
		if cerr := cancelErr(ctx); cerr != nil {
			return cerr
		}
		if err != nil {
			return fmt.Errorf("scan %s: %w", path, err)
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != walkRoot {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if !videoExts[ext] {
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("scan %s: %w", path, ierr)
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		p := toAlias(path)
		rel, _ := filepath.Rel(abs, p)
		parts := strings.Split(rel, string(filepath.Separator))
		top := p
		if len(parts) > 1 {
			top = filepath.Join(abs, parts[0])
		}
		v := vidFile{path: p, name: d.Name(), size: fi.Size(), mtime: fi.ModTime().Unix(), mtimeNs: fi.ModTime().UnixNano()}
		if tv {
			parseEpisode(&v, p, rel)
		}
		files = append(files, v)
		series[top] = append(series[top], v)
		tr.seen(p)
		return nil
	})
	if err != nil {
		return 0, err
	}
	if _, err := db.MarkMissingLibraryFiles(lib.ID); err != nil {
		return 0, err
	}

	tops := make([]string, 0, len(series))
	for k := range series {
		tops = append(tops, k)
	}
	sort.Strings(tops)

	count := 0
	var itemErrors []error
	for _, top := range tops {
		if cerr := cancelErr(ctx); cerr != nil {
			return count, cerr
		}
		group := series[top]
		sort.Slice(group, func(i, j int) bool {
			if group[i].season != group[j].season {
				return group[i].season < group[j].season
			}
			return group[i].episode < group[j].episode
		})
		skip := make([]bool, len(group))
		anyChanged := false
		for i := range group {
			skip[i] = fileUnchanged(db, group[i].path, group[i].size, group[i].mtime, group[i].mtimeNs)
			if !skip[i] {
				anyChanged = true
			}
		}
		if !anyChanged {
			title, year := titleYear(filepath.Base(top))
			authorPtr := nullable(year)
			if id, ok := db.FindWorkID(lib.ID, title, &authorPtr); ok {
				if err := ensureCoverVideo(ctx, db, abs, id, top, group[0].path, coversDir); err != nil {
					return count, err
				}
			}
			continue
		}
		title, year := titleYear(filepath.Base(top))
		nfo := readWorkNFO(top, group[0].path)
		if nfo != nil {
			if nfo.Title != "" {
				title = nfo.Title
			}
			if y := nfo.YearValue(); y != "" {
				year = y
			}
		}
		authorPtr := nullable(year)
		w := &store.Work{LibraryID: lib.ID, Title: title, Author: &authorPtr}
		if nfo != nil {
			if d := nfo.Description(); d != "" {
				w.Description = &d
			}
		}
		type probedVid struct {
			f        *vidFile
			edTitle  string
			rawTitle string
		}
		var probed []probedVid
		for i := range group {
			f := &group[i]
			if skip[i] {
				continue
			}
			if err := f.probe(ctx, abs); err != nil {
				if cerr := ctx.Err(); cerr != nil {
					return count, cerr
				}
				itemErrors = append(itemErrors, fmt.Errorf("probe %s: %w", f.path, err))
				continue
			}
			tr.probed()
			edTitle := title
			if tv {
				if f.epTitle != "" {
					edTitle = f.epTitle
				} else {
					edTitle = fmt.Sprintf("S%02dE%02d", f.season, f.episode)
				}
			}
			rawTitle := edTitle
			edTitle = episodeTitleFromNFO(f.path, edTitle)
			probed = append(probed, probedVid{f: f, edTitle: edTitle, rawTitle: rawTitle})
		}
		if len(probed) == 0 {
			continue
		}
		var workID int64
		err := db.Update(func(tx *store.Tx) error {
			var err error
			workID, err = tx.UpsertWork(w)
			if err != nil {
				return err
			}
			if w.Created {
				tr.work()
			}
			if err := applyNFO(tx, workID, nfo); err != nil {
				fmt.Fprintf(os.Stderr, "libteca: skip nfo %s: %v\n", top, err)
			}
			for _, pv := range probed {
				f := pv.f
				dur := f.duration
				e := &store.Edition{WorkID: workID, Format: "video", Title: pv.edTitle, DurationSecs: &dur}
				if tv {
					s, ep := f.season, f.episode
					e.SeasonNum, e.EpisodeNum = &s, &ep
					e.Position = int64(ep)
				}
				editionID, err := tx.UpsertEdition(e)
				if err != nil {
					return err
				}
				if pv.edTitle != pv.rawTitle || (tv && f.epTitle != "") {
					_ = tx.SetEpisodeTitle(editionID, pv.edTitle)
				}
				c, ct, vc, w_, h, br := f.codec, f.container, f.vcodec, f.width, f.height, f.bitrate
				fr := &store.FileRec{
					EditionID: editionID, Path: f.path, Seq: 1,
					SizeBytes: f.size, MtimeSecs: f.mtime, MtimeNS: f.mtimeNs, Hash: &f.hash,
					Codec: &c, VideoCodec: &vc, Width: &w_, Height: &h, Container: &ct,
					Bitrate: &br, DurationSecs: f.duration, Chapters: "[]",
				}
				if err := tx.UpsertFile(fr); err != nil {
					return err
				}
				tr.file(fr.Inserted)
				count++
			}
			return nil
		})
		if err != nil {
			return count, err
		}
		if err := ensureCoverVideo(ctx, db, abs, workID, top, group[0].path, coversDir); err != nil {
			return count, err
		}
	}
	return count, errors.Join(itemErrors...)
}

func ensureCoverVideo(ctx context.Context, db *store.DB, root string, workID int64, top, mediaPath, coversDir string) error {
	ok, err := importSidecarPoster(db, workID, top, coversDir, findWorkNFO(top) != "")
	if err != nil || ok {
		if err == nil {
			importFanart(workID, top, mediaPath, coversDir)
		}
		return err
	}
	ok, err = importSuffixArt(db, workID, mediaPath, coversDir)
	if err != nil || ok {
		if err == nil {
			importFanart(workID, top, mediaPath, coversDir)
		}
		return err
	}
	dst := filepath.Join(coversDir, fmt.Sprintf("%d.jpg", workID))
	if src, oerr := mediafs.Open(root, mediaPath); oerr == nil {
		cmd := extractCmd(ctx, src, dst)
		ran := cmd != nil && cmd.Run() == nil
		src.Close()
		if ran {
			_ = db.SetWorkCover(workID, fmt.Sprintf("%d.jpg", workID))
		}
	}
	importFanart(workID, top, mediaPath, coversDir)
	return nil
}

func parseEpisode(v *vidFile, path, rel string) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	trimSeries := func(s string) string {
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) >= 3 {
			series := strings.TrimSpace(parts[len(parts)-3])
			s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), series))
		}
		return s
	}
	if m := reSxxExx.FindStringSubmatch(base); m != nil {
		v.season, _ = strconv.Atoi(m[1])
		v.episode, _ = strconv.Atoi(m[2])
		v.epTitle = cleanTitle(trimSeries(strings.Replace(base, m[0], " ", 1)))
		return
	}
	if m := reExx.FindStringSubmatch(base); m != nil {
		v.season, _ = strconv.Atoi(m[1])
		v.episode, _ = strconv.Atoi(m[2])
		v.epTitle = cleanTitle(trimSeries(strings.Replace(base, m[0], " ", 1)))
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

func scanMusicLibrary(ctx context.Context, db *store.DB, lib *store.Library, coversDir string, tr *tracker) (int, error) {
	abs, err := filepath.Abs(lib.Path)
	if err != nil {
		return 0, err
	}
	walkRoot, toAlias, err := scanRoot(abs)
	if err != nil {
		return 0, err
	}
	type track struct {
		path    string
		num     int
		name    string
		size    int64
		mtime   int64
		mtimeNs int64
	}
	albums := map[string][]track{}
	err = filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
		if cerr := cancelErr(ctx); cerr != nil {
			return cerr
		}
		if err != nil {
			return fmt.Errorf("scan %s: %w", path, err)
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != walkRoot {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if !musicExts[ext] {
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("scan %s: %w", path, ierr)
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		p := toAlias(path)
		rel, _ := filepath.Rel(abs, p)
		parts := strings.Split(rel, string(filepath.Separator))
		top := p
		if len(parts) > 1 {
			top = filepath.Join(abs, parts[0])
		}
		base := strings.TrimSuffix(d.Name(), ext)
		num := 0
		if n, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(base, " ", 2)[0])); err == nil {
			num = n
		}
		albums[top] = append(albums[top], track{path: p, num: num, name: base, size: fi.Size(), mtime: fi.ModTime().Unix(), mtimeNs: fi.ModTime().UnixNano()})
		tr.seen(p)
		return nil
	})
	if err != nil {
		return 0, err
	}
	if _, err := db.MarkMissingLibraryFiles(lib.ID); err != nil {
		return 0, err
	}

	tops := make([]string, 0, len(albums))
	for k := range albums {
		tops = append(tops, k)
	}
	sort.Strings(tops)

	count := 0
	var itemErrors []error
	for _, top := range tops {
		if cerr := cancelErr(ctx); cerr != nil {
			return count, cerr
		}
		group := albums[top]
		sort.Slice(group, func(i, j int) bool { return group[i].num < group[j].num })
		skip := make([]bool, len(group))
		anyChanged := false
		for i := range group {
			skip[i] = fileUnchanged(db, group[i].path, group[i].size, group[i].mtime, group[i].mtimeNs)
			if !skip[i] {
				anyChanged = true
			}
		}
		if !anyChanged {
			base := filepath.Base(top)
			title, artist := base, ""
			if i := strings.Index(base, " - "); i > 0 {
				artist, title = strings.TrimSpace(base[:i]), strings.TrimSpace(base[i+3:])
			}
			artistPtr := nullable(artist)
			if id, ok := db.FindWorkID(lib.ID, title, &artistPtr); ok {
				if err := ensureCoverVideo(ctx, db, abs, id, top, group[0].path, coversDir); err != nil {
					return count, err
				}
			}
			continue
		}
		base := filepath.Base(top)
		title, artist := base, ""
		if i := strings.Index(base, " - "); i > 0 {
			artist, title = strings.TrimSpace(base[:i]), strings.TrimSpace(base[i+3:])
		}
		artistPtr := nullable(artist)
		w := &store.Work{LibraryID: lib.ID, Title: title, Author: &artistPtr}
		type probedTrack struct {
			t       *track
			title   string
			info    *audio.Info
			hash    string
			ordinal int64
		}
		var probedTracks []probedTrack
		for i := range group {
			t := &group[i]
			if skip[i] {
				continue
			}
			tf, oerr := mediafs.Open(abs, t.path)
			if oerr != nil {
				if cerr := ctx.Err(); cerr != nil {
					return count, cerr
				}
				itemErrors = append(itemErrors, fmt.Errorf("probe %s: %w", t.path, oerr))
				continue
			}
			info, err := audio.ProbeFile(ctx, tf)
			tf.Close()
			if err != nil {
				if cerr := ctx.Err(); cerr != nil {
					return count, cerr
				}
				itemErrors = append(itemErrors, fmt.Errorf("probe %s: %w", t.path, err))
				continue
			}
			tr.probed()
			trackTitle := t.name
			if v := info.Meta["title"]; v != "" {
				trackTitle = v
			}
			probedTracks = append(probedTracks, probedTrack{t: t, title: trackTitle, info: info, hash: hashFile(t.path, t.size), ordinal: int64(i + 1)})
		}
		if len(probedTracks) == 0 {
			continue
		}
		var workID int64
		err := db.Update(func(tx *store.Tx) error {
			var err error
			workID, err = tx.UpsertWork(w)
			if err != nil {
				return err
			}
			if w.Created {
				tr.work()
			}
			for i := range group {
				if !skip[i] {
					continue
				}
				var edID int64
				if qerr := tx.QueryRow(`SELECT edition_id FROM files WHERE path = ?`, group[i].path).Scan(&edID); qerr == nil {
					if _, uerr := tx.Exec(`UPDATE editions SET position = ? WHERE id = ?`, int64(i+1), edID); uerr != nil {
						return uerr
					}
				} else if !errors.Is(qerr, sql.ErrNoRows) {
					return qerr
				}
			}
			for _, pt := range probedTracks {
				dur := pt.info.Duration
				e := &store.Edition{WorkID: workID, Format: "audio", Title: pt.title, DurationSecs: &dur, Position: pt.ordinal}
				editionID, err := tx.UpsertEdition(e)
				if err != nil {
					return err
				}
				c, ct, br := pt.info.Codec, pt.info.Container, pt.info.Bitrate
				hash := pt.hash
				fr := &store.FileRec{
					EditionID: editionID, Path: pt.t.path, Seq: 1, SizeBytes: pt.t.size, MtimeSecs: pt.t.mtime, MtimeNS: pt.t.mtimeNs,
					Hash: &hash, Codec: &c, Container: &ct, Bitrate: &br,
					DurationSecs: pt.info.Duration, Chapters: "[]",
				}
				if err := tx.UpsertFile(fr); err != nil {
					return err
				}
				tr.file(fr.Inserted)
				count++
			}
			return nil
		})
		if err != nil {
			return count, err
		}
		if err := ensureCoverVideo(ctx, db, abs, workID, top, group[0].path, coversDir); err != nil {
			return count, err
		}
	}
	return count, errors.Join(itemErrors...)
}
