package sourceinventory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

type RootMapping struct {
	LibraryID      int64  `json:"library_id"`
	DisposableRoot string `json:"disposable_root"`
}

type Library struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	StoredRoot     string   `json:"stored_root"`
	NormalizedRoot string   `json:"normalized_root"`
	InspectionRoot string   `json:"inspection_root,omitempty"`
	Findings       []string `json:"findings"`
}

type Candidate struct {
	LibraryID    int64  `json:"library_id"`
	RelativePath string `json:"relative_path"`
	Inspection   string `json:"inspection"`
	ObservedSize *int64 `json:"observed_size_bytes,omitempty"`
}

type File struct {
	ID             int64       `json:"id"`
	EditionID      *int64      `json:"edition_id"`
	Path           string      `json:"stored_path"`
	NormalizedPath string      `json:"normalized_path"`
	Seq            int64       `json:"seq"`
	Size           int64       `json:"size_bytes"`
	Mtime          int64       `json:"mtime_secs"`
	Hash           *string     `json:"stored_hash"`
	Codec          *string     `json:"codec"`
	Container      *string     `json:"container"`
	Duration       float64     `json:"duration_secs"`
	Missing        bool        `json:"marked_missing"`
	Chapters       string      `json:"chapters_json"`
	Metadata       string      `json:"embedded_meta_json"`
	Candidates     []Candidate `json:"lexical_root_candidates"`
	Findings       []string    `json:"findings"`
}

type Progress struct {
	ID         int64    `json:"id"`
	UserID     int64    `json:"user_id"`
	EditionID  int64    `json:"edition_id"`
	FileID     *int64   `json:"file_id"`
	FileOffset float64  `json:"file_offset_secs"`
	Position   float64  `json:"edition_position_secs"`
	Duration   *float64 `json:"duration_secs"`
	Finished   bool     `json:"is_finished"`
	Page       *int64   `json:"page"`
	Percent    *float64 `json:"percent"`
	Locator    *string  `json:"locator"`
	UpdatedAt  int64    `json:"updated_at"`
	Revision   int64    `json:"revision"`
	Deleted    bool     `json:"deleted"`
	Findings   []string `json:"findings"`
}

type Edition struct {
	ID        int64      `json:"id"`
	WorkID    int64      `json:"work_id"`
	LibraryID *int64     `json:"apparent_library_id"`
	WorkTitle *string    `json:"work_title"`
	Author    *string    `json:"author"`
	Title     string     `json:"title"`
	Format    string     `json:"format"`
	Duration  *float64   `json:"duration_secs"`
	Position  int64      `json:"position"`
	Files     []File     `json:"files"`
	Progress  []Progress `json:"progress"`
	Findings  []string   `json:"findings"`
}

type Report struct {
	Version           int            `json:"report_version"`
	SnapshotSHA256    string         `json:"snapshot_sha256"`
	ReadOnly          bool           `json:"read_only"`
	OwnershipAssigned bool           `json:"ownership_assigned"`
	Libraries         []Library      `json:"libraries"`
	Editions          []Edition      `json:"editions"`
	UnattachedFiles   []File         `json:"unattached_files"`
	OrphanProgress    []Progress     `json:"orphan_progress"`
	FindingCounts     map[string]int `json:"finding_counts"`
	Limits            []string       `json:"limits"`
}

