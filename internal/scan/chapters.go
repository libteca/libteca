package scan

import (
	"encoding/json"

	"github.com/libteca/libteca/internal/audio"
)

func marshalChapters(chapters []audio.Chapter) (string, error) {
	if chapters == nil {
		chapters = []audio.Chapter{}
	}
	b, err := json.Marshal(chapters)
	return string(b), err
}
