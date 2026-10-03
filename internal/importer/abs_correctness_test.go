package importer

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func editABSFixture(t *testing.T, root string, statements ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "abs_database.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func TestABSPodcastMetadataFallback(t *testing.T) {
	for _, statement := range []string{"DROP TABLE podcasts", "ALTER TABLE podcasts RENAME COLUMN itunesAuthor TO author"} {
		t.Run(statement, func(t *testing.T) {
			root := buildABSFixture(t)
			editABSFixture(t, root, statement)
			plan, err := ABS(root, openStore(t), true)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Works != 2 || plan.Editions != 2 {
				t.Fatalf("import without podcast author metadata = %+v", plan)
			}
		})
	}
}

func TestABSPodcastUsesMediaID(t *testing.T) {
	root := buildABSFixture(t)
	editABSFixture(t, root,
		"UPDATE podcasts SET id = 42 WHERE id = 2",
		"UPDATE libraryItems SET mediaId = 42 WHERE mediaType = 'podcast'",
		"UPDATE podcastEpisodes SET podcastId = 42")
	db := openStore(t)
	plan, err := ABS(root, db, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Works != 2 || plan.Editions != 2 || plan.ProgressRows != 3 {
		t.Fatalf("podcast episode lost when library item and media IDs differ: %+v", plan)
	}
}

func TestABSProgressDurationSeparatesMediaTypes(t *testing.T) {
	root := buildABSFixture(t)
	editABSFixture(t, root,
		"UPDATE podcastEpisodes SET id = 1 WHERE id = 7",
		"UPDATE mediaProgress SET mediaItemId = 1 WHERE mediaItemType = 'podcastEpisode'")
	db := openStore(t)
	if _, err := ABS(root, db, false); err != nil {
		t.Fatal(err)
	}
	for title, want := range map[string]float64{"Book One": 3600, "Ep One": 1800} {
		var got float64
		if err := db.QueryRow(`SELECT p.duration_secs FROM progress p JOIN editions e ON e.id = p.edition_id JOIN users u ON u.id = p.user_id WHERE e.title = ? AND u.name = 'tyler'`, title).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s imported duration = %v, want %v", title, got, want)
		}
	}
}

func TestABSUnnumberedPodcastEpisodesStayDistinct(t *testing.T) {
	for _, number := range []string{"NULL", "0"} {
		t.Run(number, func(t *testing.T) {
			root := buildABSFixture(t)
			editABSFixture(t, root,
				"UPDATE podcastEpisodes SET season = "+number+", episode = "+number,
				"INSERT INTO podcastEpisodes SELECT 8, podcastId, season, episode, 'Another Episode', duration, replace(audioFile, 'ep1.mp3', 'ep2.mp3') FROM podcastEpisodes WHERE id = 7")
			first := filepath.Join(filepath.Dir(root), "media", "ep1.mp3")
			second := filepath.Join(filepath.Dir(root), "media", "ep2.mp3")
			data, err := os.ReadFile(first)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(second, data, 0o644); err != nil {
				t.Fatal(err)
			}
			db := openStore(t)
			if _, err := ABS(root, db, false); err != nil {
				t.Fatal(err)
			}
			if got := countRows(t, db, `SELECT count(*) FROM editions e JOIN works w ON w.id = e.work_id WHERE w.title = 'The Cast'`); got != 2 {
				t.Fatalf("unnumbered podcast editions = %d, want 2", got)
			}
		})
	}
}
