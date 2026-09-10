package store

import (
	"database/sql"
	"errors"
)

var ErrSameWork = errors.New("cannot merge a work into itself")

type MoveResult struct {
	TargetWorkID  int64
	Created       bool
	SourceWorkID  int64
	SourceDeleted bool
}

// EditionRow loads an edition without requiring files; EditionByID reports
// ErrNotFound for editions whose files are all missing, which linking must
// tolerate.
func (d *DB) EditionRow(id int64) (*Edition, error) {
	var e Edition
	var abr int
	err := d.QueryRow(`SELECT id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, created_at FROM editions WHERE id = ?`, id).
		Scan(&e.ID, &e.WorkID, &e.Format, &e.Title, &e.Language, &abr, &e.DurationSecs, &e.Position, &e.SeasonNum, &e.EpisodeNum, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.Abridged = abr != 0
	return &e, nil
}

// EnsureWorkInLibrary resolves a work by UpsertWork's match semantics (the
// idx_works_match unique index) or creates it. Unlike UpsertWork it never
// rewrites an existing work's metadata, so linking into an existing work
// keeps its subtitle/description/cover intact.
func (d *DB) EnsureWorkInLibrary(w *Work) (int64, error) {
	if id, ok := d.FindWorkID(w.LibraryID, w.Title, w.Author); ok {
		return id, nil
	}
	return d.UpsertWork(w)
}

// MoveEditionToWork reparents an edition; files and progress follow the
// edition id. A source work left without editions is deleted, so a move can
// never strand an empty work. Moving to a work in another library is allowed:
// editions carry no library FK, the work's library applies to all of them.
func (d *DB) MoveEditionToWork(editionID, targetWorkID int64) (MoveResult, error) {
	e, err := d.EditionRow(editionID)
	if err != nil {
		return MoveResult{}, err
	}
	if e.WorkID == targetWorkID {
		return MoveResult{TargetWorkID: targetWorkID, SourceWorkID: e.WorkID}, nil
	}
	if _, err := d.Exec(`UPDATE editions SET work_id = ? WHERE id = ?`, targetWorkID, editionID); err != nil {
		return MoveResult{}, err
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM editions WHERE work_id = ?`, e.WorkID).Scan(&n); err != nil {
		return MoveResult{}, err
	}
	res := MoveResult{TargetWorkID: targetWorkID, SourceWorkID: e.WorkID}
	if n == 0 {
		if _, err := d.Exec(`DELETE FROM works WHERE id = ?`, e.WorkID); err != nil {
			return MoveResult{}, err
		}
		res.SourceDeleted = true
	}
	return res, nil
}

// MergeWorks moves every edition of source into target, then deletes source
// (editions first so FKs stay satisfied). Target keeps its own cover; the
// source's cover file, if any, is left on disk.
func (d *DB) MergeWorks(sourceID, targetID int64) error {
	if sourceID == targetID {
		return ErrSameWork
	}
	if _, err := d.WorkByID(sourceID); err != nil {
		return err
	}
	if _, err := d.WorkByID(targetID); err != nil {
		return err
	}
	if _, err := d.Exec(`UPDATE editions SET work_id = ? WHERE work_id = ?`, targetID, sourceID); err != nil {
		return err
	}
	_, err := d.Exec(`DELETE FROM works WHERE id = ?`, sourceID)
	return err
}

// SplitEditionToNewWork reparents an edition to a fresh work derived from its
// own metadata: title defaults to the edition title, author to the source
// work's author. If a work with that (title, author) already exists in the
// library, the edition moves into it instead of duplicating.
func (d *DB) SplitEditionToNewWork(editionID int64, title string, author *string) (MoveResult, error) {
	e, err := d.EditionRow(editionID)
	if err != nil {
		return MoveResult{}, err
	}
	src, err := d.WorkByID(e.WorkID)
	if err != nil {
		return MoveResult{}, err
	}
	if title == "" {
		title = e.Title
	}
	if author == nil {
		author = src.Author
	}
	w := &Work{LibraryID: src.LibraryID, Title: title, Author: author}
	targetID, err := d.EnsureWorkInLibrary(w)
	if err != nil {
		return MoveResult{}, err
	}
	res, err := d.MoveEditionToWork(editionID, targetID)
	if err != nil {
		return MoveResult{}, err
	}
	res.Created = w.Created
	return res, nil
}
