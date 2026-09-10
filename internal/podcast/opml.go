package podcast

import (
	"encoding/xml"
	"fmt"

	"github.com/libteca/libteca/internal/store"
)

type opmlOutline struct {
	Text     string        `xml:"text,attr"`
	Type     string        `xml:"type,attr"`
	XMLURL   string        `xml:"xmlUrl,attr"`
	Outlines []opmlOutline `xml:"outline"`
}

type opmlDocument struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    struct {
		Title string `xml:"title"`
	} `xml:"head"`
	Body struct {
		Outlines []opmlOutline `xml:"outline"`
	} `xml:"body"`
}

// ParseOPML collects every outline xmlUrl (nested folders included), in
// document order, deduplicated.
func ParseOPML(data []byte) ([]string, error) {
	var doc opmlDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse opml: %w", err)
	}
	var urls []string
	seen := map[string]bool{}
	var walk func([]opmlOutline)
	walk = func(outlines []opmlOutline) {
		for _, o := range outlines {
			if o.XMLURL != "" && !seen[o.XMLURL] {
				seen[o.XMLURL] = true
				urls = append(urls, o.XMLURL)
			}
			walk(o.Outlines)
		}
	}
	walk(doc.Body.Outlines)
	return urls, nil
}

func BuildOPML(title string, podcasts []store.Podcast) ([]byte, error) {
	var doc opmlDocument
	doc.Version = "2.0"
	doc.Head.Title = title
	for _, p := range podcasts {
		doc.Body.Outlines = append(doc.Body.Outlines, opmlOutline{Text: p.Title, Type: "rss", XMLURL: p.FeedURL})
	}
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(body)+40)
	out = append(out, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"...)
	out = append(out, body...)
	out = append(out, '\n')
	return out, nil
}
