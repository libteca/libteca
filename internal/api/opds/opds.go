// Package opds implements the OPDS 1.2 + OPDS-PSE face: navigation +
// acquisition feeds, Basic auth, OpenSearch search, CBZ page streaming, so
// KOReader/Chunky/KyBook/Moon+/Panel connect unchanged.
//
// ID scheme: feed ids are urn:libteca:opds:<name>[:<key>]; feed entries use
// l-<libraryID> for libraries, e-<editionID> for editions, and the plain
// strings "all" / "in-progress" / "newest" for the special root entries.
// Cover hrefs are work-scoped (/opds/cover/<workID>) because cover_path
// lives on works.
package opds

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

const (
	nsAtom       = "http://www.w3.org/2005/Atom"
	nsOPDS       = "http://opds-spec.org/2010/catalog"
	nsOpenSearch = "http://a9.com/-/spec/opensearch/1.1/"

	typeNav = "application/atom+xml;profile=opds-catalog;kind=navigation"
	typeAcq = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	typeOSD = "application/opensearchdescription+xml"

	relAcquisition = "http://opds-spec.org/acquisition"
	relImage       = "http://opds-spec.org/image"
	relThumbnail   = "http://opds-spec.org/image/thumbnail"
	relPSE         = "http://vaemendis.net/opds-pse/stream"
	relSubsection  = "subsection"

	pageLimit = 50 // corpus: page param base (0) + per-page size unverified against KOReader/Chunky
)

type API struct {
	DB           *store.DB
	Dir          string
	LoginLimiter *auth.Limiter
}

func New(db *store.DB, dir string) *API {
	return &API{DB: db, Dir: dir}
}

func (a *API) Mount(r *neutron.Router) {
	r.HandleFunc("GET /opds", a.auth(a.root))
	r.HandleFunc("GET /opds/search-description.xml", a.auth(a.searchDescription))
	r.HandleFunc("GET /opds/libraries/{id}", a.auth(a.libraryFeed))
	r.HandleFunc("GET /opds/all", a.auth(a.allFeed))
	r.HandleFunc("GET /opds/in-progress", a.auth(a.inProgressFeed))
	r.HandleFunc("GET /opds/newest", a.auth(a.newestFeed))
	r.HandleFunc("GET /opds/search", a.auth(a.searchFeed))
	r.HandleFunc("GET /opds/download/{editionId}", a.auth(a.download))
	r.HandleFunc("GET /opds/cover/{workId}", a.auth(a.cover))
	r.HandleFunc("GET /opds/pse/{editionId}/{pageNumber}", a.auth(a.psePage))
}

// Basic auth: username + argon2 password via the shared auth path. Successful
// verifications are cached briefly (hashed key) because OPDS clients send
// Basic on every request — PSE browsing would otherwise re-run argon2 per page.
const basicTTLMillis = 15 * 60 * 1000

type cachedBasic struct {
	userID int64
	at     int64
}

var basicCache sync.Map

func (a *API) auth(h func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			a.unauthorized(w)
			return
		}
		ip := auth.ClientIP(r) + "|" + strings.ToLower(strings.TrimSpace(user))
		if a.LoginLimiter != nil {
			if ok, retry := a.LoginLimiter.Allow(ip); !ok {
				auth.WriteRetryAfter(w, retry)
				http.Error(w, "too many attempts, try again later", http.StatusTooManyRequests)
				return
			}
		}
		var key [32]byte = sha256.Sum256([]byte(user + "\x00" + pass))
		k := key[:]
		if v, hit := basicCache.Load(string(k)); hit {
			if c, ok := v.(*cachedBasic); ok && time.Now().UnixMilli()-c.at < basicTTLMillis {
				h(w, r, c.userID)
				return
			}
			basicCache.Delete(string(k))
		}
		u, err := a.DB.UserByName(user)
		if err != nil || !auth.Verify(pass, u.PasswordHash) {
			if a.LoginLimiter != nil {
				a.LoginLimiter.Failure(ip)
			}
			a.unauthorized(w)
			return
		}
		if a.LoginLimiter != nil {
			a.LoginLimiter.Success(ip)
		}
		basicCache.Store(string(k), &cachedBasic{userID: u.ID, at: time.Now().UnixMilli()})
		h(w, r, u.ID)
	}
}

