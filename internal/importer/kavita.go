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
// reading progress), read-only. The source layout is detected first: a
// released Kavita database keeps users in the ASP.NET identity tables and
// reading history in AppUserProgresses; the library type enum follows the
// released LibraryType meanings (Manga/Comic/ComicVine share our comics
// type, Book/LightNovel map to books, loose-image libraries are unsupported
// and warned, never silently relabeled). Required catalog tables missing
// from a detected layout are fatal — an import must not report success with
// an empty catalog. Users without an account here get one with a temp
// password (Kavita's ASP.NET hashes cannot migrate).
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

	if err := detectKavitaLayout(fdb); err != nil {
		return nil, err
	}

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
			if err := rows.Err(); err != nil {
				return nil, err
			}
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
	{
		srows, err := fdb.Query(`SELECT Id, LibraryId, Name FROM Series`)
		if err != nil {
			return nil, fmt.Errorf("Series unreadable: %w", err)
		}
		for srows.Next() {
			var s seriesRow
			if srows.Scan(&s.id, &s.libID, &s.name) == nil {
				s.author = authors[s.id]
				seriesByLib[s.libID] = append(seriesByLib[s.libID], s)
			}
		}
		srows.Close()
		if err := srows.Err(); err != nil {
			return nil, err
		}
	}

	type chapterRow struct {
		id       int64
		seriesID int64
		title    string
		pages    int
	}
	chaptersBySeries := map[int64][]chapterRow{}
	{
		crows, err := fdb.Query(`SELECT c.Id, v.SeriesId, c.Title, c.Range, c.Number, c.Pages
			FROM Chapter c JOIN Volume v ON v.Id = c.VolumeId`)
		if err != nil {
			return nil, fmt.Errorf("Chapter/Volume unreadable: %w", err)
		}
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
		if err := crows.Err(); err != nil {
			return nil, err
		}
	}

	filesByChapter := map[int64][]fileSpec{}
	formatsByChapter := map[int64]string{}
	{
		frows, err := fdb.Query(`SELECT ChapterId, FilePath, Format FROM MangaFile`)
		if err != nil {
			return nil, fmt.Errorf("MangaFile unreadable: %w", err)
		}
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
			kind, supported := kavitaFormat(format, path)
			if !supported {
				plan.warnf("chapter %d: file %q has unsupported MangaFormat %d; skipped", chID, path, format)
				continue
			}
			filesByChapter[chID] = append(filesByChapter[chID], fileSpec{Path: path})
			formatsByChapter[chID] = kind
		}
		frows.Close()
		if err := frows.Err(); err != nil {
			return nil, err
		}
	}

	users, err := kavitaUsers(fdb, plan)
	if err != nil {
		return nil, err
	}

	type progRow struct {
		userID    foreignID
		chapterID int64
		pagesRead int
	}
	var progRows []progRow
	{
		prows, err := fdb.Query(`SELECT AppUserId, ChapterId, PagesRead FROM AppUserProgresses`)
		if err == nil {
			for prows.Next() {
				var p progRow
				var userID any
				if prows.Scan(&userID, &p.chapterID, &p.pagesRead) == nil {
					p.userID = idOf(userID)
					progRows = append(progRows, p)
				}
			}
			prows.Close()
			if err := prows.Err(); err != nil {
				return nil, err
			}
		} else if !schemaErr(err) {
			return nil, err
		} else {
			plan.warnf("AppUserProgresses unreadable; no reading progress imported")
		}
	}

	libIndex := map[int64]int{}
	libType := map[int64]string{}
	for _, l := range libs {
		typ, ok := kavitaLibraryType(l.typ)
		if !ok {
			plan.warnf("library %q (id %d) has unsupported type %d; skipped", l.name, l.id, l.typ)
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

	userIDs := map[foreignID]bool{}
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

	err = db.Update(func(tx *store.Tx) error {
		userMap, err := applyUsers(tx, users, plan, true)
		if err != nil {
			return err
		}

		libIDs := map[int64]int64{}
		for _, lib := range libs {
			idx, ok := libIndex[lib.id]
			if !ok {
				continue
			}
			id, err := ensureLibrary(tx, lib.name, libType[lib.id], filepath.Join(filepath.Dir(dbPath), "imported-kavita", lib.name), plan, idx, true)
			if err != nil {
				return err
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
			wid, err := tx.UpsertWork(w)
			if err != nil {
				return err
			}
			for _, re := range rs.editions {
				pages := re.ch.pages
				e := &store.EditionPages{
					Edition:   store.Edition{WorkID: wid, Format: re.format, Title: re.ch.title, DurationSecs: nil},
					PageCount: &pages,
				}
				eid, err := tx.UpsertEditionPages(e)
				if err != nil {
					return err
				}
				if _, err := applyFilesTx(tx, eid, re.files); err != nil {
					return err
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
			if err := tx.SetReadingProgress(rp); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return plan, nil
}

func detectKavitaLayout(fdb *sql.DB) error {
	cols, err := tableColumns(fdb, "Library")
	if err != nil {
		if schemaErr(err) {
			return fmt.Errorf("not a Kavita database: %w", err)
		}
		return err
	}
	if len(cols) == 0 {
		return fmt.Errorf("not a Kavita database: Library table is absent")
	}
	for _, table := range []string{"Series", "Chapter", "Volume", "MangaFile"} {
		if _, err := tableColumns(fdb, table); err != nil {
			return fmt.Errorf("unsupported Kavita database: %s unreadable: %w", table, err)
		}
	}
	return nil
}

func kavitaUsers(fdb *sql.DB, plan *Plan) ([]foreignUser, error) {
	rows, err := fdb.Query(`SELECT Id, UserName FROM AspNetUsers`)
	if err != nil {
		if !schemaErr(err) {
			return nil, err
		}
		plan.warnf("AspNetUsers unreadable; users and progress cannot be imported")
		return nil, nil
	}
	var users []foreignUser
	for rows.Next() {
		var u foreignUser
		var id any
		var name sql.NullString
		if rows.Scan(&id, &name) == nil && name.String != "" {
			u.ID = idOf(id)
			u.Name = name.String
			users = append(users, u)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	admins := map[foreignID]bool{}
	arows, err := fdb.Query(`SELECT aur.UserId FROM AspNetUserRoles aur
		JOIN AspNetRoles ar ON ar.Id = aur.RoleId WHERE ar.Name = 'Admin'`)
	if err == nil {
		for arows.Next() {
			var id any
			if arows.Scan(&id) == nil {
				admins[idOf(id)] = true
			}
		}
		arows.Close()
		if err := arows.Err(); err != nil {
			return nil, err
		}
	} else if !schemaErr(err) {
		return nil, err
	} else {
		plan.warnf("AspNetUserRoles/AspNetRoles unreadable; imported users are non-admin")
	}
	for i := range users {
		users[i].IsAdmin = admins[users[i].ID]
	}
	return users, nil
}

func kavitaLibraryType(sourceType int) (string, bool) {
	switch sourceType {
	case 0, 1, 5:
		return "comics", true
	case 2, 4:
		return "books", true
	default:
		return "", false
	}
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

func kavitaFormat(format int, path string) (string, bool) {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".epub":
		return "epub", true
	case ".pdf":
		return "pdf", true
	case ".cbz", ".zip":
		return "cbz", true
	case ".cbr", ".rar":
		return "cbr", true
	}
	switch format {
	case 1:
		return "cbz", true
	case 3:
		return "epub", true
	case 4:
		return "pdf", true
	default:
		return "", false
	}
}
