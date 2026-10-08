package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var ErrSourceConflict = errors.New("physical source identity requires a reviewed repair")

func nullSource(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func sourceEditionID(q dbtx, e *Edition) (int64, error) {
	if e.SourceLibraryID <= 0 || e.SourceKey == "" || !filepath.IsLocal(filepath.FromSlash(e.SourceKey)) {
		return 0, ErrSourceConflict
	}
	var id int64
	err := q.QueryRow(`SELECT a.edition_id FROM edition_source_aliases a JOIN editions e ON e.id=a.edition_id WHERE a.source_library_id=? AND a.source_key=? AND e.source_library_id=a.source_library_id UNION SELECT id FROM editions WHERE source_library_id=? AND source_key=?`, e.SourceLibraryID, e.SourceKey, e.SourceLibraryID, e.SourceKey).Scan(&id)
	if err == nil {
		if strings.HasPrefix(e.SourceKey, "abs/") || strings.HasPrefix(e.SourceKey, "kavita/") {
			var count int
			if err := q.QueryRow(`SELECT count(*) FROM files WHERE edition_id=?`, id).Scan(&count); err != nil {
				return 0, err
			}
			if count != len(e.SourcePaths) {
				return 0, ErrSourceConflict
			}
			for _, path := range e.SourcePaths {
				var exists bool
				if err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM files WHERE edition_id=? AND source_library_id=? AND path=?)`, id, e.SourceLibraryID, path).Scan(&exists); err != nil {
					return 0, err
				}
				if !exists {
					return 0, ErrSourceConflict
				}
			}
		}
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	for _, path := range e.SourcePaths {
		var candidate int64
		err := q.QueryRow(`SELECT edition_id FROM files WHERE path=? AND source_library_id=?`, path, e.SourceLibraryID).Scan(&candidate)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if id != 0 && id != candidate {
			return 0, ErrSourceConflict
		}
		id = candidate
	}
	digestMatch := false
	if id == 0 && len(e.SourceDigests) > 0 {
		for _, digest := range e.SourceDigests {
			if digest == "" {
				continue
			}
			rows, err := q.Query(`SELECT DISTINCT edition_id FROM files WHERE source_library_id=? AND sha256=? AND missing=1`, e.SourceLibraryID, digest)
			if err != nil {
				return 0, err
			}
			var matches []int64
			for rows.Next() {
				var candidate int64
				if err := rows.Scan(&candidate); err != nil {
					rows.Close()
					return 0, err
				}
				matches = append(matches, candidate)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return 0, err
			}
			if len(matches) > 1 {
				return 0, ErrSourceConflict
			}
			if len(matches) == 1 {
				if id != 0 && id != matches[0] {
					return 0, ErrSourceConflict
				}
				id = matches[0]
				digestMatch = true
			}
		}
	}

	if id != 0 {
		var key *string
		if err := q.QueryRow(`SELECT source_key FROM editions WHERE id=?`, id).Scan(&key); err != nil {
			return 0, err
		}
		if key != nil && *key != e.SourceKey && !digestMatch {
			if len(e.SourceDigests) != len(e.SourcePaths) || len(e.SourcePaths) == 0 {
				return 0, ErrSourceConflict
			}
			seen := map[string]bool{}
			for i, path := range e.SourcePaths {
				if seen[path] || len(e.SourceDigests[i]) != 64 {
					return 0, ErrSourceConflict
				}
				seen[path] = true
				var digest *string
				if err := q.QueryRow(`SELECT sha256 FROM files WHERE edition_id=? AND source_library_id=? AND path=?`, id, e.SourceLibraryID, path).Scan(&digest); err != nil {
					return 0, ErrSourceConflict
				}
				if digest != nil && *digest != "" && *digest != e.SourceDigests[i] {
					return 0, ErrSourceConflict
				}
			}
		}
		var count int
		if err := q.QueryRow(`SELECT count(*) FROM files WHERE edition_id=?`, id).Scan(&count); err != nil {
			return 0, err
		}
		if count > len(e.SourcePaths) || (key != nil && *key != e.SourceKey && count != len(e.SourcePaths)) {
			return 0, ErrSourceConflict
		}
	}
	return id, nil
}

func upsertSourceEdition(q dbtx, e *Edition) (int64, error) {
	id, err := sourceEditionID(q, e)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		res, err := q.Exec(`INSERT INTO editions(work_id,format,title,language,abridged,duration_secs,position,season_num,episode_num,created_at,source_library_id,source_key) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, e.WorkID, e.Format, e.Title, e.Language, e.Abridged, e.DurationSecs, e.Position, e.SeasonNum, e.EpisodeNum, nowMilli(), e.SourceLibraryID, e.SourceKey)
		if err != nil {
			return 0, err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
		_, err = q.Exec(`INSERT INTO edition_source_aliases(source_library_id,source_key,edition_id) VALUES(?,?,?)`, e.SourceLibraryID, e.SourceKey, id)
		return id, err
	}
	if _, err := q.Exec(`INSERT INTO edition_source_aliases(source_library_id,source_key,edition_id) VALUES(?,?,?) ON CONFLICT(source_library_id,source_key) DO NOTHING`, e.SourceLibraryID, e.SourceKey, id); err != nil {
		return 0, err
	}
	if strings.HasPrefix(e.SourceKey, "abs/") || strings.HasPrefix(e.SourceKey, "kavita/") {
		return id, nil
	}
	_, err = q.Exec(`UPDATE editions SET source_library_id=?,source_key=CASE WHEN source_key LIKE 'abs/%' OR source_key LIKE 'kavita/%' THEN source_key ELSE ? END,language=?,abridged=?,duration_secs=?,position=CASE WHEN ?>0 THEN ? ELSE position END WHERE id=?`, e.SourceLibraryID, e.SourceKey, e.Language, e.Abridged, e.DurationSecs, e.Position, e.Position, id)
	return id, err
}

func (t *Tx) UpsertSourceWork(w *Work, paths []string, sourceLibraryID int64) (int64, error) {
	var workID int64
	for _, path := range paths {
		var candidate int64
		err := t.QueryRow(`SELECT e.work_id FROM files f JOIN editions e ON e.id=f.edition_id WHERE f.path=? AND f.source_library_id=?`, path, sourceLibraryID).Scan(&candidate)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if workID != 0 && workID != candidate {
			return 0, ErrSourceConflict
		}
		workID = candidate
	}
	if workID != 0 {
		w.ID = workID
		w.Created = false
		return workID, nil
	}
	return t.UpsertWork(w)
}

func (d *DB) UnresolvedSources() ([]int64, error) {
	rows, err := d.Query(`SELECT id FROM files WHERE edition_id IS NOT NULL AND source_library_id IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func SourceKey(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("source outside library")
	}
	return filepath.ToSlash(rel), nil
}

func (t *Tx) UpsertSourceWorkDigests(w *Work, paths []string, sourceLibraryID int64, digests []string) (int64, error) {
	for _, path := range paths {
		var count int
		if err := t.QueryRow(`SELECT count(*) FROM files WHERE path=? AND source_library_id=?`, path, sourceLibraryID).Scan(&count); err != nil {
			return 0, err
		}
		if count > 0 {
			return t.UpsertSourceWork(w, paths, sourceLibraryID)
		}
	}
	var workID int64
	for _, digest := range digests {
		if digest == "" {
			continue
		}
		rows, err := t.Query(`SELECT DISTINCT e.work_id FROM files f JOIN editions e ON e.id=f.edition_id WHERE f.source_library_id=? AND f.sha256=? AND f.missing=1`, sourceLibraryID, digest)
		if err != nil {
			return 0, err
		}
		var candidates []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, err
			}
			candidates = append(candidates, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
		if len(candidates) > 1 {
			return 0, ErrSourceConflict
		}
		if len(candidates) == 1 {
			if workID != 0 && workID != candidates[0] {
				return 0, ErrSourceConflict
			}
			workID = candidates[0]
		}
	}
	if workID != 0 {
		w.ID = workID
		w.Created = false
		return workID, nil
	}
	return t.UpsertSourceWork(w, paths, sourceLibraryID)
}
