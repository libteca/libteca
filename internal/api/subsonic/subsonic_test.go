package subsonic

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

type env struct {
	db   *store.DB
	h    http.Handler
	dir  string
	user int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := os.MkdirAll(filepath.Join(dir, "covers"), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('tyler', ?, 1, ?, ?)`,
		auth.Hash("secret"), now, now)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	uid, _ := res.LastInsertId()
	r := neutron.New().Router()
	New(db, dir).Mount(r)
	return &env{db: db, h: r, dir: dir, user: uid}
}

const plainAuth = "u=tyler&p=secret"

func (e *env) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) post(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// rest hits an endpoint with plain auth; extra must already be query-encoded.
func (e *env) rest(t *testing.T, endpoint, extra string) *httptest.ResponseRecorder {
	t.Helper()
	return e.get(t, "/rest/"+endpoint+".view?"+plainAuth+"&"+extra)
}

func (e *env) addLibrary(t *testing.T, typ string) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES (?,?,?,?)`,
		"lib-"+typ, typ, t.TempDir(), time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("seed library: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

type track struct {
	title string
	dur   float64
}

func (e *env) addAlbum(t *testing.T, libID int64, artist, title string, tracks []track, createdAt int64) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO works (library_id, title, author, created_at, updated_at) VALUES (?,?,?,?,?)`,
		libID, title, artist, createdAt, createdAt)
	if err != nil {
		t.Fatalf("seed work: %v", err)
	}
	workID, _ := res.LastInsertId()
	mediaDir := t.TempDir()
	for i, tr := range tracks {
		eres, err := e.db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, position, created_at) VALUES (?,?,?,?,?,?)`,
			workID, "mp3", tr.title, tr.dur, i+1, createdAt)
		if err != nil {
			t.Fatalf("seed edition: %v", err)
		}
		edID, _ := eres.LastInsertId()
		data := []byte(fmt.Sprintf("MP3DATA-%s-%02d", title, i+1))
		path := filepath.Join(mediaDir, fmt.Sprintf("%02d.mp3", i+1))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := e.db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, codec, container, bitrate, duration_secs, chapters, embedded_meta, missing, probed_at)
			VALUES (?,?,1,?,?, 'mp3', 'mp3', 128000, ?, '[]', '{}', 0, 0)`,
			edID, path, len(data), createdAt, tr.dur); err != nil {
			t.Fatalf("seed file: %v", err)
		}
	}
	return workID
}

func (e *env) editionID(t *testing.T, workID int64, position int) int64 {
	t.Helper()
	var id int64
	if err := e.db.QueryRow(`SELECT id FROM editions WHERE work_id = ? AND position = ?`, workID, position).Scan(&id); err != nil {
		t.Fatalf("edition lookup w%d p%d: %v", workID, position, err)
	}
	return id
}

func (e *env) seedProgress(t *testing.T, edID int64, updatedAt int64) {
	t.Helper()
	if _, err := e.db.Exec(`INSERT INTO progress (user_id, edition_id, edition_position_secs, duration_secs, is_finished, updated_at)
		VALUES (?,?,10,100,0,?)`, e.user, edID, updatedAt); err != nil {
		t.Fatalf("seed progress: %v", err)
	}
}

func (e *env) addCover(t *testing.T, workID int64) {
	t.Helper()
	name := fmt.Sprintf("%d.jpg", workID)
	if err := os.WriteFile(filepath.Join(e.dir, "covers", name), []byte("JPEGBYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE works SET cover_path = ? WHERE id = ?`, name, workID); err != nil {
		t.Fatal(err)
	}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("json decode: %v\nbody: %s", err, rec.Body.String())
	}
	return m["subsonic-response"].(map[string]any)
}

func subMap(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("key %q missing or not an object: %v", key, m)
	}
	return v
}

var wywhTracks = []track{{"Shine On You Crazy Diamond", 810}, {"Welcome to the Machine", 462}}

