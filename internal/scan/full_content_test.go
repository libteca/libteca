package scan

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type failingContent struct {
	seek   bool
	reader *strings.Reader
}

func (f *failingContent) Seek(offset int64, whence int) (int64, error) {
	if f.seek {
		return 0, io.ErrUnexpectedEOF
	}
	return f.reader.Seek(offset, whence)
}
func (f *failingContent) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func TestMandatoryFullContentProofRejectsSeekAndReadFailure(t *testing.T) {
	for _, seek := range []bool{false, true} {
		if digest, err := fullContentDigest(&failingContent{seek: seek, reader: strings.NewReader("content")}); digest != "" || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(digest, err)
		}
	}
	digest, err := fullContentDigest(strings.NewReader("content"))
	if err != nil || len(digest) != 64 {
		t.Fatal(digest, err)
	}
}
