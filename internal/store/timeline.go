package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
)

var ErrTimelineUnavailable = errors.New("timeline contains unavailable files")

type TimelineFile struct {
	FileID   int64    `json:"fileId"`
	Duration *float64 `json:"durationSecs"`
	Start    *float64 `json:"startSecs"`
	File     FileRec  `json:"-"`
}
type Timeline struct {
	EditionID  int64          `json:"editionId"`
	Generation string         `json:"generation"`
	Files      []TimelineFile `json:"files"`
	Total      *float64       `json:"totalDurationSecs"`
	Edition    Edition        `json:"-"`
}

func (d *DB) EditionTimeline(id int64) (*Timeline, error) {
	result := &Timeline{EditionID: id}
	err := d.Update(func(tx *Tx) error {
		ed, err := editionRow(tx, id)
		if err != nil {
			return err
		}
		result.Edition = *ed
		var generation int64
		if err := tx.QueryRow(`SELECT timeline_generation FROM editions WHERE id=?`, id).Scan(&generation); err != nil {
			return err
		}
		result.Generation = fmt.Sprintf("%d:%d", id, generation)
		rows, err := tx.Query(`SELECT `+fileCols+` FROM files WHERE edition_id=? ORDER BY seq,id`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		total := 0.0
		known := true
		for rows.Next() {
			f, err := scanFile(rows)
			if err != nil {
				return err
			}
			if f.Missing {
				return ErrTimelineUnavailable
			}
			tf := TimelineFile{FileID: f.ID, File: f}
			if known {
				n := total
				tf.Start = &n
			}
			if f.DurationSecs > 0 && !math.IsNaN(f.DurationSecs) && !math.IsInf(f.DurationSecs, 0) {
				duration := f.DurationSecs
				tf.Duration = &duration
				if known {
					next := total + duration
					if next <= total || next > MaxPlaytimeSeconds {
						return ErrTimelineUnavailable
					}
					total = next
				}
			} else {
				known = false
			}
			result.Files = append(result.Files, tf)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(result.Files) == 0 {
			return ErrNotFound
		}
		if known {
			result.Total = &total
		}
		return nil
	})
	return result, err
}

func (t *Timeline) Select(fileID int64, offset float64) (*TimelineFile, *float64, error) {
	if offset < 0 || math.IsNaN(offset) || math.IsInf(offset, 0) {
		return nil, nil, errors.New("invalid file offset")
	}
	for i := range t.Files {
		file := &t.Files[i]
		if file.FileID != fileID {
			continue
		}
		if file.Duration != nil && offset > *file.Duration {
			return nil, nil, errors.New("file offset exceeds duration")
		}
		var position *float64
		if file.Start != nil {
			n := *file.Start + offset
			position = &n
		}
		return file, position, nil
	}
	return nil, nil, ErrNotFound
}

func (d *DB) TimelineGeneration(id int64) (string, error) {
	var generation int64
	err := d.QueryRow(`SELECT timeline_generation FROM editions WHERE id=?`, id).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return fmt.Sprintf("%d:%d", id, generation), err
}