func (a *API) unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="libteca"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("libteca: opds request failed", "path", r.URL.Path, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// InvalidateUser drops cached Basic credentials for a user so password
// changes and deletion take effect before the TTL expires.
func InvalidateUser(userID int64) {
	basicCache.Range(func(k, v any) bool {
		if c, ok := v.(*cachedBasic); ok && c.userID == userID {
			basicCache.Delete(k)
		}
		return true
	})
}

type Feed struct {
	XMLName         xml.Name   `xml:"feed"`
	XMLNS           string     `xml:"xmlns,attr"`
	XMLNSOpds       string     `xml:"xmlns:opds,attr"`
	XMLNSOpensearch string     `xml:"xmlns:opensearch,attr"`
	ID              string     `xml:"id"`
	Title           string     `xml:"title"`
	Updated         string     `xml:"updated"`
	Links           []FeedLink `xml:"link"`
	TotalResults    int        `xml:"opensearch:totalResults"`
	ItemsPerPage    int        `xml:"opensearch:itemsPerPage"`
	Entries         []Entry    `xml:"entry"`
}

type FeedLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type Entry struct {
	ID      string       `xml:"id"`
	Title   string       `xml:"title"`
	Updated string       `xml:"updated"`
	Author  *EntryAuthor `xml:"author,omitempty"`
	Summary string       `xml:"summary,omitempty"`
	Links   []FeedLink   `xml:"link"`
	Metas   []EntryMeta  `xml:"meta,omitempty"`
}

type EntryAuthor struct {
	Name string `xml:"name"`
}

type EntryMeta struct {
	Name    string `xml:"name,attr"`
	Content string `xml:"content,attr"`
}

func atomTime(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func pageParam(r *http.Request) int {
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			// Clamped so page*limit arithmetic cannot overflow: an int64-max
			// page wrapped the offset negative and SQLite read from the top.
			const maxPage = 1 << 26
			if n > maxPage {
				return maxPage
			}
			return n
		}
	}
	return 0
}

func pageLink(r *http.Request, page int) string {
	u := *r.URL
	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return u.RequestURI()
}

func writeFeed(w http.ResponseWriter, contentType string, f *Feed) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(f)
}

func (a *API) root(w http.ResponseWriter, r *http.Request, _ int64) {
	libs, err := a.DB.Libraries()
	if err != nil {
		serverError(w, r, err)
		return
	}
	f := &Feed{
		XMLNS: nsAtom, XMLNSOpds: nsOPDS, XMLNSOpensearch: nsOpenSearch,
		ID: "urn:libteca:opds:root", Title: "libteca", Updated: atomTime(time.Now().UnixMilli()),
		Links: []FeedLink{
			{Href: "/opds", Rel: "self", Type: typeNav},
			{Href: "/opds", Rel: "start", Type: typeNav},
			{Href: "/opds/search-description.xml", Rel: "search", Type: typeOSD},
		},
	}
	for _, l := range libs {
		if l.Type != "books" && l.Type != "comics" {
			continue
		}
		// corpus: rel="subsection" pointing at acquisition feeds — matches
		// calibre/Kavita shape, KOReader/Chunky acceptance unverified
		f.Entries = append(f.Entries, Entry{
			ID: "l-" + strconv.FormatInt(l.ID, 10), Title: l.Name, Updated: atomTime(l.CreatedAt),
			Links: []FeedLink{{Href: "/opds/libraries/" + strconv.FormatInt(l.ID, 10), Rel: relSubsection, Type: typeAcq}},
		})
	}
	f.Entries = append(f.Entries,
		navEntry("all", "All Items", "/opds/all"),
		navEntry("in-progress", "In Progress", "/opds/in-progress"),
		navEntry("newest", "Newest", "/opds/newest"),
	)
	writeFeed(w, typeNav, f)
}

