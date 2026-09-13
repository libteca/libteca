package scan

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"

	"github.com/libteca/libteca/internal/audio"
	"github.com/libteca/libteca/internal/store"
)

var audioExts = map[string]bool{".m4b": true, ".mp3": true, ".m4a": true}

type bookFile struct {
	path  string
	dir   string
	top   string
	name  string
	disc  int
	track int
	info  *audio.Info
	hash  string
	size  int64
	mtime int64
}

func cancelErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("scan cancelled: %w", err)
	}
	return nil
}

func All(ctx context.Context, db *store.DB, coversDir string, onProgress ProgressFn) (int, error) {
	libs, err := db.Libraries()
	if err != nil {
		return 0, err
	}
	tr := newTracker(onProgress)
	total := 0
	for _, lib := range libs {
		if cerr := cancelErr(ctx); cerr != nil {
			return total, cerr
		}
		n, err := Library(ctx, db, &lib, coversDir, tr.fn)
		if err != nil {
			return total, fmt.Errorf("library %q: %w", lib.Name, err)
		}
		total += n
	}
	return total, nil
}

func Library(ctx context.Context, db *store.DB, lib *store.Library, coversDir string, onProgress ProgressFn) (int, error) {
	tr := newTracker(onProgress)
	defer tr.flush()
	switch lib.Type {
	case "movies":
		return scanVideoLibrary(ctx, db, lib, coversDir, false, tr)
	case "tv":
		return scanVideoLibrary(ctx, db, lib, coversDir, true, tr)
	case "music":
		return scanMusicLibrary(ctx, db, lib, coversDir, tr)
	case "books", "comics":
		return scanBooksLibrary(ctx, db, lib, coversDir, tr)
	case "podcasts":
		return 0, nil
	case "games":
		return scanGamesLibrary(ctx, db, lib, coversDir, tr)
	}
	return scanAudioLibrary(ctx, db, lib, coversDir, tr)
}

func scanAudioLibrary(ctx context.Context, db *store.DB, lib *store.Library, coversDir string, tr *tracker) (int, error) {
	abs, err := filepath.Abs(lib.Path)
	if err != nil {
		return 0, err
	}
	var files []bookFile
	err = filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if cerr := cancelErr(ctx); cerr != nil {
			return cerr
		}
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
		if !audioExts[ext] {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return nil
		}
		parts := strings.Split(rel, string(filepath.Separator))
		top := abs
		if len(parts) > 1 {
			top = filepath.Join(abs, parts[0])
		}
		fi, ierr := d.Info()
		if ierr != nil {
			// Entry vanished or became unreadable after enumeration; a nil
			// deref here was a process-killing panic in a bare goroutine.
			return nil
		}
		files = append(files, bookFile{
			path:  path,
			dir:   filepath.Dir(path),
			top:   top,
			name:  d.Name(),
			size:  fi.Size(),
			mtime: fi.ModTime().Unix(),
		})
		tr.seen(path)
		return nil
	})
	if err != nil {
		return 0, err
	}

	groups := map[string][]bookFile{}
	for _, f := range files {
		groups[f.top] = append(groups[f.top], f)
	}
	topFolders := make([]string, 0, len(groups))
	for k := range groups {
		topFolders = append(topFolders, k)
	}
	sort.Strings(topFolders)

	count := 0
	for _, top := range topFolders {
		if cerr := cancelErr(ctx); cerr != nil {
			return count, cerr
		}
		group := groups[top]
		sort.Slice(group, func(i, j int) bool { return natLess(group[i].name, group[j].name) })
		if err := scanBook(db, lib, top, group, coversDir, tr); err != nil {
			fmt.Fprintf(os.Stderr, "libteca: skip %s: %v\n", top, err)
			continue
		}
		count++
	}
	return count, nil
}

