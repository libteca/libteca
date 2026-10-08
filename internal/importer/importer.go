// Package importer reads foreign media-server databases (Audiobookshelf,
// Kavita) read-only and replays them into our store through the normal
// upsert paths. It never writes to the foreign database and never accepts
// client-supplied SQL.
package importer

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	Source              string        `json:"source"`
	SourceSchemaVersion int64         `json:"sourceSchemaVersion"`
	SourceConsistency   string        `json:"sourceConsistency"`
	Libraries           []LibraryPlan `json:"libraries"`
	Works               int           `json:"works"`
	Editions            int           `json:"editions"`
	Files               int           `json:"files"`
	Users               []UserPlan    `json:"users"`
	ProgressRows        int           `json:"progressRows"`
	Playlists           int           `json:"playlists"`
	Warnings            []string      `json:"warnings"`
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
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`BEGIN`); err != nil {
		db.Close()
		return nil, fmt.Errorf("begin source snapshot: %w", err)
	}
	var version int
	if err := db.QueryRow(`PRAGMA schema_version`).Scan(&version); err != nil {
		db.Close()
		return nil, fmt.Errorf("read source snapshot: %w", err)
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
	Snapshot os.FileInfo
	Digest   string
}

var audioExts = map[string]string{
	".m4b": "m4b", ".m4a": "m4b", ".aac": "m4b",
	".mp3": "mp3", ".flac": "mp3", ".opus": "mp3", ".ogg": "mp3", ".wav": "mp3",
}

// audioPathsIn lists the audio files under dir (hidden dirs skipped),
// naturally sorted like the scanner would see them. Durations are the
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

var readRandom = rand.Read

