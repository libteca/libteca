package importer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/libteca/libteca/internal/store"
)

// ABS imports an Audiobookshelf instance. dataDir is the ABS config directory
// (the one containing abs_database.db); media files are resolved from the
// paths recorded in the foreign database. Best-effort: anything the schema
// does not carry (or that is missing on disk) becomes a warning, never a
// failed import. Server settings, backups and feeds config are not migrated.
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

	libPaths := map[int64]string{}
	if rows, err := fdb.Query(`SELECT libraryId, path FROM libraryFolders`); err == nil {
		for rows.Next() {
			var libID int64
			var p string
			if rows.Scan(&libID, &p) == nil && p != "" {
				if _, ok := libPaths[libID]; !ok {
					libPaths[libID] = p
				}
			}
		}
		rows.Close()
	}

	type libRow struct {
		id   int64
		name string
		typ  string
	}
	var libs []libRow
	lrows, err := fdb.Query(`SELECT id, name, mediaType FROM libraries`)
	if err != nil {
		if schemaErr(err) {
			return nil, fmt.Errorf("not an Audiobookshelf database: %w", err)
		}
		return nil, err
	}
	for lrows.Next() {
		var l libRow
		if err := lrows.Scan(&l.id, &l.name, &l.typ); err != nil {
			lrows.Close()
			return nil, err
		}
		libs = append(libs, l)
	}
	lrows.Close()
	if err := lrows.Err(); err != nil {
		return nil, err
	}

	var users []foreignUser
	if urows, err := fdb.Query(`SELECT id, username, type FROM users`); err == nil {
		for urows.Next() {
			var u foreignUser
			var typ string
			if urows.Scan(&u.ID, &u.Name, &typ) == nil {
				u.IsAdmin = typ == "admin"
				users = append(users, u)
			}
		}
		urows.Close()
	} else if !schemaErr(err) {
		return nil, err
	} else {
		plan.warnf("users table unreadable; progress cannot be imported")
	}

	trackDurs := map[int64][]float64{}
	if trows, err := fdb.Query(`SELECT bookId, duration FROM audioTracks ORDER BY bookId, "index"`); err == nil {
		for trows.Next() {
			var bookID int64
			var dur float64
			if trows.Scan(&bookID, &dur) == nil {
				trackDurs[bookID] = append(trackDurs[bookID], dur)
			}
		}
		trows.Close()
	}

	type bookRow struct {
		itemID   int64
		libID    int64
		title    string
		author   string
		desc     string
		duration float64
		dir      string
		mediaID  int64
	}
	var bookRows []bookRow
	brows, err := fdb.Query(`SELECT li.id, li.libraryId, li.title, li.path, li.mediaId, b.author, b.description, b.durationSec
		FROM libraryItems li LEFT JOIN books b ON b.id = li.mediaId WHERE li.mediaType = 'book'`)
	if err != nil {
		if !schemaErr(err) {
			return nil, err
		}
		plan.warnf("libraryItems/books unreadable; no books imported")
	} else {
		for brows.Next() {
			var b bookRow
			var author, desc sql.NullString
			var dir sql.NullString
			var dur sql.NullFloat64
			if err := brows.Scan(&b.itemID, &b.libID, &b.title, &dir, &b.mediaID, &author, &desc, &dur); err != nil {
				brows.Close()
				return nil, err
			}
			b.author = plainAuthor(author.String)
			b.desc = desc.String
			b.duration = dur.Float64
			b.dir = dir.String
			bookRows = append(bookRows, b)
		}
		brows.Close()
		if err := brows.Err(); err != nil {
			return nil, err
		}
	}

	type podRow struct {
		itemID int64
		libID  int64
		title  string
		author string
	}
	var podRows []podRow
	prows, err := fdb.Query(`SELECT li.id, li.libraryId, li.title, p.itunesAuthor
		FROM libraryItems li LEFT JOIN podcasts p ON p.id = li.mediaId WHERE li.mediaType = 'podcast'`)
	if err != nil && schemaErr(err) {
		prows, err = fdb.Query(`SELECT li.id, li.libraryId, li.title FROM libraryItems li WHERE li.mediaType = 'podcast'`)
	}
	if err != nil {
		if !schemaErr(err) {
			return nil, err
		}
		plan.warnf("podcast items unreadable; no podcasts imported")
	} else {
		for prows.Next() {
			var p podRow
			var author sql.NullString
			if err := prows.Scan(&p.itemID, &p.libID, &p.title, &author); err != nil {
				prows.Close()
				return nil, err
			}
			p.author = plainAuthor(author.String)
			podRows = append(podRows, p)
		}
		prows.Close()
		if err := prows.Err(); err != nil {
			return nil, err
		}
	}

	type epRow struct {
		id        int64
		podID     int64
		title     string
		season    sql.NullInt64
		episode   sql.NullInt64
		duration  float64
		audioFile string
	}
	epsByPod := map[int64][]epRow{}
	if erows, err := fdb.Query(`SELECT id, podcastId, title, season, episode, duration, audioFile FROM podcastEpisodes`); err == nil {
		for erows.Next() {
			var e epRow
			if err := erows.Scan(&e.id, &e.podID, &e.title, &e.season, &e.episode, &e.duration, &e.audioFile); err != nil {
				erows.Close()
				return nil, err
			}
			epsByPod[e.podID] = append(epsByPod[e.podID], e)
		}
		erows.Close()
	}

	type progRow struct {
		userID   int64
		mediaID  int64
		itemType string
		current  float64
		ebook    float64
		finished bool
	}
	var progRows []progRow
	if mrows, err := fdb.Query(`SELECT userId, mediaItemId, mediaItemType, currentTime, ebookProgress, isFinished FROM mediaProgress`); err == nil {
		for mrows.Next() {
			var p progRow
			var cur, eb sql.NullFloat64
			var fin sql.NullInt64
			if mrows.Scan(&p.userID, &p.mediaID, &p.itemType, &cur, &eb, &fin) == nil {
				p.current = cur.Float64
				p.ebook = eb.Float64
				p.finished = fin.Int64 != 0
				progRows = append(progRows, p)
			}
		}
		mrows.Close()
	} else if !schemaErr(err) {
		return nil, err
	} else {
		plan.warnf("mediaProgress unreadable; no progress imported")
	}

	type plRow struct {
		id     int64
		userID int64
		name   string
		items  []int64
	}
	var playlists []plRow
	if prow2, err := fdb.Query(`SELECT id, userId, name FROM playlists`); err == nil {
		for prow2.Next() {
			var p plRow
			if prow2.Scan(&p.id, &p.userID, &p.name) == nil {
				playlists = append(playlists, p)
			}
		}
		prow2.Close()
		items := map[int64][]int64{}
		if irows, err := fdb.Query(`SELECT playlistId, mediaItemId, mediaItemType FROM playlistMediaItems ORDER BY id`); err == nil {
			for irows.Next() {
				var plID, itemID int64
				var typ string
				if irows.Scan(&plID, &itemID, &typ) == nil && typ == "book" {
					items[plID] = append(items[plID], itemID)
				}
			}
			irows.Close()
		}
		for i := range playlists {
			playlists[i].items = items[playlists[i].id]
		}
	}

	ourType := map[string]string{"book": "audiobooks", "podcast": "podcasts"}
	libType := map[int64]string{}
	libIndex := map[int64]int{}
	for _, l := range libs {
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
		b       bookRow
		files   []fileSpec
		format  string
		libPath string
	}
	var books []resolvedBook
	itemLib := map[int64]int64{}
	for _, b := range bookRows {
		if _, ok := libType[b.libID]; !ok {
			continue
		}
		paths := audioPathsIn(b.dir)
		if len(paths) == 0 {
			plan.Libraries[libIndex[b.libID]].Skipped++
			plan.warnf("book %q: no audio files found at %q; skipped", b.title, b.dir)
			continue
		}
		rb := resolvedBook{b: b, libPath: libPaths[b.libID]}
		durs := trackDurs[b.mediaID]
		per := b.duration / float64(len(paths))
		for i, p := range paths {
			d := per
			if i < len(durs) && durs[i] > 0 {
				d = durs[i]
			}
			rb.files = append(rb.files, fileSpec{Path: p, Duration: d})
		}
		rb.format = audioExts[strings.ToLower(filepath.Ext(paths[0]))]
		for _, p := range paths {
			if strings.EqualFold(filepath.Ext(p), ".m4b") {
				rb.format = "m4b"
				break
			}
		}
		books = append(books, rb)
		itemLib[b.itemID] = b.libID
		plan.Libraries[libIndex[b.libID]].Works++
		plan.Libraries[libIndex[b.libID]].Editions++
		plan.Works++
		plan.Editions++
		plan.Files += len(rb.files)
	}

	type resolvedEpisode struct {
		e        epRow
		file     fileSpec
		format   string
		fallback string
	}
	type resolvedPodcast struct {
		p        podRow
		libPath  string
		episodes []resolvedEpisode
	}
	var podcasts []resolvedPodcast
	episodePod := map[int64]int64{}
	for _, p := range podRows {
		if _, ok := libType[p.libID]; !ok {
			continue
		}
		rp := resolvedPodcast{p: p, libPath: libPaths[p.libID]}
		for _, e := range epsByPod[p.itemID] {
			path, dur := episodeFile(e.audioFile, e.duration)
			if path == "" {
				plan.Libraries[libIndex[p.libID]].Skipped++
				plan.warnf("podcast episode %q: no downloaded audio file; skipped", e.title)
				continue
			}
			if dur <= 0 {
				dur = e.duration
			}
			rp.episodes = append(rp.episodes, resolvedEpisode{
				e: e, file: fileSpec{Path: path, Duration: dur},
				format: audioExts[strings.ToLower(filepath.Ext(path))], fallback: fmt.Sprintf("Episode %d", e.episode.Int64),
			})
			episodePod[e.id] = p.itemID
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

	userIDs := map[int64]bool{}
	for _, u := range users {
		userIDs[u.ID] = true
	}
	type mappedProgress struct {
		p        progRow
		duration float64
	}
	var progress []mappedProgress
	skippedProg := 0
	editionDurations := map[int64]float64{}
	for _, b := range books {
		for _, f := range b.files {
			editionDurations[b.b.itemID] += f.Duration
		}
	}
	for _, rp := range podcasts {
		for _, e := range rp.episodes {
			editionDurations[e.e.id] = e.file.Duration
		}
	}
	for _, p := range progRows {
		if !userIDs[p.userID] {
			skippedProg++
			continue
		}
		switch p.itemType {
		case "book":
			if _, ok := itemLib[p.mediaID]; !ok {
				skippedProg++
				continue
			}
		case "podcastEpisode":
			if _, ok := episodePod[p.mediaID]; !ok {
				skippedProg++
				continue
			}
		default:
			skippedProg++
			continue
		}
		progress = append(progress, mappedProgress{p: p, duration: editionDurations[p.mediaID]})
	}
	if skippedProg > 0 {
		plan.warnf("%d mediaProgress rows skipped (unknown user or unimported item)", skippedProg)
	}
	plan.ProgressRows = len(progress)

	var mappedPlaylists []plRow
	for _, p := range playlists {
		if !userIDs[p.userID] || len(p.items) == 0 {
			plan.warnf("playlist %q skipped (unknown user or no imported items)", p.name)
			continue
		}
		kept := make([]int64, 0, len(p.items))
		for _, it := range p.items {
			if _, ok := itemLib[it]; ok {
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

	if len(users) > 0 {
		plan.warnf("password hashes cannot be migrated; imported users get a temp password the admin must reset")
	}

	if dryRun {
		if _, err := applyUsers(db, users, plan, false); err != nil {
			return nil, err
		}
		return plan, nil
	}

	userMap, err := applyUsers(db, users, plan, true)
	if err != nil {
		return nil, err
	}

	type edRef struct {
		id    int64
		files []fileSpec
		ids   []int64
	}
	bookEds := map[int64]*edRef{}
	epEds := map[int64]*edRef{}

	libIDs := map[int64]int64{}
	for _, l := range libs {
		if _, ok := libIndex[l.id]; !ok {
			continue
		}
		fallback := filepath.Join(dataDir, "imported", plan.Libraries[libIndex[l.id]].Name)
		if p := libPaths[l.id]; p != "" {
			fallback = p
		}
		id, err := ensureLibrary(db, l.name, libType[l.id], fallback, plan, libIndex[l.id], true)
		if err != nil {
			return nil, err
		}
		libIDs[l.id] = id
	}

	for _, b := range books {
		w := &store.Work{LibraryID: libIDs[b.b.libID], Title: b.b.title, Author: strPtr(b.b.author), Description: strPtr(b.b.desc)}
		wid, err := db.UpsertWork(w)
		if err != nil {
			return nil, err
		}
		total := 0.0
		for _, f := range b.files {
			total += f.Duration
		}
		e := &store.Edition{WorkID: wid, Format: b.format, Title: b.b.title, DurationSecs: &total}
		eid, err := db.UpsertEdition(e)
		if err != nil {
			return nil, err
		}
		ids, err := applyFiles(db, eid, b.files)
		if err != nil {
			return nil, err
		}
		bookEds[b.b.itemID] = &edRef{id: eid, files: b.files, ids: ids}
	}

	for _, rp := range podcasts {
		w := &store.Work{LibraryID: libIDs[rp.p.libID], Title: rp.p.title, Author: strPtr(rp.p.author)}
		wid, err := db.UpsertWork(w)
		if err != nil {
			return nil, err
		}
		for _, e := range rp.episodes {
			season := int(e.e.season.Int64)
			episode := int(e.e.episode.Int64)
			title := e.e.title
			if title == "" {
				title = e.fallback
			}
			dur := e.file.Duration
			ed := &store.Edition{
				WorkID: wid, Format: e.format, Title: title, DurationSecs: &dur,
				SeasonNum: &season, EpisodeNum: &episode,
			}
			eid, err := db.UpsertEdition(ed)
			if err != nil {
				return nil, err
			}
			ids, err := applyFiles(db, eid, []fileSpec{e.file})
			if err != nil {
				return nil, err
			}
			epEds[e.e.id] = &edRef{id: eid, files: []fileSpec{e.file}, ids: ids}
		}
	}

	for _, p := range progress {
		uid, ok := userMap[p.p.userID]
		if !ok {
			continue
		}
		var ref *edRef
		switch p.p.itemType {
		case "book":
			ref = bookEds[p.p.mediaID]
		case "podcastEpisode":
			ref = epEds[p.p.mediaID]
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
			if err := db.SetReadingProgress(rp); err != nil {
				return nil, err
			}
			continue
		}
		var dur *float64
		if p.duration > 0 {
			d := p.duration
			dur = &d
		}
		if err := db.SetProgress(&store.Progress{
			UserID: uid, EditionID: ref.id, FileID: fid, FileOffsetSecs: off,
			EditionPositionSecs: p.p.current, DurationSecs: dur,
			IsFinished: p.p.finished, Device: strPtr("abs-import"),
		}); err != nil {
			return nil, err
		}
	}

	for _, p := range mappedPlaylists {
		uid, ok := userMap[p.userID]
		if !ok {
			continue
		}
		plID := int64(0)
		existing, err := db.ListPlaylists(uid)
		if err != nil {
			return nil, err
		}
		for _, ex := range existing {
			if ex.Name == p.name {
				plID = ex.ID
				break
			}
		}
		if plID == 0 {
			plID, err = db.CreatePlaylist(uid, p.name)
			if err != nil {
				return nil, err
			}
		}
		for _, it := range p.items {
			if ref := bookEds[it]; ref != nil {
				if _, err := db.AddPlaylistItem(plID, ref.id); err != nil {
					return nil, err
				}
			}
		}
	}

	return plan, nil
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
