package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

type MediaOperation struct {
	Version         int     `json:"version"`
	OperationID     string  `json:"operationId"`
	OwnerID         string  `json:"ownerId"`
	Kind            string  `json:"kind"`
	TargetID        int64   `json:"targetId"`
	InstallationID  string  `json:"installationId"`
	SessionID       string  `json:"sessionId"`
	Sequence        int64   `json:"sequence"`
	IntentEpoch     int64   `json:"intentEpoch"`
	Generation      string  `json:"generation"`
	BaseRevision    int64   `json:"baseRevision"`
	ResetGeneration int64   `json:"resetGeneration"`
	PredecessorID   string  `json:"predecessorId,omitempty"`
	Intent          string  `json:"intent"`
	FileID          int64   `json:"fileId"`
	FileOffset      float64 `json:"fileOffset"`
	Position        float64 `json:"position"`
	Duration        float64 `json:"duration"`
	Finished        bool    `json:"finished"`
}
type MediaFile struct {
	ID       int64   `json:"id"`
	Duration float64 `json:"duration"`
	Missing  bool    `json:"missing"`
}
type MediaSnapshot struct {
	OwnerID         string      `json:"ownerId"`
	Kind            string      `json:"kind"`
	TargetID        int64       `json:"targetId"`
	Generation      string      `json:"generation"`
	Revision        int64       `json:"revision"`
	ResetGeneration int64       `json:"resetGeneration"`
	Deleted         bool        `json:"deleted"`
	Position        float64     `json:"position"`
	Finished        bool        `json:"finished"`
	Files           []MediaFile `json:"files"`
	ResumeConflict  bool        `json:"resumeConflict"`
}
type MediaReceipt struct {
	OperationID     string `json:"operationId"`
	Revision        int64  `json:"revision"`
	ResetGeneration int64  `json:"resetGeneration"`
}

var ErrMediaConflict = errors.New("media progress conflict")
var ErrMediaEnvelope = errors.New("invalid media operation")