func tempPassword() (string, error) {
	var b [16]byte
	if _, err := readRandom(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
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
	ID      foreignID
	Name    string
	IsAdmin bool
}

type userStore interface {
	UserByName(string) (*store.User, error)
	CreateUser(string, string, bool) (int64, error)
}

// applyUsers maps foreign users onto ours by (case-insensitive) name. New
// users get a temp password on commit — foreign bcrypt hashes cannot be
// migrated into our argon2id scheme; the admin resets them after first login.
func applyUsers(db userStore, users []foreignUser, plan *Plan, commit bool) (map[foreignID]int64, error) {
	ids := map[foreignID]int64{}
	for _, u := range users {
		up := UserPlan{Name: u.Name, IsAdmin: u.IsAdmin}
		existing, err := db.UserByName(u.Name)
		if err == nil {
			up.Exists = true
			ids[u.ID] = existing.ID
		} else if errors.Is(err, store.ErrNotFound) && commit {
			pw, err := tempPassword()
			if err != nil {
				return nil, fmt.Errorf("create user %s temporary password: %w", u.Name, err)
			}
			hash, err := auth.Hash(pw)
			if err != nil {
				return nil, fmt.Errorf("create user %s: %w", u.Name, err)
			}
			id, err := db.CreateUser(u.Name, hash, u.IsAdmin)
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

type libraryStore interface {
	Libraries() ([]store.Library, error)
	AddLibrary(string, string, string) (int64, error)
}

// ensureLibrary matches one of our libraries by name and type, creating it on
// commit when missing. Foreign servers do not always record a usable path;
// the placeholder keeps the row valid and the admin fixes it before scanning.
func ensureLibrary(db libraryStore, name, typ, fallbackPath string, plan *Plan, libIndex int, commit bool) (int64, error) {
	libs, err := db.Libraries()
	if err != nil {
		return 0, err
	}
	for _, l := range libs {
		if l.Name == name && l.Type == typ && sameLibraryRoot(l.Path, fallbackPath) {
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
func applyFiles(db *store.DB, editionID int64, files []fileSpec) ([]int64, error) {
	var ids []int64
	err := db.Update(func(tx *store.Tx) error {
		var err error
		ids, err = applyFilesTx(tx, editionID, files)
		return err
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func applyFilesTx(tx *store.Tx, editionID int64, files []fileSpec) ([]int64, error) {
	var ids []int64
	for i, f := range files {
		fi, err := os.Stat(f.Path)
		if err != nil {
			return nil, fmt.Errorf("planned file vanished during import: %s: %w", f.Path, err)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("planned file is not a regular media file: %s", f.Path)
		}
		if f.Snapshot != nil && (!os.SameFile(fi, f.Snapshot) || fi.Size() != f.Snapshot.Size() || fi.ModTime() != f.Snapshot.ModTime()) {
			return nil, fmt.Errorf("planned file changed during import: %s", f.Path)
		}
		var existing, owner int64
		var digest *string
		var missing bool
		err = tx.QueryRow(`SELECT id,edition_id,sha256,missing FROM files WHERE path=?`, f.Path).Scan(&existing, &owner, &digest, &missing)
		if err == nil {
			if owner != editionID {
				return nil, store.ErrSourceConflict
			}
			if !missing && (digest == nil || *digest == "" || *digest == f.Digest) {
				if digest == nil || *digest == "" {
					if _, err := tx.Exec(`UPDATE files SET sha256=?,size_bytes=?,mtime_secs=?,mtime_ns=? WHERE id=?`, f.Digest, fi.Size(), fi.ModTime().Unix(), fi.ModTime().UnixNano(), existing); err != nil {
						return nil, err
					}
				}
				ids = append(ids, existing)
				continue
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		fr := &store.FileRec{
			EditionID: editionID, Path: f.Path, Seq: i + 1,
			SizeBytes: fi.Size(), MtimeSecs: fi.ModTime().Unix(), MtimeNS: fi.ModTime().UnixNano(),
			DurationSecs: f.Duration, Chapters: "[]", SHA256: strPtr(f.Digest),
		}
		if err := tx.UpsertFile(fr); err != nil {
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

func snapshotFiles(files []fileSpec) error {
	for i := range files {
		fi, err := os.Stat(files[i].Path)
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("not a regular media file")
		}
		input, err := os.Open(files[i].Path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, readErr := io.Copy(h, input)
		after, statErr := input.Stat()
		closeErr := input.Close()
		if err := errors.Join(readErr, statErr, closeErr); err != nil {
			return err
		}
		if !os.SameFile(fi, after) || fi.Size() != after.Size() || fi.ModTime() != after.ModTime() {
			return fmt.Errorf("file changed during content proof")
		}
		files[i].Digest = fmt.Sprintf("%x", h.Sum(nil))
		files[i].Snapshot = fi
	}
	return nil
}

func sourcePaths(files []fileSpec) []string {
	paths := make([]string, len(files))
	for i := range files {
		paths[i] = files[i].Path
	}
	return paths
}

func sameLibraryRoot(a, b string) bool {
	a, _ = filepath.Abs(a)
	b, _ = filepath.Abs(b)
	if ar, err := filepath.EvalSymlinks(a); err == nil {
		a = ar
	}
	if br, err := filepath.EvalSymlinks(b); err == nil {
		b = br
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func sourceDigests(files []fileSpec) []string {
	digests := make([]string, len(files))
	for i := range files {
		digests[i] = files[i].Digest
	}
	return digests
}

func physicalImportRoot(db libraryStore, name, typ string, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("no physical files for imported library")
	}
	libraries, err := db.Libraries()
	if err != nil {
		return "", err
	}
	root := ""
	for _, library := range libraries {
		if library.Name != name || library.Type != typ {
			continue
		}
		contains := true
		for _, path := range paths {
			if _, err := store.SourceKey(library.Path, path); err != nil {
				contains = false
				break
			}
		}
		if contains {
			if root != "" {
				return "", store.ErrSourceConflict
			}
			root = library.Path
		}
	}
	if root != "" {
		return root, nil
	}
	root = filepath.Dir(paths[0])
	for _, path := range paths[1:] {
		for {
			if _, err := store.SourceKey(root, path); err == nil {
				break
			}
			parent := filepath.Dir(root)
			if parent == root {
				return "", store.ErrSourceConflict
			}
			root = parent
		}
	}
	if root == filepath.VolumeName(root)+string(filepath.Separator) || !filepath.IsAbs(root) {
		return "", store.ErrSourceConflict
	}
	return root, nil
}