func navEntry(id, title, href string) Entry {
	return Entry{
		ID: id, Title: title, Updated: atomTime(time.Now().UnixMilli()),
		Links: []FeedLink{{Href: href, Rel: relSubsection, Type: typeAcq}},
	}
}

func (a *API) emitAcquisition(w http.ResponseWriter, r *http.Request, id, title string, editions []store.OPDSEdition, total int) {
	page := pageParam(r)
	f := &Feed{
		XMLNS: nsAtom, XMLNSOpds: nsOPDS, XMLNSOpensearch: nsOpenSearch,
		ID: id, Title: title, Updated: atomTime(time.Now().UnixMilli()),
		Links: []FeedLink{
			{Href: r.URL.RequestURI(), Rel: "self", Type: typeAcq},
			{Href: "/opds", Rel: "start", Type: typeNav},
			{Href: "/opds/search-description.xml", Rel: "search", Type: typeOSD},
		},
		TotalResults: total, ItemsPerPage: pageLimit,
	}
	// corpus: rel="next"/"prev" follow RFC5005 names
	if page*pageLimit+pageLimit < total {
		f.Links = append(f.Links, FeedLink{Href: pageLink(r, page+1), Rel: "next", Type: typeAcq})
	}
	if page > 0 {
		f.Links = append(f.Links, FeedLink{Href: pageLink(r, page-1), Rel: "prev", Type: typeAcq})
	}
	f.Entries = make([]Entry, 0, len(editions))
	for _, e := range editions {
		f.Entries = append(f.Entries, editionEntry(e))
	}
	writeFeed(w, typeAcq, f)
}

func editionEntry(e store.OPDSEdition) Entry {
	id := strconv.FormatInt(e.EditionID, 10)
	en := Entry{
		ID: "e-" + id, Title: e.Title, Updated: atomTime(e.CreatedAt),
		Summary: formatSummary(e),
	}
	if e.Author != nil && *e.Author != "" {
		en.Author = &EntryAuthor{Name: *e.Author}
	}
	en.Links = append(en.Links, FeedLink{
		Href: "/opds/download/" + id, Rel: relAcquisition, Type: acquisitionMime(e.Format),
	})
	if e.CoverPath != nil && *e.CoverPath != "" {
		cover := "/opds/cover/" + strconv.FormatInt(e.WorkID, 10)
		en.Links = append(en.Links,
			FeedLink{Href: cover, Rel: relImage, Type: "image/jpeg"},
			FeedLink{Href: cover + "?size=thumb", Rel: relThumbnail, Type: "image/jpeg"},
		)
	}
	if e.Format == "cbz" {
		// corpus: OPDS-PSE — literal {pageNumber} placeholder, 1-based; page
		// type declared image/jpeg but served with the stored ext's mime
		en.Links = append(en.Links, FeedLink{
			Href: "/opds/pse/" + id + "/{pageNumber}", Rel: relPSE, Type: "image/jpeg",
		})
		if e.PageCount != nil {
			en.Metas = append(en.Metas, EntryMeta{Name: "pse,count", Content: strconv.Itoa(*e.PageCount)})
		}
	}
	return en
}

func formatSummary(e store.OPDSEdition) string {
	name := map[string]string{"epub": "EPUB", "pdf": "PDF", "cbz": "CBZ", "cbr": "CBR"}[e.Format]
	if name == "" {
		name = strings.ToUpper(e.Format)
	}
	if (e.Format == "cbz" || e.Format == "cbr") && e.PageCount != nil {
		return fmt.Sprintf("%s edition, %d pages", name, *e.PageCount)
	}
	return name + " edition"
}

