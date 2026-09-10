package subsonic

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
)

func (e *env) addUser(t *testing.T, name, pass string) {
	t.Helper()
	now := time.Now().UnixMilli()
	if _, err := e.db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?,?,0,?,?)`,
		name, auth.Hash(pass), now, now); err != nil {
		t.Fatalf("seed user %s: %v", name, err)
	}
}

func (e *env) restAs(t *testing.T, user, pass, endpoint, extra string) *httptest.ResponseRecorder {
	t.Helper()
	return e.get(t, "/rest/"+endpoint+".view?u="+user+"&p="+pass+"&"+extra)
}

func plError(t *testing.T, rec *httptest.ResponseRecorder) float64 {
	t.Helper()
	sr := decode(t, rec)
	if sr["status"] != "failed" {
		t.Fatalf("expected failed status: %s", rec.Body.String())
	}
	return subMap(t, sr, "error")["code"].(float64)
}

func plCreate(t *testing.T, e *env, name string, songIDs ...string) map[string]any {
	t.Helper()
	q := "name=" + name
	for _, id := range songIDs {
		q += "&songId=" + id
	}
	rec := e.rest(t, "createPlaylist", q+"&f=json")
	sr := decode(t, rec)
	if sr["status"] != "ok" {
		t.Fatalf("createPlaylist failed: %s", rec.Body.String())
	}
	return subMap(t, sr, "playlist")
}

func plEntryIDs(t *testing.T, pl map[string]any) []string {
	t.Helper()
	entries, ok := pl["entry"].([]any)
	if !ok {
		t.Fatalf("playlist has no entry array: %v", pl)
	}
	out := make([]string, 0, len(entries))
	for _, en := range entries {
		out = append(out, en.(map[string]any)["id"].(string))
	}
	return out
}

func TestPlaylistCreateAndList(t *testing.T) {
	e := newEnv(t)
	wywh, _, _, _ := seedMusic(t, e)
	shine := e.editionID(t, wywh, 1)
	machine := e.editionID(t, wywh, 2)

	pl := plCreate(t, e, "Road", "so-"+fmt.Sprint(shine), "so-"+fmt.Sprint(machine))
	want := map[string]any{
		"id": "pl-1", "name": "Road", "songCount": float64(2), "duration": float64(810 + 462),
		"owner": "tyler",
	}
	for k, v := range want {
		if pl[k] != v {
			t.Fatalf("playlist[%q] = %v, want %v", k, pl[k], v)
		}
	}
	if !strings.HasSuffix(pl["created"].(string), "Z") || !strings.HasSuffix(pl["changed"].(string), "Z") {
		t.Fatalf("created/changed not ISO: %v %v", pl["created"], pl["changed"])
	}
	if len(pl) != 8 { // id, name, songCount, duration, owner, created, changed, entry
		t.Fatalf("playlist key set = %v", pl)
	}
	got := plEntryIDs(t, pl)
	if got[0] != "so-"+fmt.Sprint(shine) || got[1] != "so-"+fmt.Sprint(machine) {
		t.Fatalf("entry order = %v", got)
	}

	// getPlaylists mirrors the same shape
	rec := e.rest(t, "getPlaylists", "f=json")
	list := subMap(t, decode(t, rec), "playlists")["playlist"].([]any)
	if len(list) != 1 {
		t.Fatalf("getPlaylists = %v", list)
	}
	row := list[0].(map[string]any)
	for k, v := range want {
		if row[k] != v {
			t.Fatalf("getPlaylists row[%q] = %v, want %v", k, row[k], v)
		}
	}
	if row["created"] != pl["created"] {
		t.Fatalf("list vs detail created mismatch: %v vs %v", row["created"], pl["created"])
	}

	// createPlaylist without a name: error 10
	rec = e.rest(t, "createPlaylist", "f=json")
	if code := plError(t, rec); code != 10 {
		t.Fatalf("missing name = %v, want 10", code)
	}
	// unknown song id: error 70
	rec = e.rest(t, "createPlaylist", "name=X&songId=so-999999&f=json")
	if code := plError(t, rec); code != 70 {
		t.Fatalf("unknown song = %v, want 70", code)
	}
}

func TestPlaylistGetChildrenMatchAlbumShape(t *testing.T) {
	e := newEnv(t)
	wywh, _, _, _ := seedMusic(t, e)
	e.addCover(t, wywh)
	shine := e.editionID(t, wywh, 1)
	machine := e.editionID(t, wywh, 2)

	// getAlbum children are the reference shape
	rec := e.rest(t, "getAlbum", "id=al-"+fmt.Sprint(wywh)+"&f=json")
	albumSongs := subMap(t, decode(t, rec), "album")["song"].([]any)

	plCreate(t, e, "Road", "so-"+fmt.Sprint(shine), "so-"+fmt.Sprint(machine))
	rec = e.rest(t, "getPlaylist", "id=pl-1&f=json")
	pl := subMap(t, decode(t, rec), "playlist")
	if pl["songCount"].(float64) != 2 || pl["duration"].(float64) != 810+462 {
		t.Fatalf("playlist header = %v", pl)
	}
	entries := pl["entry"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %v", entries)
	}
	for i := range entries {
		got := entries[i].(map[string]any)
		want := albumSongs[i].(map[string]any)
		if len(got) != len(want) {
			t.Fatalf("entry %d key set = %v, want %v", i, got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("entry %d [%q] = %v, want %v (album child %v)", i, k, got[k], v, want)
			}
		}
	}

	// XML renders entry children
	rec = e.rest(t, "getPlaylist", "id=pl-1")
	if !strings.Contains(rec.Body.String(), "<entry ") {
		t.Fatalf("xml missing entry children: %s", rec.Body.String())
	}

	// malformed and unknown ids: error 70
	for _, bad := range []string{"pl-x", "pl-999999", "so-1", "1"} {
		rec = e.rest(t, "getPlaylist", "id="+bad+"&f=json")
		if code := plError(t, rec); code != 70 {
			t.Fatalf("getPlaylist(%q) = %v, want 70", bad, code)
		}
	}
}

func TestPlaylistUpdateAddRemoveByIndex(t *testing.T) {
	e := newEnv(t)
	wywh, animals, kob, _ := seedMusic(t, e)
	shine := e.editionID(t, wywh, 1)
	machine := e.editionID(t, wywh, 2)
	dogs := e.editionID(t, animals, 1)
	soWhat := e.editionID(t, kob, 1)

	plCreate(t, e, "Mix",
		"so-"+fmt.Sprint(shine), "so-"+fmt.Sprint(machine), "so-"+fmt.Sprint(soWhat))

	// remove indexes 0 (shine) and 2 (soWhat) against the snapshot, add dogs
	rec := e.rest(t, "updatePlaylist",
		"playlistId=pl-1&songIndexToRemove=0&songIndexToRemove=2&songId=so-"+fmt.Sprint(dogs)+"&f=json")
	if decode(t, rec)["status"] != "ok" {
		t.Fatalf("updatePlaylist failed: %s", rec.Body.String())
	}
	rec = e.rest(t, "getPlaylist", "id=pl-1&f=json")
	pl := subMap(t, decode(t, rec), "playlist")
	got := plEntryIDs(t, pl)
	want := []string{"so-" + fmt.Sprint(machine), "so-" + fmt.Sprint(dogs)}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("entries after update = %v, want %v", got, want)
	}
	if pl["songCount"].(float64) != 2 || pl["duration"].(float64) != 462+1021 {
		t.Fatalf("stats after update = %v", pl)
	}

	// rename via updatePlaylist
	e.rest(t, "updatePlaylist", "playlistId=pl-1&name=Remixed&f=json")
	rec = e.rest(t, "getPlaylists", "f=json")
	row := subMap(t, decode(t, rec), "playlists")["playlist"].([]any)[0].(map[string]any)
	if row["name"] != "Remixed" {
		t.Fatalf("rename via updatePlaylist failed: %v", row)
	}

	// duplicate add is a no-op
	e.rest(t, "updatePlaylist", "playlistId=pl-1&songId=so-"+fmt.Sprint(dogs)+"&f=json")
	rec = e.rest(t, "getPlaylist", "id=pl-1&f=json")
	if pl := subMap(t, decode(t, rec), "playlist"); pl["songCount"].(float64) != 2 {
		t.Fatalf("duplicate add changed count: %v", pl)
	}

	// out-of-range removal index is skipped, not an error
	rec = e.rest(t, "updatePlaylist", "playlistId=pl-1&songIndexToRemove=99&f=json")
	if decode(t, rec)["status"] != "ok" {
		t.Fatalf("out-of-range index must not fail: %s", rec.Body.String())
	}

	// unknown added song: error 70, nothing appended
	rec = e.rest(t, "updatePlaylist", "playlistId=pl-1&songId=so-999999&f=json")
	if code := plError(t, rec); code != 70 {
		t.Fatal("unknown song in update must be 70")
	}
}

// createPlaylist with playlistId replaces content per spec.
func TestPlaylistCreateOverwrites(t *testing.T) {
	e := newEnv(t)
	wywh, animals, _, _ := seedMusic(t, e)
	shine := e.editionID(t, wywh, 1)
	machine := e.editionID(t, wywh, 2)
	dogs := e.editionID(t, animals, 1)

	plCreate(t, e, "Old", "so-"+fmt.Sprint(shine), "so-"+fmt.Sprint(machine))
	rec := e.rest(t, "createPlaylist", "playlistId=pl-1&name=New&songId=so-"+fmt.Sprint(dogs)+"&f=json")
	sr := decode(t, rec)
	if sr["status"] != "ok" {
		t.Fatalf("overwrite failed: %s", rec.Body.String())
	}
	pl := subMap(t, sr, "playlist")
	if pl["name"] != "New" || pl["songCount"].(float64) != 1 {
		t.Fatalf("overwritten playlist = %v", pl)
	}
	if got := plEntryIDs(t, pl); got[0] != "so-"+fmt.Sprint(dogs) {
		t.Fatalf("entries after overwrite = %v", got)
	}
}

func TestPlaylistDelete(t *testing.T) {
	e := newEnv(t)
	wywh, _, _, _ := seedMusic(t, e)
	shine := e.editionID(t, wywh, 1)

	plCreate(t, e, "Temp", "so-"+fmt.Sprint(shine))
	rec := e.rest(t, "deletePlaylist", "id=pl-1&f=json")
	if decode(t, rec)["status"] != "ok" {
		t.Fatalf("deletePlaylist failed: %s", rec.Body.String())
	}
	rec = e.rest(t, "getPlaylists", "f=json")
	if list := subMap(t, decode(t, rec), "playlists")["playlist"].([]any); len(list) != 0 {
		t.Fatalf("playlists after delete = %v", list)
	}
	if code := plError(t, e.rest(t, "getPlaylist", "id=pl-1&f=json")); code != 70 {
		t.Fatal("deleted playlist must be 70")
	}
	if code := plError(t, e.rest(t, "deletePlaylist", "id=pl-1&f=json")); code != 70 {
		t.Fatal("re-delete must be 70")
	}
}

func TestPlaylistCrossUserIsolation(t *testing.T) {
	e := newEnv(t)
	wywh, _, _, _ := seedMusic(t, e)
	shine := e.editionID(t, wywh, 1)
	machine := e.editionID(t, wywh, 2)
	e.addUser(t, "bob", "bobsecret")

	plCreate(t, e, "Tylers", "so-"+fmt.Sprint(shine))

	// bob cannot see, mutate, or delete tyler's playlist: all 70
	if code := plError(t, e.restAs(t, "bob", "bobsecret", "getPlaylist", "id=pl-1&f=json")); code != 70 {
		t.Fatalf("bob getPlaylist = %v, want 70", code)
	}
	if code := plError(t, e.restAs(t, "bob", "bobsecret", "updatePlaylist", "playlistId=pl-1&name=Hacked&f=json")); code != 70 {
		t.Fatal("bob updatePlaylist must be 70")
	}
	if code := plError(t, e.restAs(t, "bob", "bobsecret", "deletePlaylist", "id=pl-1&f=json")); code != 70 {
		t.Fatal("bob deletePlaylist must be 70")
	}
	if code := plError(t, e.restAs(t, "bob", "bobsecret", "createPlaylist", "playlistId=pl-1&songId=so-"+fmt.Sprint(machine)+"&f=json")); code != 70 {
		t.Fatal("bob overwrite must be 70")
	}

	// bob's own list is empty; tyler's is untouched
	rec := e.restAs(t, "bob", "bobsecret", "getPlaylists", "f=json")
	if list := subMap(t, decode(t, rec), "playlists")["playlist"].([]any); len(list) != 0 {
		t.Fatalf("bob sees foreign playlists: %v", list)
	}
	rec = e.rest(t, "getPlaylists", "f=json")
	if list := subMap(t, decode(t, rec), "playlists")["playlist"].([]any); len(list) != 1 {
		t.Fatalf("tyler's list changed: %v", list)
	}

	// bob can create and see his own
	rec = e.restAs(t, "bob", "bobsecret", "createPlaylist", "name=Bobs&f=json")
	if decode(t, rec)["status"] != "ok" {
		t.Fatalf("bob create failed: %s", rec.Body.String())
	}
	rec = e.restAs(t, "bob", "bobsecret", "getPlaylists", "f=json")
	list := subMap(t, decode(t, rec), "playlists")["playlist"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["owner"] != "bob" {
		t.Fatalf("bob's own list = %v", list)
	}
}
