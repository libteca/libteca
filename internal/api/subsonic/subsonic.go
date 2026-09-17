package subsonic

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cespare/xxhash/v2"
	"github.com/libteca/libteca/internal/audio"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

// ID scheme (stable, documented):
//
//	song     so-<edition id>
//	album    al-<work id>
//	artist   ar-<16 hex, xxhash64 of the lowercased artist name>
//	playlist pl-<playlist id>
//
// Artist ids are name-derived, not row-derived: an artist is a GROUP BY over
// works.author across all type='music' libraries, so no single work id can
// identify it. Same artist string always hashes to the same id.

const (
	apiVersion    = "1.16.1"
	serverVersion = "0.1.0" // corpus: keep in sync with the release version
)

const (
	errGeneric          = 0
	errMissingParam     = 10
	errWrongCredentials = 40
	errNotImplemented   = 50
	errNotFound         = 70
)

const ignoredArticles = "The El La Los Las Le Les" // corpus: value unverified against real servers

type API struct {
	LoginLimiter *auth.Limiter
	DB           *store.DB
	Dir          string
}

func New(db *store.DB, dir string) *API {
	return &API{DB: db, Dir: dir}
}

func subsonicCors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) Mount(r *neutron.Router) {
	rest := r.Group("/rest", subsonicCors)
	routes := map[string]func(http.ResponseWriter, *http.Request, int64){
		"ping":                      a.ping,
		"getOpenSubsonicExtensions": a.getOpenSubsonicExtensions,
		"getStarred2":               a.getStarred2,
		"savePlayQueue":             a.savePlayQueue,
		"getPlayQueue":              a.getPlayQueue,
		"getAlbumInfo2":             a.getAlbumInfo2,
		"getArtists":                a.getArtists,
		"getIndexes":                a.getIndexes,
		"getArtist":                 a.getArtist,
		"getAlbum":                  a.getAlbum,
		"getSong":                   a.getSong,
		"getAlbumList2":             a.getAlbumList2,
		"stream":                    a.stream,
		"download":                  a.stream,
		"getCoverArt":               a.getCoverArt,
		"search3":                   a.search3,
		"scrobble":                  a.scrobble,
		"getPlaylists":              a.getPlaylists,
		"getPlaylist":               a.getPlaylist,
		"createPlaylist":            a.createPlaylist,
		"updatePlaylist":            a.updatePlaylist,
		"deletePlaylist":            a.deletePlaylist,
	}
	for name, h := range routes {
		for _, suffix := range []string{".view", ""} {
			for _, method := range []string{"GET", "POST"} {
				rest.HandleFunc(method+" /"+name+suffix, a.wrap(h))
			}
		}
	}
	rest.HandleFunc("GET /{path}", a.notFound)
	rest.HandleFunc("POST /{path}", a.notFound)
}

func (a *API) notFound(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	a.respond(w, r, errResponse(70, "endpoint not supported"))
}

func (a *API) wrap(h func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		uid, ok, err := a.authenticate(r)
		if err != nil {
			if errors.Is(err, auth.ErrKDFBusy) {
				auth.WriteRetryAfter(w, time.Second)
				w.WriteHeader(http.StatusTooManyRequests)
				a.respond(w, r, errResponse(errGeneric, "server busy, try again later"))
				return
			}
			slog.Warn("libteca: subsonic authentication failed", "err", err)
			a.respond(w, r, errResponse(errGeneric, "Internal server error"))
			return
		}
		if !ok {
			a.respond(w, r, errResponse(errWrongCredentials, "Wrong username or password"))
			return
		}
		h(w, r, uid)
	}
}

// Token auth (t + s = md5(password+salt)) requires the plaintext password;
// libteca stores argon2 hashes. The plaintext is captured in settings on the
// first successful plain/hex login and reused for token verification after.
// corpus: Navidrome stores reversible passwords for the same reason.
// limiterKey is principal-aware: a success for user A must not erase the
// failure bucket accumulated against user V from the same IP (the shared
// per-IP bucket allowed 4-guess-then-login resets to run an unbounded MD5
// oracle).
func limiterKey(r *http.Request, user string) string {
	return auth.ClientIP(r) + "|" + strings.ToLower(strings.TrimSpace(user))
}

