package scan

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
	"path"
	"strings"
)

type EpubTOCEntry struct {
	Title string `json:"title"`
	Href  string `json:"href"`
}

type EpubInfo struct {
	Title       string
	Author      string
	Language    string
	Description string
	PageCount   int
	TOC         []EpubTOCEntry
	Cover       []byte
}

type epubContainer struct {
	Rootfiles []epubRootfile `xml:"rootfiles>rootfile"`
}

type epubRootfile struct {
	FullPath string `xml:"full-path,attr"`
}

type opfPackage struct {
	Metadata opfMetadata `xml:"metadata"`
	Manifest opfManifest `xml:"manifest"`
	Spine    opfSpine    `xml:"spine"`
}

type opfMetadata struct {
	Titles       []string     `xml:"title"`
	Creators     []opfCreator `xml:"creator"`
	Languages    []string     `xml:"language"`
	Descriptions []string     `xml:"description"`
	Metas        []opfMeta    `xml:"meta"`
}

type opfCreator struct {
	Value string `xml:",chardata"`
}

type opfMeta struct {
	Name    string `xml:"name,attr"`
	Content string `xml:"content,attr"`
}

type opfManifest struct {
	Items []opfItem `xml:"item"`
}

type opfItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

type opfSpine struct {
	Toc      string       `xml:"toc,attr"`
	Itemrefs []opfItemref `xml:"itemref"`
}

type opfItemref struct {
	IDRef string `xml:"idref,attr"`
}

type navDoc struct {
	Navs []navNav `xml:"body>nav"`
}

type navNav struct {
	Type string `xml:"type,attr"`
	OL   navOL  `xml:"ol"`
}

type navOL struct {
	LIs []navLI `xml:"li"`
}

type navLI struct {
	As []navA `xml:"a"`
	OL *navOL `xml:"ol"`
}

type navA struct {
	Href string `xml:"href,attr"`
	Text string `xml:",chardata"`
}

type ncxDoc struct {
	NavMap ncxNavMap `xml:"navMap"`
}

type ncxNavMap struct {
	NavPoints []ncxNavPoint `xml:"navPoint"`
}

type ncxNavPoint struct {
	NavLabel  ncxNavLabel   `xml:"navLabel"`
	Content   ncxContent    `xml:"content"`
	NavPoints []ncxNavPoint `xml:"navPoint"`
}

type ncxNavLabel struct {
	Text string `xml:"text"`
}

type ncxContent struct {
	Src string `xml:"src,attr"`
}

const epubCoverLimit = 20 << 20

func ParseEPUB(p string) (*EpubInfo, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return parseEPUB(&zr.Reader)
}

func parseEPUB(zr *zip.Reader) (*EpubInfo, error) {
	var ct epubContainer
	if err := readXMLZip(zr, "META-INF/container.xml", &ct); err != nil {
		return nil, err
	}
	if len(ct.Rootfiles) == 0 || ct.Rootfiles[0].FullPath == "" {
		return nil, errors.New("epub: no rootfile in container.xml")
	}
	opfPath := ct.Rootfiles[0].FullPath
	var pkg opfPackage
	if err := readXMLZip(zr, opfPath, &pkg); err != nil {
		return nil, err
	}

	info := &EpubInfo{PageCount: len(pkg.Spine.Itemrefs)}
	if len(pkg.Metadata.Titles) > 0 {
		info.Title = strings.TrimSpace(pkg.Metadata.Titles[0])
	}
	creators := make([]string, 0, len(pkg.Metadata.Creators))
	for _, c := range pkg.Metadata.Creators {
		if v := strings.TrimSpace(c.Value); v != "" {
			creators = append(creators, v)
		}
	}
	info.Author = strings.Join(creators, ", ")
	if len(pkg.Metadata.Languages) > 0 {
		info.Language = strings.TrimSpace(pkg.Metadata.Languages[0])
	}
	if len(pkg.Metadata.Descriptions) > 0 {
		info.Description = strings.TrimSpace(pkg.Metadata.Descriptions[0])
	}

	byID := map[string]opfItem{}
	for _, it := range pkg.Manifest.Items {
		byID[it.ID] = it
	}
	opfDir := path.Dir(opfPath)

	for _, it := range pkg.Manifest.Items {
		if strings.Contains(it.Properties, "nav") && it.MediaType == "application/xhtml+xml" {
			info.TOC = parseNav(zr, joinZipPath(opfDir, it.Href))
			break
		}
	}
	if len(info.TOC) == 0 {
		href := ""
		if it, ok := byID[pkg.Spine.Toc]; ok {
			href = it.Href
		} else {
			for _, it := range pkg.Manifest.Items {
				if it.MediaType == "application/x-dtbncx+xml" {
					href = it.Href
					break
				}
			}
		}
		if href != "" {
			info.TOC = parseNCX(zr, joinZipPath(opfDir, href))
		}
	}

	if href := epubCoverHref(pkg); href != "" {
		if f, err := zr.Open(joinZipPath(opfDir, href)); err == nil {
			data, rerr := io.ReadAll(io.LimitReader(f, epubCoverLimit+1))
			f.Close()
			// Oversize covers are skipped, not truncated: a truncated image
			// was persisted as a valid cover.
			if rerr == nil && len(data) > 0 && len(data) <= epubCoverLimit {
				info.Cover = data
			}
		}
	}
	return info, nil
}

