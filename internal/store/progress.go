package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
)

func ValidPosition(position, total float64) error {
	if math.IsNaN(position) || math.IsInf(position, 0) || position < 0 {
		return fmt.Errorf("invalid position")
	}
	if math.IsNaN(total) || math.IsInf(total, 0) || total < 0 {
		return fmt.Errorf("invalid duration")
	}
	if total > 0 {
		if position > total+5 {
			return fmt.Errorf("position exceeds duration")
		}
		return nil
	}
	if position > 30*24*60*60 {
		return fmt.Errorf("position exceeds policy limit for unknown duration")
	}
	return nil
}

const MaxPlaytimeSeconds = float64(1<<53 - 1)

func ValidPlaytime(seconds float64) error {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > MaxPlaytimeSeconds {
		return fmt.Errorf("playtime out of supported range")
	}
	return nil
}

func (d *DB) GetProgress(userID, editionID int64) (*Progress, error) {
	var p Progress
	var fin int
	err := d.QueryRow(`SELECT user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, is_finished, device, updated_at, revision
		FROM progress WHERE user_id = ? AND edition_id = ? AND deleted = 0`, userID, editionID).
		Scan(&p.UserID, &p.EditionID, &p.FileID, &p.FileOffsetSecs, &p.EditionPositionSecs, &p.DurationSecs, &fin, &p.Device, &p.UpdatedAt, &p.Revision)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.IsFinished = fin != 0
	return &p, nil
}