func inspectSnapshot(ctx context.Context, path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("snapshot must be a regular non-symlink file")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return "", fmt.Errorf("snapshot has %s sidecar; use a consistent standalone disposable backup", suffix)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func openReadOnly(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := url.Values{"mode": {"ro"}, "immutable": {"1"}, "_pragma": {"query_only(1)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func Read(ctx context.Context, snapshot string, mappings []RootMapping) (*Report, error) {
	if snapshot == "" {
		return nil, fmt.Errorf("an explicit disposable snapshot path is required")
	}
	path, err := filepath.Abs(snapshot)
	if err != nil {
		return nil, err
	}
	before, err := inspectSnapshot(ctx, path)
	if err != nil {
		return nil, err
	}
	db, err := openReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	r := &Report{Version: 1, SnapshotSHA256: before, ReadOnly: true, Libraries: []Library{}, Editions: []Edition{}, UnattachedFiles: []File{}, OrphanProgress: []Progress{}, FindingCounts: map[string]int{}, Limits: []string{
		"Lexical root candidates are evidence for review, never assigned ownership or permission to open media.",
		"Media is only statted beneath explicitly mapped disposable roots; stored roots are never opened and media contents are never read.",
		"Possible collapse findings are heuristics, not proof; missing history and intentionally duplicated encodings require a reviewed repair map.",
		"Only current edition-backed schema is supported. Unattached files include podcast downloads; podcast ownership/progress is outside this report.",
		"Use a consistent standalone disposable snapshot with no concurrent writers. This command does not create backups or lock a live server.",
	}}
	if err := readLibraries(ctx, db, r); err != nil {
		return nil, fmt.Errorf("read current snapshot schema: %w", err)
	}
	roots := map[int64]*os.Root{}
	defer func() {
		for _, root := range roots {
			_ = root.Close()
		}
	}()
	for _, mapping := range mappings {
		if _, exists := roots[mapping.LibraryID]; exists {
			return nil, fmt.Errorf("duplicate mapping for library %d", mapping.LibraryID)
		}
		index := -1
		for i := range r.Libraries {
			if r.Libraries[i].ID == mapping.LibraryID {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, fmt.Errorf("unknown mapped library %d", mapping.LibraryID)
		}
		if !filepath.IsAbs(mapping.DisposableRoot) {
			return nil, fmt.Errorf("inspection root for library %d must be absolute", mapping.LibraryID)
		}
		root, err := os.OpenRoot(mapping.DisposableRoot)
		if err != nil {
			return nil, fmt.Errorf("open mapped disposable root for library %d: %w", mapping.LibraryID, err)
		}
		roots[mapping.LibraryID] = root
		r.Libraries[index].InspectionRoot = mapping.DisposableRoot
	}
	if err := readEditions(ctx, db, r); err != nil {
		return nil, fmt.Errorf("read current snapshot schema: %w", err)
	}
	if err := readFiles(ctx, db, r, roots); err != nil {
		return nil, fmt.Errorf("read current snapshot schema: %w", err)
	}
	if err := readProgress(ctx, db, r); err != nil {
		return nil, fmt.Errorf("read current snapshot schema: %w", err)
	}
	for i := range r.Editions {
		classifyEdition(&r.Editions[i])
		for _, finding := range r.Editions[i].Findings {
			r.FindingCounts[finding]++
		}
	}
	for _, lib := range r.Libraries {
		for _, finding := range lib.Findings {
			r.FindingCounts[finding]++
		}
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	after, err := inspectSnapshot(ctx, path)
	if err != nil {
		return nil, err
	}
	if after != before {
		return nil, fmt.Errorf("snapshot changed during inventory; discard report and use a stopped consistent copy")
	}
	return r, nil
}

func readLibraries(ctx context.Context, db *sql.DB, r *Report) error {
	rows, err := db.QueryContext(ctx, `SELECT id, name, type, path FROM libraries ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		lib := Library{Findings: []string{}}
		if err := rows.Scan(&lib.ID, &lib.Name, &lib.Type, &lib.StoredRoot); err != nil {
			return err
		}
		lib.NormalizedRoot = filepath.Clean(lib.StoredRoot)
		if !filepath.IsAbs(lib.StoredRoot) {
			lib.Findings = append(lib.Findings, "invalid_library_root")
		}
		if lib.NormalizedRoot != lib.StoredRoot {
			lib.Findings = append(lib.Findings, "noncanonical_library_root")
		}
		r.Libraries = append(r.Libraries, lib)
	}
	return rows.Err()
}

func readEditions(ctx context.Context, db *sql.DB, r *Report) error {
	rows, err := db.QueryContext(ctx, `SELECT e.id, e.work_id, w.library_id, w.title, w.author, e.title, e.format, e.duration_secs, e.position FROM editions e LEFT JOIN works w ON w.id = e.work_id ORDER BY e.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		e := Edition{Files: []File{}, Progress: []Progress{}, Findings: []string{}}
		if err := rows.Scan(&e.ID, &e.WorkID, &e.LibraryID, &e.WorkTitle, &e.Author, &e.Title, &e.Format, &e.Duration, &e.Position); err != nil {
			return err
		}
		if e.LibraryID == nil {
			e.Findings = append(e.Findings, "missing_work")
		} else {
			found := false
			for _, lib := range r.Libraries {
				if lib.ID == *e.LibraryID {
					found = true
					break
				}
			}
			if !found {
				e.Findings = append(e.Findings, "missing_library")
			}
		}
		r.Editions = append(r.Editions, e)
	}
	return rows.Err()
}

func readFiles(ctx context.Context, db *sql.DB, r *Report, roots map[int64]*os.Root) error {
	indices := map[int64]int{}
	for i := range r.Editions {
		indices[r.Editions[i].ID] = i
	}
	rows, err := db.QueryContext(ctx, `SELECT id, edition_id, path, seq, size_bytes, mtime_secs, hash, codec, container, duration_secs, missing, chapters, embedded_meta FROM files ORDER BY edition_id, seq, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		f := File{Candidates: []Candidate{}, Findings: []string{}}
		if err := rows.Scan(&f.ID, &f.EditionID, &f.Path, &f.Seq, &f.Size, &f.Mtime, &f.Hash, &f.Codec, &f.Container, &f.Duration, &f.Missing, &f.Chapters, &f.Metadata); err != nil {
			return err
		}
		f.NormalizedPath = filepath.Clean(f.Path)
		if !filepath.IsAbs(f.Path) {
			f.Findings = append(f.Findings, "invalid_file_path")
		} else {
			if f.NormalizedPath != f.Path {
				f.Findings = append(f.Findings, "noncanonical_file_path")
			}
			for _, lib := range r.Libraries {
				if !filepath.IsAbs(lib.StoredRoot) {
					continue
				}
				rel, err := filepath.Rel(lib.NormalizedRoot, f.NormalizedPath)
				if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
					continue
				}
				candidate := Candidate{LibraryID: lib.ID, RelativePath: filepath.ToSlash(rel), Inspection: "not_requested"}
				if root := roots[lib.ID]; root != nil {
					info, err := root.Lstat(rel)
					switch {
					case os.IsNotExist(err):
						candidate.Inspection = "missing"
					case err != nil:
						candidate.Inspection = "unavailable_or_outside_mapped_root"
					case info.Mode()&os.ModeSymlink != 0:
						candidate.Inspection = "symlink_not_followed"
					case !info.Mode().IsRegular():
						candidate.Inspection = "not_regular"
					default:
						size := info.Size()
						candidate.ObservedSize = &size
						candidate.Inspection = "regular_file"
						if size != f.Size {
							candidate.Inspection = "size_mismatch"
						}
					}
				}
				f.Candidates = append(f.Candidates, candidate)
			}
		}
		if len(f.Candidates) == 0 {
			f.Findings = append(f.Findings, "no_lexical_root")
		}
		if len(f.Candidates) > 1 {
			f.Findings = append(f.Findings, "ambiguous_lexical_root")
		}
		if f.Missing {
			f.Findings = append(f.Findings, "marked_missing")
		}
		index, found := 0, false
		if f.EditionID != nil {
			index, found = indices[*f.EditionID]
		}
		if found {
			e := &r.Editions[index]
			apparent := false
			for _, candidate := range f.Candidates {
				if e.LibraryID != nil && candidate.LibraryID == *e.LibraryID {
					apparent = true
				}
			}
			if !apparent {
				f.Findings = append(f.Findings, "outside_apparent_library")
			}
			e.Files = append(e.Files, f)
		} else {
			r.UnattachedFiles = append(r.UnattachedFiles, f)
		}
		for _, finding := range f.Findings {
			r.FindingCounts[finding]++
		}
	}
	return rows.Err()
}

func readProgress(ctx context.Context, db *sql.DB, r *Report) error {
	indices := map[int64]int{}
	for i := range r.Editions {
		indices[r.Editions[i].ID] = i
	}
	rows, err := db.QueryContext(ctx, `SELECT id, user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, is_finished, page, percent, locator, updated_at, revision, deleted FROM progress ORDER BY edition_id, user_id, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		p := Progress{Findings: []string{}}
		if err := rows.Scan(&p.ID, &p.UserID, &p.EditionID, &p.FileID, &p.FileOffset, &p.Position, &p.Duration, &p.Finished, &p.Page, &p.Percent, &p.Locator, &p.UpdatedAt, &p.Revision, &p.Deleted); err != nil {
			return err
		}
		if index, ok := indices[p.EditionID]; ok {
			e := &r.Editions[index]
			if p.FileID != nil {
				found := false
				for _, f := range e.Files {
					if f.ID == *p.FileID {
						found = true
						if p.FileOffset < 0 || (f.Duration > 0 && p.FileOffset > f.Duration) {
							p.Findings = append(p.Findings, "progress_offset_outside_file")
						}
						break
					}
				}
				if !found {
					p.Findings = append(p.Findings, "progress_file_not_in_edition")
				}
			}
			e.Progress = append(e.Progress, p)
		} else {
			p.Findings = append(p.Findings, "progress_edition_missing")
			r.OrphanProgress = append(r.OrphanProgress, p)
		}
		for _, finding := range p.Findings {
			r.FindingCounts[finding]++
		}
	}
	return rows.Err()
}

func classifyEdition(e *Edition) {
	if len(e.Files) == 0 {
		e.Findings = append(e.Findings, "empty_edition")
		return
	}
	roots := map[int64]bool{}
	seqs := map[int64]bool{}
	parents := map[string]bool{}
	ambiguous, outside, duplicateSeq := false, false, false
	for _, f := range e.Files {
		if len(f.Candidates) == 1 {
			roots[f.Candidates[0].LibraryID] = true
		} else {
			ambiguous = true
		}
		for _, finding := range f.Findings {
			if finding == "outside_apparent_library" {
				outside = true
			}
		}
		if seqs[f.Seq] {
			duplicateSeq = true
		}
		seqs[f.Seq] = true
		parents[filepath.Dir(f.NormalizedPath)] = true
	}
	if len(roots) > 1 {
		e.Findings = append(e.Findings, "mixed_lexical_roots")
	}
	if ambiguous {
		e.Findings = append(e.Findings, "ownership_review_required")
	}
	if outside {
		e.Findings = append(e.Findings, "apparent_root_mismatch")
	}
	if duplicateSeq {
		e.Findings = append(e.Findings, "duplicate_file_sequence")
	}
	if len(parents) > 1 {
		e.Findings = append(e.Findings, "multiple_parent_directories_review")
	}
	if len(e.Files) > 1 && (e.Format == "epub" || e.Format == "pdf" || e.Format == "cbz" || e.Format == "cbr" || duplicateSeq) {
		e.Findings = append(e.Findings, "possible_collapsed_edition")
	}
	sort.Strings(e.Findings)
}