// seedMusic builds the standard library: two music libs (one shared artist
// across both), one audiobook lib that must stay invisible.
func seedMusic(t *testing.T, e *env) (wywh, animals, kob, wall int64) {
	t.Helper()
	lib1 := e.addLibrary(t, "music")
	lib2 := e.addLibrary(t, "music")
	libBooks := e.addLibrary(t, "audiobooks")
	wywh = e.addAlbum(t, lib1, "Pink Floyd", "Wish You Were Here", wywhTracks, 3000)
	animals = e.addAlbum(t, lib1, "Pink Floyd", "Animals", []track{{"Dogs", 1021}}, 1000)
	kob = e.addAlbum(t, lib1, "Miles Davis", "Kind of Blue", []track{{"So What", 541}, {"Freddie Freeloader", 587}}, 2000)
	wall = e.addAlbum(t, lib2, "Pink Floyd", "The Wall", []track{{"Another Brick in the Wall Part 1", 200}, {"Another Brick in the Wall Part 2", 230}}, 4000)
	e.addAlbum(t, libBooks, "Pink Floyd", "Memoirs", []track{{"Chapter 1", 900}}, 5000)
	return
}

func TestPing(t *testing.T) {
	e := newEnv(t)
	rec := e.rest(t, "ping", "f=json")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q", ct)
	}
	sr := decode(t, rec)
	want := map[string]any{
		"status": "ok", "version": "1.16.1", "type": "libteca",
		"serverVersion": "0.1.0", "openSubsonic": true,
	}
	if len(sr) != len(want) {
		t.Fatalf("envelope keys = %v, want exactly %v", sr, want)
	}
	for k, v := range want {
		if sr[k] != v {
			t.Fatalf("envelope[%q] = %v, want %v", k, sr[k], v)
		}
	}
}

func TestPingUnauthenticated(t *testing.T) {
	e := newEnv(t)
	rec := e.get(t, "/rest/ping.view?f=json")
	if rec.Code != 200 {
		t.Fatalf("subsonic errors must be HTTP 200, got %d", rec.Code)
	}
	sr := decode(t, rec)
	if sr["status"] != "failed" {
		t.Fatalf("status = %v", sr["status"])
	}
	e2 := subMap(t, sr, "error")
	if e2["code"].(float64) != 40 {
		t.Fatalf("error code = %v, want 40", e2["code"])
	}
}

func TestAuthModes(t *testing.T) {
	e := newEnv(t)
	// plain password
	rec := e.get(t, "/rest/ping.view?u=tyler&p=secret&f=json")
	if decode(t, rec)["status"] != "ok" {
		t.Fatalf("plain auth failed: %s", rec.Body.String())
	}
	// hex-encoded password
	rec = e.get(t, "/rest/ping.view?u=tyler&p=enc:"+hex.EncodeToString([]byte("secret"))+"&f=json")
	if decode(t, rec)["status"] != "ok" {
		t.Fatalf("hex auth failed: %s", rec.Body.String())
	}
	// token auth: md5(password+salt), verifiable only after a plain login
	// captured the password (argon2 hashes cannot verify md5 tokens)
	salt := "abcdef"
	token := fmt.Sprintf("%x", md5.Sum([]byte("secret"+salt)))
	rec = e.get(t, "/rest/ping.view?u=tyler&t="+token+"&s="+salt+"&f=json")
	if decode(t, rec)["status"] != "ok" {
		t.Fatalf("token auth failed: %s", rec.Body.String())
	}
	// wrong salt
	rec = e.get(t, "/rest/ping.view?u=tyler&t="+token+"&s=wrong&f=json")
	sr := decode(t, rec)
	if sr["status"] != "failed" || subMap(t, sr, "error")["code"].(float64) != 40 {
		t.Fatalf("bad salt must be error 40: %s", rec.Body.String())
	}
	// wrong password, unknown user, missing params
	rec = e.get(t, "/rest/ping.view?u=tyler&p=nope&f=json")
	if subMap(t, decode(t, rec), "error")["code"].(float64) != 40 {
		t.Fatal("wrong password must be 40")
	}
	rec = e.get(t, "/rest/ping.view?u=ghost&p=secret&f=json")
	if subMap(t, decode(t, rec), "error")["code"].(float64) != 40 {
		t.Fatal("unknown user must be 40")
	}
	rec = e.get(t, "/rest/ping.view?f=json")
	if subMap(t, decode(t, rec), "error")["code"].(float64) != 40 {
		t.Fatal("missing params must be 40")
	}
}