func (a *API) authenticate(r *http.Request) (int64, bool, error) {
	name := r.Form.Get("u")
	if name == "" {
		return 0, false, nil
	}
	u, err := a.DB.UserByName(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_, verr := auth.VerifyRequest(r.Context(), r.Form.Get("p"), auth.DummyHash())
			return 0, false, verr
		}
		return 0, false, err
	}
	if token := r.Form.Get("t"); token != "" {
		salt := r.Form.Get("s")
		if salt == "" {
			return 0, false, nil
		}
		// The token branch is an online password oracle: wrong candidates
		// cost one MD5, the right one authenticates. Without the limiter it
		// bypassed the lockout that governs every other credential check.
		key := limiterKey(r, name)
		if a.LoginLimiter != nil {
			if ok, _ := a.LoginLimiter.Allow(key); !ok {
				return 0, false, nil
			}
		}
		secret, has := a.DB.GetSetting(subsonicSecretKey(u.ID))
		if !has {
			return 0, false, nil
		}
		sum := md5.Sum([]byte(secret + salt))
		if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(strings.ToLower(token))) != 1 {
			if a.LoginLimiter != nil {
				a.LoginLimiter.Failure(key)
			}
			return 0, false, nil
		}
		if a.LoginLimiter != nil {
			a.LoginLimiter.Success(key)
		}
		stillCurrent, verr := auth.VerifyRequest(r.Context(), secret, u.PasswordHash)
		if verr != nil {
			return 0, false, verr
		}
		if !stillCurrent {
			a.DB.DeleteSetting(subsonicSecretKey(u.ID))
			return 0, false, nil
		}
		return u.ID, true, nil
	}
	pass := r.Form.Get("p")
	if pass == "" {
		return 0, false, nil
	}
	plainKey := limiterKey(r, name)
	if a.LoginLimiter != nil {
		if ok, _ := a.LoginLimiter.Allow(plainKey); !ok {
			return 0, false, nil
		}
	}
	if enc, found := strings.CutPrefix(pass, "enc:"); found {
		raw, err := hex.DecodeString(enc)
		if err != nil {
			return 0, false, nil
		}
		pass = string(raw)
	}
	valid, verr := auth.VerifyRequest(r.Context(), pass, u.PasswordHash)
	if verr != nil {
		return 0, false, verr
	}
	if !valid {
		if a.LoginLimiter != nil {
			a.LoginLimiter.Failure(plainKey)
		}
		return 0, false, nil
	}
	if a.LoginLimiter != nil {
		a.LoginLimiter.Success(plainKey)
	}
	if cached, has := a.DB.GetSetting(subsonicSecretKey(u.ID)); !has || cached != pass {
		a.DB.SetSetting(subsonicSecretKey(u.ID), pass)
	}
	return u.ID, true, nil
}

func subsonicSecretKey(userID int64) string {
	return "subsonic.pw." + strconv.FormatInt(userID, 10)
}

func ok() *Response {
	return &Response{
		XMLNS: "http://subsonic.org/restapi", Status: "ok", Version: apiVersion,
		Type: "libteca", ServerVersion: serverVersion, OpenSubsonic: true,
	}
}

func errResponse(code int, msg string) *Response {
	resp := ok()
	resp.Status = "failed"
	resp.Error = &Error{Code: code, Message: msg}
	return resp
}

// Subsonic errors travel in the envelope; HTTP status is 200 even on failure.
func (a *API) respond(w http.ResponseWriter, r *http.Request, resp *Response) {
	if strings.EqualFold(r.Form.Get("f"), "json") {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			SubsonicResponse *Response `json:"subsonic-response"`
		}{resp})
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8") // corpus: exact header value unverified
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(resp)
}

func (a *API) internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("libteca: subsonic request failed", "path", r.URL.Path, "err", err)
	a.respond(w, r, errResponse(errGeneric, "Internal server error"))
}

func (a *API) ping(w http.ResponseWriter, r *http.Request, _ int64) {
	a.respond(w, r, ok())
}

func (a *API) savePlayQueue(w http.ResponseWriter, r *http.Request, _ int64) {
	a.respond(w, r, errResponse(errNotImplemented, "Play queue persistence is not implemented"))
}