func (d *DB) UserProgressList(userID int64) ([]Progress, error) {
	rows, err := d.Query(`SELECT user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, is_finished, device, updated_at, revision
		FROM progress WHERE user_id = ? AND deleted = 0 ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Progress
	for rows.Next() {
		var p Progress
		var fin int
		if err := rows.Scan(&p.UserID, &p.EditionID, &p.FileID, &p.FileOffsetSecs, &p.EditionPositionSecs, &p.DurationSecs, &fin, &p.Device, &p.UpdatedAt, &p.Revision); err != nil {
			return nil, err
		}
		p.IsFinished = fin != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) SetProgress(p *Progress) error {
	return setProgress(d, p)
}

func (t *Tx) SetProgress(p *Progress) error {
	return setProgress(t, p)
}

func setProgress(q dbtx, p *Progress) error {
	return setProgressAt(q, p, nowMilli())
}

func setProgressAt(q dbtx, p *Progress, now int64) error {
	_, err := q.Exec(`INSERT INTO progress (user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, is_finished, device, updated_at, revision)
		VALUES (?,?,?,?,?,?,?,?,?,1)
		ON CONFLICT(user_id, edition_id) DO UPDATE SET
			file_id = excluded.file_id,
			file_offset_secs = excluded.file_offset_secs,
			edition_position_secs = excluded.edition_position_secs,
			duration_secs = excluded.duration_secs,
			is_finished = excluded.is_finished,
			device = excluded.device,
			updated_at = excluded.updated_at,
			deleted = 0,
			revision = progress.revision + 1`,
		p.UserID, p.EditionID, p.FileID, p.FileOffsetSecs, p.EditionPositionSecs, p.DurationSecs, p.IsFinished, p.Device, now)
	return err
}

// DeleteProgress logically resets the row instead of removing it: the
// revision keeps advancing so a base captured before the delete can never
// match a later recreated row (ABA), and readers skip tombstones while the
// single-row reader still exposes the revision for a fresh conditional
// start. The reset generation advances on every reset and ordinary writes
// never change it, so a client holding a pre-reset operation can be
// identified even after new writes clear the tombstone. One upsert covers
// the absent-row case too: resetting a row that never existed still leaves
// a tombstone, so a base-0 operation captured while the row was absent can
// never insert over a reset lineage.
func (d *DB) DeleteProgress(userID, editionID int64) error {
	_, err := d.Exec(`INSERT INTO progress (user_id, edition_id, deleted, revision, reset_generation, updated_at)
		VALUES (?,?,1,1,1,?)
		ON CONFLICT(user_id, edition_id) DO UPDATE SET
			file_id = NULL, file_offset_secs = 0, edition_position_secs = 0,
			duration_secs = NULL, is_finished = 0, device = NULL,
			page = NULL, percent = NULL, locator = NULL,
			deleted = 1, revision = progress.revision + 1,
			reset_generation = progress.reset_generation + 1, updated_at = excluded.updated_at`,
		userID, editionID, nowMilli())
	return err
}

func (d *DB) EditionsInProgress(userID int64) ([]int64, error) {
	rows, err := d.Query(`SELECT edition_id FROM progress WHERE user_id = ? AND deleted = 0 AND is_finished = 0 ORDER BY updated_at DESC LIMIT 20`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (d *DB) CreateSession(s *Session) error {
	_, err := d.Exec(`INSERT INTO playback_sessions (id, user_id, edition_id, started_at, updated_at, position_secs, time_listened_secs, device_info)
		VALUES (?,?,?,?,?,?,?,?)`,
		s.ID, s.UserID, s.EditionID, s.StartedAt, s.UpdatedAt, s.PositionSecs, s.TimeListened, s.DeviceInfo)
	return err
}

func (d *DB) Session(id string) (*Session, error) {
	var s Session
	var fin, fid sql.NullInt64
	err := d.QueryRow(`SELECT id, user_id, edition_id, file_id, started_at, updated_at, position_secs, time_listened_secs, device_info, closed_at
		FROM playback_sessions WHERE id = ?`, id).
		Scan(&s.ID, &s.UserID, &s.EditionID, &fid, &s.StartedAt, &s.UpdatedAt, &s.PositionSecs, &s.TimeListened, &s.DeviceInfo, &fin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if fin.Valid {
		v := fin.Int64
		s.ClosedAt = &v
	}
	if fid.Valid {
		v := fid.Int64
		s.FileID = &v
	}
	return &s, err
}

// validateListenedDelta keeps the shared numeric policy on every listened
// delta the transaction helpers accumulate, so a future caller cannot bypass
// the handler-level check (A12-03).
func validateListenedDelta(v float64) error {
	if err := ValidPosition(v, 0); err != nil {
		return fmt.Errorf("invalid timeListened")
	}
	return nil
}

func (d *DB) CloseSession(id string, position, listened float64) error {
	if err := validateListenedDelta(listened); err != nil {
		return err
	}
	_, err := d.Exec(`UPDATE playback_sessions SET position_secs = ?, time_listened_secs = time_listened_secs + ?, updated_at = ?, closed_at = ? WHERE id = ? AND closed_at IS NULL`,
		position, listened, nowMilli(), nowMilli(), id)
	return err
}

func (d *DB) CloseSessionWithProgress(s *Session, p *Progress, listenedDelta float64) error {
	if err := validateListenedDelta(listenedDelta); err != nil {
		return err
	}
	return d.Update(func(tx *Tx) error {
		var owner, edition int64
		var closed sql.NullInt64
		err := tx.QueryRow(`SELECT user_id, edition_id, closed_at FROM playback_sessions WHERE id = ?`, s.ID).
			Scan(&owner, &edition, &closed)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if closed.Valid {
			return nil
		}
		if owner != s.UserID || p.UserID != owner || p.EditionID != edition {
			return ErrNotFound
		}
		now := nowMilli()
		if err := setProgressAt(tx, p, now); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE playback_sessions SET closed_at = ?, updated_at = ?, position_secs = ?, time_listened_secs = time_listened_secs + ? WHERE id = ? AND closed_at IS NULL`,
			now, now, p.EditionPositionSecs, listenedDelta, s.ID)
		return err
	})
}

// UpdateSessionWithProgress commits a live sync's progress write and session
// advancement in one transaction, mirroring CloseSessionWithProgress: a
// failure on either side leaves both rows untouched, so a client retry can
// never double-count listened time.
func (d *DB) UpdateSessionWithProgress(s *Session, p *Progress, listenedDelta float64) error {
	if err := validateListenedDelta(listenedDelta); err != nil {
		return err
	}
	return d.Update(func(tx *Tx) error {
		var owner, edition int64
		var closed sql.NullInt64
		err := tx.QueryRow(`SELECT user_id, edition_id, closed_at FROM playback_sessions WHERE id = ?`, s.ID).
			Scan(&owner, &edition, &closed)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if closed.Valid || owner != s.UserID || p.UserID != owner || p.EditionID != edition {
			return ErrNotFound
		}
		now := nowMilli()
		if err := setProgressAt(tx, p, now); err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE playback_sessions
			SET position_secs = ?, time_listened_secs = time_listened_secs + ?, updated_at = ?
			WHERE id = ? AND closed_at IS NULL`,
			p.EditionPositionSecs, listenedDelta, now, s.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrNotFound
		}
		return nil
	})
}
