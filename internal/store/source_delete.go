package store

func deletePhysicalLibraryRows(tx dbtx, libraryID int64) error {
	if _, err := tx.Exec(`DELETE FROM edition_source_aliases WHERE source_library_id=?`, libraryID); err != nil {
		return err
	}
	disappearing := `SELECT e.id FROM editions e WHERE EXISTS(SELECT 1 FROM files f WHERE f.edition_id=e.id AND f.source_library_id=?) AND NOT EXISTS(SELECT 1 FROM files f WHERE f.edition_id=e.id AND f.source_library_id IS NOT ?)`
	for _, table := range []string{"progress", "playback_sessions", "playlist_items", "game_play_sessions"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE edition_id IN (`+disappearing+`)`, libraryID, libraryID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE progress SET file_id=NULL,file_offset_secs=0 WHERE file_id IN (SELECT id FROM files WHERE source_library_id=?)`, libraryID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE playback_sessions SET file_id=NULL WHERE file_id IN (SELECT id FROM files WHERE source_library_id=?)`, libraryID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO progress(user_id,edition_id,updated_at,revision,reset_generation,deleted) SELECT u.id,e.id,?,1,1,1 FROM users u CROSS JOIN editions e WHERE e.format NOT LIKE 'game-%' AND e.id IN (SELECT edition_id FROM files WHERE source_library_id=?) AND EXISTS(SELECT 1 FROM files f WHERE f.edition_id=e.id AND f.source_library_id IS NOT ?) ON CONFLICT(user_id,edition_id) DO UPDATE SET file_id=NULL,file_offset_secs=0,edition_position_secs=0,is_finished=0,page=NULL,percent=NULL,locator=NULL,updated_at=excluded.updated_at,revision=progress.revision+1,reset_generation=progress.reset_generation+1,deleted=1`, nowMilli(), libraryID, libraryID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM files WHERE source_library_id=? AND edition_id IS NOT NULL`, libraryID); err != nil {
		return err
	}
	empty := `SELECT e.id FROM editions e JOIN works w ON w.id=e.work_id WHERE NOT EXISTS(SELECT 1 FROM files f WHERE f.edition_id=e.id) AND (e.source_library_id=? OR w.library_id=?)`
	for _, table := range []string{"progress", "playback_sessions", "playlist_items", "game_play_sessions"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE edition_id IN (`+empty+`)`, libraryID, libraryID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM editions WHERE id IN (`+empty+`)`, libraryID, libraryID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE editions SET source_key=NULL,source_library_id=(SELECT min(f.source_library_id) FROM files f WHERE f.edition_id=editions.id) WHERE source_library_id=?`, libraryID); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT w.id,min(f.source_library_id) FROM works w JOIN editions e ON e.work_id=w.id JOIN files f ON f.edition_id=e.id WHERE w.library_id=? GROUP BY w.id`, libraryID)
	if err != nil {
		return err
	}
	type move struct{ work, library int64 }
	var moves []move
	for rows.Next() {
		var m move
		if err := rows.Scan(&m.work, &m.library); err != nil {
			rows.Close()
			return err
		}
		moves = append(moves, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, m := range moves {
		w, err := workRow(tx, m.work)
		if err != nil {
			return err
		}
		if target, ok := findWorkID(tx, m.library, w.Title, w.Author); ok && target != m.work {
			if _, err := tx.Exec(`UPDATE editions SET work_id=? WHERE work_id=?`, target, m.work); err != nil {
				return err
			}
		} else if _, err := tx.Exec(`UPDATE works SET library_id=? WHERE id=?`, m.library, m.work); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`DELETE FROM works WHERE library_id=? AND NOT EXISTS(SELECT 1 FROM editions e WHERE e.work_id=works.id)`, libraryID)
	return err
}
