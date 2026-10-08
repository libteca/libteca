package opds

import (
	"archive/zip"
	"path/filepath"
	"sort"
	"strings"

	"github.com/libteca/libteca/internal/natural"
	"github.com/libteca/libteca/internal/resourcebudget"
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
	sort.Slice(names, func(i, j int) bool { return natural.Less(names[i], names[j]) })
	return names
}

func readZipPage(zr *zip.Reader, name string) ([]byte, error) {
	data, reservation, err := readZipPageReserved(zr, name)
	reservation.Release()
	return data, err
}

func readZipPageReserved(zr *zip.Reader, name string) ([]byte, *resourcebudget.Reservation, error) {
	budget, err := resourcebudget.ArchiveBudget()
	if err != nil {
		return nil, nil, err
	}
	rc, err := zr.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer rc.Close()
	return resourcebudget.ReadReserved(rc, 20<<20, budget)
}