func acquisitionMime(format string) string {
	switch format {
	case "epub":
		return "application/epub+zip"
	case "pdf":
		return "application/pdf"
	case "cbz":
		// corpus: Kavita serves application/vnd.comicbook+zip; task-pinned
		// application/zip — verify against KOReader/Chunky on capture
		return "application/zip"
	case "cbr":
		return "application/vnd.comicbook-rar"
	}
	return "application/octet-stream"
}

func pageMime(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".avif":
		return "image/avif"
	}
	return "application/octet-stream"
}

func (a *API) libraryFeed(w http.ResponseWriter, r *http.Request, _ int64) {
	libID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	lib, err := a.DB.Library(libID)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	eds, total, err := a.DB.OPDSEditionsInLibrary(libID, pageLimit, pageParam(r)*pageLimit)
	if err != nil {
		serverError(w, r, err)
		return
	}
	a.emitAcquisition(w, r, "urn:libteca:opds:library:"+strconv.FormatInt(libID, 10), lib.Name, eds, total)
}

func (a *API) allFeed(w http.ResponseWriter, r *http.Request, _ int64) {
	eds, total, err := a.DB.OPDSAllEditions(pageLimit, pageParam(r)*pageLimit)
	if err != nil {
		serverError(w, r, err)
		return
	}
	a.emitAcquisition(w, r, "urn:libteca:opds:all", "All Items", eds, total)
}

func (a *API) inProgressFeed(w http.ResponseWriter, r *http.Request, userID int64) {
	eds, total, err := a.DB.OPDSEditionsInProgress(userID, pageLimit, pageParam(r)*pageLimit)
	if err != nil {
		serverError(w, r, err)
		return
	}
	a.emitAcquisition(w, r, "urn:libteca:opds:in-progress:u"+strconv.FormatInt(userID, 10), "In Progress", eds, total)
}

func (a *API) newestFeed(w http.ResponseWriter, r *http.Request, _ int64) {
	eds, total, err := a.DB.OPDSNewestEditions(pageLimit, pageParam(r)*pageLimit)
	if err != nil {
		serverError(w, r, err)
		return
	}
	a.emitAcquisition(w, r, "urn:libteca:opds:newest", "Newest", eds, total)
}

func (a *API) searchFeed(w http.ResponseWriter, r *http.Request, _ int64) {
	var eds []store.OPDSEdition
	total := 0
	if q := r.URL.Query().Get("q"); q != "" {
		var err error
		eds, total, err = a.DB.OPDSSearchEditions(q, pageLimit, pageParam(r)*pageLimit)
		if err != nil {
			serverError(w, r, err)
			return
		}
	}
	a.emitAcquisition(w, r, "urn:libteca:opds:search", "Search", eds, total)
}

type openSearchDoc struct {
	XMLName        xml.Name        `xml:"OpenSearchDescription"`
	XMLNS          string          `xml:"xmlns,attr"`
	ShortName      string          `xml:"ShortName"`
	Description    string          `xml:"Description"`
	InputEncoding  string          `xml:"InputEncoding"`
	OutputEncoding string          `xml:"OutputEncoding"`
	Urls           []openSearchURL `xml:"Url"`
}

type openSearchURL struct {
	Type     string `xml:"type,attr"`
	Template string `xml:"template,attr"`
}

func (a *API) searchDescription(w http.ResponseWriter, _ *http.Request, _ int64) {
	doc := openSearchDoc{
		XMLNS: nsOpenSearch, ShortName: "libteca", Description: "Search libteca",
		InputEncoding: "UTF-8", OutputEncoding: "UTF-8",
		// corpus: relative template URL — some clients (KyBook?) may want absolute
		Urls: []openSearchURL{{Type: typeAcq, Template: "/opds/search?q={searchTerms}"}},
	}
	w.Header().Set("Content-Type", typeOSD)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(doc)
}