func (a *API) getStarred2(w http.ResponseWriter, r *http.Request, _ int64) {
	resp := ok()
	resp.Starred2 = &Starred2{}
	a.respond(w, r, resp)
}

func (a *API) getPlayQueue(w http.ResponseWriter, r *http.Request, _ int64) {
	a.respond(w, r, errResponse(errNotImplemented, "Play queue retrieval is not implemented"))
}

func (a *API) getAlbumInfo2(w http.ResponseWriter, r *http.Request, _ int64) {
	resp := ok()
	resp.AlbumInfo = &AlbumInfo{}
	a.respond(w, r, resp)
}

func (a *API) getOpenSubsonicExtensions(w http.ResponseWriter, r *http.Request, _ int64) {
	resp := ok()
	empty := []OpenSubsonicExtension{}
	resp.OpenSubsonicExtensions = &empty
	a.respond(w, r, resp)
}

func (a *API) getArtists(w http.ResponseWriter, r *http.Request, _ int64) {
	artists, err := a.DB.MusicArtists()
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	resp := ok()
	resp.Artists = &ArtistsID3{IgnoredArticles: ignoredArticles, Index: buildIndexesID3(artists)}
	a.respond(w, r, resp)
}

func (a *API) getIndexes(w http.ResponseWriter, r *http.Request, _ int64) {
	artists, err := a.DB.MusicArtists()
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	indexes := make([]Index, 0)
	for _, ar := range artists {
		letter := indexLetter(ar.Name)
		if len(indexes) == 0 || indexes[len(indexes)-1].Name != letter {
			indexes = append(indexes, Index{Name: letter})
		}
		last := &indexes[len(indexes)-1]
		last.Artist = append(last.Artist, IndexArtist{ID: artistID(ar.Key), Name: ar.Name})
	}
	resp := ok()
	resp.Indexes = &Indexes{IgnoredArticles: ignoredArticles, Index: indexes}
	a.respond(w, r, resp)
}

func buildIndexesID3(artists []store.MusicArtist) []ArtistIndexID3 {
	indexes := make([]ArtistIndexID3, 0)
	for _, ar := range artists {
		letter := indexLetter(ar.Name)
		if len(indexes) == 0 || indexes[len(indexes)-1].Name != letter {
			indexes = append(indexes, ArtistIndexID3{Name: letter})
		}
		last := &indexes[len(indexes)-1]
		last.Artist = append(last.Artist, ArtistID3{ID: artistID(ar.Key), Name: ar.Name, AlbumCount: ar.AlbumCount})
	}
	return indexes
}

func indexLetter(name string) string {
	runes := []rune(strings.ToUpper(name))
	if len(runes) == 0 || !unicode.IsLetter(runes[0]) {
		return "#"
	}
	return string(runes[0])
}

func (a *API) getArtist(w http.ResponseWriter, r *http.Request, _ int64) {
	id := r.Form.Get("id")
	if reArtistID.FindStringSubmatch(id) == nil {
		a.respond(w, r, errResponse(errNotFound, "Artist not found"))
		return
	}
	artists, err := a.DB.MusicArtists()
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	var found *store.MusicArtist
	for i := range artists {
		if artistID(artists[i].Key) == id {
			found = &artists[i]
			break
		}
	}
	if found == nil {
		a.respond(w, r, errResponse(errNotFound, "Artist not found"))
		return
	}
	albums, err := a.DB.MusicAlbumsByArtistKey(found.Key)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	resp := ok()
	resp.Artist = &ArtistWithAlbums{
		ArtistID3: ArtistID3{ID: id, Name: found.Name, AlbumCount: len(albums)},
		Album:     a.albumList(albums),
	}
	a.respond(w, r, resp)
}

func (a *API) getAlbum(w http.ResponseWriter, r *http.Request, _ int64) {
	workID, okID := parseID(reAlbumID, r.Form.Get("id"))
	if !okID {
		a.respond(w, r, errResponse(errNotFound, "Album not found"))
		return
	}
	album, err := a.DB.MusicAlbumByID(workID)
	if err != nil {
		a.respond(w, r, errResponse(errNotFound, "Album not found"))
		return
	}
	songs, err := a.DB.MusicSongsForWork(workID)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	resp := ok()
	resp.Album = &AlbumWithSongs{AlbumID3: a.albumID3(album), Song: a.songList(songs)}
	a.respond(w, r, resp)
}