func scanBook(db *store.DB, lib *store.Library, top string, group []bookFile, coversDir string, tr *tracker) error {
	unchanged := true
	for _, f := range group {
		if !fileUnchanged(db, f.path, f.size, f.mtime) {
			unchanged = false
			break
		}
	}
	if unchanged {
		title, author := titleAuthor(top, bookFile{})
		authorPtr := nullable(author)
		if id, ok := db.FindWorkID(lib.ID, title, &authorPtr); ok {
			if err := ensureCover(db, id, top, group, coversDir); err != nil {
				return err
			}
		}
		return nil
	}

	format := "mp3"
	for _, f := range group {
		if strings.EqualFold(filepath.Ext(f.path), ".m4b") {
			format = "m4b"
			break
		}
	}

	for i := range group {
		f := &group[i]
		probe, err := audio.Probe(f.path)
		if err != nil {
			return err
		}
		f.info = probe
		f.hash = hashFile(f.path, f.size)
		tr.probed()
	}

	first := group[0]
	title, author := titleAuthor(top, first)
	if t := metaGet(first.info, "title"); t != "" && len(group) > 1 {
		if a := metaGet(first.info, "album"); a != "" {
			title = a
		}
	}
	if author == "" {
		author = metaGet(first.info, "artist")
		if author == "" {
			author = metaGet(first.info, "album_artist")
		}
	}
	if title == "" {
		title = filepath.Base(top)
	}

	nfo := readWorkNFO(top, first.path)
	if nfo != nil {
		if nfo.Title != "" {
			title = nfo.Title
		}
		if author == "" {
			author = nfo.CreditsLine()
		}
	}
	authorPtr := nullable(author)
	w := &store.Work{LibraryID: lib.ID, Title: title, Author: &authorPtr}
	if nfo != nil {
		if d := nfo.Description(); d != "" {
			w.Description = &d
		}
	}
	var workID int64
	txErr := db.Update(func(tx *store.Tx) error {
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

		var total float64
		for _, f := range group {
			total += f.info.Duration
		}
		e := &store.Edition{WorkID: workID, Format: format, Title: title, DurationSecs: &total}
		editionID, err := tx.UpsertEdition(e)
		if err != nil {
			return err
		}

		for seq, f := range group {
			c := f.info.Codec
			ct := f.info.Container
			br := f.info.Bitrate
			ch := f.info.Channels
			sr := f.info.SampleRate
			chap, _ := marshalChapters(f.info.Chapters)
			if format == "mp3" && len(f.info.Chapters) == 0 {
				chTitle := metaGet(f.info, "title")
				if chTitle == "" {
					chTitle = strings.TrimSuffix(f.name, filepath.Ext(f.name))
				}
				chap, _ = marshalChapters([]audio.Chapter{{ID: int64(seq), Start: 0, End: f.info.Duration, Title: chTitle}})
			}
			fr := &store.FileRec{
				EditionID: editionID, Path: f.path, Seq: seq + 1,
				SizeBytes: f.size, MtimeSecs: f.mtime, Hash: &f.hash,
				Codec: &c, Container: &ct, Bitrate: &br, Channels: &ch, SampleRate: &sr,
				DurationSecs: f.info.Duration, Chapters: chap,
			}
			if err := tx.UpsertFile(fr); err != nil {
				return err
			}
			tr.file(fr.Inserted)
		}
		return nil
	})
	if txErr != nil {
		return txErr
	}

	return ensureCover(db, workID, top, group, coversDir)
}

func ensureCover(db *store.DB, workID int64, top string, group []bookFile, coversDir string) error {
	ok, err := importSidecarPoster(db, workID, top, coversDir, findWorkNFO(top) != "")
	if err != nil || ok {
		return err
	}
	dst := filepath.Join(coversDir, fmt.Sprintf("%d.jpg", workID))
	for _, f := range group {
		if f.info != nil && f.info.HasVideo {
			cmd := extractCmd(f.path, dst)
			if cmd != nil && cmd.Run() == nil {
				return db.SetWorkCover(workID, fmt.Sprintf("%d.jpg", workID))
			}
		}
	}
	return nil
}

func hashFile(path string, size int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := xxhash.New()
	const chunk = 64 * 1024
	buf := make([]byte, chunk)
	if size > 2*chunk {
		io.CopyBuffer(h, io.LimitReader(f, chunk), buf)
		f.Seek(size-chunk, 0)
		io.CopyBuffer(h, io.LimitReader(f, chunk), buf)
	} else {
		io.CopyBuffer(h, f, buf)
	}
	return fmt.Sprintf("%x-%d", h.Sum64(), size)
}

func metaGet(info *audio.Info, key string) string {
	if info == nil {
		return ""
	}
	return info.Meta[key]
}

func titleAuthor(top string, f bookFile) (string, string) {
	base := filepath.Base(top)
	if i := strings.Index(base, " - "); i > 0 {
		return strings.TrimSpace(base[i+3:]), strings.TrimSpace(base[:i])
	}
	if t := metaGet(f.info, "album"); t != "" {
		return t, ""
	}
	if strings.EqualFold(filepath.Ext(f.name), ".m4b") {
		return strings.TrimSuffix(f.name, filepath.Ext(f.name)), ""
	}
	return base, ""
}

func natLess(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		ca, cb := a[ai], b[bi]
		if isDigit(ca) && isDigit(cb) {
			na, nj := ai, bi
			for na < len(a) && isDigit(a[na]) {
				na++
			}
			for nj < len(b) && isDigit(b[nj]) {
				nj++
			}
			da, db := strings.TrimLeft(a[ai:na], "0"), strings.TrimLeft(b[bi:nj], "0")
			if len(da) != len(db) {
				return len(da) < len(db)
			}
			if da != db {
				return da < db
			}
			ai, bi = na, nj
			continue
		}
		if ca != cb {
			return ca < cb
		}
		ai++
		bi++
	}
	return len(a)-ai < len(b)-bi
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func nullable(s string) string {
	if s == "" {
		return ""
	}
	return s
}
