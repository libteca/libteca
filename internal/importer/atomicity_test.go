package importer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

type atomicImportFixture struct {
	source string
	run    func(*store.DB, bool) (*Plan, error)
	stages []atomicImportStage
	grow   func(*testing.T)
}

type atomicImportStage struct {
	name      string
	table     string
	condition string
}

func importExec(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func sourceExec(t *testing.T, path string, fn func(*sql.DB)) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fn(db)
}

func newAtomicImportFixture(t *testing.T, format string) atomicImportFixture {
	t.Helper()
	stages := []atomicImportStage{
		{"users", "users", `NEW.name = 'rollback-last-user'`},
		{"libraries", "libraries", `NEW.name = 'Rollback Last Library'`},
		{"works", "works", `1`},
		{"editions", "editions", `1`},
		{"files", "files", `1`},
		{"progress", "progress", `NEW.edition_id = (SELECT max(id) FROM editions)`},
	}
	if format == "abs" {
		dir := buildABSFixture(t)
		source := filepath.Join(dir, "abs_database.db")
		stages[2].condition = `NEW.title = 'The Cast'`
		stages[3].condition = `NEW.title = 'Ep One'`
		stages[4].condition = `NEW.path LIKE '%/ep1.mp3'`
		stages = append(stages,
			atomicImportStage{"reading-progress", "progress", `NEW.user_id = (SELECT id FROM users WHERE name = 'guest')`},
			atomicImportStage{"playlists", "playlists", `NEW.name = 'Rollback Playlist'`},
			atomicImportStage{"playlist-items", "playlist_items", `NEW.playlist_id = (SELECT id FROM playlists WHERE name = 'Rollback Playlist')`},
			atomicImportStage{"playlist-timestamp", "playlists", `NEW.name = 'Faves'`},
		)
		return atomicImportFixture{
			source: source,
			run:    func(db *store.DB, dry bool) (*Plan, error) { return ABS(dir, db, dry) },
			stages: stages,
			grow: func(t *testing.T) {
				sourceExec(t, source, func(db *sql.DB) {
					importExec(t, db, `INSERT INTO users VALUES (10, 'rollback-first-user', '', 'user'), (11, 'rollback-last-user', '', 'user')`)
					importExec(t, db, `INSERT INTO libraries VALUES (10, 'Rollback First Library', 'book'), (11, 'Rollback Last Library', 'book')`)
					importExec(t, db, `INSERT INTO playlists VALUES (10, 1, 'Rollback Playlist')`)
					importExec(t, db, `INSERT INTO playlistMediaItems VALUES (10, 10, 1, 'book')`)
				})
			},
		}
	}
	path := buildKavitaFixture(t)
	media := filepath.Join(filepath.Dir(path), "Saga 002.cbz")
	if err := os.WriteFile(media, []byte("second comic"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceExec(t, path, func(db *sql.DB) {
		importExec(t, db, `INSERT INTO Chapter VALUES (12, 9, 2, '', '', 30)`)
		importExec(t, db, `INSERT INTO MangaFile VALUES (2, 12, ?, 1)`, media)
		importExec(t, db, `INSERT INTO AppUserProgresses VALUES (2, 'u-1', 12, 20)`)
	})
	stages[3].condition = `NEW.title = 'Chapter 2'`
	stages[4].condition = `NEW.path LIKE '%/Saga 002.cbz'`
	return atomicImportFixture{
		source: path,
		run:    func(db *store.DB, dry bool) (*Plan, error) { return Kavita(path, db, dry) },
		stages: stages,
		grow: func(t *testing.T) {
			sourceExec(t, path, func(db *sql.DB) {
				importExec(t, db, `INSERT INTO AspNetUsers VALUES ('u-10', 'rollback-first-user', ''), ('u-11', 'rollback-last-user', '')`)
				importExec(t, db, `INSERT INTO Library VALUES (10, 'Rollback First Library', 2), (11, 'Rollback Last Library', 2)`)
			})
		},
	}
}

var atomicImportTables = []string{"users", "libraries", "works", "editions", "files", "progress", "playlists", "playlist_items"}

func importSnapshot(t *testing.T, db *sql.DB, tables []string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY 1, 2`)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		var values [][]any
		for rows.Next() {
			row := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range row {
				dest[i] = &row[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			values = append(values, row)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		data, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		out[table] = string(data)
	}
	return out
}

func assertImportSnapshot(t *testing.T, before, after map[string]string) {
	t.Helper()
	for _, table := range atomicImportTables {
		if before[table] != after[table] {
			t.Errorf("failed import changed destination table %s", table)
		}
	}
}

func preserveImportSentinels(t *testing.T, db *store.DB) {
	t.Helper()
	importExec(t, db, `UPDATE works SET subtitle = 'manual subtitle', description = 'manual description', updated_at = 17`)
	importExec(t, db, `UPDATE editions SET duration_secs = 77, page_count = 99, language = 'manual-language'`)
	importExec(t, db, `UPDATE files SET duration_secs = 88, size_bytes = 321, mtime_secs = 12, mtime_ns = 12345, chapters = '[{"title":"manual"}]', hash = 'manual-hash', probed_at = 19`)
	importExec(t, db, `UPDATE progress SET edition_position_secs = 7, page = 3, percent = 0.25, locator = 'manual-locator', revision = 41, deleted = CASE WHEN id % 2 = 0 THEN 1 ELSE 0 END, updated_at = 23`)
	importExec(t, db, `UPDATE playlists SET updated_at = 29`)
	importExec(t, db, `INSERT INTO playlists (user_id, name, created_at, updated_at) SELECT min(id), 'Unrelated Playlist', 27, 29 FROM users`)
	importExec(t, db, `INSERT INTO playlist_items (playlist_id, edition_id, position, added_at) SELECT p.id, min(e.id), 1, 31 FROM playlists p CROSS JOIN editions e WHERE p.name = 'Unrelated Playlist'`)
	importExec(t, db, `DELETE FROM playlist_items WHERE playlist_id IN (SELECT id FROM playlists WHERE name = 'Faves')`)
}

func installImportFailure(t *testing.T, db *store.DB, stage atomicImportStage) {
	t.Helper()
	for _, event := range []string{"INSERT", "UPDATE"} {
		if stage.name == "playlist-timestamp" && event == "INSERT" {
			continue
		}
		importExec(t, db, fmt.Sprintf(`CREATE TRIGGER import_failure_%s AFTER %s ON %s WHEN %s BEGIN SELECT RAISE(ABORT, 'injected whole-import failure'); END`, event, event, stage.table, stage.condition))
	}
}

func TestWholeImportRollbackAtEveryStage(t *testing.T) {
	for _, format := range []string{"abs", "kavita"} {
		t.Run(format, func(t *testing.T) {
			fixture := newAtomicImportFixture(t, format)
			for _, existing := range []bool{false, true} {
				for _, stage := range fixture.stages {
					t.Run(fmt.Sprintf("existing=%t/%s", existing, stage.name), func(t *testing.T) {
						f := newAtomicImportFixture(t, format)
						db := openStore(t)
						if existing {
							if _, err := f.run(db, false); err != nil {
								t.Fatal(err)
							}
							preserveImportSentinels(t, db)
						}
						f.grow(t)
						before := importSnapshot(t, db.DB, atomicImportTables)
						installImportFailure(t, db, stage)
						plan, err := f.run(db, false)
						if err == nil || !strings.Contains(err.Error(), "injected whole-import failure") {
							t.Fatalf("expected injected failure, got plan=%v, err=%v", plan, err)
						}
						if plan != nil {
							t.Error("failed import returned a successful plan")
						}
						assertImportSnapshot(t, before, importSnapshot(t, db.DB, atomicImportTables))
						importExec(t, db, `DROP TRIGGER IF EXISTS import_failure_INSERT`)
						importExec(t, db, `DROP TRIGGER IF EXISTS import_failure_UPDATE`)
						if _, err := f.run(db, false); err != nil {
							t.Fatalf("retry after rollback: %v", err)
						}
					})
				}
			}
		})
	}
}

func TestWholeImportCommitFailureRollsBack(t *testing.T) {
	for _, format := range []string{"abs", "kavita"} {
		t.Run(format, func(t *testing.T) {
			f := newAtomicImportFixture(t, format)
			db := openStore(t)
			if _, err := f.run(db, false); err != nil {
				t.Fatal(err)
			}
			preserveImportSentinels(t, db)
			f.grow(t)
			before := importSnapshot(t, db.DB, atomicImportTables)
			importExec(t, db, `CREATE TABLE import_commit_failure (user_id INTEGER REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED)`)
			importExec(t, db, `CREATE TRIGGER import_failure AFTER UPDATE ON progress BEGIN INSERT INTO import_commit_failure VALUES (-1); END`)
			plan, err := f.run(db, false)
			if err == nil || plan != nil {
				t.Fatalf("commit failure returned plan=%v, err=%v", plan, err)
			}
			assertImportSnapshot(t, before, importSnapshot(t, db.DB, atomicImportTables))
			if n := countRows(t, db, `SELECT count(*) FROM import_commit_failure`); n != 0 {
				t.Fatalf("commit failure retained %d deferred rows", n)
			}
		})
	}
}

func TestWholeImportDryRunAndRepeatedImport(t *testing.T) {
	for _, format := range []string{"abs", "kavita"} {
		t.Run(format, func(t *testing.T) {
			f := newAtomicImportFixture(t, format)
			db := openStore(t)
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
			before := importSnapshot(t, db.DB, atomicImportTables)
			sourceBefore, err := os.ReadFile(f.source)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.run(db, true); err != nil {
				t.Fatal(err)
			}
			assertImportSnapshot(t, before, importSnapshot(t, db.DB, atomicImportTables))
			if _, err := f.run(db, false); err != nil {
				t.Fatal(err)
			}
			before = importSnapshot(t, db.DB, atomicImportTables)
			if _, err := f.run(db, true); err != nil {
				t.Fatal(err)
			}
			assertImportSnapshot(t, before, importSnapshot(t, db.DB, atomicImportTables))
			identities := map[string]string{}
			for _, table := range atomicImportTables {
				var ids string
				if err := db.QueryRow(`SELECT coalesce(group_concat(rowid), '') FROM (SELECT rowid FROM ` + table + ` ORDER BY rowid)`).Scan(&ids); err != nil {
					t.Fatal(err)
				}
				identities[table] = ids
			}
			plan, err := f.run(db, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, user := range plan.Users {
				if !user.Exists || user.TempPassword != "" {
					t.Errorf("repeat import changed existing-user status for %s", user.Name)
				}
			}
			for _, table := range atomicImportTables {
				var ids string
				if err := db.QueryRow(`SELECT coalesce(group_concat(rowid), '') FROM (SELECT rowid FROM ` + table + ` ORDER BY rowid)`).Scan(&ids); err != nil {
					t.Fatal(err)
				}
				if ids != identities[table] {
					t.Errorf("repeat import changed row identities in %s", table)
				}
			}
			if before["users"] != importSnapshot(t, db.DB, []string{"users"})["users"] {
				t.Error("repeat import altered existing users")
			}
			if n := countRows(t, db, `SELECT count(*) FROM progress WHERE revision != 2`); n != 0 {
				t.Errorf("%d progress rows did not advance exactly one revision", n)
			}
			sourceAfter, err := os.ReadFile(f.source)
			if err != nil {
				t.Fatal(err)
			}
			if string(sourceAfter) != string(sourceBefore) {
				t.Error("import changed the foreign database")
			}
		})
	}
}

func TestWholeImportNonRegularFileRollback(t *testing.T) {
	for _, format := range []string{"abs", "kavita"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", format, existing), func(t *testing.T) {
				f := newAtomicImportFixture(t, format)
				db := openStore(t)
				if existing {
					if _, err := f.run(db, false); err != nil {
						t.Fatal(err)
					}
					preserveImportSentinels(t, db)
				}
				f.grow(t)
				dir := filepath.Join(t.TempDir(), "nonregular.mp3")
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				sourceExec(t, f.source, func(db *sql.DB) {
					if format == "abs" {
						file, err := json.Marshal(map[string]any{"path": dir, "duration": 1800})
						if err != nil {
							t.Fatal(err)
						}
						importExec(t, db, `UPDATE podcastEpisodes SET audioFile = ?`, string(file))
					} else {
						importExec(t, db, `UPDATE MangaFile SET FilePath = ? WHERE ChapterId = 12`, dir)
					}
				})
				before := importSnapshot(t, db.DB, atomicImportTables)
				plan, err := f.run(db, false)
				if err == nil || !strings.Contains(err.Error(), "not a regular media file") || plan != nil {
					t.Fatalf("expected non-regular file failure, got plan=%v, err=%v", plan, err)
				}
				assertImportSnapshot(t, before, importSnapshot(t, db.DB, atomicImportTables))
			})
		}
	}
}

func TestWholeImportConcurrentRepeats(t *testing.T) {
	for _, format := range []string{"abs", "kavita"} {
		t.Run(format, func(t *testing.T) {
			f := newAtomicImportFixture(t, format)
			db := openStore(t)
			start := make(chan struct{})
			errors := make(chan error, 2)
			for range 2 {
				go func() {
					<-start
					_, err := f.run(db, false)
					errors <- err
				}()
			}
			close(start)
			for range 2 {
				if err := <-errors; err != nil {
					t.Error(err)
				}
			}
			want := map[string]int{"users": 1, "libraries": 1, "works": 1, "editions": 2, "files": 2, "progress": 2, "playlists": 0, "playlist_items": 0}
			if format == "abs" {
				want = map[string]int{"users": 2, "libraries": 2, "works": 2, "editions": 2, "files": 2, "progress": 3, "playlists": 1, "playlist_items": 1}
			}
			for table, count := range want {
				if n := countRows(t, db, `SELECT count(*) FROM `+table); n != count {
					t.Errorf("concurrent import has %d %s rows, want %d", n, table, count)
				}
			}
			if n := countRows(t, db, `SELECT count(*) FROM progress WHERE revision != 2`); n != 0 {
				t.Errorf("%d progress rows did not advance exactly once per import", n)
			}
		})
	}
}
