package subsonic

import "encoding/xml"

// One struct set, dual-marshaled: XML (the wire default) and JSON (f=json).
// JSON omits nil payload pointers; XML omits them too. Slices inside payloads
// are never omitempty so JSON always emits arrays.

// corpus: field set assembled from the Subsonic 1.16.1 / OpenSubsonic docs,
// not from captured client traffic; markers flag the shakier guesses.

type Response struct {
	XMLName       xml.Name `xml:"subsonic-response" json:"-"`
	XMLNS         string   `xml:"xmlns,attr" json:"-"`
	Status        string   `xml:"status,attr" json:"status"`
	Version       string   `xml:"version,attr" json:"version"`
	Type          string   `xml:"type,attr" json:"type"`
	ServerVersion string   `xml:"serverVersion,attr" json:"serverVersion"` // corpus: sync with release version
	OpenSubsonic  bool     `xml:"openSubsonic,attr" json:"openSubsonic"`

	Error *Error `xml:"error,omitempty" json:"error,omitempty"`

	Artists       *ArtistsID3       `xml:"artists,omitempty" json:"artists,omitempty"`
	Indexes       *Indexes          `xml:"indexes,omitempty" json:"indexes,omitempty"`
	Artist        *ArtistWithAlbums `xml:"artist,omitempty" json:"artist,omitempty"`
	Album         *AlbumWithSongs   `xml:"album,omitempty" json:"album,omitempty"`
	Song          *Child            `xml:"song,omitempty" json:"song,omitempty"`
	AlbumList2    *AlbumList2       `xml:"albumList2,omitempty" json:"albumList2,omitempty"`
	SearchResult3 *SearchResult3    `xml:"searchResult3,omitempty" json:"searchResult3,omitempty"`
	Playlists     *Playlists        `xml:"playlists,omitempty" json:"playlists,omitempty"`
}

type Error struct {
	Code    int    `xml:"code,attr" json:"code"`
	Message string `xml:"message,attr" json:"message"`
}

type ArtistsID3 struct {
	IgnoredArticles string           `xml:"ignoredArticles,attr" json:"ignoredArticles"`
	Index           []ArtistIndexID3 `xml:"index" json:"index"`
}

type ArtistIndexID3 struct {
	Name   string      `xml:"name,attr" json:"name"`
	Artist []ArtistID3 `xml:"artist" json:"artist"`
}

type ArtistID3 struct {
	ID         string `xml:"id,attr" json:"id"`
	Name       string `xml:"name,attr" json:"name"`
	AlbumCount int    `xml:"albumCount,attr" json:"albumCount"`
	CoverArt   string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"` // corpus: artists have no art yet
}

type Indexes struct {
	LastModified    int64   `xml:"lastModified,attr" json:"lastModified"` // corpus: 0, no change tracking
	IgnoredArticles string  `xml:"ignoredArticles,attr" json:"ignoredArticles"`
	Index           []Index `xml:"index" json:"index"`
}

type Index struct {
	Name   string        `xml:"name,attr" json:"name"`
	Artist []IndexArtist `xml:"artist" json:"artist"`
}

type IndexArtist struct {
	ID   string `xml:"id,attr" json:"id"`
	Name string `xml:"name,attr" json:"name"`
}

type ArtistWithAlbums struct {
	ArtistID3
	Album []AlbumID3 `xml:"album" json:"album"`
}

type AlbumID3 struct {
	ID        string `xml:"id,attr" json:"id"`
	Name      string `xml:"name,attr" json:"name"`
	Artist    string `xml:"artist,attr" json:"artist"`
	ArtistID  string `xml:"artistId,attr" json:"artistId"`
	CoverArt  string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	SongCount int    `xml:"songCount,attr" json:"songCount"`
	Duration  int    `xml:"duration,attr" json:"duration"`
	PlayCount int    `xml:"playCount,attr,omitempty" json:"playCount,omitempty"` // corpus: no play counts yet
	Created   string `xml:"created,attr" json:"created"`
}

type AlbumWithSongs struct {
	AlbumID3
	Song []Child `xml:"song" json:"song"`
}

type Child struct {
	ID          string `xml:"id,attr" json:"id"`
	Parent      string `xml:"parent,attr,omitempty" json:"parent,omitempty"`
	IsDir       bool   `xml:"isDir,attr" json:"isDir"`
	Title       string `xml:"title,attr" json:"title"`
	Album       string `xml:"album,attr" json:"album"`
	Artist      string `xml:"artist,attr" json:"artist"`
	Track       int    `xml:"track,attr,omitempty" json:"track,omitempty"`
	Year        int    `xml:"year,attr,omitempty" json:"year,omitempty"`
	Genre       string `xml:"genre,attr,omitempty" json:"genre,omitempty"` // corpus: no genre in model
	CoverArt    string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	Size        int64  `xml:"size,attr,omitempty" json:"size,omitempty"`
	ContentType string `xml:"contentType,attr" json:"contentType"`
	Suffix      string `xml:"suffix,attr" json:"suffix"`
	Duration    int    `xml:"duration,attr" json:"duration"`
	BitRate     int    `xml:"bitRate,attr" json:"bitRate"`
	Path        string `xml:"path,attr,omitempty" json:"path,omitempty"` // corpus: omitted, path privacy
	IsVideo     bool   `xml:"isVideo,attr" json:"isVideo"`
	PlayCount   int    `xml:"playCount,attr,omitempty" json:"playCount,omitempty"`
	DiscNumber  int    `xml:"discNumber,attr,omitempty" json:"discNumber,omitempty"` // corpus: single disc assumed
	Created     string `xml:"created,attr" json:"created"`
	AlbumID     string `xml:"albumId,attr" json:"albumId"`
	ArtistID    string `xml:"artistId,attr" json:"artistId"`
	Type        string `xml:"type,attr,omitempty" json:"type,omitempty"`
}

type AlbumList2 struct {
	Album []AlbumID3 `xml:"album" json:"album"`
}

type SearchResult3 struct {
	Artist []ArtistID3 `xml:"artist" json:"artist"`
	Album  []AlbumID3  `xml:"album" json:"album"`
	Song   []Child     `xml:"song" json:"song"`
}

type Playlists struct {
	Playlist []Playlist `xml:"playlist" json:"playlist"`
}

type Playlist struct {
	ID        string `xml:"id,attr" json:"id"`
	Name      string `xml:"name,attr" json:"name"`
	SongCount int    `xml:"songCount,attr" json:"songCount"`
}
