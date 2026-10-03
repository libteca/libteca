package scan

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/libteca/libteca/internal/audio"
	"github.com/libteca/libteca/internal/mediafs"
	"github.com/libteca/libteca/internal/store"
)

func audioOrdinal(info *audio.Info, keys ...string) int {
	for _, key := range keys {
		value := strings.TrimSpace(strings.SplitN(metaGet(info, key), "/", 2)[0])
		if n, err := strconv.Atoi(value); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func probeBookFiles(ctx context.Context, root string, group []bookFile, tr *tracker) error {
	for i := range group {
		if err := cancelErr(ctx); err != nil {
			return err
		}
		f := &group[i]
		pf, err := mediafs.Open(root, f.path)
		if err != nil {
			return err
		}
		probe, perr := audio.ProbeFile(ctx, pf)
		pf.Close()
		if perr != nil {
			return perr
		}
		f.info = probe
		f.disc = audioOrdinal(probe, "disc", "discnumber", "disk", "disknumber")
		f.track = audioOrdinal(probe, "track", "tracknumber")
		f.hash = hashFile(f.path, f.size)
		tr.probed()
	}
	return cancelErr(ctx)
}

func sortBookFiles(top string, group []bookFile) {
	taggedDiscs, taggedTracks := false, false
	for _, f := range group {
		if f.disc > 0 {
			taggedDiscs = true
		}
		if f.track > 0 {
			taggedTracks = true
		}
	}
	sort.Slice(group, func(i, j int) bool {
		a, b := group[i], group[j]
		if !taggedDiscs && !taggedTracks {
			return natLess(relPath(top, a.path), relPath(top, b.path))
		}
		if taggedDiscs {
			ad, bd := max(a.disc, 1), max(b.disc, 1)
			if ad != bd {
				return ad < bd
			}
		} else {
			ad, bd := relPath(top, filepath.Dir(a.path)), relPath(top, filepath.Dir(b.path))
			if ad != bd {
				return natLess(ad, bd)
			}
		}
		if a.track != b.track {
			if a.track == 0 {
				return false
			}
			if b.track == 0 {
				return true
			}
			return a.track < b.track
		}
		return natLess(relPath(top, a.path), relPath(top, b.path))
	})
}

func bookOrderCurrent(db *store.DB, group []bookFile) (bool, error) {
	for _, f := range group {
		var current bool
		err := db.QueryRow(`SELECT coalesce(json_extract(embedded_meta, '$.libteca_audio_order.version'), 0) = 1 FROM files WHERE path = ?`, f.path).Scan(&current)
		if err != nil {
			return false, err
		}
		if !current {
			return false, nil
		}
	}
	return true, nil
}

func markBookOrder(tx *store.Tx, fileID int64, f bookFile) error {
	_, err := tx.Exec(`UPDATE files SET embedded_meta = json_set(embedded_meta, '$.libteca_audio_order', json_object('version', 1, 'disc', ?, 'track', ?)) WHERE id = ?`, f.disc, f.track, fileID)
	return err
}

func repairBookOrder(ctx context.Context, db *store.DB, group []bookFile, tr *tracker) error {
	err := db.Update(func(tx *store.Tx) error {
		ids := make([]int64, len(group))
		editions := make([]int64, len(group))
		durations := make([]float64, len(group))
		counts := map[int64]int{}
		for i, f := range group {
			if err := cancelErr(ctx); err != nil {
				return err
			}
			if err := tx.QueryRow(`SELECT id, edition_id, duration_secs FROM files WHERE path = ? AND missing = 0`, f.path).Scan(&ids[i], &editions[i], &durations[i]); err != nil {
				return err
			}
			counts[editions[i]]++
		}
		complete := map[int64]bool{}
		for editionID, count := range counts {
			var total int
			if err := tx.QueryRow(`SELECT count(*) FROM files WHERE edition_id = ? AND missing = 0`, editionID).Scan(&total); err != nil {
				return err
			}
			complete[editionID] = total == count
		}
		seqs := map[int64]int{}
		offsets := map[int64]float64{}
		for i, f := range group {
			if err := cancelErr(ctx); err != nil {
				return err
			}
			if complete[editions[i]] {
				seqs[editions[i]]++
				if _, err := tx.Exec(`UPDATE files SET seq = ? WHERE id = ?`, seqs[editions[i]], ids[i]); err != nil {
					return err
				}
				offset := offsets[editions[i]]
				if _, err := tx.Exec(`UPDATE progress SET edition_position_secs = ? + file_offset_secs, revision = revision + 1
					WHERE edition_id = ? AND file_id = ? AND deleted = 0 AND edition_position_secs != ? + file_offset_secs`,
					offset, editions[i], ids[i], offset); err != nil {
					return err
				}
				offsets[editions[i]] += durations[i]
			}
			if err := markBookOrder(tx, ids[i], f); err != nil {
				return err
			}
		}
		return cancelErr(ctx)
	})
	if err == nil {
		for range group {
			tr.file(false)
		}
	}
	return err
}

func ensureStoredBookCovers(ctx context.Context, db *store.DB, root, top string, group []bookFile, coversDir string) error {
	byWork := map[int64][]bookFile{}
	for _, f := range group {
		var id int64
		if err := db.QueryRow(`SELECT e.work_id FROM files f JOIN editions e ON e.id = f.edition_id WHERE f.path = ?`, f.path).Scan(&id); err != nil {
			return err
		}
		byWork[id] = append(byWork[id], f)
	}
	for id, files := range byWork {
		if err := cancelErr(ctx); err != nil {
			return err
		}
		if err := ensureCover(ctx, db, root, id, top, files, coversDir); err != nil {
			return err
		}
	}
	return nil
}
