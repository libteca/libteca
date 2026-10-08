package store

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"strings"
)

type MediaResume struct {
	FileID          *int64   `json:"resumeFileId,omitempty"`
	FileOffset      float64  `json:"resumeFileOffset"`
	Position        *float64 `json:"resumePosition,omitempty"`
	SavedGeneration *string  `json:"progressGeneration,omitempty"`
	Conflict        bool     `json:"resumeConflict"`
}

func mediaFileIdentity(q dbtx, id int64) (string, error) {
	rows, e := q.Query(`SELECT `+fileCols+` FROM files WHERE id=?`, id)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	if !rows.Next() {
		if e = rows.Err(); e != nil {
			return "", e
		}
		return "", sql.ErrNoRows
	}
	f, e := scanFile(rows)
	if e != nil {
		return "", e
	}
	if f.SHA256 != nil {
		if raw, err := hex.DecodeString(*f.SHA256); err == nil && len(raw) == 32 {
			return "sha256:" + strings.ToLower(*f.SHA256), nil
		}
	}
	return "", nil
}
func mediaResume(q dbtx, userID int64, s *MediaSnapshot) (*MediaResume, error) {
	result := &MediaResume{}
	var identity *string
	var finished, deleted bool
	var position float64
	var e error
	if s.Kind == "edition" {
		e = q.QueryRow(`SELECT file_id,file_offset_secs,edition_position_secs,is_finished,deleted,media_generation,media_file_identity FROM progress WHERE user_id=? AND edition_id=?`, userID, s.TargetID).Scan(&result.FileID, &result.FileOffset, &position, &finished, &deleted, &result.SavedGeneration, &identity)
	} else {
		e = q.QueryRow(`SELECT position_secs,is_finished,deleted,media_generation,media_file_identity FROM podcast_episode_progress WHERE user_id=? AND episode_id=?`, userID, s.TargetID).Scan(&result.FileOffset, &finished, &deleted, &result.SavedGeneration, &identity)
		position = result.FileOffset
		if len(s.Files) == 1 {
			fid := s.Files[0].ID
			result.FileID = &fid
		}
	}
	if errors.Is(e, sql.ErrNoRows) {
		return result, nil
	}
	if e != nil {
		return nil, e
	}
	if finished || deleted {
		result.FileID = nil
		return result, nil
	}
	if result.FileID == nil {
		result.Conflict = position > 0
		return result, nil
	}
	current, e := mediaFileIdentity(q, *result.FileID)
	if errors.Is(e, sql.ErrNoRows) {
		result.Conflict = true
		return result, nil
	}
	if e != nil {
		return nil, e
	}
	if result.SavedGeneration == nil || identity == nil || current == "" || *identity != current {
		result.Conflict = position > 0 || result.FileOffset > 0
		return result, nil
	}
	before := 0.0
	for _, file := range s.Files {
		if file.Missing {
			result.Conflict = true
			return result, nil
		}
		if file.ID == *result.FileID {
			if math.IsNaN(result.FileOffset) || math.IsInf(result.FileOffset, 0) || result.FileOffset < 0 || (file.Duration > 0 && result.FileOffset > file.Duration) {
				result.Conflict = true
				return result, nil
			}
			n := before + result.FileOffset
			result.Position = &n
			return result, nil
		}
		if file.Duration <= 0 {
			result.Conflict = true
			return result, nil
		}
		before += file.Duration
	}
	result.Conflict = true
	return result, nil
}
func (d *DB) MediaResumeForTarget(userID int64, kind string, id int64, expectedGeneration string) (result *MediaResume, err error) {
	err = d.Update(func(tx *Tx) error {
		s, e := mediaSnapshot(tx, userID, kind, id)
		if e != nil {
			return e
		}
		if expectedGeneration != "" && s.Generation != expectedGeneration {
			return ErrMediaConflict
		}
		result, e = mediaResume(tx, userID, s)
		return e
	})
	return
}
