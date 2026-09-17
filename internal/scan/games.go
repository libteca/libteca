package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/libteca/libteca/internal/store"
)

// Games scanner (PLAN-GAMES §3 G1): every ROM file is one file on a
// platform edition; files that clean to the same title share one work.
// No probing stage - ROMs carry no stream metadata to ffprobe - and no
// network identity: titles come from filenames, providers enrich later (G2).
//
// The platform table and its shared-extension disambiguation are PORTED from
// omilator's GameSystem (data-library/.../Game.kt) rather than re-derived:
// the collision rules (.bin/.iso/.cue/.chd across genesis/psx/saturn/ps2,
// .elf across psp/3ds, .app across ds/3ds) were already solved there, with
// the same preference map semantics.

type gamePlatform struct {
	Tag  string
	Name string
	Exts []string
}

var gamePlatforms = []gamePlatform{
	{"nes", "Nintendo Entertainment System", []string{"nes", "nez", "unf", "unif"}},
	{"snes", "Super Nintendo", []string{"sfc", "smc", "fig", "swc"}},
	{"gb", "Game Boy", []string{"gb"}},
	{"gbc", "Game Boy Color", []string{"gbc"}},
	{"gba", "Game Boy Advance", []string{"gba"}},
	{"genesis", "Sega Genesis / Mega Drive", []string{"md", "bin", "smd", "gen"}},
	{"n64", "Nintendo 64", []string{"n64", "z64", "v64"}},
	{"psx", "PlayStation", []string{"cue", "chd", "m3u", "pbp", "img", "iso", "bin"}},
	{"nds", "Nintendo DS", []string{"nds", "ids", "app"}},
	{"psp", "PlayStation Portable", []string{"iso", "cso", "prx", "elf"}},
	{"gamecube", "Nintendo GameCube", []string{"gcm", "gci", "ciso"}},
	{"wii", "Nintendo Wii", []string{"wbfs", "wad", "gcz", "ciso"}},
	{"n3ds", "Nintendo 3DS", []string{"3ds", "3dsx", "cci", "cxi", "elf", "app"}},
	{"ps2", "PlayStation 2", []string{"bin", "elf", "nrg", "mdf", "gz"}},
	{"dreamcast", "Sega Dreamcast", []string{"cdi", "gdi", "chd", "m3u", "gdl"}},
	{"saturn", "Sega Saturn", []string{"cue", "iso", "bin", "chd", "m3u"}},
}

// gameExtPreference resolves extensions claimed by several platforms, with
// the documented preference order omilator settled on: ambiguous disc-image
// extensions lean PlayStation, .elf leans PSP, .app leans 3DS, .ciso leans
// GameCube. Callers can override per-directory later; this is the default.
var gameExtPreference = map[string]string{
	"iso": "psx", "bin": "psx", "chd": "psx", "m3u": "psx", "cue": "psx",
	"elf": "psp", "app": "n3ds", "ciso": "gamecube",
}

var (
	gameExtsByPlatform = func() map[string][]string {
		m := map[string][]string{}
		for _, p := range gamePlatforms {
			for _, e := range p.Exts {
				m[e] = append(m[e], p.Tag)
			}
		}
		return m
	}()
	gamePlatformsByTag = func() map[string]*gamePlatform {
		m := map[string]*gamePlatform{}
		for i := range gamePlatforms {
			m[gamePlatforms[i].Tag] = &gamePlatforms[i]
		}
		return m
	}()
)

// detectGamePlatform returns the platform an extension resolves to:
// unique extensions map directly; shared ones go through the preference
// table; an extension absent from the preference table but claimed by
// several platforms has no defensible default and is refused.
func detectGamePlatform(ext string) (*gamePlatform, bool) {
	tags := gameExtsByPlatform[ext]
	switch len(tags) {
	case 0:
		return nil, false
	case 1:
		return gamePlatformsByTag[tags[0]], true
	}
	if tag, ok := gameExtPreference[ext]; ok {
		return gamePlatformsByTag[tag], true
	}
	return nil, false
}

type gameDoc struct {
	path     string
	name     string
	size     int64
	mtime    int64
	mtimeNs  int64
	platform *gamePlatform
	title    string
}

// cleanRomTitle strips the tag noise ROM filenames accumulate - region,
// revision, dump markers, disc numbers - so the same game on two platforms
// (and two dumps on one platform) collapse to one work title.
func cleanRomTitle(name string) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	for {
		next := stripTrailingTag(base)
		if next == base {
			break
		}
		base = next
	}
	base = strings.NewReplacer("_", " ", ".", " ").Replace(base)
	base = strings.Join(strings.Fields(base), " ")
	return base
}

func stripTrailingTag(s string) string {
	s = strings.TrimRight(s, " _-")
	if strings.HasSuffix(s, ")") {
		if i := strings.LastIndex(s, "("); i > 0 {
			return strings.TrimRight(s[:i], " _-")
		}
	}
	if strings.HasSuffix(s, "]") {
		if i := strings.LastIndex(s, "["); i > 0 {
			return strings.TrimRight(s[:i], " _-")
		}
	}
	return s
}