func (a *API) getSong(w http.ResponseWriter, r *http.Request, _ int64) {
	song, err := a.resolveSong(r.Form.Get("id"))
	if err != nil {
		a.respond(w, r, errResponse(errNotFound, "Song not found"))
		return
	}
	resp := ok()
	child := a.songChild(song)
	resp.Song = &child
	a.respond(w, r, resp)
}

func (a *API) getAlbumList2(w http.ResponseWriter, r *http.Request, uid int64) {
	size := intParam(r, "size", 10, 500)
	offset := intParam(r, "offset", 0, 0)
	var albums []store.MusicAlbum
	var err error
	switch r.Form.Get("type") {
	case "newest":
		albums, err = a.DB.MusicAlbumsNewest(size, offset)
	case "recent":
		albums, err = a.DB.MusicAlbumsRecent(uid, size, offset)
	case "frequent":
		albums, err = a.DB.MusicAlbumsFrequent(uid, size, offset)
	case "random":
		albums, err = a.DB.MusicAlbumsRandom(size, offset)
	case "alphabeticalByName":
		albums, err = a.DB.MusicAlbumsByName(size, offset)
	case "alphabeticalByArtist":
		albums, err = a.DB.MusicAlbumsByArtistOrder(size, offset)
	case "starred", "highest", "byYear", "byGenre":
		// no ratings/genres/year in the model yet: honest empty list
	default:
		a.respond(w, r, errResponse(errMissingParam, "Missing or invalid list type"))
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	resp := ok()
	resp.AlbumList2 = &AlbumList2{Album: a.albumList(albums)}
	a.respond(w, r, resp)
}

func (a *API) stream(w http.ResponseWriter, r *http.Request, _ int64) {
	song, err := a.resolveSong(r.Form.Get("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	f, err := os.Open(song.File.Path)
	if err != nil {
		http.Error(w, "gone", 404)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "gone", 404)
		return
	}
	suffix, mime := songFormat(&song.File)
	name := "song"
	if suffix != "" {
		name = "song." + suffix
	}
	// corpus: maxBitRate accepted and ignored (direct stream only this slice)
	w.Header().Set("Content-Type", mime)
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func (a *API) getCoverArt(w http.ResponseWriter, r *http.Request, _ int64) {
	var workID int64
	if id, okID := parseID(reAlbumID, r.Form.Get("id")); okID {
		workID = id
	} else if song, err := a.resolveSong(r.Form.Get("id")); err == nil {
		workID = song.Work.ID
	} else {
		http.Error(w, "not found", 404)
		return
	}
	album, err := a.DB.MusicAlbumByID(workID)
	if err != nil || album.CoverPath == nil || *album.CoverPath == "" {
		http.Error(w, "not found", 404)
		return
	}
	f, err := os.Open(filepath.Join(a.Dir, "covers", filepath.Base(*album.CoverPath)))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	// corpus: size param ignored (no thumbnailer)
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, "cover.jpg", fi.ModTime(), f)
}

func (a *API) search3(w http.ResponseWriter, r *http.Request, _ int64) {
	query := r.Form.Get("query")
	if query == "" {
		a.respond(w, r, errResponse(errMissingParam, "Required parameter 'query' missing"))
		return
	}
	artistCount := intParam(r, "artistCount", 20, 500)
	albumCount := intParam(r, "albumCount", 20, 500)
	songCount := intParam(r, "songCount", 20, 500)
	artists, err := a.DB.MusicArtistsSearch(query, artistCount, intParam(r, "artistOffset", 0, 0))
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	albums, err := a.DB.MusicAlbumsSearch(query, albumCount, intParam(r, "albumOffset", 0, 0))
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	songs, err := a.DB.MusicSongsSearch(query, songCount, intParam(r, "songOffset", 0, 0))
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	resp := ok()
	resp.SearchResult3 = &SearchResult3{Artist: a.artistList(artists), Album: a.albumList(albums), Song: a.songList(songs)}
	a.respond(w, r, resp)
}

// Scrobble maps onto the same progress rows the ABS and Jellyfin faces write:
// submission=true marks the track finished at full duration, submission=false
// resets to position 0 (now playing). corpus: time param ignored.
func (a *API) scrobble(w http.ResponseWriter, r *http.Request, uid int64) {
	ids := r.Form["id"]
	if len(ids) == 0 {
		a.respond(w, r, errResponse(errMissingParam, "Required parameter 'id' missing"))
		return
	}
	submission := r.Form.Get("submission") != "false"
	songs := make([]store.MusicSong, 0, len(ids))
	for _, id := range ids {
		song, err := a.resolveSong(id)
		if err != nil {
			a.respond(w, r, errResponse(errNotFound, "Song not found: "+id))
			return
		}
		songs = append(songs, *song)
	}
	device := "subsonic"
	for i := range songs {
		s := &songs[i]
		dur := songDuration(s)
		pos := dur
		if !submission {
			pos = 0
		}
		fileID := s.File.ID
		if err := a.DB.SetProgress(&store.Progress{
			UserID: uid, EditionID: s.Edition.ID, FileID: &fileID,
			EditionPositionSecs: pos, DurationSecs: &dur,
			IsFinished: submission, Device: &device,
		}); err != nil {
			a.respond(w, r, errResponse(errGeneric, "Failed to save progress"))
			return
		}
	}
	a.respond(w, r, ok())
}

// Playlists are v1 owner-only: getPlaylists lists the caller's playlists,
// and any other user's playlist id answers 70 (the spec's not-found code).
// The header stats count every item; entries render only music-library
// editions (the face's currency).
func (a *API) getPlaylists(w http.ResponseWriter, r *http.Request, uid int64) {
	list, err := a.DB.ListPlaylists(uid)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	resp := ok()
	resp.Playlists = &Playlists{Playlist: a.playlistList(list)}
	a.respond(w, r, resp)
}

func (a *API) getPlaylist(w http.ResponseWriter, r *http.Request, uid int64) {
	p := a.fetchOwnedPlaylist(w, r, uid, r.Form.Get("id"))
	if p == nil {
		return
	}
	entries, err := a.playlistChildren(p.ID)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	resp := ok()
	resp.Playlist = &PlaylistWithSongs{Playlist: a.playlistAttrs(p), Entry: entries}
	a.respond(w, r, resp)
}

// createPlaylist per spec: name + songId[] creates; playlistId + songId[]
// replaces the playlist's content (and renames when name is given).
func (a *API) createPlaylist(w http.ResponseWriter, r *http.Request, uid int64) {
	editions, okIDs := a.resolveSongIDs(w, r, r.Form["songId"])
	if !okIDs {
		return
	}
	var pid int64
	if v := r.Form.Get("playlistId"); v != "" {
		p := a.fetchOwnedPlaylist(w, r, uid, v)
		if p == nil {
			return
		}
		pid = p.ID
		var namePtr *string
		if name := r.Form.Get("name"); name != "" {
			namePtr = &name
		}
		if err := a.DB.ReplacePlaylist(pid, namePtr, editions); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				a.respond(w, r, errResponse(errNotFound, "song not found"))
				return
			}
			a.internalError(w, r, err)
			return
		}
	} else {
		name := r.Form.Get("name")
		if name == "" {
			a.respond(w, r, errResponse(errMissingParam, "Required parameter 'name' missing"))
			return
		}
		id, err := a.DB.CreatePlaylistWithItems(uid, name, editions)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				a.respond(w, r, errResponse(errNotFound, "song not found"))
				return
			}
			a.internalError(w, r, err)
			return
		}
		pid = id
	}
	p, err := a.DB.Playlist(pid)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	entries, err := a.playlistChildren(pid)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	resp := ok()
	resp.Playlist = &PlaylistWithSongs{Playlist: a.playlistAttrs(p), Entry: entries}
	a.respond(w, r, resp)
}

