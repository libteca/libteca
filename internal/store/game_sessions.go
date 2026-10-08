package store

import (
	"database/sql"
	"errors"
	"strings"
)

var ErrPlaytimeInvalid = errors.New("invalid game playtime report")

var ErrPlaytimeConflict = errors.New("playtime was reset")

func (d *DB) ReportGamePlaytime(userID, editionID, epoch int64, sessionID string, elapsed float64) (*Progress, error) {
	if epoch < 0 || len(sessionID) < 1 || len(sessionID) > 128 || strings.TrimSpace(sessionID) != sessionID {
		return nil, ErrPlaytimeInvalid
	}
	if err := ValidPlaytime(elapsed); err != nil {
		return nil, err
	}
	result := &Progress{UserID: userID, EditionID: editionID}
	err := d.Update(func(tx *Tx) error {
		var format string
		if err := tx.QueryRow(`SELECT format FROM editions WHERE id=?`, editionID).Scan(&format); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if !strings.HasPrefix(format, "game-") {
			return ErrPlaytimeInvalid
		}
		var deleted int
		err := tx.QueryRow(`SELECT edition_position_secs, revision, reset_generation, deleted FROM progress WHERE user_id=? AND edition_id=?`, userID, editionID).Scan(&result.EditionPositionSecs, &result.Revision, &result.ResetGeneration, &deleted)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if result.ResetGeneration != epoch {
			return ErrPlaytimeConflict
		}
		var previous float64
		err = tx.QueryRow(`SELECT elapsed_secs FROM game_play_sessions WHERE user_id=? AND edition_id=? AND reset_generation=? AND session_id=?`, userID, editionID, epoch, sessionID).Scan(&previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		result.Deleted = deleted != 0
		if elapsed <= previous {
			return nil
		}
		delta := elapsed - previous
		total := result.EditionPositionSecs + delta
		if err := ValidPlaytime(total); err != nil {
			return ErrPlaytimeInvalid
		}
		if _, err := tx.Exec(`INSERT INTO game_play_sessions(user_id,edition_id,reset_generation,session_id,elapsed_secs) VALUES(?,?,?,?,?) ON CONFLICT(user_id,edition_id,reset_generation,session_id) DO UPDATE SET elapsed_secs=excluded.elapsed_secs`, userID, editionID, epoch, sessionID, elapsed); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO progress(user_id,edition_id,edition_position_secs,updated_at,revision,reset_generation) VALUES(?,?,?,?,1,?) ON CONFLICT(user_id,edition_id) DO UPDATE SET edition_position_secs=excluded.edition_position_secs,updated_at=excluded.updated_at,revision=progress.revision+1,deleted=0`, userID, editionID, total, nowMilli(), epoch); err != nil {
			return err
		}
		result.EditionPositionSecs = total
		result.Revision++
		result.Deleted = false
		return nil
	})
	return result, err
}

func (d *DB) SetLegacyGameProgressFields(p *ReadingProgress, fields ProgressFields) error {
	return d.Update(func(tx *Tx) error {
		var count int
		if err := tx.QueryRow(`SELECT count(*) FROM game_play_sessions WHERE user_id=? AND edition_id=?`, p.UserID, p.EditionID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrPlaytimeConflict
		}
		return setReadingProgressFields(tx, p, fields)
	})
}