func TestTokenAuthNeedsCapture(t *testing.T) {
	e := newEnv(t)
	// fresh server, no plain login yet: token auth cannot be verified
	token := fmt.Sprintf("%x", md5.Sum([]byte("secret"+"salty")))
	rec := e.get(t, "/rest/ping.view?u=tyler&t="+token+"&s=salty&f=json")
	if subMap(t, decode(t, rec), "error")["code"].(float64) != 40 {
		t.Fatal("token auth before capture must be 40")
	}
}

func TestTokenAuthRejectsStaleSecret(t *testing.T) {
	e := newEnv(t)
	if rec := e.get(t, "/rest/ping.view?"+plainAuth+"&f=json"); decode(t, rec)["status"] != "ok" {
		t.Fatalf("plain login failed: %s", rec.Body.String())
	}
	salt := "pepper"
	oldToken := fmt.Sprintf("%x", md5.Sum([]byte("secret"+salt)))
	rec := e.get(t, "/rest/ping.view?u=tyler&t="+oldToken+"&s="+salt+"&f=json")
	if decode(t, rec)["status"] != "ok" {
		t.Fatalf("token auth before password change failed: %s", rec.Body.String())
	}
	if _, err := e.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, auth.Hash("rotated"), e.user); err != nil {
		t.Fatal(err)
	}
	rec = e.get(t, "/rest/ping.view?u=tyler&t="+oldToken+"&s="+salt+"&f=json")
	if subMap(t, decode(t, rec), "error")["code"].(float64) != 40 {
		t.Fatal("token minted from the old password must fail after a password change")
	}
	if _, has := e.db.GetSetting(subsonicSecretKey(e.user)); has {
		t.Fatal("stale subsonic secret must be dropped after rejection")
	}
	newToken := fmt.Sprintf("%x", md5.Sum([]byte("rotated"+salt)))
	rec = e.get(t, "/rest/ping.view?u=tyler&t="+newToken+"&s="+salt+"&f=json")
	if subMap(t, decode(t, rec), "error")["code"].(float64) != 40 {
		t.Fatal("secret for the new password must require a fresh plain login")
	}
}

