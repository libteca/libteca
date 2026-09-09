package scan

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
)

type vpOut struct {
	Streams []struct {
		CodecName string `json:"codec_name"`
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
}

func probeVideo(path string) (string, int, int) {
	out, err := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json", "-show_streams", "-select_streams", "v:0", path).Output()
	if err != nil {
		return "", 0, 0
	}
	var p vpOut
	if json.Unmarshal(out, &p) != nil {
		return "", 0, 0
	}
	for _, s := range p.Streams {
		return s.CodecName, s.Width, s.Height
	}
	return "", 0, 0
}

var _ = strings.TrimSpace
var _ = strconv.Itoa