// updatePlaylist per spec's awkward shape: songId[] APPENDS, while
// songIndexToRemove[] holds indexes into the playlist's current order
// (resolved against the pre-request snapshot, then removed by edition).
func (a *API) updatePlaylist(w http.ResponseWriter, r *http.Request, uid int64) {
	p := a.fetchOwnedPlaylist(w, r, uid, r.Form.Get("playlistId"))
	if p == nil {
		return
	}
	// Validate the COMPLETE request before mutating anything: removals used
	// to commit first, and a bad songId afterwards returned an error for a
	// request that had already permanently changed the playlist.
	removeEditions := []int64{}
	if idxs := r.Form["songIndexToRemove"]; len(idxs) > 0 {
		items, err := a.DB.PlaylistItems(p.ID)
		if err != nil {
			a.internalError(w, r, err)
			return
		}
		seenRemove := map[int64]bool{}
		for _, s := range idxs {
			n, err := strconv.Atoi(s)
			if err != nil || n < 0 || n >= len(items) {
				continue // corpus: out-of-range indexes skipped, not errors
			}
			eid := items[n].EditionID
			if seenRemove[eid] {
				continue // duplicate index: the same row twice
			}
			seenRemove[eid] = true
			removeEditions = append(removeEditions, eid)
		}
	}
	var addEditions []int64
	if ids := r.Form["songId"]; len(ids) > 0 {
		editions, okIDs := a.resolveSongIDs(w, r, ids)
		if !okIDs {
			return
		}
		addEditions = editions
	}
	newName := r.Form.Get("name")

	// One transaction for the whole delta: duplicate removal indexes and
	// mid-request store failures used to commit part of the request and
	// then return an error for it.
	var namePtr *string
	if newName != "" {
		namePtr = &newName
	}
	if err := a.DB.UpdatePlaylistDelta(p.ID, removeEditions, addEditions, namePtr); err != nil {
		a.internalError(w, r, err)
		return
	}
	a.respond(w, r, ok())
}

