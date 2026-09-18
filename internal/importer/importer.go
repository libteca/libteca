// Package importer reads foreign media-server databases (Audiobookshelf,
// Kavita) read-only and replays them into our store through the normal
// upsert paths. It never writes to the foreign database and never accepts
// client-supplied SQL.
package importer

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/natural"
	"github.com/libteca/libteca/internal/store"
	_ "modernc.org/sqlite"
)

type UserPlan struct {
	Name         string `json:"name"`
	IsAdmin      bool   `json:"isAdmin"`
	Exists       bool   `json:"exists"`
	TempPassword string `json:"tempPassword,omitempty"`
}

type LibraryPlan struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	New      bool   `json:"new"`
	Works    int    `json:"works"`
	Editions int    `json:"editions"`
	Skipped  int    `json:"skipped"`
}

type Plan struct {
	Source       string        `json:"source"`
	Libraries    []LibraryPlan `json:"libraries"`
	Works        int           `json:"works"`
	Editions     int           `json:"editions"`
	Files        int           `json:"files"`
	Users        []UserPlan    `json:"users"`
	ProgressRows int           `json:"progressRows"`
	Playlists    int           `json:"playlists"`
	Warnings     []string      `json:"warnings"`
}

func (p *Plan) warnf(format string, args ...any) {
	p.Warnings = append(p.Warnings, fmt.Sprintf(format, args...))
}

// openForeign opens a foreign SQLite database strictly read-only. A live
// server holding a hot WAL can block recovery on a read-only handle; stop the
// server (or import a copy) in that case.
func openForeign(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot open %s read-only (stop the source server or import a copy): %w", path, err)
	}
	return db, nil
}

func schemaErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "no such table") || strings.Contains(s, "no such column")
}

type fileSpec struct {
	Path     string
	Duration float64
}

var audioExts = map[string]string{
	".m4b": "m4b", ".m4a": "m4b", ".aac": "m4b",
	".mp3": "mp3", ".flac": "mp3", ".opus": "mp3", ".ogg": "mp3", ".wav": "mp3",
}

// audioPathsIn lists the audio files under dir (hidden dirs skipped),
// naturally sorted like the scanner would see them. Durations are the
// caller's problem — foreign metadata carries them. Traversal errors fail
// the discovery instead of silently producing a partial import plan, and
// only regular files are accepted.
func audioPathsIn(dir string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := audioExts[strings.ToLower(filepath.Ext(d.Name()))]; !ok {
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("unsupported import entry: %s", p)
		}
		names = append(names, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(names, func(i, j int) bool { return natural.Less(names[i], names[j]) })
	out := make([]string, 0, len(names))
	for _, n := range names {
		if _, err := os.Stat(n); err == nil {
			out = append(out, n)
		}
	}
	return out, nil
}

func tempPassword() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// plainAuthor tolerates foreign author columns that store either plain text
// or a JSON array/string of names.
func plainAuthor(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '[' {
		var names []string
		if json.Unmarshal([]byte(s), &names) == nil {
			return strings.Join(names, ", ")
		}
	}
	if len(s) >= 2 && s[0] == '"' {
		var one string
		if json.Unmarshal([]byte(s), &one) == nil {
			return one
		}
	}
	return s
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type foreignUser struct {
	ID      int64
	Name    string
	IsAdmin bool
}

// applyUsers maps foreign users onto ours by (case-insensitive) name. New
// users get a temp password on commit — foreign bcrypt hashes cannot be
// migrated into our argon2id scheme; the admin resets them after first login.
func applyUsers(db *store.DB, users []foreignUser, plan *Plan, commit bool) (map[int64]int64, error) {
	ids := map[int64]int64{}
	for _, u := range users {
		up := UserPlan{Name: u.Name, IsAdmin: u.IsAdmin}
		existing, err := db.UserByName(u.Name)
		if err == nil {
			up.Exists = true
			ids[u.ID] = existing.ID
		} else if errors.Is(err, store.ErrNotFound) && commit {
			// Only a confirmed-missing user is created: treating an
			// arbitrary lookup failure as "not exists" created users from
			// database faults.
			pw := tempPassword()
			id, err := db.CreateUser(u.Name, auth.Hash(pw), u.IsAdmin)
			if err != nil {
				return nil, fmt.Errorf("create user %s: %w", u.Name, err)
			}
			up.TempPassword = pw
			ids[u.ID] = id
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("lookup user %s: %w", u.Name, err)
		}
		plan.Users = append(plan.Users, up)
	}
	return ids, nil
}

// ensureLibrary matches one of our libraries by name and type, creating it on
// commit when missing. Foreign servers do not always record a usable path;
// the placeholder keeps the row valid and the admin fixes it before scanning.
func ensureLibrary(db *store.DB, name, typ, fallbackPath string, plan *Plan, libIndex int, commit bool) (int64, error) {
	libs, err := db.Libraries()
	if err != nil {
		return 0, err
	}
	for _, l := range libs {
		if l.Name == name && l.Type == typ {
			return l.ID, nil
		}
	}
	if !commit {
		plan.Libraries[libIndex].New = true
		return 0, nil
	}
	id, err := db.AddLibrary(name, typ, fallbackPath)
	if err != nil {
		return 0, err
	}
	plan.Libraries[libIndex].New = true
	return id, nil
}

// applyFiles stats each resolved file and upserts it under the edition.
// Only regular files are accepted: a symlink or device node with an audio
// extension must fail the import rather than become a served path.
func applyFiles(db *store.DB, editionID int64, files []fileSpec) ([]int64, error) {
	var ids []int64
	for i, f := range files {
		fi, err := os.Stat(f.Path)
		if err != nil {
			// A planned file vanishing mid-import must fail the whole
			// import: silently skipping it desynchronized the file slice
			// from the returned id slice, mis-linking progress and
			// panicking on the index past the compaction.
			return nil, fmt.Errorf("planned file vanished during import: %s: %w", f.Path, err)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("planned file is not a regular media file: %s", f.Path)
		}
		fr := &store.FileRec{
			EditionID: editionID, Path: f.Path, Seq: i + 1,
			SizeBytes: fi.Size(), MtimeSecs: fi.ModTime().Unix(), MtimeNS: fi.ModTime().UnixNano(),
			DurationSecs: f.Duration, Chapters: "[]",
		}
		if err := db.UpsertFile(fr); err != nil {
			return nil, err
		}
		ids = append(ids, fr.ID)
	}
	return ids, nil
}

// locate maps an edition position onto (file, offset) from the file list.
func locate(files []fileSpec, ids []int64, pos float64) (*int64, float64) {
	if len(ids) == 0 {
		return nil, 0
	}
	cum := 0.0
	for i, f := range files {
		cum += f.Duration
		if pos < cum || i == len(files)-1 {
			off := pos - (cum - f.Duration)
			if off < 0 {
				off = 0
			}
			id := ids[i]
			return &id, off
		}
	}
	id := ids[0]
	return &id, 0
}
