package importer

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/libteca/libteca/internal/store"
)

// Kavita imports a Kavita app.db (series/libraries/chapters/files/users/
// reading progress), read-only. Reading progress lands as page + percent
// against the imported edition's page_count. Users without an account here
// get one with a temp password (Kavita's ASP.NET hashes cannot migrate).
func Kavita(dbPath string, db *store.DB, dryRun bool) (*Plan, error) {
	plan := &Plan{Source: "kavita"}
	if fi, err := os.Stat(dbPath); err != nil || fi.IsDir() {
		return nil, fmt.Errorf("kavita database not found at %s", dbPath)
	}
	fdb, err := openForeign(dbPath)
	if err != nil {
		return nil, err
	}
	defer fdb.Close()

	// Kavita stores the library type as an int enum: 1=Book, 2=Comic,
	// 3=Manga. Manga shares our comics type.
	type libRow struct {
		id   int64
		name string
		typ  int
	}
	var libs []libRow
	lrows, err := fdb.Query(`SELECT Id, Name, Type FROM Library`)
	if err != nil {
		if schemaErr(err) {
			return nil, fmt.Errorf("not a Kavita database: %w", err)
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

	authors := map[int64]string{}
	seriesSummary := map[int64]string{}
	{
		rows, err := fdb.Query(`SELECT sm.SeriesId, sm.Summary, p.Name
			FROM SeriesMetadata sm
			JOIN SeriesMetadataPeople smp ON smp.SeriesMetadataId = sm.Id
			JOIN Person p ON p.Id = smp.PersonId`)
		if err == nil {
			for rows.Next() {
				var sid int64
				var summary, name sql.NullString
				if rows.Scan(&sid, &summary, &name) == nil {
					seriesSummary[sid] = summary.String
					if _, ok := authors[sid]; !ok && name.String != "" {
						authors[sid] = name.String
					}
				}
			}
			rows.Close()
		}
		// Author credit is best-effort: when the people tables are absent
		// (or empty) the series imports without an author, not a failure.
	}

	type seriesRow struct {
		id     int64
		libID  int64
		name   string
		author string
	}
	seriesByLib := map[int64][]seriesRow{}
	if srows, err := fdb.Query(`SELECT Id, LibraryId, Name FROM Series`); err == nil {
		for srows.Next() {
			var s seriesRow
			if srows.Scan(&s.id, &s.libID, &s.name) == nil {
				s.author = authors[s.id]
				seriesByLib[s.libID] = append(seriesByLib[s.libID], s)
			}
		}
		srows.Close()
	}

	type chapterRow struct {
		id       int64
		seriesID int64
		title    string
		pages    int
	}
	chaptersBySeries := map[int64][]chapterRow{}
	if crows, err := fdb.Query(`SELECT c.Id, v.SeriesId, c.Title, c.Range, c.Number, c.Pages
		FROM Chapter c JOIN Volume v ON v.Id = c.VolumeId`); err == nil {
		for crows.Next() {
			var c chapterRow
			var title, numRange sql.NullString
			var number sql.NullFloat64
			if crows.Scan(&c.id, &c.seriesID, &title, &numRange, &number, &c.pages) == nil {
				c.title = chapterTitle(title.String, numRange.String, number)
				chaptersBySeries[c.seriesID] = append(chaptersBySeries[c.seriesID], c)
			}
		}
		crows.Close()
	}

	filesByChapter := map[int64][]fileSpec{}
	formatsByChapter := map[int64]string{}
	if frows, err := fdb.Query(`SELECT ChapterId, FilePath, Format FROM MangaFile`); err == nil {
		for frows.Next() {
			var chID int64
			var path string
			var format int
			if frows.Scan(&chID, &path, &format) != nil {
				continue
			}
			if _, err := os.Stat(path); err != nil {
				continue
			}
			filesByChapter[chID] = append(filesByChapter[chID], fileSpec{Path: path})
			formatsByChapter[chID] = kavitaFormat(format, path)
		}
		frows.Close()
	}

	var users []foreignUser
	if urows, err := fdb.Query(`SELECT Id, Username, Roles FROM AppUser`); err == nil {
		for urows.Next() {
			var u foreignUser
			var roles string
			if urows.Scan(&u.ID, &u.Name, &roles) == nil {
				u.IsAdmin = strings.Contains(roles, "Admin")
				users = append(users, u)
			}
		}
		urows.Close()
	} else if !schemaErr(err) {
		return nil, err
	}

	type progRow struct {
		userID    int64
		chapterID int64
		pagesRead int
	}
	var progRows []progRow
	if prows, err := fdb.Query(`SELECT AppUserId, ChapterId, PagesRead FROM AppUserProgress`); err == nil {
		for prows.Next() {
			var p progRow
			if prows.Scan(&p.userID, &p.chapterID, &p.pagesRead) == nil {
				progRows = append(progRows, p)
			}
		}
		prows.Close()
	}

	ourType := map[int]string{1: "books", 2: "comics", 3: "comics"}
	libIndex := map[int64]int{}
	libType := map[int64]string{}
	for _, l := range libs {
		typ, ok := ourType[l.typ]
		if !ok {
			plan.warnf("library %q has unsupported type %d; skipped", l.name, l.typ)
			continue
		}
		libType[l.id] = typ
		libIndex[l.id] = len(plan.Libraries)
		plan.Libraries = append(plan.Libraries, LibraryPlan{Name: l.name, Type: typ})
	}

	type resolvedEdition struct {
		ch     chapterRow
		files  []fileSpec
		format string
	}
	type resolvedSeries struct {
		s        seriesRow
		editions []resolvedEdition
	}
	var series []resolvedSeries
	editionByChapter := map[int64]resolvedEdition{}
	for _, lib := range libs {
		if _, ok := libIndex[lib.id]; !ok {
			continue
		}
		for _, s := range seriesByLib[lib.id] {
			rs := resolvedSeries{s: s}
			for _, ch := range chaptersBySeries[s.id] {
				files := filesByChapter[ch.id]
				if len(files) == 0 {
					plan.Libraries[libIndex[lib.id]].Skipped++
					plan.warnf("series %q chapter %q: no files on disk; skipped", s.name, ch.title)
					continue
				}
				re := resolvedEdition{ch: ch, files: files, format: formatsByChapter[ch.id]}
				rs.editions = append(rs.editions, re)
				editionByChapter[ch.id] = re
			}
			if len(rs.editions) == 0 {
				plan.warnf("series %q has no chapters with files; skipped", s.name)
				continue
			}
			series = append(series, rs)
			plan.Libraries[libIndex[lib.id]].Works++
			plan.Libraries[libIndex[lib.id]].Editions += len(rs.editions)
			plan.Works++
			plan.Editions += len(rs.editions)
			for _, re := range rs.editions {
				plan.Files += len(re.files)
			}
		}
	}

	userIDs := map[int64]bool{}
	for _, u := range users {
		userIDs[u.ID] = true
	}
	skippedProg := 0
	var progress []progRow
	for _, p := range progRows {
		if !userIDs[p.userID] {
			skippedProg++
			continue
		}
		if _, ok := editionByChapter[p.chapterID]; !ok {
			skippedProg++
			continue
		}
		progress = append(progress, p)
	}
	if skippedProg > 0 {
		plan.warnf("%d progress rows skipped (unknown user or unimported chapter)", skippedProg)
	}
	plan.ProgressRows = len(progress)

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

	libIDs := map[int64]int64{}
	for _, lib := range libs {
		idx, ok := libIndex[lib.id]
		if !ok {
			continue
		}
		id, err := ensureLibrary(db, lib.name, libType[lib.id], filepath.Join(filepath.Dir(dbPath), "imported-kavita", lib.name), plan, idx, true)
		if err != nil {
			return nil, err
		}
		libIDs[lib.id] = id
	}

	edIDs := map[int64]int64{}
	edPages := map[int64]int{}
	for _, rs := range series {
		w := &store.Work{
			LibraryID:   libIDs[rs.s.libID],
			Title:       rs.s.name,
			Author:      strPtr(rs.s.author),
			Description: strPtr(seriesSummary[rs.s.id]),
		}
		wid, err := db.UpsertWork(w)
		if err != nil {
			return nil, err
		}
		for _, re := range rs.editions {
			pages := re.ch.pages
			e := &store.EditionPages{
				Edition:   store.Edition{WorkID: wid, Format: re.format, Title: re.ch.title, DurationSecs: nil},
				PageCount: &pages,
			}
			eid, err := db.UpsertEditionPages(e)
			if err != nil {
				return nil, err
			}
			if _, err := applyFiles(db, eid, re.files); err != nil {
				return nil, err
			}
			edIDs[re.ch.id] = eid
			edPages[eid] = pages
		}
	}

	for _, p := range progress {
		uid, ok := userMap[p.userID]
		if !ok {
			continue
		}
		eid, ok := edIDs[p.chapterID]
		if !ok {
			continue
		}
		pct := 0.0
		if pages := edPages[eid]; pages > 0 {
			pct = float64(p.pagesRead) / float64(pages)
			if pct > 1 {
				pct = 1
			}
		}
		rp := &store.ReadingProgress{
			Progress: store.Progress{UserID: uid, EditionID: eid, IsFinished: pct >= 1, Device: strPtr("kavita-import")},
			Percent:  &pct,
		}
		if p.pagesRead > 0 {
			page := int64(p.pagesRead)
			rp.Page = &page
		}
		if err := db.SetReadingProgress(rp); err != nil {
			return nil, err
		}
	}

	return plan, nil
}

func chapterTitle(title, numRange string, number sql.NullFloat64) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	if r := strings.TrimSpace(numRange); r != "" {
		return r
	}
	if number.Valid {
		return "Chapter " + strconv.FormatFloat(number.Float64, 'f', -1, 64)
	}
	return "Untitled"
}

func kavitaFormat(format int, path string) string {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".epub":
		return "epub"
	case ".pdf":
		return "pdf"
	case ".cbz", ".zip":
		return "cbz"
	case ".cbr", ".rar":
		return "cbr"
	}
	switch format {
	case 2:
		return "pdf"
	case 3:
		return "epub"
	default:
		return "cbz"
	}
}
