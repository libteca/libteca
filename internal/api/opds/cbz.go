package opds

import (
	"fmt"
	"archive/zip"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// CBZ page listing/extraction. Mirrors probeCBZ in internal/scan/books.go
// (image extensions, dotfile + __MACOSX junk filter, natural order) so scan
// page counts and PSE page indexes agree on what a page is.

var cbzPageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true, ".avif": true,
}

func cbzPageNames(zr *zip.Reader) []string {
	var names []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !cbzPageExts[strings.ToLower(filepath.Ext(f.Name))] {
			continue
		}
		if n := filepath.Base(f.Name); strings.HasPrefix(n, ".") || strings.Contains(strings.ToUpper(f.Name), "__MACOSX") {
			continue
		}
		names = append(names, f.Name)
	}
	sort.Slice(names, func(i, j int) bool { return natLess(names[i], names[j]) })
	return names
}

func readZipPage(zr *zip.Reader, name string) ([]byte, error) {
	rc, err := zr.Open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 20<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 20<<20 {
		return nil, fmt.Errorf("page %s exceeds the 20 MB limit", name)
	}
	return data, nil
}

// natLess is the same natural-order comparison as internal/scan/scan.go
// (digit runs compare numerically after stripping leading zeros).
func natLess(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		ca, cb := a[ai], b[bi]
		if isDigit(ca) && isDigit(cb) {
			na, nj := ai, bi
			for na < len(a) && isDigit(a[na]) {
				na++
			}
			for nj < len(b) && isDigit(b[nj]) {
				nj++
			}
			da, db := strings.TrimLeft(a[ai:na], "0"), strings.TrimLeft(b[bi:nj], "0")
			if len(da) != len(db) {
				return len(da) < len(db)
			}
			if da != db {
				return da < db
			}
			ai, bi = na, nj
			continue
		}
		if ca != cb {
			return ca < cb
		}
		ai++
		bi++
	}
	return len(a)-ai < len(b)-bi
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