func TestGetArtistsShape(t *testing.T) {
	e := newEnv(t)
	wywh, _, kob, wall := seedMusic(t, e)
	_ = wywh
	_ = kob
	_ = wall
	rec := e.rest(t, "getArtists", "f=json")
	sr := decode(t, rec)
	artists := subMap(t, sr, "artists")
	if artists["ignoredArticles"] != ignoredArticles {
		t.Fatalf("ignoredArticles = %v", artists["ignoredArticles"])
	}
	idx := artists["index"].([]any)
	if len(idx) != 2 {
		t.Fatalf("want 2 index groups (M, P), got %d: %v", len(idx), idx)
	}
	m := idx[0].(map[string]any)
	if m["name"] != "M" {
		t.Fatalf("first group = %v", m["name"])
	}
	miles := m["artist"].([]any)[0].(map[string]any)
	if miles["name"] != "Miles Davis" || miles["albumCount"].(float64) != 1 {
		t.Fatalf("miles = %v", miles)
	}
	p := idx[1].(map[string]any)
	if p["name"] != "P" {
		t.Fatalf("second group = %v", p["name"])
	}
	pf := p["artist"].([]any)[0].(map[string]any)
	if pf["name"] != "Pink Floyd" || pf["albumCount"].(float64) != 3 {
		t.Fatalf("pink floyd = %v", pf)
	}
	wantPF := fmt.Sprintf("ar-%016x", xxhash.Sum64([]byte("pink floyd")))
	if pf["id"] != wantPF {
		t.Fatalf("artist id = %v, want %v", pf["id"], wantPF)
	}

	// XML: default format, well-formed, spot-checked
	rec = e.rest(t, "getArtists", "")
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/xml") {
		t.Fatalf("default format must be XML, content-type %q", ct)
	}
	var xr struct {
		XMLName xml.Name `xml:"subsonic-response"`
		Status  string   `xml:"status,attr"`
		Artists struct {
			Index []struct {
				Name   string `xml:"name,attr"`
				Artist []struct {
					ID         string `xml:"id,attr"`
					Name       string `xml:"name,attr"`
					AlbumCount int    `xml:"albumCount,attr"`
				} `xml:"artist"`
			} `xml:"index"`
		} `xml:"artists"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &xr); err != nil {
		t.Fatalf("xml not well-formed: %v\n%s", err, rec.Body.String())
	}
	if xr.Status != "ok" || len(xr.Artists.Index) != 2 {
		t.Fatalf("xml shape: %+v", xr)
	}
	if xr.Artists.Index[1].Artist[0].Name != "Pink Floyd" || xr.Artists.Index[1].Artist[0].AlbumCount != 3 {
		t.Fatalf("xml pink floyd: %+v", xr.Artists.Index[1].Artist[0])
	}
}

func TestGetIndexesShape(t *testing.T) {
	e := newEnv(t)
	seedMusic(t, e)
	rec := e.rest(t, "getIndexes", "f=json")
	sr := decode(t, rec)
	indexes := subMap(t, sr, "indexes")
	idx := indexes["index"].([]any)
	if len(idx) != 2 {
		t.Fatalf("want 2 indexes, got %v", idx)
	}
	first := idx[0].(map[string]any)["artist"].([]any)[0].(map[string]any)
	if first["name"] != "Miles Davis" {
		t.Fatalf("first artist = %v", first)
	}
	if _, has := first["albumCount"]; has {
		t.Fatal("legacy getIndexes artists must not carry albumCount")
	}
}

func TestBrowseRoundTrip(t *testing.T) {
	e := newEnv(t)
	wywh, _, _, _ := seedMusic(t, e)
	e.addCover(t, wywh)

	// getArtists -> artist id
	rec := e.rest(t, "getArtists", "f=json")
	pfID := fmt.Sprintf("ar-%016x", xxhash.Sum64([]byte("pink floyd")))

	// getArtist -> albums
	rec = e.rest(t, "getArtist", "id="+pfID+"&f=json")
	sr := decode(t, rec)
	artist := subMap(t, sr, "artist")
	if artist["id"] != pfID || artist["albumCount"].(float64) != 3 {
		t.Fatalf("artist = %v", artist)
	}
	albums := artist["album"].([]any)
	names := make([]string, 0, len(albums))
	for _, a := range albums {
		am := a.(map[string]any)
		names = append(names, am["name"].(string))
		if !strings.HasPrefix(am["id"].(string), "al-") {
			t.Fatalf("album id %v", am["id"])
		}
	}
	want := []string{"Animals", "The Wall", "Wish You Were Here"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("album order = %v, want %v", names, want)
	}

	// getAlbum -> songs
	rec = e.rest(t, "getAlbum", "id=al-"+fmt.Sprint(wywh)+"&f=json")
	sr = decode(t, rec)
	album := subMap(t, sr, "album")
	if album["songCount"].(float64) != 2 || album["coverArt"] != "al-"+fmt.Sprint(wywh) {
		t.Fatalf("album = %v", album)
	}
	songs := album["song"].([]any)
	first := songs[0].(map[string]any)
	if first["title"] != "Shine On You Crazy Diamond" || first["track"].(float64) != 1 {
		t.Fatalf("first song = %v", first)
	}
	songID := first["id"].(string)

	// getSong -> full child
	rec = e.rest(t, "getSong", "id="+songID+"&f=json")
	sr = decode(t, rec)
	song := subMap(t, sr, "song")
	expect := map[string]any{
		"id": songID, "parent": "al-" + fmt.Sprint(wywh), "isDir": false,
		"title": "Shine On You Crazy Diamond", "album": "Wish You Were Here",
		"artist": "Pink Floyd", "track": float64(1), "coverArt": "al-" + fmt.Sprint(wywh),
		"size":        float64(len("MP3DATA-Wish You Were Here-01")),
		"contentType": "audio/mpeg", "suffix": "mp3", "duration": float64(810),
		"bitRate": float64(128), "isVideo": false,
		"albumId":  "al-" + fmt.Sprint(wywh),
		"artistId": pfID, "type": "music",
	}
	for k, v := range expect {
		if song[k] != v {
			t.Fatalf("song[%q] = %v, want %v", k, song[k], v)
		}
	}
	for _, absent := range []string{"year", "genre", "path", "playCount", "discNumber"} {
		if _, has := song[absent]; has {
			t.Fatalf("song must not carry %q", absent)
		}
	}
	if !strings.HasSuffix(song["created"].(string), "Z") {
		t.Fatalf("created not ISO: %v", song["created"])
	}

	// stream -> bytes, Range
	rec = e.rest(t, "stream", "id="+songID)
	if rec.Code != 200 || rec.Body.String() != "MP3DATA-Wish You Were Here-01" {
		t.Fatalf("stream: %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Fatalf("stream content-type %q", ct)
	}
	req := httptest.NewRequest("GET", "/rest/stream.view?"+plainAuth+"&id="+songID, nil)
	req.Header.Set("Range", "bytes=4-9")
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != 206 {
		t.Fatalf("range status %d", rr.Code)
	}
	if rr.Body.String() != "ATA-Wi" {
		t.Fatalf("range body %q", rr.Body.String())
	}
	if cr := rr.Header().Get("Content-Range"); !strings.HasPrefix(cr, "bytes 4-9/") {
		t.Fatalf("content-range %q", cr)
	}

	// download.view serves identical bytes
	rec = e.rest(t, "download", "id="+songID)
	if rec.Body.String() != "MP3DATA-Wish You Were Here-01" {
		t.Fatalf("download body %q", rec.Body.String())
	}

	// cover art by album id and by song id
	for _, id := range []string{"al-" + fmt.Sprint(wywh), songID} {
		rec = e.rest(t, "getCoverArt", "id="+id+"&size=64")
		if rec.Code != 200 || rec.Body.String() != "JPEGBYTES" {
			t.Fatalf("cover(%s): %d %q", id, rec.Code, rec.Body.String())
		}
	}

	// bare path (no .view) and POST both work
	rec = e.get(t, "/rest/getSong?"+plainAuth+"&id="+songID+"&f=json")
	if subMap(t, decode(t, rec), "song")["title"] != "Shine On You Crazy Diamond" {
		t.Fatal("bare path failed")
	}
	rec = e.post(t, "/rest/getSong.view?"+plainAuth+"&id="+songID+"&f=json")
	if subMap(t, decode(t, rec), "song")["title"] != "Shine On You Crazy Diamond" {
		t.Fatal("POST failed")
	}

	// unknown and non-music ids are error 70
	for _, bad := range []string{"so-999999", "al-999999", "ar-0000000000000000", "e1"} {
		rec = e.rest(t, "getSong", "id="+bad+"&f=json")
		sr = decode(t, rec)
		if sr["status"] != "failed" || subMap(t, sr, "error")["code"].(float64) != 70 {
			t.Fatalf("getSong(%q) must be 70: %s", bad, rec.Body.String())
		}
	}
}

func TestGetAlbumList2(t *testing.T) {
	e := newEnv(t)
	wywh, animals, kob, wall := seedMusic(t, e)
	shine := e.editionID(t, wywh, 1)
	soWhat := e.editionID(t, kob, 1)
	freddie := e.editionID(t, kob, 2)
	_ = animals
	_ = wall

	names := func(rec *httptest.ResponseRecorder) []string {
		sr := decode(t, rec)
		list := subMap(t, sr, "albumList2")["album"].([]any)
		out := make([]string, 0, len(list))
		for _, a := range list {
			out = append(out, a.(map[string]any)["name"].(string))
		}
		return out
	}

	if got := names(e.rest(t, "getAlbumList2", "type=newest&f=json")); strings.Join(got, "|") != "The Wall|Wish You Were Here|Kind of Blue|Animals" {
		t.Fatalf("newest = %v", got)
	}

	e.seedProgress(t, shine, 5000)
	e.seedProgress(t, soWhat, 6000)
	e.seedProgress(t, freddie, 7000)

	if got := names(e.rest(t, "getAlbumList2", "type=recent&f=json")); strings.Join(got, "|") != "Kind of Blue|Wish You Were Here" {
		t.Fatalf("recent = %v", got)
	}
	if got := names(e.rest(t, "getAlbumList2", "type=frequent&f=json")); strings.Join(got, "|") != "Kind of Blue|Wish You Were Here" {
		t.Fatalf("frequent = %v", got)
	}

	got := names(e.rest(t, "getAlbumList2", "type=random&size=10&f=json"))
	if len(got) != 4 {
		t.Fatalf("random returned %d: %v", len(got), got)
	}
	seen := map[string]bool{}
	for _, n := range got {
		seen[n] = true
	}
	if len(seen) != 4 {
		t.Fatalf("random dupes: %v", got)
	}

	// size/offset paging
	if got := names(e.rest(t, "getAlbumList2", "type=newest&size=2&offset=0&f=json")); strings.Join(got, "|") != "The Wall|Wish You Were Here" {
		t.Fatalf("page 1 = %v", got)
	}
	if got := names(e.rest(t, "getAlbumList2", "type=newest&size=2&offset=2&f=json")); strings.Join(got, "|") != "Kind of Blue|Animals" {
		t.Fatalf("page 2 = %v", got)
	}

	// rating/genre types: honest empty list; garbage type: error 10
	if got := names(e.rest(t, "getAlbumList2", "type=starred&f=json")); len(got) != 0 {
		t.Fatalf("starred = %v", got)
	}
	sr := decode(t, e.rest(t, "getAlbumList2", "type=bogus&f=json"))
	if subMap(t, sr, "error")["code"].(float64) != 10 {
		t.Fatalf("bogus type must be 10: %v", sr)
	}
}

func TestSearch3(t *testing.T) {
	e := newEnv(t)
	seedMusic(t, e)

	rec := e.rest(t, "search3", "query=pink&f=json")
	sr := subMap(t, decode(t, rec), "searchResult3")
	artists := sr["artist"].([]any)
	if len(artists) != 1 || artists[0].(map[string]any)["name"] != "Pink Floyd" {
		t.Fatalf("artists = %v", artists)
	}
	if albums := sr["album"].([]any); len(albums) != 3 {
		t.Fatalf("albums = %v", albums)
	}
	if songs := sr["song"].([]any); len(songs) != 0 {
		t.Fatalf("songs = %v", songs)
	}

	rec = e.rest(t, "search3", "query=machine&f=json")
	sr = subMap(t, decode(t, rec), "searchResult3")
	songs := sr["song"].([]any)
	if len(songs) != 1 || songs[0].(map[string]any)["title"] != "Welcome to the Machine" {
		t.Fatalf("songs = %v", songs)
	}
	if artists := sr["artist"].([]any); len(artists) != 0 {
		t.Fatalf("artists = %v", artists)
	}

	rec = e.rest(t, "search3", "query=the&songCount=1&f=json")
	sr = subMap(t, decode(t, rec), "searchResult3")
	if songs := sr["song"].([]any); len(songs) != 1 {
		t.Fatalf("songCount=1 returned %d", len(songs))
	}

	rec = e.rest(t, "search3", "f=json")
	if subMap(t, decode(t, rec), "error")["code"].(float64) != 10 {
		t.Fatal("missing query must be 10")
	}
}

func TestScrobbleWritesProgress(t *testing.T) {
	e := newEnv(t)
	wywh, _, _, _ := seedMusic(t, e)
	shine := e.editionID(t, wywh, 1)

	rec := e.rest(t, "scrobble", "id=so-"+fmt.Sprint(shine)+"&submission=true&f=json")
	if decode(t, rec)["status"] != "ok" {
		t.Fatalf("scrobble failed: %s", rec.Body.String())
	}
	p, err := e.db.GetProgress(e.user, shine)
	if err != nil {
		t.Fatalf("progress missing after scrobble: %v", err)
	}
	if !p.IsFinished || p.EditionPositionSecs != 810 {
		t.Fatalf("submission=true progress = %+v", p)
	}
	if p.Device == nil || *p.Device != "subsonic" {
		t.Fatalf("device = %v", p.Device)
	}

	// now-playing submission resets position, POST variant
	rec = e.post(t, "/rest/scrobble.view?"+plainAuth+"&id=so-"+fmt.Sprint(shine)+"&submission=false&f=json")
	if decode(t, rec)["status"] != "ok" {
		t.Fatalf("scrobble POST failed: %s", rec.Body.String())
	}
	p, _ = e.db.GetProgress(e.user, shine)
	if p.IsFinished || p.EditionPositionSecs != 0 {
		t.Fatalf("submission=false progress = %+v", p)
	}

	// default submission is true
	rec = e.rest(t, "scrobble", "id=so-"+fmt.Sprint(shine)+"&f=json")
	p, _ = e.db.GetProgress(e.user, shine)
	if !p.IsFinished {
		t.Fatalf("default submission should mark finished: %+v", p)
	}

	// unknown song: error 70, HTTP 200
	rec = e.rest(t, "scrobble", "id=so-999999&f=json")
	if subMap(t, decode(t, rec), "error")["code"].(float64) != 70 {
		t.Fatal("unknown scrobble id must be 70")
	}
}

func TestDualFormatEnvelope(t *testing.T) {
	e := newEnv(t)
	recJSON := e.rest(t, "getPlaylists", "f=json")
	if ct := recJSON.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("json content-type %q", ct)
	}
	sr := decode(t, recJSON)
	playlists := subMap(t, sr, "playlists")
	if list, ok := playlists["playlist"].([]any); !ok || len(list) != 0 {
		t.Fatalf("playlist = %v", playlists["playlist"])
	}

	recXML := e.rest(t, "getPlaylists", "")
	if ct := recXML.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/xml") {
		t.Fatalf("xml content-type %q", ct)
	}
	if !strings.Contains(recXML.Body.String(), "<playlists") {
		t.Fatalf("xml body missing playlists: %s", recXML.Body.String())
	}
	var xr struct {
		XMLName   xml.Name `xml:"subsonic-response"`
		Status    string   `xml:"status,attr"`
		Playlists struct {
			Playlist []struct {
				ID string `xml:"id,attr"`
			} `xml:"playlist"`
		} `xml:"playlists"`
	}
	if err := xml.Unmarshal(recXML.Body.Bytes(), &xr); err != nil {
		t.Fatalf("xml not well-formed: %v", err)
	}
	if xr.Status != "ok" || xr.Playlists.Playlist != nil {
		t.Fatalf("xml playlists = %+v", xr)
	}

	// f=xml is explicit XML too
	recExplicit := e.rest(t, "getPlaylists", "f=xml")
	if !strings.HasPrefix(recExplicit.Header().Get("Content-Type"), "text/xml") {
		t.Fatal("f=xml must be XML")
	}
}

func TestPlaylistMissingIDReturns70(t *testing.T) {
	e := newEnv(t)
	for _, ep := range []string{"updatePlaylist", "deletePlaylist"} {
		rec := e.rest(t, ep, "name=x&f=json")
		if rec.Code != 200 {
			t.Fatalf("%s: HTTP %d", ep, rec.Code)
		}
		sr := decode(t, rec)
		if sr["status"] != "failed" {
			t.Fatalf("%s: status %v", ep, sr["status"])
		}
		if code := subMap(t, sr, "error")["code"].(float64); code != 70 {
			t.Fatalf("%s: code %v, want 70 (missing id)", ep, code)
		}
	}
}