var epubCoverMedia = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
}

func epubCoverHref(pkg opfPackage) string {
	for _, it := range pkg.Manifest.Items {
		if strings.Contains(it.Properties, "cover-image") && epubCoverMedia[it.MediaType] {
			return it.Href
		}
	}
	byID := map[string]opfItem{}
	for _, it := range pkg.Manifest.Items {
		byID[it.ID] = it
	}
	for _, m := range pkg.Metadata.Metas {
		if m.Name != "cover" {
			continue
		}
		if it, ok := byID[m.Content]; ok && epubCoverMedia[it.MediaType] {
			return it.Href
		}
	}
	for _, it := range pkg.Manifest.Items {
		if strings.Contains(strings.ToLower(it.ID), "cover") && epubCoverMedia[it.MediaType] {
			return it.Href
		}
	}
	return ""
}

func joinZipPath(dir, href string) string {
	if u, err := url.PathUnescape(href); err == nil {
		href = u
	}
	if i := strings.Index(href, "#"); i >= 0 {
		href = href[:i]
	}
	href = strings.TrimPrefix(href, "/")
	if dir == "" || dir == "." || dir == "/" {
		return href
	}
	return path.Join(dir, href)
}

func readXMLZip(zr *zip.Reader, name string, v any) error {
	f, err := zr.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return xml.NewDecoder(f).Decode(v)
}

func parseNav(zr *zip.Reader, name string) []EpubTOCEntry {
	var doc navDoc
	if err := readXMLZip(zr, name, &doc); err != nil {
		return nil
	}
	for _, n := range doc.Navs {
		if strings.Contains(n.Type, "toc") {
			return navOLEntries(&n.OL)
		}
	}
	if len(doc.Navs) > 0 {
		return navOLEntries(&doc.Navs[0].OL)
	}
	return nil
}

func navOLEntries(ol *navOL) []EpubTOCEntry {
	var out []EpubTOCEntry
	for _, li := range ol.LIs {
		if len(li.As) > 0 {
			out = append(out, EpubTOCEntry{Title: strings.TrimSpace(li.As[0].Text), Href: li.As[0].Href})
		}
		if li.OL != nil {
			out = append(out, navOLEntries(li.OL)...)
		}
	}
	return out
}

func parseNCX(zr *zip.Reader, name string) []EpubTOCEntry {
	var doc ncxDoc
	if err := readXMLZip(zr, name, &doc); err != nil {
		return nil
	}
	return ncxPoints(doc.NavMap.NavPoints)
}

func ncxPoints(pts []ncxNavPoint) []EpubTOCEntry {
	var out []EpubTOCEntry
	for _, p := range pts {
		out = append(out, EpubTOCEntry{Title: strings.TrimSpace(p.NavLabel.Text), Href: p.Content.Src})
		out = append(out, ncxPoints(p.NavPoints)...)
	}
	return out
}