func (a *API) download(w http.ResponseWriter, r *http.Request, _ int64) {
	eid, err := strconv.ParseInt(r.PathValue("editionId"), 10, 64)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	f, format, err := a.DB.EditionFile(eid)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	file, err := os.Open(f.Path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", acquisitionMime(format))
	http.ServeContent(w, r, filepath.Base(f.Path), fi.ModTime(), file)
}

func (a *API) cover(w http.ResponseWriter, r *http.Request, _ int64) {
	wid, err := strconv.ParseInt(r.PathValue("workId"), 10, 64)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	wv, err := a.DB.WorkByID(wid)
	if err != nil || wv.CoverPath == nil || *wv.CoverPath == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	srcPath := filepath.Join(a.Dir, "covers", filepath.Base(*wv.CoverPath))
	if r.URL.Query().Get("size") == "thumb" {
		a.coverThumb(w, r, wid, srcPath)
		return
	}
	serveCoverFile(w, r, srcPath)
}

const thumbWidth = 160

// coverThumb serves a ~thumbWidth-wide JPEG for the work cover: cached on
// disk under covers/thumb/, downscaled with a deterministic box-average
// (stdlib only). Covers already narrower than the target — or in formats
// stdlib cannot decode (webp/avif) — pass through unchanged and uncached.
func (a *API) coverThumb(w http.ResponseWriter, r *http.Request, wid int64, srcPath string) {
	thumbDir := filepath.Join(a.Dir, "covers", "thumb")
	thumbPath := filepath.Join(thumbDir, strconv.FormatInt(wid, 10)+".jpg")
	if _, err := os.Stat(thumbPath); err == nil {
		serveCoverFile(w, r, thumbPath)
		return
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() <= thumbWidth {
		serveCoverFile(w, r, srcPath)
		return
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaleToWidth(img, thumbWidth), &jpeg.Options{Quality: 85}); err != nil {
		serverError(w, r, err)
		return
	}
	if err := os.MkdirAll(thumbDir, 0o755); err == nil {
		os.WriteFile(thumbPath, buf.Bytes(), 0o644)
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

func serveCoverFile(w http.ResponseWriter, r *http.Request, path string) {
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), file)
}

// scaleToWidth shrinks src to width w with a box-average kernel: every
// destination pixel is the exact mean of the source box it covers. Integer
// math only, so the output is deterministic across runs and machines.
func scaleToWidth(src image.Image, w int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	th := (sh*w + sw/2) / sw
	if th < 1 {
		th = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, th))
	for y := 0; y < th; y++ {
		y0, y1 := y*sh/th, (y+1)*sh/th
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, (x+1)*sw/w
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var rs, gs, bs, as, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					pr, pg, pb, pa := src.At(b.Min.X+xx, b.Min.Y+yy).RGBA()
					rs, gs, bs, as = rs+uint64(pr), gs+uint64(pg), bs+uint64(pb), as+uint64(pa)
					n++
				}
			}
			dst.SetRGBA(x, y, color.RGBA{R: uint8(rs / n >> 8), G: uint8(gs / n >> 8), B: uint8(bs / n >> 8), A: uint8(as / n >> 8)})
		}
	}
	return dst
}

func (a *API) psePage(w http.ResponseWriter, r *http.Request, _ int64) {
	eid, err := strconv.ParseInt(r.PathValue("editionId"), 10, 64)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	page, err := strconv.Atoi(r.PathValue("pageNumber"))
	if err != nil || page < 1 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	f, format, err := a.DB.EditionFile(eid)
	if err != nil || format != "cbz" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	zr, err := zip.OpenReader(f.Path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer zr.Close()
	names := cbzPageNames(&zr.Reader)
	if page > len(names) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	name := names[page-1]
	data, err := readZipPage(&zr.Reader, name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", pageMime(strings.ToLower(filepath.Ext(name))))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
