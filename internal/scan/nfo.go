package scan

import (
	"bytes"
	"database/sql"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/libteca/libteca/internal/store"
)

// workDB is the work-mutation surface applyNFO needs; satisfied by both
// *store.DB and *store.Tx so it composes inside scan batch transactions.
type workDB interface {
	WorkByID(id int64) (*store.Work, error)
	WorkGenres(workID int64) []string
	SetWorkGenres(workID int64, genres []string) error
	Exec(query string, args ...any) (sql.Result, error)
}

// NFO is the common Kodi/Jellyfin sidecar subset: movie, tvshow,
// episodedetails, season. Unknown child tags are ignored.
type NFO struct {
	Root      string
	Title     string
	Plot      string
	Outline   string
	Year      string
	Premiered string
	Aired     string
	Genres    []string
	Studio    string
	Credits   []string
	Directors []string
}

type nfoRaw struct {
	XMLName   xml.Name
	Title     string   `xml:"title"`
	Plot      string   `xml:"plot"`
	Outline   string   `xml:"outline"`
	Year      string   `xml:"year"`
	Premiered string   `xml:"premiered"`
	Aired     string   `xml:"aired"`
	Genres    []string `xml:"genre"`
	Studio    string   `xml:"studio"`
	Credits   []string `xml:"credits"`
	Directors []string `xml:"director"`
}

var sidecarPosters = []string{
	"poster.jpg", "Poster.jpg", "folder.jpg", "Folder.jpg", "cover.jpg", "Cover.jpg",
}

var sidecarFanart = []string{
	"fanart.jpg", "Fanart.jpg", "backdrop.jpg", "Backdrop.jpg",
}

var reFallbackTitle = regexp.MustCompile(`(?i)^S\d{2}E\d{2,3}$`)

func ParseNFO(data []byte) (*NFO, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("empty nfo")
	}
	var raw nfoRaw
	if err := xml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	n := &NFO{
		Root:      raw.XMLName.Local,
		Title:     strings.TrimSpace(raw.Title),
		Plot:      strings.TrimSpace(raw.Plot),
		Outline:   strings.TrimSpace(raw.Outline),
		Year:      strings.TrimSpace(raw.Year),
		Premiered: strings.TrimSpace(raw.Premiered),
		Aired:     strings.TrimSpace(raw.Aired),
		Studio:    strings.TrimSpace(raw.Studio),
	}
	for _, g := range raw.Genres {
		if g = strings.TrimSpace(g); g != "" {
			n.Genres = append(n.Genres, g)
		}
	}
	for _, c := range raw.Credits {
		if c = strings.TrimSpace(c); c != "" {
			n.Credits = append(n.Credits, c)
		}
	}
	for _, d := range raw.Directors {
		if d = strings.TrimSpace(d); d != "" {
			n.Directors = append(n.Directors, d)
		}
	}
	return n, nil
}

func (n *NFO) Description() string {
	if n == nil {
		return ""
	}
	if n.Plot != "" {
		return n.Plot
	}
	return n.Outline
}

func (n *NFO) YearValue() string {
	if n == nil {
		return ""
	}
	if y := n.Year; len(y) >= 4 && isYear4(y[:4]) {
		return y[:4]
	}
	if y := n.Premiered; len(y) >= 4 && isYear4(y[:4]) {
		return y[:4]
	}
	if y := n.Aired; len(y) >= 4 && isYear4(y[:4]) {
		return y[:4]
	}
	return ""
}

func (n *NFO) CreditsLine() string {
	if n == nil {
		return ""
	}
	if len(n.Directors) > 0 {
		return strings.Join(n.Directors, ", ")
	}
	return strings.Join(n.Credits, ", ")
}

func (n *NFO) isEpisode() bool {
	return n != nil && strings.EqualFold(n.Root, "episodedetails")
}