func scanGamesLibrary(ctx context.Context, db *store.DB, lib *store.Library, coversDir string, tr *tracker) (int, error) {
	abs, err := filepath.Abs(lib.Path)
	if err != nil {
		return 0, err
	}
	if err := validateScanRoot(abs); err != nil {
		return 0, err
	}
	var docs []gameDoc
	err = filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
		if cerr := cancelErr(ctx); cerr != nil {
			return cerr
		}
		if err != nil {
			return fmt.Errorf("scan %s: %w", p, err)
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != abs {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(d.Name()), "."))
		plat, ok := detectGamePlatform(ext)
		if !ok {
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("scan %s: %w", p, ierr)
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		docs = append(docs, gameDoc{
			path: p, name: d.Name(), size: fi.Size(), mtime: fi.ModTime().Unix(), mtimeNs: fi.ModTime().UnixNano(),
			platform: plat, title: cleanRomTitle(d.Name()),
		})
		tr.seen(p)
		return nil
	})
	if err != nil {
		return 0, err
	}

	sort.Slice(docs, func(i, j int) bool { return natLess(docs[i].path, docs[j].path) })

	count := 0
	for i := range docs {
		if cerr := cancelErr(ctx); cerr != nil {
			return count, cerr
		}
		d := &docs[i]
		if d.title == "" {
			d.title = strings.TrimSuffix(d.name, filepath.Ext(d.name))
		}
		if size, mtime, mtimeNs, ok, serr := db.FileStatByPath(d.path); serr == nil && ok && size == d.size && mtime == d.mtime && mtimeNs == d.mtimeNs && mtimeNs != 0 {
			continue
		}
		if serr := storeGame(db, lib, d, coversDir, tr); serr != nil {
			return count, serr
		}
		tr.probed()
		count++
	}
	return count, nil
}

func storeGame(db *store.DB, lib *store.Library, d *gameDoc, coversDir string, tr *tracker) error {
	var workID int64
	err := db.Update(func(tx *store.Tx) error {
		// Games set no work metadata of their own (providers fill it in G2),
		// so every file links to the shared work without unconditional updates.
		if id, ok := tx.FindWorkID(lib.ID, d.title, nil); ok {
			workID = id
		}
		if workID == 0 {
			w := &store.Work{LibraryID: lib.ID, Title: d.title}
			var err error
			workID, err = tx.UpsertWork(w)
			if err != nil {
				return err
			}
			if w.Created {
				tr.work()
			}
		}

		e := &store.Edition{WorkID: workID, Format: "game-" + d.platform.Tag, Title: d.platform.Name}
		editionID, err := tx.UpsertEdition(e)
		if err != nil {
			return err
		}

		// An updated existing file KEEPS its sequence: recomputing
		// max(seq)+1 for an update moved the first of two discs to the end
		// and changed the default selected file.
		var seq int64
		err = tx.QueryRow(`SELECT seq FROM files WHERE path = ? AND edition_id = ?`, d.path, editionID).Scan(&seq)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRow(`SELECT coalesce(max(seq), 0) + 1 FROM files WHERE edition_id = ?`, editionID).Scan(&seq)
		}
		if err != nil {
			return err
		}
		hash := hashFile(d.path, d.size)
		fr := &store.FileRec{
			EditionID: editionID, Path: d.path, Seq: int(seq),
			SizeBytes: d.size, MtimeSecs: d.mtime, MtimeNS: d.mtimeNs, Hash: &hash,
			Container: &d.platform.Tag, DurationSecs: 0, Chapters: "[]",
		}
		if err := tx.UpsertFile(fr); err != nil {
			return err
		}
		tr.file(fr.Inserted)
		return nil
	})
	if err != nil {
		return err
	}
	return writeGameCover(db, workID, d, coversDir)
}

// writeGameCover prefers art sitting beside the ROM under the ROM's own name
// (the convention omilator's local cover rule already uses), then the
// folder-level cover files books use.
func writeGameCover(db *store.DB, workID int64, d *gameDoc, coversDir string) error {
	dst := filepath.Join(coversDir, fmt.Sprintf("%d.jpg", workID))
	if _, err := os.Stat(dst); err == nil {
		return db.SetWorkCover(workID, fmt.Sprintf("%d.jpg", workID))
	}
	base := strings.TrimSuffix(d.name, filepath.Ext(d.name))
	dir := filepath.Dir(d.path)
	for _, candidate := range []string{base + ".png", base + ".jpg", "cover.jpg", "Cover.jpg", "folder.jpg", "Folder.jpg"} {
		src := filepath.Join(dir, candidate)
		data, rerr := readSidecar(src)
		if rerr != nil || len(data) == 0 {
			continue
		}
		if werr := writeCoverFile(dst, data); werr != nil {
			return nil
		}
		return db.SetWorkCover(workID, fmt.Sprintf("%d.jpg", workID))
	}
	return nil
}
