package importer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/libteca/libteca/internal/store"
)

// ABS imports an Audiobookshelf instance. dataDir is the ABS config directory
// (the one containing abs_database.db); media files are resolved from the
// paths recorded in the foreign database. The source layout is detected
// read-only before anything is applied: released ABS databases key every row
// by UUID and carry per-file metadata in books.audioFiles, while the legacy
// synthetic layout uses integer ids and an audioTracks table. Anything else
// is refused instead of importing an empty catalog. Best-effort: optional
// stages (users, progress, playlists) degrade to warnings, never a failed
// import. Server settings, backups and feeds config are not migrated.
func ABS(dataDir string, db *store.DB, dryRun bool) (*Plan, error) {
	plan := &Plan{Source: "abs"}
	dbPath := filepath.Join(dataDir, "abs_database.db")
	if fi, err := os.Stat(dbPath); err != nil || fi.IsDir() {
		return nil, fmt.Errorf("abs_database.db not found in %s", dataDir)
	}
	fdb, err := openForeign(dbPath)
	if err != nil {
		return nil, err
	}
	defer fdb.Close()

	layout, err := detectABSLayout(fdb)
	if err != nil {
		return nil, err
	}
	snap, err := discoverABS(fdb, layout, plan)
	if err != nil {
		return nil, err
	}

	ourType := map[string]string{"book": "audiobooks", "podcast": "podcasts"}
	libType := map[foreignID]string{}
	libIndex := map[foreignID]int{}
	for _, l := range snap.libs {
		typ, ok := ourType[l.typ]
		if !ok {
			plan.warnf("library %q has unsupported mediaType %q; skipped", l.name, l.typ)
			continue
		}
		libType[l.id] = typ
		libIndex[l.id] = len(plan.Libraries)
		plan.Libraries = append(plan.Libraries, LibraryPlan{Name: l.name, Type: typ})
	}

	type resolvedBook struct {
		b       absSourceBook
		files   []fileSpec
		format  string
		libPath string
	}
	var books []resolvedBook
	bookMediaLib := map[foreignID]foreignID{}
	for _, b := range snap.books {
		if _, ok := libType[b.libID]; !ok {
			continue
		}
		if _, serr := os.Stat(b.dir); serr != nil && errors.Is(serr, os.ErrNotExist) {
			plan.Libraries[libIndex[b.libID]].Skipped++
			plan.warnf("book %q: directory %q is gone; skipped", b.title, b.dir)
			continue
		}
		files, format, ferr := resolveABSBookFiles(layout, b, plan)
		if ferr != nil {
			return nil, ferr
		}
		if len(files) == 0 {
			plan.Libraries[libIndex[b.libID]].Skipped++
			plan.warnf("book %q: no audio files found; skipped", b.title)
			continue
		}
		rb := resolvedBook{b: b, files: files, format: format, libPath: snap.libPaths[b.libID]}
		books = append(books, rb)
		bookMediaLib[b.mediaID] = b.libID
		plan.Libraries[libIndex[b.libID]].Works++
		plan.Libraries[libIndex[b.libID]].Editions++
		plan.Works++
		plan.Editions++
		plan.Files += len(rb.files)
	}

	type resolvedEpisode struct {
		e        absSourceEpisode
		file     fileSpec
		format   string
		fallback string
	}
	type resolvedPodcast struct {
		p        absSourcePodcast
		libPath  string
		episodes []resolvedEpisode
	}
	var podcasts []resolvedPodcast
	episodeKnown := map[foreignID]bool{}
	for _, p := range snap.podcasts {
		if _, ok := libType[p.libID]; !ok {
			continue
		}
		rp := resolvedPodcast{p: p, libPath: snap.libPaths[p.libID]}
		for _, e := range snap.episodes[p.mediaID] {
			path, dur := episodeFile(e.audioFile, e.duration)
			if path == "" {
				plan.Libraries[libIndex[p.libID]].Skipped++
				plan.warnf("podcast episode %q: no downloaded audio file; skipped", e.title)
				continue
			}
			if dur <= 0 {
				dur = e.duration
			}
			fallback := ""
			if e.episode.Valid {
				fallback = fmt.Sprintf("Episode %d", e.episode.Int64)
			}
			rp.episodes = append(rp.episodes, resolvedEpisode{
				e: e, file: fileSpec{Path: path, Duration: dur},
				format: audioExts[strings.ToLower(filepath.Ext(path))], fallback: fallback,
			})
			episodeKnown[e.id] = true
		}
		if len(rp.episodes) == 0 {
			plan.warnf("podcast %q has no playable episodes; skipped", p.title)
			continue
		}
		podcasts = append(podcasts, rp)
		plan.Libraries[libIndex[p.libID]].Works++
		plan.Libraries[libIndex[p.libID]].Editions += len(rp.episodes)
		plan.Works++
		plan.Editions += len(rp.episodes)
		plan.Files += len(rp.episodes)
	}

	userIDs := map[foreignID]bool{}
	for _, u := range snap.users {
		userIDs[u.ID] = true
	}
	type mappedProgress struct {
		p        absSourceProgress
		duration float64
	}
	var progress []mappedProgress
	skippedProg := 0
	editionDurations := map[absMediaKey]float64{}
	for _, b := range books {
		key := absMediaKey{kind: absBook, id: b.b.mediaID}
		for _, f := range b.files {
			editionDurations[key] += f.Duration
		}
	}
	for _, rp := range podcasts {
		for _, e := range rp.episodes {
			editionDurations[absMediaKey{kind: absEpisode, id: e.e.id}] = e.file.Duration
		}
	}
	for _, p := range snap.progress {
		if !userIDs[p.userID] {
			skippedProg++
			continue
		}
		key := absMediaKey{kind: p.kind, id: p.mediaID}
		switch p.kind {
		case absBook:
			if _, ok := bookMediaLib[p.mediaID]; !ok {
				skippedProg++
				continue
			}
		case absEpisode:
			if !episodeKnown[p.mediaID] {
				skippedProg++
				continue
			}
		default:
			skippedProg++
			continue
		}
		progress = append(progress, mappedProgress{p: p, duration: editionDurations[key]})
	}
	if skippedProg > 0 {
		plan.warnf("%d mediaProgress rows skipped (unknown user or unimported item)", skippedProg)
	}
	plan.ProgressRows = len(progress)

	var mappedPlaylists []absSourcePlaylist
	for _, p := range snap.playlists {
		if !userIDs[p.userID] || len(p.items) == 0 {
			plan.warnf("playlist %q skipped (unknown user or no imported items)", p.name)
			continue
		}
		kept := make([]foreignID, 0, len(p.items))
		for _, it := range p.items {
			if _, ok := bookMediaLib[it]; ok {
				kept = append(kept, it)
			}
		}
		if len(kept) == 0 {
			plan.warnf("playlist %q skipped (no imported items)", p.name)
			continue
		}
		p.items = kept
		mappedPlaylists = append(mappedPlaylists, p)
	}
	plan.Playlists = len(mappedPlaylists)

	if len(snap.users) > 0 {
		plan.warnf("password hashes cannot be migrated; imported users get a temp password the admin must reset")
	}

	if dryRun {
		if _, err := applyUsers(db, snap.users, plan, false); err != nil {
			return nil, err
		}
		return plan, nil
	}

	err = db.Update(func(tx *store.Tx) error {
		userMap, err := applyUsers(tx, snap.users, plan, true)
		if err != nil {
			return err
		}

		type edRef struct {
			id    int64
			files []fileSpec
			ids   []int64
		}
		bookEds := map[absMediaKey]*edRef{}
		epEds := map[absMediaKey]*edRef{}

		libIDs := map[foreignID]int64{}
		for _, l := range snap.libs {
			if _, ok := libIndex[l.id]; !ok {
				continue
			}
			fallback := filepath.Join(dataDir, "imported", plan.Libraries[libIndex[l.id]].Name)
			if p := snap.libPaths[l.id]; p != "" {
				fallback = p
			}
			id, err := ensureLibrary(tx, l.name, libType[l.id], fallback, plan, libIndex[l.id], true)
			if err != nil {
				return err
			}
			libIDs[l.id] = id
		}

		for _, b := range books {
			w := &store.Work{LibraryID: libIDs[b.b.libID], Title: b.b.title, Author: strPtr(b.b.author), Description: strPtr(b.b.desc)}
			wid, err := tx.UpsertWork(w)
			if err != nil {
				return err
			}
			total := 0.0
			for _, f := range b.files {
				total += f.Duration
			}
			e := &store.Edition{WorkID: wid, Format: b.format, Title: b.b.title, DurationSecs: &total}
			eid, err := tx.UpsertEdition(e)
			if err != nil {
				return err
			}
			ids, err := applyFilesTx(tx, eid, b.files)
			if err != nil {
				return err
			}
			bookEds[absMediaKey{kind: absBook, id: b.b.mediaID}] = &edRef{id: eid, files: b.files, ids: ids}
		}

		for _, rp := range podcasts {
			w := &store.Work{LibraryID: libIDs[rp.p.libID], Title: rp.p.title, Author: strPtr(rp.p.author)}
			wid, err := tx.UpsertWork(w)
			if err != nil {
				return err
			}
			for _, e := range rp.episodes {
				var season, episode *int
				if e.e.episode.Valid && e.e.episode.Int64 > 0 {
					s, n := int(e.e.season.Int64), int(e.e.episode.Int64)
					season, episode = &s, &n
				}
				title := e.e.title
				if title == "" {
					title = e.fallback
				}
				dur := e.file.Duration
				ed := &store.Edition{
					WorkID: wid, Format: e.format, Title: title, DurationSecs: &dur,
					SeasonNum: season, EpisodeNum: episode,
				}
				eid, err := tx.UpsertEdition(ed)
				if err != nil {
					return err
				}
				ids, err := applyFilesTx(tx, eid, []fileSpec{e.file})
				if err != nil {
					return err
				}
				epEds[absMediaKey{kind: absEpisode, id: e.e.id}] = &edRef{id: eid, files: []fileSpec{e.file}, ids: ids}
			}
		}

		for _, p := range progress {
			uid, ok := userMap[p.p.userID]
			if !ok {
				continue
			}
			var ref *edRef
			switch p.p.kind {
			case absBook:
				ref = bookEds[absMediaKey{kind: absBook, id: p.p.mediaID}]
			case absEpisode:
				ref = epEds[absMediaKey{kind: absEpisode, id: p.p.mediaID}]
			}
			if ref == nil {
				continue
			}
			fid, off := locate(ref.files, ref.ids, p.p.current)
			if p.p.ebook > 0 && p.p.current == 0 {
				rp := &store.ReadingProgress{
					Progress: store.Progress{UserID: uid, EditionID: ref.id, FileID: fid, EditionPositionSecs: 0, IsFinished: p.p.finished},
					Percent:  &p.p.ebook,
				}
				if err := tx.SetReadingProgress(rp); err != nil {
					return err
				}
				continue
			}
			var dur *float64
			if p.duration > 0 {
				d := p.duration
				dur = &d
			}
			if err := tx.SetProgress(&store.Progress{
				UserID: uid, EditionID: ref.id, FileID: fid, FileOffsetSecs: off,
				EditionPositionSecs: p.p.current, DurationSecs: dur,
				IsFinished: p.p.finished, Device: strPtr("abs-import"),
			}); err != nil {
				return err
			}
		}

		for _, p := range mappedPlaylists {
			uid, ok := userMap[p.userID]
			if !ok {
				continue
			}
			plID := int64(0)
			existing, err := tx.ListPlaylists(uid)
			if err != nil {
				return err
			}
			for _, ex := range existing {
				if ex.Name == p.name {
					plID = ex.ID
					break
				}
			}
			if plID == 0 {
				plID, err = tx.CreatePlaylist(uid, p.name)
				if err != nil {
					return err
				}
			}
			for _, it := range p.items {
				if ref := bookEds[absMediaKey{kind: absBook, id: it}]; ref != nil {
					if _, err := tx.AddPlaylistItem(plID, ref.id); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return plan, nil
}

type foreignID string

type absMediaKind string

const (
	absBook    absMediaKind = "book"
	absEpisode absMediaKind = "podcastEpisode"
)

type absMediaKey struct {
	kind absMediaKind
	id   foreignID
}

type absLayout int

const (
	absLayoutLegacy absLayout = iota
	absLayoutReleased
)

func idOf(v any) foreignID {
	switch t := v.(type) {
	case string:
		return foreignID(t)
	case []byte:
		return foreignID(t)
	case int64:
		return foreignID(strconv.FormatInt(t, 10))
	case int:
		return foreignID(strconv.Itoa(t))
	case float64:
		return foreignID(strconv.FormatFloat(t, 'f', -1, 64))
	case nil:
		return ""
	}
	return foreignID(fmt.Sprintf("%v", v))
}

type absLib struct {
	id   foreignID
	name string
	typ  string
}

type absSourceBook struct {
	itemID     foreignID
	mediaID    foreignID
	libID      foreignID
	title      string
	author     string
	desc       string
	dir        string
	duration   float64
	trackDurs  []float64
	audioFiles []absAudioFile
}

type absSourcePodcast struct {
	itemID  foreignID
	mediaID foreignID
	libID   foreignID
	title   string
	author  string
}

type absSourceEpisode struct {
	id        foreignID
	title     string
	season    sql.NullInt64
	episode   sql.NullInt64
	duration  float64
	audioFile string
}

type absSourceProgress struct {
	userID   foreignID
	mediaID  foreignID
	kind     absMediaKind
	current  float64
	ebook    float64
	finished bool
}

type absSourcePlaylist struct {
	id     foreignID
	userID foreignID
	name   string
	items  []foreignID
}

type absSnapshot struct {
	libs      []absLib
	libPaths  map[foreignID]string
	users     []foreignUser
	books     []absSourceBook
	podcasts  []absSourcePodcast
	episodes  map[foreignID][]absSourceEpisode
	progress  []absSourceProgress
	playlists []absSourcePlaylist
}

func tableColumns(fdb *sql.DB, table string) (map[string]bool, error) {
	rows, err := fdb.Query(`SELECT name FROM pragma_table_info('` + strings.ReplaceAll(table, "'", "''") + `')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols[strings.ToLower(name)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}

func detectABSLayout(fdb *sql.DB) (absLayout, error) {
	cols, err := tableColumns(fdb, "books")
	if err != nil {
		if schemaErr(err) {
			return absLayoutLegacy, fmt.Errorf("not an Audiobookshelf database: %w", err)
		}
		return absLayoutLegacy, err
	}
	if len(cols) == 0 {
		return absLayoutLegacy, fmt.Errorf("not an Audiobookshelf database: books table is absent")
	}
	if cols["audiofiles"] {
		return absLayoutReleased, nil
	}
	if cols["durationsec"] && cols["author"] {
		return absLayoutLegacy, nil
	}
	return absLayoutLegacy, fmt.Errorf("unsupported Audiobookshelf books schema (columns neither released audioFiles/duration nor legacy durationSec/author)")
}

func discoverABS(fdb *sql.DB, layout absLayout, plan *Plan) (*absSnapshot, error) {
	snap := &absSnapshot{libPaths: map[foreignID]string{}, episodes: map[foreignID][]absSourceEpisode{}}

	if rows, err := fdb.Query(`SELECT libraryId, path FROM libraryFolders`); err == nil {
		for rows.Next() {
			var libID any
			var p sql.NullString
			if rows.Scan(&libID, &p) == nil && p.String != "" {
				if _, ok := snap.libPaths[idOf(libID)]; !ok {
					snap.libPaths[idOf(libID)] = p.String
				}
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	lrows, err := fdb.Query(`SELECT id, name, mediaType FROM libraries`)
	if err != nil {
		if schemaErr(err) {
			return nil, fmt.Errorf("not an Audiobookshelf database: %w", err)
		}
		return nil, err
	}
	for lrows.Next() {
		var l absLib
		var id any
		if err := lrows.Scan(&id, &l.name, &l.typ); err != nil {
			lrows.Close()
			return nil, err
		}
		l.id = idOf(id)
		snap.libs = append(snap.libs, l)
	}
	lrows.Close()
	if err := lrows.Err(); err != nil {
		return nil, err
	}

	if urows, err := fdb.Query(`SELECT id, username, type FROM users`); err == nil {
		for urows.Next() {
			var u foreignUser
			var id any
			var typ sql.NullString
			if urows.Scan(&id, &u.Name, &typ) == nil {
				u.ID = idOf(id)
				u.IsAdmin = typ.String == "admin"
				snap.users = append(snap.users, u)
			}
		}
		urows.Close()
		if err := urows.Err(); err != nil {
			return nil, err
		}
	} else if !schemaErr(err) {
		return nil, err
	} else {
		plan.warnf("users table unreadable; progress cannot be imported")
	}

	trackDurs := map[foreignID][]float64{}
	if layout == absLayoutLegacy {
		if trows, err := fdb.Query(`SELECT bookId, duration FROM audioTracks ORDER BY bookId, "index"`); err == nil {
			for trows.Next() {
				var bookID any
				var dur float64
				if trows.Scan(&bookID, &dur) == nil {
					trackDurs[idOf(bookID)] = append(trackDurs[idOf(bookID)], dur)
				}
			}
			trows.Close()
			if err := trows.Err(); err != nil {
				return nil, err
			}
		}
	}

	audioFiles := map[foreignID][]absAudioFile{}
	bookAuthors := map[foreignID]string{}
	if layout == absLayoutReleased {
		if arows, err := fdb.Query(`SELECT id, audioFiles FROM books`); err == nil {
			for arows.Next() {
				var id any
				var raw sql.NullString
				if arows.Scan(&id, &raw) == nil && raw.String != "" {
					audioFiles[idOf(id)] = parseABSAudioFiles(raw.String)
				}
			}
			arows.Close()
			if err := arows.Err(); err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("released books.audioFiles unreadable: %w", err)
		}
		if brows, err := fdb.Query(`SELECT ba.bookId, a.name FROM bookAuthors ba JOIN authors a ON a.id = ba.authorId`); err == nil {
			for brows.Next() {
				var bookID any
				var name sql.NullString
				if brows.Scan(&bookID, &name) == nil && name.String != "" {
					if _, ok := bookAuthors[idOf(bookID)]; !ok {
						bookAuthors[idOf(bookID)] = name.String
					}
				}
			}
			brows.Close()
			if err := brows.Err(); err != nil {
				return nil, err
			}
		} else if !schemaErr(err) {
			return nil, err
		} else {
			plan.warnf("bookAuthors/authors unreadable; book authors import empty")
		}
	}

	var bookRows []absSourceBook
	bookQuery := `SELECT li.id, li.libraryId, li.title, li.path, li.mediaId, b.author, b.description, b.durationSec
		FROM libraryItems li LEFT JOIN books b ON b.id = li.mediaId WHERE li.mediaType = 'book'`
	if layout == absLayoutReleased {
		bookQuery = `SELECT li.id, li.libraryId, li.title, li.path, li.mediaId, b.duration, b.description
			FROM libraryItems li LEFT JOIN books b ON b.id = li.mediaId WHERE li.mediaType = 'book'`
	}
	brows, err := fdb.Query(bookQuery)
	if err != nil {
		return nil, fmt.Errorf("libraryItems/books unreadable: %w", err)
	}
	for brows.Next() {
		var b absSourceBook
		var itemID, libID, mediaID any
		var title sql.NullString
		var dir sql.NullString
		if layout == absLayoutReleased {
			var dur sql.NullFloat64
			var desc sql.NullString
			if err := brows.Scan(&itemID, &libID, &title, &dir, &mediaID, &dur, &desc); err != nil {
				brows.Close()
				return nil, err
			}
			b.author = plainAuthor(bookAuthors[idOf(mediaID)])
			b.desc = desc.String
			b.duration = dur.Float64
		} else {
			var author, desc sql.NullString
			var dur sql.NullFloat64
			if err := brows.Scan(&itemID, &libID, &title, &dir, &mediaID, &author, &desc, &dur); err != nil {
				brows.Close()
				return nil, err
			}
			b.author = plainAuthor(author.String)
			b.desc = desc.String
			b.duration = dur.Float64
		}
		b.itemID = idOf(itemID)
		b.libID = idOf(libID)
		b.mediaID = idOf(mediaID)
		b.title = title.String
		b.dir = dir.String
		b.trackDurs = trackDurs[b.mediaID]
		b.audioFiles = audioFiles[b.mediaID]
		bookRows = append(bookRows, b)
	}
	brows.Close()
	if err := brows.Err(); err != nil {
		return nil, err
	}
	snap.books = bookRows

	var podRows []absSourcePodcast
	prows, err := fdb.Query(`SELECT li.id, li.libraryId, li.mediaId, li.title, p.itunesAuthor
		FROM libraryItems li LEFT JOIN podcasts p ON p.id = li.mediaId WHERE li.mediaType = 'podcast'`)
	if err != nil && schemaErr(err) {
		prows, err = fdb.Query(`SELECT li.id, li.libraryId, li.mediaId, li.title, NULL FROM libraryItems li WHERE li.mediaType = 'podcast'`)
	}
	if err != nil {
		return nil, fmt.Errorf("podcast items unreadable: %w", err)
	}
	for prows.Next() {
		var p absSourcePodcast
		var itemID, libID, mediaID any
		var title sql.NullString
		var author sql.NullString
		if err := prows.Scan(&itemID, &libID, &mediaID, &title, &author); err != nil {
			prows.Close()
			return nil, err
		}
		p.itemID = idOf(itemID)
		p.libID = idOf(libID)
		p.mediaID = idOf(mediaID)
		p.title = title.String
		p.author = plainAuthor(author.String)
		podRows = append(podRows, p)
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return nil, err
	}
	snap.podcasts = podRows

	erows, err := fdb.Query(`SELECT id, podcastId, title, season, episode, duration, audioFile FROM podcastEpisodes`)
	if err != nil {
		return nil, fmt.Errorf("podcastEpisodes unreadable: %w", err)
	}
	for erows.Next() {
		var id, podID any
		var e absSourceEpisode
		var title sql.NullString
		var dur sql.NullFloat64
		var audioFile sql.NullString
		if err := erows.Scan(&id, &podID, &title, &e.season, &e.episode, &dur, &audioFile); err != nil {
			erows.Close()
			return nil, err
		}
		e.id = idOf(id)
		e.title = title.String
		e.duration = dur.Float64
		e.audioFile = audioFile.String
		snap.episodes[idOf(podID)] = append(snap.episodes[idOf(podID)], e)
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return nil, err
	}

	progressQuery := `SELECT userId, mediaItemId, mediaItemType, currentTime, ebookProgress, isFinished FROM mediaProgresses`
	mrows, err := fdb.Query(progressQuery)
	if err != nil && schemaErr(err) {
		mrows, err = fdb.Query(`SELECT userId, mediaItemId, mediaItemType, currentTime, ebookProgress, isFinished FROM mediaProgress`)
	}
	if err != nil {
		if !schemaErr(err) {
			return nil, err
		}
		plan.warnf("mediaProgress unreadable; no progress imported")
	} else {
		for mrows.Next() {
			var p absSourceProgress
			var userID, mediaID any
			var kind sql.NullString
			var cur, eb sql.NullFloat64
			var fin sql.NullInt64
			if mrows.Scan(&userID, &mediaID, &kind, &cur, &eb, &fin) == nil {
				p.userID = idOf(userID)
				p.mediaID = idOf(mediaID)
				p.kind = absMediaKind(kind.String)
				p.current = cur.Float64
				p.ebook = eb.Float64
				p.finished = fin.Int64 != 0
				snap.progress = append(snap.progress, p)
			}
		}
		mrows.Close()
		if err := mrows.Err(); err != nil {
			return nil, err
		}
	}

	var playlists []absSourcePlaylist
	if prow2, err := fdb.Query(`SELECT id, userId, name FROM playlists`); err == nil {
		for prow2.Next() {
			var p absSourcePlaylist
			var id, userID any
			var name sql.NullString
			if prow2.Scan(&id, &userID, &name) == nil {
				p.id = idOf(id)
				p.userID = idOf(userID)
				p.name = name.String
				playlists = append(playlists, p)
			}
		}
		prow2.Close()
		if err := prow2.Err(); err != nil {
			return nil, err
		}
		items := map[foreignID][]foreignID{}
		if irows, err := fdb.Query(`SELECT playlistId, mediaItemId, mediaItemType FROM playlistMediaItems ORDER BY id`); err == nil {
			for irows.Next() {
				var plID, itemID any
				var typ sql.NullString
				if irows.Scan(&plID, &itemID, &typ) == nil && typ.String == "book" {
					items[idOf(plID)] = append(items[idOf(plID)], idOf(itemID))
				}
			}
			irows.Close()
			if err := irows.Err(); err != nil {
				return nil, err
			}
		} else if !schemaErr(err) {
			return nil, err
		}
		for i := range playlists {
			playlists[i].items = items[playlists[i].id]
		}
	} else if !schemaErr(err) {
		return nil, err
	}
	snap.playlists = playlists

	return snap, nil
}

type absAudioFileMetadata struct {
	Path    string `json:"path"`
	RelPath string `json:"relPath"`
}

type absAudioFile struct {
	Path     string               `json:"path"`
	RelPath  string               `json:"relPath"`
	Index    int                  `json:"index"`
	Duration float64              `json:"duration"`
	Metadata absAudioFileMetadata `json:"metadata"`
}

func (af absAudioFile) filePath() string {
	if af.Metadata.Path != "" {
		return af.Metadata.Path
	}
	return af.Path
}

func parseABSAudioFiles(raw string) []absAudioFile {
	var files []absAudioFile
	if json.Unmarshal([]byte(raw), &files) != nil {
		return nil
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].Index < files[j].Index })
	return files
}

func resolveABSBookFiles(layout absLayout, b absSourceBook, plan *Plan) ([]fileSpec, string, error) {
	if layout == absLayoutReleased {
		entries := b.audioFiles
		files := make([]fileSpec, 0, len(entries))
		for _, af := range entries {
			path := af.filePath()
			if path == "" {
				continue
			}
			if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
				plan.warnf("book %q: audio file %q from books.audioFiles is gone; skipped", b.title, path)
				continue
			}
			files = append(files, fileSpec{Path: path, Duration: af.Duration})
		}
		if len(files) == 0 {
			return nil, "", nil
		}
		if b.duration > 0 {
			var known float64
			for _, f := range files {
				if f.Duration > 0 {
					known += f.Duration
				}
			}
			if known <= 0 {
				per := b.duration / float64(len(files))
				for i := range files {
					files[i].Duration = per
				}
			}
		}
		return files, absAudioFormat(files), nil
	}
	paths, aerr := audioPathsIn(b.dir)
	if aerr != nil {
		return nil, "", fmt.Errorf("book %q: discovery at %q failed: %w", b.title, b.dir, aerr)
	}
	if len(paths) == 0 {
		return nil, "", nil
	}
	durs := b.trackDurs
	per := b.duration / float64(len(paths))
	files := make([]fileSpec, 0, len(paths))
	for i, p := range paths {
		d := per
		if i < len(durs) && durs[i] > 0 {
			d = durs[i]
		}
		files = append(files, fileSpec{Path: p, Duration: d})
	}
	return files, absAudioFormat(files), nil
}

func absAudioFormat(files []fileSpec) string {
	format := audioExts[strings.ToLower(filepath.Ext(files[0].Path))]
	for _, f := range files {
		if strings.EqualFold(filepath.Ext(f.Path), ".m4b") {
			return "m4b"
		}
	}
	return format
}

// episodeFile digs the on-disk path out of an ABS podcast episode's audioFile
// JSON blob (no stable schema: try metadata.path then path).
func episodeFile(raw string, fallbackDur float64) (string, float64) {
	if raw == "" {
		return "", fallbackDur
	}
	var af struct {
		Duration float64 `json:"duration"`
		Path     string  `json:"path"`
		Metadata struct {
			Path string `json:"path"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(raw), &af) != nil {
		return "", fallbackDur
	}
	path := af.Metadata.Path
	if path == "" {
		path = af.Path
	}
	if path == "" {
		return "", fallbackDur
	}
	if _, err := os.Stat(path); err != nil {
		return "", fallbackDur
	}
	return path, af.Duration
}
