package core

import (
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/libteca/libteca/internal/auth"
)

type resumeItem struct {
	WorkID       int64   `json:"workId"`
	EditionID    int64   `json:"editionId"`
	LibraryID    int64   `json:"libraryId"`
	LibraryType  string  `json:"libraryType"`
	Title        string  `json:"title"`
	Author       string  `json:"author"`
	HasCover     bool    `json:"hasCover"`
	PositionSecs float64 `json:"positionSecs"`
	DurationSecs float64 `json:"durationSecs"`
	Percent      float64 `json:"percent"`
	UpdatedAt    int64   `json:"updatedAt"`
}

type searchResult struct {
	WorkID      int64    `json:"workId"`
	LibraryID   int64    `json:"libraryId"`
	LibraryType string   `json:"libraryType"`
	Title       string   `json:"title"`
	Author      string   `json:"author"`
	HasCover    bool     `json:"hasCover"`
	Percent     *float64 `json:"percent,omitempty"`
}

type recentItem struct {
	WorkID      int64  `json:"workId"`
	LibraryID   int64  `json:"libraryId"`
	LibraryType string `json:"libraryType"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	HasCover    bool   `json:"hasCover"`
	AddedAt     int64  `json:"addedAt"`
}

func authorStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func hasCover(p *string) bool {
	return p != nil && *p != ""
}

func (a *API) resume(w http.ResponseWriter, r *http.Request) {
	items, err := a.DB.ResumeItems(auth.UserID(r), 20)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	out := make([]resumeItem, 0, len(items))
	for _, it := range items {
		out = append(out, resumeItem{
			WorkID: it.WorkID, EditionID: it.EditionID, LibraryID: it.LibraryID,
			LibraryType: it.LibraryType, Title: it.Title, Author: authorStr(it.Author),
			HasCover: hasCover(it.CoverPath), PositionSecs: it.PositionSecs,
			DurationSecs: it.DurationSecs, Percent: it.Percent, UpdatedAt: it.UpdatedAt,
		})
	}
	writeJSON(w, 200, map[string]any{"items": out})
}

func (a *API) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	results := []searchResult{}
	if utf8.RuneCountInString(q) >= 2 {
		hits, err := a.DB.SearchWorks(auth.UserID(r), q, 30)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "internal error"})
			return
		}
		results = make([]searchResult, 0, len(hits))
		for _, h := range hits {
			results = append(results, searchResult{
				WorkID: h.WorkID, LibraryID: h.LibraryID, LibraryType: h.LibraryType,
				Title: h.Title, Author: authorStr(h.Author), HasCover: hasCover(h.CoverPath),
				Percent: h.Percent,
			})
		}
	}
	writeJSON(w, 200, map[string]any{"results": results})
}

func (a *API) nextUp(w http.ResponseWriter, r *http.Request) {
	items, err := a.DB.NextUp(auth.UserID(r), 0, true)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	if len(items) > 12 {
		items = items[:12]
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"workId": it.WorkID, "editionId": it.EditionID,
			"title": it.Title, "episodeTitle": it.EpisodeTitle,
			"seasonNum": it.SeasonNum, "episodeNum": it.EpisodeNum,
			"hasCover": it.CoverPath != nil && *it.CoverPath != "",
		})
	}
	writeJSON(w, 200, map[string]any{"items": out})
}

func (a *API) recent(w http.ResponseWriter, r *http.Request) {
	limit := 12
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 50 {
		limit = 50
	}
	works, err := a.DB.RecentWorks(limit)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	out := make([]recentItem, 0, len(works))
	for _, wv := range works {
		out = append(out, recentItem{
			WorkID: wv.WorkID, LibraryID: wv.LibraryID, LibraryType: wv.LibraryType,
			Title: wv.Title, Author: authorStr(wv.Author), HasCover: hasCover(wv.CoverPath),
			AddedAt: wv.AddedAt,
		})
	}
	writeJSON(w, 200, map[string]any{"items": out})
}