func (a *API) deletePlaylist(w http.ResponseWriter, r *http.Request, uid int64) {
	p := a.fetchOwnedPlaylist(w, r, uid, r.Form.Get("id"))
	if p == nil {
		return
	}
	if err := a.DB.DeletePlaylist(p.ID); err != nil {
		a.internalError(w, r, err)
		return
	}
	a.respond(w, r, ok())
}

// fetchOwnedPlaylist loads the playlist and enforces v1 owner-only scoping;
// unknown, malformed, and foreign ids are all a 70. nil means it responded.
func (a *API) fetchOwnedPlaylist(w http.ResponseWriter, r *http.Request, uid int64, id string) *store.Playlist {
	pid, okID := parseID(rePlaylistID, id)
	if !okID {
		a.respond(w, r, errResponse(errNotFound, "Playlist not found"))
		return nil
	}
	p, err := a.DB.Playlist(pid)
	if err != nil || p.UserID != uid {
		a.respond(w, r, errResponse(errNotFound, "Playlist not found"))
		return nil
	}
	return p
}

// resolveSongIDs validates so-<id> song references; false means it responded.
func (a *API) resolveSongIDs(w http.ResponseWriter, r *http.Request, ids []string) ([]int64, bool) {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		song, err := a.resolveSong(id)
		if err != nil {
			a.respond(w, r, errResponse(errNotFound, "Song not found: "+id))
			return nil, false
		}
		out = append(out, song.Edition.ID)
	}
	return out, true
}

func (a *API) playlistChildren(playlistID int64) ([]Child, error) {
	songs, err := a.DB.PlaylistMusicSongs(playlistID)
	if err != nil {
		return nil, err
	}
	return a.songList(songs), nil
}

func (a *API) playlistAttrs(p *store.Playlist) Playlist {
	return Playlist{
		ID: playlistID(p.ID), Name: p.Name,
		SongCount: p.SongCount, Duration: int(p.DurationSecs + 0.5),
		Owner: p.Owner, Created: isoTime(p.CreatedAt), Changed: isoTime(p.UpdatedAt),
	}
}

func (a *API) playlistList(list []store.Playlist) []Playlist {
	out := make([]Playlist, 0, len(list))
	for i := range list {
		out = append(out, a.playlistAttrs(&list[i]))
	}
	return out
}

var (
	reSongID     = regexp.MustCompile(`^so-(\d+)$`)
	reAlbumID    = regexp.MustCompile(`^al-(\d+)$`)
	reArtistID   = regexp.MustCompile(`^ar-([0-9a-f]{16})$`)
	rePlaylistID = regexp.MustCompile(`^pl-(\d+)$`)
)

func parseID(re *regexp.Regexp, s string) (int64, bool) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func (a *API) resolveSong(id string) (*store.MusicSong, error) {
	eid, okID := parseID(reSongID, id)
	if !okID {
		return nil, store.ErrNotFound
	}
	return a.DB.MusicSongByID(eid)
}