func mediaSnapshot(q dbtx, userID int64, kind string, id int64) (*MediaSnapshot, error) {
	s := &MediaSnapshot{OwnerID: strconv.FormatInt(userID, 10), Kind: kind, TargetID: id, Files: []MediaFile{}}
	var generation int64
	var err error
	if kind == "edition" {
		err = q.QueryRow(`SELECT timeline_generation FROM editions WHERE id=? AND (format IN ('audio','video','mp3','m4b','m4a','flac','ogg','wav','aac','opus'))`, id).Scan(&generation)
		if err == nil {
			s.Generation = fmt.Sprintf("%d:%d", id, generation)
			rows, e := q.Query(`SELECT id,duration_secs,missing FROM files WHERE edition_id=? ORDER BY seq,id`, id)
			if e != nil {
				return nil, e
			}
			for rows.Next() {
				var f MediaFile
				if e = rows.Scan(&f.ID, &f.Duration, &f.Missing); e != nil {
					rows.Close()
					return nil, e
				}
				s.Files = append(s.Files, f)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return nil, e
			}
			err = q.QueryRow(`SELECT revision,reset_generation,deleted,edition_position_secs,is_finished FROM progress WHERE user_id=? AND edition_id=?`, userID, id).Scan(&s.Revision, &s.ResetGeneration, &s.Deleted, &s.Position, &s.Finished)
			if errors.Is(err, sql.ErrNoRows) {
				err = nil
			}
		}
	} else if kind == "podcast-episode" {
		var fileID *int64
		var duration *float64
		err = q.QueryRow(`SELECT media_generation,file_id,duration_secs FROM podcast_episodes WHERE id=?`, id).Scan(&generation, &fileID, &duration)
		if err == nil {
			s.Generation = fmt.Sprintf("podcast:%d:%d", id, generation)
			if fileID != nil {
				var f MediaFile
				err = q.QueryRow(`SELECT id,duration_secs,missing FROM files WHERE id=?`, *fileID).Scan(&f.ID, &f.Duration, &f.Missing)
				if err == nil {
					s.Files = append(s.Files, f)
				}
			}
			if err == nil {
				err = q.QueryRow(`SELECT revision,reset_generation,deleted,position_secs,is_finished FROM podcast_episode_progress WHERE user_id=? AND episode_id=?`, userID, id).Scan(&s.Revision, &s.ResetGeneration, &s.Deleted, &s.Position, &s.Finished)
				if errors.Is(err, sql.ErrNoRows) {
					err = nil
				}
			}
		}
	} else {
		return nil, ErrMediaEnvelope
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}
func (d *DB) MediaSnapshot(userID int64, kind string, id int64) (s *MediaSnapshot, err error) {
	err = d.Update(func(tx *Tx) error {
		var e error
		s, e = mediaSnapshot(tx, userID, kind, id)
		if e != nil {
			return e
		}
		resume, e := mediaResume(tx, userID, s)
		if e != nil {
			return e
		}
		s.ResumeConflict = resume.Conflict
		if resume.Position != nil {
			s.Position = *resume.Position
		}
		if resume.Conflict {
			s.Position = 0
		}
		return nil
	})
	return
}
func mediaReceipt(q dbtx, userID int64, id string) (string, *MediaReceipt, error) {
	var envelope, result string
	err := q.QueryRow(`SELECT envelope,result FROM media_operation_receipts WHERE user_id=? AND operation_id=?`, userID, id).Scan(&envelope, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, err
	}
	var r MediaReceipt
	err = json.Unmarshal([]byte(result), &r)
	return envelope, &r, err
}
func (d *DB) MediaReceipt(userID int64, id string) (*MediaReceipt, error) {
	_, r, e := mediaReceipt(d, userID, id)
	return r, e
}
func (d *DB) ApplyMediaOperation(userID int64, o MediaOperation) (receipt *MediaReceipt, err error) {
	valid := func(n float64) bool { return n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }
	if o.Version != 1 || o.OwnerID != strconv.FormatInt(userID, 10) || o.TargetID <= 0 || o.BaseRevision < 0 || o.ResetGeneration < 0 || o.Sequence < 1 || o.IntentEpoch < 0 || !valid(o.Position) || !valid(o.FileOffset) || !valid(o.Duration) {
		return nil, ErrMediaEnvelope
	}
	for _, v := range []string{o.OperationID, o.InstallationID, o.SessionID, o.Generation} {
		if len(v) == 0 || len(v) > 200 {
			return nil, ErrMediaEnvelope
		}
	}
	switch o.Intent {
	case "heartbeat", "seek", "restart", "finish", "reset":
	default:
		return nil, ErrMediaEnvelope
	}
	if o.Finished != (o.Intent == "finish") || o.Intent == "reset" && (o.Position != 0 || o.FileOffset != 0) {
		return nil, ErrMediaEnvelope
	}
	raw, _ := json.Marshal(o)
	err = d.Update(func(tx *Tx) error {
		old, r, e := mediaReceipt(tx, userID, o.OperationID)
		if e == nil {
			if old != string(raw) {
				return ErrMediaEnvelope
			}
			receipt = r
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		s, e := mediaSnapshot(tx, userID, o.Kind, o.TargetID)
		if e != nil {
			return e
		}
		if o.Generation != s.Generation || o.ResetGeneration != s.ResetGeneration {
			return ErrMediaConflict
		}
		base := o.BaseRevision
		if o.PredecessorID != "" {
			praw, pr, e := mediaReceipt(tx, userID, o.PredecessorID)
			if e != nil {
				return ErrMediaConflict
			}
			var p MediaOperation
			if json.Unmarshal([]byte(praw), &p) != nil || p.Kind != o.Kind || p.TargetID != o.TargetID || p.InstallationID != o.InstallationID || p.SessionID != o.SessionID || p.Sequence >= o.Sequence || p.IntentEpoch > o.IntentEpoch || p.ResetGeneration != o.ResetGeneration || p.Generation != o.Generation {
				return ErrMediaConflict
			}
			base = pr.Revision
		}
		if base != s.Revision {
			return ErrMediaConflict
		}
		var lastID *string
		if o.Kind == "edition" {
			e = tx.QueryRow(`SELECT media_operation_id FROM progress WHERE user_id=? AND edition_id=?`, userID, o.TargetID).Scan(&lastID)
		} else {
			e = tx.QueryRow(`SELECT media_operation_id FROM podcast_episode_progress WHERE user_id=? AND episode_id=?`, userID, o.TargetID).Scan(&lastID)
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if lastID != nil {
			previous, _, err := mediaReceipt(tx, userID, *lastID)
			if err != nil {
				return err
			}
			var last MediaOperation
			if json.Unmarshal([]byte(previous), &last) != nil {
				return ErrMediaConflict
			}
			if last.SessionID == o.SessionID && last.InstallationID == o.InstallationID && (o.Sequence <= last.Sequence || o.IntentEpoch < last.IntentEpoch) {
				return ErrMediaConflict
			}
		}
		if o.Intent == "heartbeat" && (s.Finished || s.Deleted || o.Position < s.Position) {
			return ErrMediaConflict
		}
		if o.Intent != "reset" {
			for _, file := range s.Files {
				if file.Missing {
					return ErrMediaConflict
				}
			}
			before := 0.0
			found := false
			known := true
			for i, f := range s.Files {
				if f.Missing {
					return ErrMediaConflict
				}
				if f.ID == o.FileID {
					found = true
					if f.Duration > 0 && o.FileOffset > f.Duration+0.001 {
						return ErrMediaEnvelope
					}
					if known && math.Abs(before+o.FileOffset-o.Position) > 0.001 {
						return ErrMediaEnvelope
					}
					if !known {
						return ErrMediaConflict
					}
					if o.Finished && i != len(s.Files)-1 {
						return ErrMediaEnvelope
					}
					break
				}
				if f.Duration <= 0 {
					known = false
				} else {
					before += f.Duration
				}
			}
			if !found {
				return ErrMediaConflict
			}
		}
		fileIdentity := ""
		if o.Intent != "reset" {
			fileIdentity, e = mediaFileIdentity(tx, o.FileID)
			if e != nil {
				return e
			}
		}
		revision := s.Revision + 1
		reset := s.ResetGeneration
		deleted := o.Intent == "reset"
		if deleted {
			reset++
		}
		if o.Kind == "edition" {
			_, e = tx.Exec(`INSERT INTO progress(user_id,edition_id,file_id,file_offset_secs,edition_position_secs,duration_secs,is_finished,updated_at,revision,reset_generation,deleted) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,edition_id) DO UPDATE SET file_id=excluded.file_id,file_offset_secs=excluded.file_offset_secs,edition_position_secs=excluded.edition_position_secs,duration_secs=excluded.duration_secs,is_finished=excluded.is_finished,updated_at=excluded.updated_at,revision=excluded.revision,reset_generation=excluded.reset_generation,deleted=excluded.deleted`, userID, o.TargetID, nullableMediaFile(o.FileID, deleted), o.FileOffset, o.Position, o.Duration, o.Finished, nowMilli(), revision, reset, deleted)
		} else {
			_, e = tx.Exec(`INSERT INTO podcast_episode_progress(user_id,episode_id,position_secs,duration_secs,is_finished,updated_at,revision,reset_generation,deleted) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,episode_id) DO UPDATE SET position_secs=excluded.position_secs,duration_secs=excluded.duration_secs,is_finished=excluded.is_finished,updated_at=excluded.updated_at,revision=excluded.revision,reset_generation=excluded.reset_generation,deleted=excluded.deleted`, userID, o.TargetID, o.Position, o.Duration, o.Finished, nowMilli(), revision, reset, deleted)
		}
		if e != nil {
			return e
		}
		if o.Kind == "edition" {
			_, e = tx.Exec(`UPDATE progress SET media_generation=?,media_file_identity=?,media_operation_id=? WHERE user_id=? AND edition_id=?`, o.Generation, fileIdentity, o.OperationID, userID, o.TargetID)
		} else {
			_, e = tx.Exec(`UPDATE podcast_episode_progress SET media_generation=?,media_file_identity=?,media_operation_id=? WHERE user_id=? AND episode_id=?`, o.Generation, fileIdentity, o.OperationID, userID, o.TargetID)
		}
		if e != nil {
			return e
		}
		receipt = &MediaReceipt{o.OperationID, revision, reset}
		result, _ := json.Marshal(receipt)
		_, e = tx.Exec(`INSERT INTO media_operation_receipts(user_id,operation_id,envelope,result) VALUES(?,?,?,?)`, userID, o.OperationID, string(raw), string(result))
		return e
	})
	return
}
func nullableMediaFile(id int64, deleted bool) any {
	if deleted {
		return nil
	}
	return id
}

func (d *DB) ResetEpisodeProgress(userID, episodeID int64) error {
	_, err := d.Exec(`INSERT INTO podcast_episode_progress(user_id,episode_id,position_secs,is_finished,updated_at,revision,reset_generation,deleted) VALUES(?,?,0,0,?,1,1,1) ON CONFLICT(user_id,episode_id) DO UPDATE SET position_secs=0,is_finished=0,updated_at=excluded.updated_at,revision=podcast_episode_progress.revision+1,reset_generation=podcast_episode_progress.reset_generation+1,deleted=1`, userID, episodeID, nowMilli())
	return err
}
