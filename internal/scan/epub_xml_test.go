package scan

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestReadXMLZipBounded(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("big.opf")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte("<package><metadata><description>"))
	chunk := bytes.Repeat([]byte("a"), 1<<20)
	for i := 0; i < (epubXMLMax>>20)+2; i++ {
		fw.Write(chunk)
	}
	fw.Write([]byte("</description></metadata></package>"))
	zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Description string `xml:"metadata>description"`
	}
	if err := readXMLZip(zr, "big.opf", &v); err == nil {
		t.Fatal("oversized EPUB XML entry must be rejected")
	}
}
