package store_test

import (
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func plUser(t *testing.T, db *store.DB, name string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?,?,0,0,0)`, name, "h")
	if err != nil {
		t.Fatalf("seed user %s: %v", name, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func plEdition(t *testing.T, db *store.DB, title string, dur float64) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('L','music','/x',0)`)
	if err != nil {
		t.Fatalf("seed library: %v", err)
	}
	libID, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO works (library_id, title, author, created_at, updated_at) VALUES (?,?,?,0,0)`, libID, "Album "+title, "Artist", 0)
	if err != nil {
		t.Fatalf("seed work: %v", err)
	}
	workID, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, position, created_at) VALUES (?,?,?,?,1,0)`, workID, "mp3", title, dur)
	if err != nil {
		t.Fatalf("seed edition: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func plPositions(t *testing.T, db *store.DB, playlistID int64) []int64 {
	t.Helper()
	items, err := db.PlaylistItems(playlistID)
	if err != nil {
		t.Fatalf("PlaylistItems: %v", err)
	}
	out := make([]int64, 0, len(items))
	for _, it := range items {
		out = append(out, it.EditionID)
	}
	return out
}

func ids(vals ...int64) []int64 { return vals }

func eq(t *testing.T, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestPlaylistStoreCRUD(t *testing.T) {
	db := openTestDB(t)
	u1 := plUser(t, db, "alice")
	u2 := plUser(t, db, "bob")
	e1 := plEdition(t, db, "One", 60)
	e2 := plEdition(t, db, "Two", 70.5)
	e3 := plEdition(t, db, "Three", 0)

	plID, err := db.CreatePlaylist(u1, "Road")
	if err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	for _, eid := range ids(e1, e2, e3) {
		added, err := db.AddPlaylistItem(plID, eid)
		if err != nil {
			t.Fatalf("AddPlaylistItem(%d): %v", eid, err)
		}
		if !added {
			t.Fatalf("AddPlaylistItem(%d) must report added=true on insert", eid)
		}
	}

	p, items, err := db.PlaylistDetail(plID)
	if err != nil {
		t.Fatalf("PlaylistDetail: %v", err)
	}
	if p.Name != "Road" || p.UserID != u1 || p.Owner != "alice" || p.SongCount != 3 || p.DurationSecs != 130.5 {
		t.Fatalf("playlist row = %+v", p)
	}
	if len(items) != 3 {
		t.Fatalf("items = %+v", items)
	}
	for i, it := range items {
		if it.Position != int64(i+1) {
			t.Fatalf("item %d position = %d", i, it.Position)
		}
		if it.Title != items2title(i) || it.Format != "mp3" || it.WorkID <= 0 || it.WorkTitle != "Album "+items2title(i) || it.WorkAuthor == nil || *it.WorkAuthor != "Artist" {
			t.Fatalf("item %d join fields = %+v", i, it)
		}
	}
	if items[2].DurationSecs != 0 {
		t.Fatalf("nil duration must be 0, got %v", items[2].DurationSecs)
	}

	// duplicate add is a no-op reporting added=false
	added, err := db.AddPlaylistItem(plID, e2)
	if err != nil {
		t.Fatalf("duplicate AddPlaylistItem must not error: %v", err)
	}
	if added {
		t.Fatal("duplicate AddPlaylistItem must report added=false")
	}
	if got := plPositions(t, db, plID); len(got) != 3 {
		t.Fatalf("duplicate add changed items: %v", got)
	}

	// remove + compaction
	if err := db.RemovePlaylistItem(plID, e1); err != nil {
		t.Fatalf("RemovePlaylistItem: %v", err)
	}
	items, err = db.PlaylistItems(plID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Position != 1 || items[1].Position != 2 {
		t.Fatalf("compaction after remove: %+v", items)
	}

	// reorder: move e3 (currently pos 2) to front
	if err := db.ReorderPlaylistItem(plID, e3, 1); err != nil {
		t.Fatalf("ReorderPlaylistItem: %v", err)
	}
	eq(t, plPositions(t, db, plID), ids(e3, e2))

	// clamp: 99 past the end, 0 before the start
	if err := db.ReorderPlaylistItem(plID, e3, 99); err != nil {
		t.Fatal(err)
	}
	eq(t, plPositions(t, db, plID), ids(e2, e3))
	if err := db.ReorderPlaylistItem(plID, e2, 0); err != nil {
		t.Fatal(err)
	}
	eq(t, plPositions(t, db, plID), ids(e2, e3))

	// rename touches updated_at
	if err := db.RenamePlaylist(plID, "Highway"); err != nil {
		t.Fatalf("RenamePlaylist: %v", err)
	}
	p, err = db.Playlist(plID)
	if err != nil || p.Name != "Highway" {
		t.Fatalf("after rename: %+v %v", p, err)
	}
	if p.UpdatedAt < p.CreatedAt {
		t.Fatalf("rename must touch updated_at: %+v", p)
	}

	// v1 scoping: own-only per user, 0 = all
	pl2, err := db.CreatePlaylist(u2, "Bobs")
	if err != nil {
		t.Fatal(err)
	}
	mine, err := db.ListPlaylists(u1)
	if err != nil || len(mine) != 1 || mine[0].ID != plID {
		t.Fatalf("ListPlaylists(u1) = %+v %v", mine, err)
	}
	bobs, err := db.ListPlaylists(u2)
	if err != nil || len(bobs) != 1 || bobs[0].ID != pl2 || bobs[0].Owner != "bob" {
		t.Fatalf("ListPlaylists(u2) = %+v %v", bobs, err)
	}
	all, err := db.ListPlaylists(0)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListPlaylists(0) = %+v %v", all, err)
	}

	// delete playlist cascades items
	if err := db.DeletePlaylist(plID); err != nil {
		t.Fatalf("DeletePlaylist: %v", err)
	}
	if _, err := db.Playlist(plID); err != store.ErrNotFound {
		t.Fatalf("deleted playlist must be ErrNotFound, got %v", err)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM playlist_items WHERE playlist_id = ?`, plID).Scan(&n)
	if n != 0 {
		t.Fatalf("items survived playlist delete: %d", n)
	}
}

func items2title(i int) string {
	return []string{"One", "Two", "Three"}[i]
}

func TestPlaylistStoreCascades(t *testing.T) {
	db := openTestDB(t)
	u := plUser(t, db, "carol")
	e := plEdition(t, db, "Solo", 10)
	plID, err := db.CreatePlaylist(u, "Gone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddPlaylistItem(plID, e); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteUser(u); err != nil {
		t.Fatalf("DeleteUser with playlists must cascade: %v", err)
	}
	if _, err := db.Playlist(plID); err != store.ErrNotFound {
		t.Fatalf("playlist survived user delete: %v", err)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM playlist_items WHERE playlist_id = ?`, plID).Scan(&n)
	if n != 0 {
		t.Fatalf("items survived user delete: %d", n)
	}
}

func TestPlaylistStoreErrors(t *testing.T) {
	db := openTestDB(t)
	u := plUser(t, db, "dave")
	e := plEdition(t, db, "Kept", 5)
	plID, _ := db.CreatePlaylist(u, "X")

	if _, err := db.AddPlaylistItem(plID, 999999); err != store.ErrNotFound {
		t.Fatalf("unknown edition must be ErrNotFound, got %v", err)
	}
	if err := db.RemovePlaylistItem(plID, e); err != store.ErrNotFound {
		t.Fatalf("absent item remove must be ErrNotFound, got %v", err)
	}
	if err := db.RemovePlaylistItem(999999, e); err != store.ErrNotFound {
		t.Fatalf("absent playlist remove must be ErrNotFound, got %v", err)
	}
	if err := db.ReorderPlaylistItem(plID, e, 1); err != store.ErrNotFound {
		t.Fatalf("absent item reorder must be ErrNotFound, got %v", err)
	}
	if err := db.RenamePlaylist(999999, "Y"); err != store.ErrNotFound {
		t.Fatalf("absent playlist rename must be ErrNotFound, got %v", err)
	}
	if err := db.DeletePlaylist(999999); err != store.ErrNotFound {
		t.Fatalf("absent playlist delete must be ErrNotFound, got %v", err)
	}

	// clear keeps the row
	if _, err := db.AddPlaylistItem(plID, e); err != nil {
		t.Fatal(err)
	}
	if err := db.ClearPlaylistItems(plID); err != nil {
		t.Fatal(err)
	}
	p, err := db.Playlist(plID)
	if err != nil || p.SongCount != 0 || p.Name != "X" {
		t.Fatalf("after clear: %+v %v", p, err)
	}
}
