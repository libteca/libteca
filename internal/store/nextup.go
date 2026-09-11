package store

import (
	"database/sql"
	"sort"
)

// NextUpEpisode is the next episode to watch for one series.
type NextUpEpisode struct {
	WorkID       int64
	EditionID    int64
	SeasonNum    int
	EpisodeNum   int
	LastUpdate   int64
	Title        string
	EpisodeTitle string
	CoverPath    *string
}

type nextUpState struct {
	workID       int64
	title        string
	cover        *string
	epTitle      map[int64]string
	lastUpdate   int64
	finSeason    int
	finEpisode   int
	hasFinished  bool
	partSeason   int
	partEpisode  int
	hasPartial   bool
	nextID       int64
	firstID      int64
	firstSeason  int
	firstEpisode int
	firstSet     bool
}

func nextUpRow(st *nextUpState, edID int64, season, episode int, upd int64) NextUpEpisode {
	return NextUpEpisode{
		WorkID: st.workID, EditionID: edID, SeasonNum: season, EpisodeNum: episode,
		LastUpdate: upd, Title: st.title, EpisodeTitle: st.epTitle[edID], CoverPath: st.cover,
	}
}

// NextUp returns, for each TV series the user has a progress row on, the next
// episode to watch: the partially-watched episode if one exists, else the first
// episode ordered by (season_num, episode_num) after the last finished one.
// Series with no progress are included only when includeUnstarted is true
// (Jellyfin's DisableFirstEpisode=true); their next episode is the first.
// seriesID > 0 restricts to one series. Result order: most recently touched
// series first, then title.
func (d *DB) NextUp(userID, seriesID int64, includeUnstarted bool) ([]NextUpEpisode, error) {
	q := `SELECT w.id, w.title, w.cover_path, e.id, e.title, e.season_num, e.episode_num, p.is_finished, p.updated_at
		FROM works w
		JOIN libraries l ON l.id = w.library_id AND l.type = 'tv'
		JOIN editions e ON e.work_id = w.id AND e.season_num IS NOT NULL AND e.episode_num IS NOT NULL
		LEFT JOIN progress p ON p.edition_id = e.id AND p.user_id = ?`
	args := []any{userID}
	q += ` WHERE 1=1`
	if seriesID > 0 {
		q += ` AND w.id = ?`
		args = append(args, seriesID)
	} else if !includeUnstarted {
		q += ` AND EXISTS (SELECT 1 FROM progress p3 JOIN editions e3 ON e3.id = p3.edition_id
			WHERE e3.work_id = w.id AND p3.user_id = ?)`
		args = append(args, userID)
	}
	q += ` ORDER BY w.id, e.season_num, e.episode_num`

	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var order []int64
	states := map[int64]*nextUpState{}
	for rows.Next() {
		var workID int64
		var title, epTitle string
		var cover *string
		var edID int64
		var season, episode int
		var fin, upd sql.NullInt64
		if err := rows.Scan(&workID, &title, &cover, &edID, &epTitle, &season, &episode, &fin, &upd); err != nil {
			return nil, err
		}
		st, ok := states[workID]
		if !ok {
			st = &nextUpState{workID: workID, title: title, cover: cover, epTitle: map[int64]string{}}
			states[workID] = st
			order = append(order, workID)
		}
		st.epTitle[edID] = epTitle
		if !st.firstSet {
			st.firstSet = true
			st.firstID, st.firstSeason, st.firstEpisode = edID, season, episode
		}
		if !fin.Valid {
			continue
		}
		if upd.Valid && upd.Int64 > st.lastUpdate {
			st.lastUpdate = upd.Int64
		}
		if fin.Int64 != 0 {
			if !st.hasFinished || season > st.finSeason || (season == st.finSeason && episode > st.finEpisode) {
				st.finSeason, st.finEpisode, st.hasFinished = season, episode, true
			}
		} else if !st.hasPartial || season > st.partSeason || (season == st.partSeason && episode > st.partEpisode) {
			st.partSeason, st.partEpisode, st.hasPartial, st.nextID = season, episode, true, edID
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []NextUpEpisode
	for _, workID := range order {
		st := states[workID]
		if st.hasPartial {
			out = append(out, nextUpRow(st, st.nextID, st.partSeason, st.partEpisode, st.lastUpdate))
			continue
		}
		if !st.hasFinished {
			if !includeUnstarted {
				continue
			}
			out = append(out, nextUpRow(st, st.firstID, st.firstSeason, st.firstEpisode, 0))
			continue
		}
		rows2, err := d.Query(`SELECT id, title, season_num, episode_num FROM editions
			WHERE work_id = ? AND season_num IS NOT NULL AND episode_num IS NOT NULL
			  AND (season_num > ? OR (season_num = ? AND episode_num > ?))
			ORDER BY season_num, episode_num LIMIT 1`,
			st.workID, st.finSeason, st.finSeason, st.finEpisode)
		if err != nil {
			return nil, err
		}
		if rows2.Next() {
			var id int64
			var et string
			var s, e int
			if err := rows2.Scan(&id, &et, &s, &e); err != nil {
				rows2.Close()
				return nil, err
			}
			st.epTitle[id] = et
			out = append(out, nextUpRow(st, id, s, e, st.lastUpdate))
		}
		rows2.Close()
		if err := rows2.Err(); err != nil {
			return nil, err
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].LastUpdate != out[j].LastUpdate {
			return out[i].LastUpdate > out[j].LastUpdate
		}
		return states[out[i].WorkID].title < states[out[j].WorkID].title
	})
	return out, nil
}