func artistID(key string) string {
	return fmt.Sprintf("ar-%016x", xxhash.Sum64([]byte(key)))
}

func albumID(workID int64) string   { return "al-" + strconv.FormatInt(workID, 10) }
func songID(editionID int64) string { return "so-" + strconv.FormatInt(editionID, 10) }
func playlistID(id int64) string    { return "pl-" + strconv.FormatInt(id, 10) }

func (a *API) artistList(artists []store.MusicArtist) []ArtistID3 {
	out := make([]ArtistID3, 0, len(artists))
	for i := range artists {
		out = append(out, ArtistID3{ID: artistID(artists[i].Key), Name: artists[i].Name, AlbumCount: artists[i].AlbumCount})
	}
	return out
}

func (a *API) albumList(albums []store.MusicAlbum) []AlbumID3 {
	out := make([]AlbumID3, 0, len(albums))
	for i := range albums {
		out = append(out, a.albumID3(&albums[i]))
	}
	return out
}

func (a *API) songList(songs []store.MusicSong) []Child {
	out := make([]Child, 0, len(songs))
	for i := range songs {
		out = append(out, a.songChild(&songs[i]))
	}
	return out
}

func (a *API) albumID3(m *store.MusicAlbum) AlbumID3 {
	artist := authorName(m.Author)
	album := AlbumID3{
		ID: albumID(m.ID), Name: m.Title,
		Artist: artist, ArtistID: artistID(strings.ToLower(artist)),
		SongCount: m.SongCount, Duration: int(m.Duration + 0.5),
		Created: isoTime(m.CreatedAt), // corpus: timestamp format unverified
	}
	if m.CoverPath != nil && *m.CoverPath != "" {
		album.CoverArt = album.ID
	}
	return album
}

func (a *API) songChild(s *store.MusicSong) Child {
	artist := authorName(s.Work.Author)
	suffix, mime := songFormat(&s.File)
	bitRate := 0
	if s.File.Bitrate != nil {
		bitRate = int(*s.File.Bitrate / 1000)
	}
	child := Child{
		ID: songID(s.Edition.ID), Parent: albumID(s.Work.ID),
		IsDir: false, Title: s.Edition.Title,
		Album: s.Work.Title, Artist: artist,
		Track:       int(s.Edition.Position),
		Size:        s.File.SizeBytes,
		ContentType: mime, Suffix: suffix,
		Duration: int(songDuration(s) + 0.5), BitRate: bitRate,
		IsVideo:  false,
		Created:  isoTime(s.Edition.CreatedAt),
		AlbumID:  albumID(s.Work.ID),
		ArtistID: artistID(strings.ToLower(artist)),
		Type:     "music",
	}
	if s.Work.CoverPath != nil && *s.Work.CoverPath != "" {
		child.CoverArt = child.Parent
	}
	return child
}

func authorName(author *string) string {
	if author == nil || strings.TrimSpace(*author) == "" {
		return "Unknown Artist"
	}
	return *author
}

func songDuration(s *store.MusicSong) float64 {
	if s.File.DurationSecs > 0 {
		return s.File.DurationSecs
	}
	if s.Edition.DurationSecs != nil {
		return *s.Edition.DurationSecs
	}
	return 0
}

var containerSuffixes = []string{"mp3", "flac", "ogg", "opus", "wav", "aac", "m4a", "mp4", "mov", "webm", "wma"}

func songFormat(f *store.FileRec) (suffix, contentType string) {
	container, codec := "", ""
	if f.Container != nil {
		container = *f.Container
	}
	if f.Codec != nil {
		codec = *f.Codec
	}
	for _, want := range containerSuffixes {
		if strings.Contains(","+container+",", ","+want+",") {
			suffix = want
			break
		}
	}
	if suffix == "" && codec != "" {
		suffix = codec
	}
	return suffix, audio.MimeType(codec, container)
}

func isoTime(unixMilli int64) string {
	return time.UnixMilli(unixMilli).UTC().Format("2006-01-02T15:04:05.000Z")
}

func intParam(r *http.Request, key string, def, max int) int {
	v := r.Form.Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	if max > 0 && n > max {
		n = max
	}
	return n
}
