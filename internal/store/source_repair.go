package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"github.com/libteca/libteca/internal/mediafs"
	"io"
	"path/filepath"
	"strings"
)

type SourceRepair struct {
	Version               int                `json:"version"`
	Reason                string             `json:"reason"`
	ResetAffectedProgress bool               `json:"resetAffectedProgress"`
	Files                 []SourceRepairFile `json:"files"`
}

type SourceRepairFile struct {
	FileID             int64  `json:"fileId"`
	EditionID          int64  `json:"editionId"`
	Generation         string `json:"generation"`
	OldSourceLibraryID int64  `json:"oldSourceLibraryId"`
	NewSourceLibraryID int64  `json:"newSourceLibraryId"`
	TargetEditionID    int64  `json:"targetEditionId,omitempty"`
	TargetGeneration   string `json:"targetGeneration,omitempty"`
	TargetSourceKey    string `json:"targetSourceKey,omitempty"`
	Path               string `json:"path"`
	SizeBytes          int64  `json:"sizeBytes"`
	MtimeNS            int64  `json:"mtimeNs"`
	SHA256             string `json:"sha256"`
}

func (d *DB) RepairSources(plan SourceRepair, apply bool) error {
	if plan.Version != 1 || len(plan.Files) == 0 || strings.TrimSpace(plan.Reason) == "" || len(plan.Reason) > 1024 {
		return ErrSourceConflict
	}
	seen := map[int64]bool{}
	for _, item := range plan.Files {
		if seen[item.FileID] || item.FileID <= 0 || item.NewSourceLibraryID <= 0 || len(item.SHA256) != 64 {
			return ErrSourceConflict
		}
		if item.TargetSourceKey != "" && (!filepath.IsLocal(filepath.FromSlash(item.TargetSourceKey)) || item.TargetEditionID != 0 || !plan.ResetAffectedProgress) {
			return ErrSourceConflict
		}
		seen[item.FileID] = true
		lib, err := d.Library(item.NewSourceLibraryID)
		if err != nil {
			return err
		}
		f, err := mediafs.Open(lib.Path, item.Path)
		if err != nil {
			return fmt.Errorf("source repair file %d: %w", item.FileID, err)
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		if st.Size() != item.SizeBytes || st.ModTime().UnixNano() != item.MtimeNS || hex.EncodeToString(h.Sum(nil)) != item.SHA256 {
			return ErrSourceConflict
		}
	}
	return d.Update(func(tx *Tx) error {
		affected := map[int64]bool{}
		for _, item := range plan.Files {
			var edition, source, size, stamp, generation int64
			var path string
			err := tx.QueryRow(`SELECT f.edition_id,coalesce(f.source_library_id,0),f.path,f.size_bytes,f.mtime_ns,e.timeline_generation FROM files f JOIN editions e ON e.id=f.edition_id WHERE f.id=?`, item.FileID).Scan(&edition, &source, &path, &size, &stamp, &generation)
			if err != nil {
				return err
			}
			if edition != item.EditionID || source != item.OldSourceLibraryID || path != item.Path || size != item.SizeBytes || stamp != item.MtimeNS || fmt.Sprintf("%d:%d", edition, generation) != item.Generation {
				return ErrSourceConflict
			}
			if item.TargetSourceKey != "" {
				var count int
				if err := tx.QueryRow(`SELECT count(*) FROM editions WHERE source_library_id=? AND source_key=?`, item.NewSourceLibraryID, item.TargetSourceKey).Scan(&count); err != nil {
					return err
				}
				if count != 0 {
					return ErrSourceConflict
				}
			}
			target := item.TargetEditionID
			if target == 0 {
				target = edition
			}
			if _, err := editionRow(tx, target); err != nil {
				return err
			}
			if target != edition {
				var current int64
				if err := tx.QueryRow(`SELECT timeline_generation FROM editions WHERE id=?`, target).Scan(&current); err != nil {
					return err
				}
				if fmt.Sprintf("%d:%d", target, current) != item.TargetGeneration {
					return ErrSourceConflict
				}
			}
			if target != edition && !plan.ResetAffectedProgress {
				return ErrSourceConflict
			}
			affected[edition] = true
			affected[target] = true
		}
		if !apply {
			return nil
		}
		for _, item := range plan.Files {
			target := item.TargetEditionID
			if target == 0 {
				target = item.EditionID
			}
			if item.TargetSourceKey != "" {
				err := tx.QueryRow(`SELECT id FROM editions WHERE source_library_id=? AND source_key=?`, item.NewSourceLibraryID, item.TargetSourceKey).Scan(&target)
				if err == sql.ErrNoRows {
					res, err := tx.Exec(`INSERT INTO editions(work_id,format,title,language,abridged,position,season_num,episode_num,page_count,created_at,source_library_id,source_key) SELECT work_id,format,title,language,abridged,position,season_num,episode_num,page_count,?, ?,? FROM editions WHERE id=?`, nowMilli(), item.NewSourceLibraryID, item.TargetSourceKey, item.EditionID)
					if err != nil {
						return err
					}
					target, err = res.LastInsertId()
					if err != nil {
						return err
					}
				} else if err != nil {
					return err
				}
				affected[target] = true
			}
			if _, err := tx.Exec(`UPDATE files SET source_library_id=?,edition_id=?,sha256=? WHERE id=?`, item.NewSourceLibraryID, target, item.SHA256, item.FileID); err != nil {
				return err
			}
		}
		for edition := range affected {
			if _, err := tx.Exec(`UPDATE editions SET source_key=NULL,source_library_id=(SELECT min(source_library_id) FROM files WHERE edition_id=?) WHERE id=?`, edition, edition); err != nil {
				return err
			}
			if plan.ResetAffectedProgress {
				if _, err := tx.Exec(`INSERT INTO progress(user_id,edition_id,updated_at,revision,reset_generation,deleted) SELECT id,?,?,1,1,1 FROM users WHERE 1 ON CONFLICT(user_id,edition_id) DO UPDATE SET file_id=NULL,file_offset_secs=0,edition_position_secs=0,is_finished=0,page=NULL,percent=NULL,locator=NULL,updated_at=excluded.updated_at,revision=progress.revision+1,reset_generation=progress.reset_generation+1,deleted=1`, edition, nowMilli()); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