func isYear4(s string) bool {
	if len(s) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func titleLooksLikeFilename(title string) bool {
	if title == "" {
		return true
	}
	if strings.Contains(title, " ") {
		return false
	}
	if strings.Contains(title, ".") || strings.Contains(title, "_") {
		return true
	}
	return reFallbackTitle.MatchString(title)
}

func findWorkNFO(folder string) string {
	base := filepath.Base(folder)
	for _, name := range []string{"tvshow.nfo", "movie.nfo", "season.nfo", base + ".nfo"} {
		p := filepath.Join(folder, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func findFileNFO(mediaPath string) string {
	dir := filepath.Dir(mediaPath)
	base := strings.TrimSuffix(filepath.Base(mediaPath), filepath.Ext(mediaPath))
	p := filepath.Join(dir, base+".nfo")
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return ""
}

func parseNFOPath(path string) *NFO {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	n, err := ParseNFO(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "libteca: skip nfo %s: %v\n", path, err)
		return nil
	}
	return n
}

func readWorkNFO(folder, mediaPath string) *NFO {
	if p := findWorkNFO(folder); p != "" {
		if n := parseNFOPath(p); n != nil && !n.isEpisode() {
			return n
		}
	}
	if mediaPath == "" {
		return nil
	}
	if p := findFileNFO(mediaPath); p != "" {
		if n := parseNFOPath(p); n != nil && !n.isEpisode() {
			return n
		}
	}
	return nil
}

func readFileNFO(mediaPath string) *NFO {
	p := findFileNFO(mediaPath)
	if p == "" {
		return nil
	}
	return parseNFOPath(p)
}

func episodeTitleFromNFO(mediaPath, current string) string {
	n := readFileNFO(mediaPath)
	if n == nil || n.Title == "" || !titleLooksLikeFilename(current) {
		return current
	}
	return n.Title
}

// applyNFO overlays sidecar title/plot onto the work (NFO wins when present)
// and writes genres only when the work has none. Author is left to the caller
// (video stores year there; audio stores the person).
func applyNFO(db workDB, workID int64, n *NFO) error {
	if n == nil {
		return nil
	}
	w, err := db.WorkByID(workID)
	if err != nil {
		return err
	}
	title := w.Title
	if n.Title != "" {
		title = n.Title
	}
	desc := w.Description
	if d := n.Description(); d != "" {
		desc = &d
	}
	if _, err := db.Exec(`UPDATE works SET title = ?, title_l = ?, description = ?, updated_at = ? WHERE id = ?`,
		title, strings.ToLower(title), desc, time.Now().UnixMilli(), workID); err != nil {
		return err
	}
	if len(n.Genres) > 0 && len(db.WorkGenres(workID)) == 0 {
		return db.SetWorkGenres(workID, n.Genres)
	}
	return nil
}

func importSidecarPoster(db *store.DB, workID int64, folder, coversDir string, force bool) (bool, error) {
	rel := fmt.Sprintf("%d.jpg", workID)
	dst := filepath.Join(coversDir, rel)
	if !force {
		if _, err := os.Stat(dst); err == nil {
			return true, db.SetWorkCover(workID, rel)
		}
	}
	for _, name := range sidecarPosters {
		data, err := os.ReadFile(filepath.Join(folder, name))
		if err != nil {
			continue
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return false, err
		}
		return true, db.SetWorkCover(workID, rel)
	}
	if _, err := os.Stat(dst); err == nil {
		return true, db.SetWorkCover(workID, rel)
	}
	return false, nil
}

// fileSuffixArt returns the first existing Kodi/Plex-style per-file artwork
// sibling: <base>-poster.jpg, <base>.jpg, <base>-thumb.jpg.
func fileSuffixArt(mediaPath string) string {
	dir := filepath.Dir(mediaPath)
	base := strings.TrimSuffix(filepath.Base(mediaPath), filepath.Ext(mediaPath))
	for _, suffix := range []string{"-poster.jpg", ".jpg", "-thumb.jpg"} {
		for _, name := range []string{base + suffix, base + strings.ToUpper(suffix)} {
			if p := filepath.Join(dir, name); fileOKMedia(p) {
				return p
			}
		}
	}
	return ""
}

func fileOKMedia(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Size() > 0
}

// importSuffixArt copies per-file artwork (<base>-poster.jpg etc.) as the
// work cover when folder-level names found nothing.
func importSuffixArt(db *store.DB, workID int64, mediaPath, coversDir string) (bool, error) {
	src := fileSuffixArt(mediaPath)
	if src == "" {
		return false, nil
	}
	dst := filepath.Join(coversDir, fmt.Sprintf("%d.jpg", workID))
	data, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return false, err
	}
	return true, db.SetWorkCover(workID, fmt.Sprintf("%d.jpg", workID))
}

// importFanart copies folder fanart/backdrop or <base>-fanart.jpg to
// covers/{workID}-fanart.jpg. Existence alone marks the work (no column);
// the work API stats the file.
func importFanart(workID int64, folder, mediaPath, coversDir string) {
	dst := filepath.Join(coversDir, fmt.Sprintf("%d-fanart.jpg", workID))
	if fileOKMedia(dst) {
		return
	}
	var src string
	for _, name := range sidecarFanart {
		if p := filepath.Join(folder, name); fileOKMedia(p) {
			src = p
			break
		}
	}
	if src == "" {
		base := strings.TrimSuffix(filepath.Base(mediaPath), filepath.Ext(mediaPath))
		for _, name := range []string{base + "-fanart.jpg", base + "-Fanart.jpg"} {
			if p := filepath.Join(filepath.Dir(mediaPath), name); fileOKMedia(p) {
				src = p
				break
			}
		}
	}
	if src == "" {
		return
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	_ = os.WriteFile(dst, data, 0o644)
}
