package meta

import (
	"context"
	"os"
	"strings"
	"sync"
)

const userAgent = "libteca/0.1"

type Query struct {
	Kind           string
	Title, Author  string
	Year           *int
	Language, ISBN string
	ASIN           string
}

type Chapter struct {
	Title            string
	StartSec, EndSec float64
}

type Result struct {
	Provider, ID               string
	Title, Author, Description string
	Year                       *int
	CoverURL                   string
	Rating                     float64
	Genres                     []string
	Chapters                   []Chapter
	Extra                      map[string]string
}

type Provider interface {
	Name() string
	Search(ctx context.Context, q Query) ([]Result, error)
	Fetch(ctx context.Context, id string) (*Result, error)
}

// Kinds: movie, tv, audiobook, book, comic, music. Providers return
// (nil, nil) for queries outside their kind.

var tmdbDisabledLogOnce sync.Once

// Registry returns every enabled provider in stable order. Key-gated
// constructors return nil when disabled and are filtered here.
func Registry() []Provider {
	var out []Provider
	if p := NewTMDB(); p != nil {
		out = append(out, p)
	}
	out = append(out, NewAudible(), NewMusicBrainz(), NewOpenLibrary())
	if p := NewComicVine(); p != nil {
		out = append(out, p)
	}
	return out
}

func envKey(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
